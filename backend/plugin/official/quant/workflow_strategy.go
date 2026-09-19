package quant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

var quantCodeStrategyConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"series":{"type":"array","title":"来源别名与回看范围","minItems":1,"maxItems":8,"items":{"type":"object","properties":{"alias":{"type":"string","title":"别名","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"},"lookback":{"type":"integer","title":"回看根数","minimum":1,"maximum":500}},"required":["alias","lookback"],"additionalProperties":false},"default":[{"alias":"main","lookback":30}]},"parameters":{"type":"object","title":"参数（小数使用字符串）","default":{"target":"1"}},"source":{"type":"string","title":"CEL 表达式","minLength":1,"maxLength":4096,"default":"{\"long\": decimalGt(last(ohlcv.main.close), sma(ohlcv.main.close, 20)), \"target\": params.target}"},"booleanOutputs":{"type":"array","title":"布尔输出","items":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"},"minItems":1,"maxItems":32,"uniqueItems":true,"default":["long"]},"decimalOutputs":{"type":"array","title":"小数输出","items":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"},"maxItems":32,"uniqueItems":true,"default":["target"]},"branchField":{"type":"string","title":"分支字段","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$","default":"long"}},"required":["series","parameters","source","booleanOutputs","decimalOutputs","branchField"],"additionalProperties":false}`)

var quantCodeStrategyOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"booleans":{"type":"object","additionalProperties":{"type":"boolean"}},"decimals":{"type":"object","additionalProperties":{"type":"string","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true}},"ready":{"type":"boolean"},"branch":{"type":"string","enum":["true","false","unavailable"]},"entered":{"type":"boolean"},"triggered":{"type":"boolean"},"evaluatedAt":{"type":"string","format":"date-time"}},"required":["booleans","decimals","ready","branch","entered","triggered","evaluatedAt"],"additionalProperties":false}`)

var quantPositionConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"市场","enum":["spot","usdm"],"default":"spot"},"targetMode":{"type":"string","title":"目标来源","enum":["fixed","input"],"enumLabels":["固定目标仓位","引用上游小数"],"default":"fixed"},"fixedTarget":{"type":"string","title":"固定目标仓位","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true,"default":"1","x-visible-when":{"targetMode":"fixed"}}},"required":["market","targetMode"],"additionalProperties":false,"allOf":[{"if":{"properties":{"targetMode":{"const":"fixed"}}},"then":{"required":["fixedTarget"]}}]}`)
var quantPositionInputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"target":{"type":"string","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true,"x-coinsphere-field-source":true},"evaluatedAt":{"type":"string","format":"date-time","x-coinsphere-field-source":true}},"required":["evaluatedAt"],"additionalProperties":false}`)
var quantPositionOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"targetPosition":{"type":"string","x-coinsphere-decimal":true},"evaluatedAt":{"type":"string","format":"date-time"},"sourceNodeInstanceId":{"type":"string"}},"required":["targetPosition","evaluatedAt","sourceNodeInstanceId"],"additionalProperties":false}`)

var quantOutputSignalInputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false}`)
var quantOutputSignalOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"signalId":{"type":"integer","minimum":0},"businessKey":{"type":"string"},"action":{"type":"string","enum":["buy","sell","hold"]},"previousTargetPosition":{"type":"string","x-coinsphere-decimal":true},"targetPosition":{"type":"string","x-coinsphere-decimal":true},"target":{"type":"string","x-coinsphere-decimal":true},"evaluatedAt":{"type":"string","format":"date-time"},"nodeValues":{"type":"object"},"branch":{"type":"string","enum":["realtime","unchanged"]}},"required":["signalId","businessKey","action","previousTargetPosition","targetPosition","target","evaluatedAt","nodeValues","branch"],"additionalProperties":false}`)

var quantBacktestStartConfigSchema = emptyObjectSchema
var quantBacktestStartInputSchema = emptyObjectSchema
var quantBacktestStartOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"branch":{"type":"string","enum":["each","completed"]},"backtestId":{"type":"integer"},"venue":{"type":"string"},"strategyId":{"type":"string"},"strategyVersion":{"type":"string"},"finalEquity":{"type":"string","x-coinsphere-decimal":true},"totalReturn":{"type":"string","x-coinsphere-decimal":true},"maxDrawdown":{"type":"string","x-coinsphere-decimal":true},"totalFees":{"type":"string","x-coinsphere-decimal":true},"tradeCount":{"type":"integer"},"candleCount":{"type":"integer"}},"required":["branch","backtestId","venue","strategyId","strategyVersion","finalEquity","totalReturn","maxDrawdown","totalFees","tradeCount","candleCount"],"additionalProperties":false}`)

type quantPositionAction struct{}
type quantOutputSignalAction struct{ runtime *quantRuntime }

// quantReplayTrigger is a draggable manual entry node. Core invokes its
// ActionHandler for a replay operation; Run only keeps the trigger dormant when
// a workflow is activated, so replay never creates a second realtime source.
type quantReplayTrigger struct{ runtime *quantRuntime }

func (t quantReplayTrigger) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	return (quantWorkflowBacktestAction{runtime: t.runtime}).Execute(ctx, request)
}

func (quantReplayTrigger) Run(ctx context.Context, _ sdk.TriggerRequest, _ sdk.Emitter) error {
	<-ctx.Done()
	return ctx.Err()
}

func validateQuantPositionConfig(raw json.RawMessage) error {
	var config struct {
		Market, TargetMode, FixedTarget string
	}
	if !decodeQuantStrict(raw, &config) ||
		config.TargetMode != "fixed" && config.TargetMode != "input" {
		return errors.New("quant position configuration is invalid")
	}
	if config.Market != "spot" && config.Market != "usdm" {
		return errors.New("invalid position market")
	}
	if config.TargetMode == "input" {
		return nil
	}
	target, err := decimal.NewFromString(config.FixedTarget)
	if err != nil || config.Market != "spot" && config.Market != "usdm" || config.TargetMode == "fixed" &&
		(config.Market == "spot" && (target.Sign() < 0 || target.GreaterThan(quantOne)) ||
			config.Market == "usdm" && (target.LessThan(decimal.NewFromInt(-1)) || target.GreaterThan(quantOne))) {
		return errors.New("quant target position is outside the market range")
	}
	return nil
}

func (q *quantRuntime) registerWorkflowStrategyNodes(registrar sdk.Registrar) error {
	replayDescriptor := quantNodeMeta(sdk.NodeDescriptor{
		Type: "official.quant.replay", Version: "1.0.0", Kind: sdk.NodeKindTrigger,
		Branches: []string{"each", "completed"}, ConfigSchema: quantBacktestStartConfigSchema,
		FrameSourcePorts: []string{"each"},
		UISchema:         json.RawMessage(`{"ui:order":[]}`), InputSchema: quantBacktestStartInputSchema, OutputSchema: quantBacktestStartOutputSchema,
		Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		Capabilities: sdk.NodeCapabilities{FrameDriver: true, ManualTrigger: true}, Role: sdk.NodeRoleEntry,
		ProfileSlots: []sdk.ProfileSlot{{Key: "market", Title: "行情 Profile", ProfileTypes: []string{"market.data"}, Required: true}, {Key: "backtest", Title: "回放参数 Profile", ProfileTypes: []string{"quant.backtest"}, Required: true}},
		// The same descriptor is a canvas trigger and an explicit frame-driven
		// operation. The operation carries the selected trigger's profiles and
		// result node configuration; replay itself needs no extra node config.
		OperationConfig: func(_ json.RawMessage, _ []json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
	}, "回放", "手动逐帧重放行情并复用实时策略下游。", "start", "#7c3aed", "history")
	if err := registrar.Trigger(replayDescriptor, quantReplayTrigger{runtime: q}); err != nil {
		return err
	}
	nodes := []struct {
		descriptor  sdk.NodeDescriptor
		handler     sdk.ActionHandler
		title       string
		description string
		category    string
		color       string
		icon        string
	}{
		{sdk.NodeDescriptor{
			Type: "official.quant.code_strategy", Version: "1.0.0", Kind: sdk.NodeKindAction,
			Branches: []string{"true", "false", "unavailable"}, ConfigSchema: quantCodeStrategyConfigSchema,
			UISchema:     json.RawMessage(`{"ui:order":["series","parameters","source","booleanOutputs","decimalOutputs","branchField"],"source":{"ui:widget":"code","ui:language":"cel"}}`),
			InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"context":{"type":"object","title":"行情上下文"}},"required":["context"],"additionalProperties":false}`),
			OutputSchema: quantCodeStrategyOutputSchema, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
			Capabilities:   sdk.NodeCapabilities{FrameSafe: true},
			ValidateConfig: validateQuantCodeStrategyConfig,
		}, &quantCodeStrategyAction{runtime: q}, "代码策略", "使用受限 CEL 对多行情序列执行确定性策略判断。", "strategy", "#2563eb", "code-xml"},
		{sdk.NodeDescriptor{
			Type: "official.quant.position", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: quantPositionConfigSchema, UISchema: json.RawMessage(`{"ui:order":["market","targetMode","fixedTarget"]}`),
			InputSchema: quantPositionInputSchema, OutputSchema: quantPositionOutputSchema,
			Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
			Capabilities:   sdk.NodeCapabilities{FrameSafe: true},
			ValidateConfig: validateQuantPositionConfig,
		}, quantPositionAction{}, "仓位计算", "将策略输出转换为通用目标仓位。", "strategy", "#2563eb", "percent"},
		{sdk.NodeDescriptor{
			Type: "official.quant.output_signal", Version: "1.0.0", Kind: sdk.NodeKindAction,
			Branches: []string{"realtime", "unchanged"}, ConfigSchema: emptyObjectSchema,
			UISchema:    json.RawMessage(`{"ui:order":[]}`),
			InputSchema: quantOutputSignalInputSchema, OutputSchema: quantOutputSignalOutputSchema,
			Pool: sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
			Capabilities: sdk.NodeCapabilities{Deterministic: true, FrameSafe: true, FrameResult: true},
			ProfileSlots: []sdk.ProfileSlot{{Key: "market", Title: "行情 Profile", ProfileTypes: []string{"market.data"}, Required: true}},
		}, quantOutputSignalAction{runtime: q}, "策略信号", "汇总目标仓位并持久化通用量化 Signal。", "strategy", "#0f766e", "radio"},
	}
	for _, node := range nodes {
		if err := registrar.Action(quantNodeMeta(node.descriptor, node.title, node.description, node.category, node.color, node.icon), node.handler); err != nil {
			return err
		}
	}
	return nil
}

func (quantPositionAction) Execute(_ context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config struct {
		Market, TargetMode, FixedTarget string
	}
	var input struct {
		Target, EvaluatedAt string
	}
	if !decodeQuantStrict(request.Config, &config) || !decodeQuantStrict(request.Input, &input) {
		return sdk.ActionResult{}, errors.New("quant position configuration or input is invalid")
	}
	targetText := config.FixedTarget
	if config.TargetMode == "input" {
		targetText = input.Target
	}
	target, err := decimal.NewFromString(targetText)
	if err != nil || config.TargetMode != "fixed" && config.TargetMode != "input" ||
		config.Market == "spot" && (target.Sign() < 0 || target.GreaterThan(quantOne)) ||
		config.Market == "usdm" && (target.LessThan(decimal.NewFromInt(-1)) || target.GreaterThan(quantOne)) {
		return sdk.ActionResult{}, errors.New("quant target position is outside the market range")
	}
	evaluatedAt, err := parseQuantUTCTime(input.EvaluatedAt)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"targetPosition": target.String(), "evaluatedAt": evaluatedAt.Format(time.RFC3339Nano),
		"sourceNodeInstanceId": request.NodeInstanceID,
	})}, nil
}

func (a quantOutputSignalAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	series, err := resolveQuantMarketProfile(ctx, request.Profiles, request.ProfileBindings, "market")
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		Candidates []struct {
			TargetPosition, EvaluatedAt, SourceNodeInstanceID string
		} `json:"candidates"`
		PreviousTargetPosition string         `json:"previousTargetPosition"`
		NodeValues             map[string]any `json:"nodeValues"`
	}
	for _, incoming := range request.Incoming {
		var candidate struct{ TargetPosition, EvaluatedAt, SourceNodeInstanceID string }
		if json.Unmarshal(incoming.Output, &candidate) == nil && candidate.TargetPosition != "" {
			if candidate.SourceNodeInstanceID == "" {
				candidate.SourceNodeInstanceID = incoming.NodeInstanceID
			}
			input.Candidates = append(input.Candidates, candidate)
		}
	}
	if len(request.FrameContext) > 0 {
		var frame struct {
			PreviousTargetPosition string `json:"previousTargetPosition"`
		}
		if json.Unmarshal(request.FrameContext, &frame) != nil {
			return sdk.ActionResult{}, errors.New("quant output signal frame context is invalid")
		}
		input.PreviousTargetPosition = frame.PreviousTargetPosition
	}
	if len(input.Candidates) == 0 {
		return sdk.ActionResult{}, errors.New("quant output signal requires at least one reached position")
	}
	target, err := decimal.NewFromString(input.Candidates[0].TargetPosition)
	if err != nil {
		return sdk.ActionResult{}, errors.New("quant output signal target is invalid")
	}
	evaluatedAt, err := parseQuantUTCTime(input.Candidates[0].EvaluatedAt)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	nodeValues := input.NodeValues
	if nodeValues == nil {
		nodeValues = map[string]any{}
	}
	for _, candidate := range input.Candidates {
		value, valueErr := decimal.NewFromString(candidate.TargetPosition)
		at, timeErr := parseQuantUTCTime(candidate.EvaluatedAt)
		if valueErr != nil || timeErr != nil || !value.Equal(target) || !at.Equal(evaluatedAt) {
			return sdk.ActionResult{}, fmt.Errorf("quant position conflict at %s", evaluatedAt.Format(time.RFC3339Nano))
		}
		nodeValues[candidate.SourceNodeInstanceID] = map[string]any{"targetPosition": value.String()}
	}
	workflowID, err := quantInt64(request.Revision.WorkflowID)
	if err != nil {
		return sdk.ActionResult{}, errors.New("quant workflow identity is invalid")
	}
	businessKey := fmt.Sprintf("quant:%s:%s:%s", series.Market, series.Instrument, request.NodeInstanceID)
	previous := decimal.Zero
	if request.ExecutionMode == sdk.ExecutionModeBacktestFrame {
		if input.PreviousTargetPosition != "" {
			previous, err = decimal.NewFromString(input.PreviousTargetPosition)
		}
	} else {
		previous, err = a.runtime.latestQuantWorkflowTarget(ctx, workflowID, request.NodeInstanceID, businessKey, request.OperationKey)
	}
	if err != nil {
		return sdk.ActionResult{}, err
	}
	action, branch := "hold", "unchanged"
	if target.GreaterThan(previous) {
		action = "buy"
	} else if target.LessThan(previous) {
		action = "sell"
	}
	signalID := int64(0)
	if !target.Equal(previous) {
		branch = "realtime"
		if request.ExecutionMode != sdk.ExecutionModeBacktestFrame {
			result, err := (quantSignalAction{runtime: a.runtime}).Execute(ctx, sdk.ActionRequest{
				Revision: request.Revision, NodeInstanceID: request.NodeInstanceID, OperationKey: request.OperationKey,
				Config: mustMarshal(series), ExecutionMode: request.ExecutionMode,
				Input: mustMarshal(map[string]any{"strategyId": "workflow", "strategyVersion": request.Revision.RevisionID,
					"target": target.String(), "evaluatedAt": evaluatedAt.Format(time.RFC3339Nano), "businessKey": businessKey}),
			})
			if err != nil {
				return sdk.ActionResult{}, err
			}
			var persisted struct {
				SignalID int64 `json:"signalId"`
			}
			_ = json.Unmarshal(result.Output, &persisted)
			signalID = persisted.SignalID
		}
	}
	return sdk.ActionResult{Port: branch, Output: mustMarshal(map[string]any{
		"signalId": signalID, "businessKey": businessKey, "action": action,
		"previousTargetPosition": previous.String(), "targetPosition": target.String(), "target": target.String(),
		"evaluatedAt": evaluatedAt.Format(time.RFC3339Nano), "nodeValues": nodeValues, "branch": branch,
	})}, nil
}

func (q *quantRuntime) latestQuantWorkflowTarget(ctx context.Context, workflowID int64, nodeID, businessKey, excludedOperation string) (decimal.Decimal, error) {
	var signal quantSignal
	err := q.db.WithContext(ctx).Where(
		"workflow_id = ? AND node_instance_id = ? AND business_key = ? AND operation_key <> ?",
		workflowID, nodeID, businessKey, excludedOperation,
	).Order("evaluated_at DESC, id DESC").First(&signal).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return decimal.Zero, nil
	}
	if err != nil {
		return decimal.Zero, errors.New("load previous Quant workflow target failed")
	}
	return signal.Target, nil
}

var _ sdk.ActionHandler = quantPositionAction{}
var _ sdk.ActionHandler = quantOutputSignalAction{}
