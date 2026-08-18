package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"coinsphere/backend/internal/db"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func init() {
	registerNode(&workflowNodeDefinition{
		TypeCode: "strategy.evaluate", Label: "执行策略",
		ConfigSchema: M{"type": "object", "properties": M{
			"strategyInstanceId": M{"type": "string", "title": "策略实例"},
			"candleOpenTimePath": M{"type": "string", "title": "K 线时间路径", "default": "trigger.payload.openTime"},
			"executionMode":      M{"type": "string", "title": "执行模式", "enum": []string{"sync", "async"}, "default": "sync"},
			"timeoutSeconds":     M{"type": "integer", "title": "等待秒数", "minimum": 5, "maximum": 300, "default": 30},
		}, "required": []string{"strategyInstanceId"}}, Execute: strategyEvaluateExecute,
	})
}

type strategyTaskState struct {
	ID              string
	Status          string
	FailureCategory string
	ErrorMessage    string
}

func strategyEvaluateExecute(ctx *nodeExecContext) (*nodeExecResult, error) {
	config := nodeConfig(ctx)
	instanceID, err := requiredStrategyUUID(asString(config["strategyInstanceId"]), "strategyInstanceId")
	if err != nil {
		return nil, permanentErr(err)
	}
	path := cfgStr(config, "candleOpenTimePath", "trigger.payload.openTime")
	openTime, err := parseOptionalUTCTime(asString(ctx.State.get(path)), "candleOpenTime")
	if err != nil || openTime == nil {
		return nil, permanentErr(invalidStrategy("candleOpenTimePath must resolve to UTC RFC3339Nano"))
	}
	mode := cfgStr(config, "executionMode", "sync")
	if mode != "sync" && mode != "async" {
		return nil, permanentErr(invalidStrategy("executionMode must be sync or async"))
	}
	timeoutSeconds := cfgInt(config, "timeoutSeconds", 30)
	if timeoutSeconds < 5 || timeoutSeconds > 300 {
		return nil, permanentErr(invalidStrategy("timeoutSeconds must be between 5 and 300"))
	}
	task, deduplicated, err := ctx.App.enqueueStrategyEvaluation(ctx.Ctx, instanceID, openTime.UTC())
	if err != nil {
		if errors.Is(err, ErrStrategyInstanceMissing) {
			return nil, permanentErr(err)
		}
		return nil, retryableErr(err)
	}
	if mode == "async" {
		output := strategyTaskOutput(task, deduplicated, nil)
		setNodeOutput(ctx, output)
		return &nodeExecResult{Output: output}, nil
	}

	deadline := time.NewTimer(time.Duration(timeoutSeconds) * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		switch task.Status {
		case "succeeded":
			var signal db.StrategySignal
			err := ctx.App.dbWithContext(ctx.Ctx).Where("id = ?", task.ID).First(&signal).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, permanentErr(fmt.Errorf("strategy task %s succeeded without a signal", task.ID))
			}
			if err != nil {
				return nil, retryableErr(err)
			}
			output := strategyTaskOutput(task, deduplicated, serializeStrategySignal(signal))
			setNodeOutput(ctx, output)
			return &nodeExecResult{Output: output}, nil
		case "failed", "canceled":
			return nil, permanentErr(fmt.Errorf("strategy task %s", task.Status))
		}
		select {
		case <-ctx.Ctx.Done():
			return nil, ctx.Ctx.Err()
		case <-deadline.C:
			return nil, retryableErr(fmt.Errorf("strategy task %s timed out", task.ID))
		case <-ticker.C:
			task, err = ctx.App.loadStrategyTask(ctx.Ctx, task.ID)
			if err != nil {
				return nil, retryableErr(err)
			}
		}
	}
}

func (a *App) enqueueStrategyEvaluation(ctx context.Context, instanceID uuid.UUID, candleOpenTime time.Time) (strategyTaskState, bool, error) {
	var enabled int64
	err := a.dbWithContext(ctx).Raw(`
SELECT COUNT(*)
FROM strategy_instances AS instance
JOIN strategy_versions AS version ON version.id = instance.strategy_version_id
WHERE instance.id = ? AND instance.is_enabled AND version.status = 'published'
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

func (a *App) loadStrategyTask(ctx context.Context, taskID string) (strategyTaskState, error) {
	var task strategyTaskState
	err := a.dbWithContext(ctx).Raw(
		"SELECT id, status, COALESCE(failure_category, '') AS failure_category, COALESCE(error_message, '') AS error_message FROM worker_tasks WHERE id = ?", taskID,
	).Scan(&task).Error
	if err == nil && task.ID == "" {
		err = errors.New("strategy task was not found")
	}
	return task, err
}

func strategyTaskOutput(task strategyTaskState, deduplicated bool, signal any) M {
	return M{"taskId": task.ID, "taskStatus": task.Status, "deduplicated": deduplicated, "signal": signal}
}
