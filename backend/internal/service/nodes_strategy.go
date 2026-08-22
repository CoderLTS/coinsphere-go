package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/internal/marketdata"
	"coinsphere/backend/internal/perm"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func init() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "strategy.evaluate", Label: "执行策略", RequiredPermission: perm.TradingOverviewView,
		ExecutionMode: nodeExecutionWorkerJob,
		InputPorts: []workflowNodePortDefinition{
			nodePort("candleOpenTime", "K 线时间", true, M{"type": "string", "format": "date-time"}),
		},
		OutputPorts: []workflowNodePortDefinition{
			nodePort("result", "执行任务", false, M{"type": "object"}),
			nodePort("taskId", "任务 ID", false, M{"type": "string"}),
		},
		ConfigSchema: M{"type": "object", "properties": M{
			"strategyVersionId": M{"type": "string", "title": "策略版本", "resource": "strategy-version"},
			"instrumentId":      M{"type": "string", "title": "币种", "resource": "market-instrument"},
			"interval":          M{"type": "string", "title": "周期", "enum": []string{"1m", "5m", "15m", "1h", "4h", "1d"}},
			"environment":       M{"type": "string", "title": "环境", "enum": []string{"paper", "testnet", "live"}, "default": "paper"},
			"mode":              M{"type": "string", "title": "运行模式", "enum": []string{"signal_only", "manual", "auto"}, "default": "signal_only"},
			"tradingAccountId":  M{"type": "string", "title": "交易账户", "resource": "trading-account"},
			"allocationUsdt":    M{"type": "string", "format": "decimal", "title": "运行额度"},
			"stopLossRatio":     M{"type": "string", "format": "decimal", "title": "止损比例"},
			"parameters":        M{"type": "object", "title": "策略参数"},
		}, "required": []string{"strategyVersionId", "instrumentId", "interval"}}, Execute: strategyEvaluateExecute,
	})
}

type strategyTaskState struct {
	ID              string
	Status          string
	FailureCategory string
	ErrorMessage    string
}

func strategyEvaluateExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	var instance db.StrategyInstance
	err := ctx.App.dbWithContext(ctx.Ctx).Where(
		"workflow_definition_id = ? AND workflow_node_id = ? AND is_enabled AND archived_at IS NULL",
		ctx.Definition.ID, asString(ctx.Node["id"]),
	).Take(&instance).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, permanentErr(ErrStrategyInstanceMissing)
	}
	if err != nil {
		return nil, retryableErr(err)
	}
	payload, _ := ctx.State.get("trigger.payload").(map[string]any)
	if asString(payload["instrumentId"]) != instance.InstrumentID.String() ||
		asString(payload["interval"]) != instance.Interval {
		return nil, permanentErr(invalidStrategy("触发事件的币种或周期与策略节点不一致"))
	}
	openTime, err := parseOptionalUTCTime(asString(ctx.Inputs["candleOpenTime"]), "candleOpenTime")
	if err != nil || openTime == nil {
		return nil, permanentErr(invalidStrategy("candleOpenTime must be UTC RFC3339Nano"))
	}
	task, deduplicated, err := ctx.App.enqueueStrategyEvaluation(ctx.Ctx, instance.ID, openTime.UTC())
	if err != nil {
		if errors.Is(err, ErrStrategyInstanceMissing) {
			return nil, permanentErr(err)
		}
		return nil, retryableErr(err)
	}
	output := strategyTaskOutput(task, deduplicated, nil)
	setNodeOutput(ctx, output)
	return &nodeExecResult{Output: output, Wait: &workflowWaitRequest{
		Kind: "worker_job", TargetType: "strategy_signal", TargetID: task.ID,
		Request: M{"workerTaskId": task.ID},
	}}, nil
}

func (a *App) reconcileWorkflowStrategiesWithDB(database *gorm.DB, definition *db.WorkflowDefinition) error {
	graph := loadJSONObject(definition.GraphJSON)
	nodes, _ := graph["nodes"].([]any)
	for _, nodeAny := range nodes {
		node, ok := nodeAny.(map[string]any)
		if !ok || asString(node["type"]) != "strategy.evaluate" {
			continue
		}
		if definition.CreatedBy == nil || *definition.CreatedBy <= 0 {
			return bizErr("策略工作流必须有明确的创建用户")
		}
		config := rawNodeConfig(node)
		versionID, err := requiredStrategyUUID(asString(config["strategyVersionId"]), "strategyVersionId")
		if err != nil {
			return err
		}
		instrumentID, err := requiredStrategyUUID(asString(config["instrumentId"]), "instrumentId")
		if err != nil {
			return err
		}
		interval := strings.TrimSpace(asString(config["interval"]))
		if _, ok := marketdata.CandleIntervalDuration(marketdata.CandleInterval(interval)); !ok {
			return invalidStrategy("interval is not supported")
		}
		var version db.StrategyVersion
		if err := database.Where("id = ? AND published_by_user_id = ? AND status = 'published'", versionID, *definition.CreatedBy).Take(&version).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStrategyVersionMissing
			}
			return err
		}
		var instrument db.MarketInstrument
		if err := database.Where(
			"id = ? AND venue = ? AND quote_asset = 'USDT' AND status = 'trading'",
			instrumentID, marketdata.VenueBinance,
		).Take(&instrument).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrStrategyInstrumentMissing
			}
			return err
		}
		nodeID := strings.TrimSpace(asString(node["id"]))
		if !workflowHasCandleEntry(graph, nodeID, instrumentID.String(), interval) {
			return invalidStrategy("策略节点必须连接匹配币种和周期的 K 线事件入口")
		}
		parameters := map[string]json.RawMessage{}
		if raw, ok := config["parameters"].(map[string]any); ok {
			for key, value := range raw {
				encoded, err := json.Marshal(value)
				if err != nil {
					return invalidStrategy("parameters must be a JSON object")
				}
				parameters[key] = encoded
			}
		}
		name := strings.TrimSpace(asString(config["name"]))
		if name == "" {
			name = version.Name
		}
		payload := StrategyInstanceCreatePayload{
			StrategyVersionID: versionID.String(), InstrumentID: instrumentID.String(), Interval: interval,
			TradingAccountID: asString(config["tradingAccountId"]), AllocationUSDT: asString(config["allocationUsdt"]),
			StopLossRatio: asString(config["stopLossRatio"]), Name: name, Mode: asString(config["mode"]),
			Environment: asString(config["environment"]), Parameters: parameters,
		}
		validated, err := validateStrategyInstancePayload(
			payload, version, instrument.Market, a.spotLiveManualEnabled(), a.usdmLiveManualEnabled(),
			a.spotLiveAutoEnabled(), a.usdmLiveAutoEnabled(),
		)
		if err != nil {
			return err
		}
		var instance db.StrategyInstance
		err = database.Where(
			"workflow_definition_id = ? AND workflow_node_id = ? AND archived_at IS NULL",
			definition.ID, nodeID,
		).Take(&instance).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		now := time.Now().UTC()
		if errors.Is(err, gorm.ErrRecordNotFound) {
			instance.ID, err = uuid.NewV7()
			if err != nil {
				return err
			}
			instance.CreatedAt = now
		}
		instance.OwnerUserID = *definition.CreatedBy
		instance.StrategyVersionID = versionID
		instance.Market = instrument.Market
		instance.InstrumentID = instrumentID
		instance.Interval = interval
		instance.WorkflowDefinitionID = definition.ID
		instance.WorkflowNodeID = nodeID
		instance.TradingAccountID = validated.TradingAccountID
		instance.AllocationUSDT = validated.AllocationUSDT
		instance.StopLossRatio = validated.StopLossRatio
		instance.Name = validated.Name
		instance.Mode = validated.Mode
		instance.Environment = validated.Environment
		instance.ParametersJSON = validated.ParametersJSON
		instance.IsEnabled = true
		instance.UpdatedAt = now
		if err := a.validateStrategyInstanceExecutionReady(database, instance); err != nil {
			return err
		}
		if err := database.Save(&instance).Error; err != nil {
			return err
		}
		subscription := db.MarketWorkflowSubscription{
			WorkflowDefinitionID: definition.ID, NodeID: nodeID, InstrumentID: instrumentID,
			Interval: interval, CreatedAt: now, UpdatedAt: now,
		}
		if err := database.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "workflow_definition_id"}, {Name: "node_id"}},
			DoUpdates: clause.Assignments(map[string]any{
				"instrument_id": instrumentID, "interval_code": interval, "updated_at": now,
			}),
		}).Create(&subscription).Error; err != nil {
			return err
		}
	}
	return nil
}

func workflowHasCandleEntry(graph M, strategyNodeID, instrumentID, interval string) bool {
	nodes, _ := graph["nodes"].([]any)
	edges, _ := graph["edges"].([]any)
	adjacency := map[string][]string{}
	for _, edgeAny := range edges {
		if edge, ok := edgeAny.(map[string]any); ok {
			adjacency[asString(edge["source"])] = append(adjacency[asString(edge["source"])], asString(edge["target"]))
		}
	}
	for _, nodeAny := range nodes {
		node, ok := nodeAny.(map[string]any)
		if !ok || asString(node["type"]) != "start.event" {
			continue
		}
		config := rawNodeConfig(node)
		if asString(config["eventType"]) != "market.candle.closed" {
			continue
		}
		matchedInstrument, matchedInterval := false, false
		filters, _ := config["filters"].([]any)
		for _, filterAny := range filters {
			filter, _ := filterAny.(map[string]any)
			switch asString(filter["path"]) {
			case "instrumentId":
				matchedInstrument = asString(filter["equals"]) == instrumentID
			case "interval":
				matchedInterval = asString(filter["equals"]) == interval
			}
		}
		if matchedInstrument && matchedInterval {
			visited := map[string]bool{}
			queue := []string{asString(node["id"])}
			for len(queue) > 0 {
				current := queue[0]
				queue = queue[1:]
				if current == strategyNodeID {
					return true
				}
				if visited[current] {
					continue
				}
				visited[current] = true
				queue = append(queue, adjacency[current]...)
			}
		}
	}
	return false
}

func workflowContainsStrategy(graph M) bool {
	nodes, _ := graph["nodes"].([]any)
	for _, nodeAny := range nodes {
		if node, ok := nodeAny.(map[string]any); ok && asString(node["type"]) == "strategy.evaluate" {
			return true
		}
	}
	return false
}

func (a *App) enqueueStrategyEvaluation(ctx context.Context, instanceID uuid.UUID, candleOpenTime time.Time) (strategyTaskState, bool, error) {
	var enabled int64
	err := a.dbWithContext(ctx).Raw(`
SELECT COUNT(*)
FROM strategy_instances AS instance
JOIN strategy_versions AS version ON version.id = instance.strategy_version_id
WHERE instance.id = ? AND instance.is_enabled AND instance.archived_at IS NULL
  AND version.status = 'published'
`, instanceID).Scan(&enabled).Error
	if err != nil {
		return strategyTaskState{}, false, err
	}
	if enabled != 1 {
		return strategyTaskState{}, false, ErrStrategyInstanceMissing
	}
	taskID, err := uuid.NewV7()
	if err != nil {
		return strategyTaskState{}, false, err
	}
	openTime := formatUTC(candleOpenTime)
	payload, _ := json.Marshal(M{"instanceId": instanceID.String(), "candleOpenTime": openTime})
	dedupeKey := "strategy.realtime:" + instanceID.String() + ":" + openTime
	result := a.dbWithContext(ctx).Exec(
		"INSERT INTO worker_tasks (id, task_type, payload_json, lane, dedupe_key) VALUES (?, 'strategy.realtime', ?, 'realtime', ?) ON CONFLICT DO NOTHING",
		taskID.String(), string(payload), dedupeKey,
	)
	if result.Error != nil {
		return strategyTaskState{}, false, result.Error
	}
	var task strategyTaskState
	if err := a.dbWithContext(ctx).Raw(
		"SELECT id, status, COALESCE(failure_category, '') AS failure_category, COALESCE(error_message, '') AS error_message FROM worker_tasks WHERE task_type = 'strategy.realtime' AND dedupe_key = ?",
		dedupeKey,
	).Scan(&task).Error; err != nil {
		return strategyTaskState{}, false, err
	}
	if strings.TrimSpace(task.ID) == "" {
		return strategyTaskState{}, false, errors.New("strategy task was not persisted")
	}
	return task, result.RowsAffected == 0, nil
}

func strategyTaskOutput(task strategyTaskState, deduplicated bool, signal any) M {
	return M{"taskId": task.ID, "taskStatus": task.Status, "deduplicated": deduplicated, "signal": signal}
}
