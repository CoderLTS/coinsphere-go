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

var quantCodeStrategyConfigSchema = json.RawMessage(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema","type":"object",
  "properties":{
    "series":{"type":"array","title":"行情序列","minItems":1,"maxItems":8,"items":{"type":"object","properties":{"alias":{"type":"string","title":"别名","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"},"market":{"type":"string","title":"市场","enum":["spot","usdm"]},"instrument":{"type":"string","title":"品种","pattern":"^[A-Z0-9]{2,32}$"},"interval":{"type":"string","title":"周期","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"]},"lookback":{"type":"integer","title":"回看根数","minimum":1,"maximum":500}},"required":["alias","market","instrument","interval","lookback"],"additionalProperties":false},"default":[{"alias":"main","market":"spot","instrument":"BTCUSDT","interval":"1h","lookback":30}]},
    "parameters":{"type":"object","title":"参数（Decimal 使用字符串）","default":{"target":"1"}},
    "source":{"type":"string","title":"CEL 代码","minLength":1,"maxLength":4096,"default":"{\"long\": decimalGt(last(ohlcv.main.close), sma(ohlcv.main.close, 20)), \"target\": params.target}"},
    "booleanOutputs":{"type":"array","title":"Boolean 输出","items":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"},"minItems":1,"maxItems":32,"uniqueItems":true,"default":["long"]},
    "decimalOutputs":{"type":"array","title":"Decimal 输出","items":{"type":"string","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$"},"maxItems":32,"uniqueItems":true,"default":["target"]},
    "branchField":{"type":"string","title":"分支字段","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$","default":"long"}
  },"required":["series","parameters","source","booleanOutputs","decimalOutputs","branchField"],"additionalProperties":false
}`)

var quantCodeStrategyOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"booleans":{"type":"object","additionalProperties":{"type":"boolean"}},"decimals":{"type":"object","additionalProperties":{"type":"string","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true}},"ready":{"type":"boolean"},"branch":{"type":"string","enum":["true","false"]},"entered":{"type":"boolean"},"triggered":{"type":"boolean"},"evaluatedAt":{"type":"string","format":"date-time"}},"required":["booleans","decimals","ready","branch","entered","triggered","evaluatedAt"],"additionalProperties":false}`)

var quantPositionConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"市场","enum":["spot","usdm"],"default":"spot"},"targetMode":{"type":"string","title":"目标来源","enum":["fixed","input"],"enumLabels":["固定目标仓位","引用上游 Decimal"],"default":"fixed"},"fixedTarget":{"type":"string","title":"固定目标仓位","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true,"default":"1"},"decimalField":{"type":"string","title":"Decimal 字段名","pattern":"^[A-Za-z][A-Za-z0-9_]{0,63}$","default":"target"}},"required":["market","targetMode","fixedTarget","decimalField"],"additionalProperties":false}`)
var quantPositionInputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"target":{"type":"string","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true,"x-coinsphere-field-source":true},"evaluatedAt":{"type":"string","format":"date-time","x-coinsphere-field-source":true}},"required":["evaluatedAt"],"additionalProperties":false}`)
var quantPositionOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"targetPosition":{"type":"string","x-coinsphere-decimal":true},"evaluatedAt":{"type":"string","format":"date-time"},"sourceNodeInstanceId":{"type":"string"}},"required":["targetPosition","evaluatedAt","sourceNodeInstanceId"],"additionalProperties":false}`)

var quantOutputSignalInputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"candidates":{"type":"array","items":{"type":"object","properties":{"targetPosition":{"type":"string","x-coinsphere-decimal":true},"evaluatedAt":{"type":"string","format":"date-time"},"sourceNodeInstanceId":{"type":"string"}},"required":["targetPosition","evaluatedAt","sourceNodeInstanceId"],"additionalProperties":false}},"previousTargetPosition":{"type":"string","x-coinsphere-decimal":true},"nodeValues":{"type":"object"}},"additionalProperties":false}`)
var quantOutputSignalOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"signalId":{"type":"integer","minimum":0},"businessKey":{"type":"string"},"action":{"type":"string","enum":["buy","sell","hold"]},"previousTargetPosition":{"type":"string","x-coinsphere-decimal":true},"targetPosition":{"type":"string","x-coinsphere-decimal":true},"target":{"type":"string","x-coinsphere-decimal":true},"evaluatedAt":{"type":"string","format":"date-time"},"nodeValues":{"type":"object"},"branch":{"type":"string","enum":["realtime","unchanged"]}},"required":["signalId","businessKey","action","previousTargetPosition","targetPosition","target","evaluatedAt","nodeValues","branch"],"additionalProperties":false}`)

var quantBacktestStartConfigSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"市场","enum":["spot","usdm"],"default":"spot"},"instrument":{"type":"string","title":"主品种","pattern":"^[A-Z0-9]{2,32}$","default":"BTCUSDT"},"interval":{"type":"string","title":"主周期","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"],"default":"1h"}},"required":["market","instrument","interval"],"additionalProperties":false}`)
var quantBacktestStartInputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"startTime":{"type":"string","format":"date-time"},"endTime":{"type":"string","format":"date-time"},"initialCapital":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"feeRate":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true},"slippageRate":{"type":"string","pattern":"^[0-9]+(?:\\.[0-9]+)?$","x-coinsphere-decimal":true}},"required":["startTime","endTime","initialCapital","feeRate","slippageRate"],"additionalProperties":false}`)
var quantBacktestStartOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"branch":{"type":"string","enum":["each","completed"]},"backtestId":{"type":"integer"},"strategyId":{"type":"string"},"strategyVersion":{"type":"string"},"finalEquity":{"type":"string","x-coinsphere-decimal":true},"totalReturn":{"type":"string","x-coinsphere-decimal":true},"maxDrawdown":{"type":"string","x-coinsphere-decimal":true},"totalFees":{"type":"string","x-coinsphere-decimal":true},"tradeCount":{"type":"integer"},"candleCount":{"type":"integer"},"detailSha256":{"type":"string","pattern":"^[0-9a-f]{64}$"}},"required":["branch","backtestId","strategyId","strategyVersion","finalEquity","totalReturn","maxDrawdown","totalFees","tradeCount","candleCount","detailSha256"],"additionalProperties":false}`)

type quantPositionAction struct{}
type quantOutputSignalAction struct{ runtime *quantRuntime }

func validateQuantPositionConfig(raw json.RawMessage) error {
	var config struct {
		Market, TargetMode, FixedTarget, DecimalField string
	}
	if !decodeQuantStrict(raw, &config) || !quantCodeNamePattern.MatchString(config.DecimalField) ||
		config.TargetMode != "fixed" && config.TargetMode != "input" {
		return errors.New("quant position configuration is invalid")
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
	nodes := []struct {
		descriptor sdk.NodeDescriptor
		handler    sdk.ActionHandler
	}{
		{sdk.NodeDescriptor{
			Type: "official.quant.backtest_start", Version: "1.0.0", Kind: sdk.NodeKindAction,
			Branches: []string{"each", "completed"}, ConfigSchema: quantBacktestStartConfigSchema,
			UISchema:    json.RawMessage(`{"ui:order":["market","instrument","interval"]}`),
			InputSchema: quantBacktestStartInputSchema, OutputSchema: quantBacktestStartOutputSchema,
			Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
		}, quantWorkflowBacktestAction{runtime: q}},
		{sdk.NodeDescriptor{
			Type: "official.quant.code_strategy", Version: "1.0.0", Kind: sdk.NodeKindAction,
			Branches: []string{"true", "false"}, ConfigSchema: quantCodeStrategyConfigSchema,
			UISchema:     json.RawMessage(`{"ui:order":["series","parameters","source","booleanOutputs","decimalOutputs","branchField"],"source":{"ui:widget":"code","ui:language":"cel"}}`),
			InputSchema:  json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"eventTime":{"type":"string","format":"date-time"},"pathEntered":{"type":"boolean"}},"required":["eventTime"],"additionalProperties":false}`),
			OutputSchema: quantCodeStrategyOutputSchema, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
			ValidateConfig: validateQuantCodeStrategyConfig,
		}, &quantCodeStrategyAction{runtime: q}},
		{sdk.NodeDescriptor{
			Type: "official.quant.position", Version: "1.0.0", Kind: sdk.NodeKindAction,
			ConfigSchema: quantPositionConfigSchema, UISchema: json.RawMessage(`{"ui:order":["market","targetMode","fixedTarget"]}`),
			InputSchema: quantPositionInputSchema, OutputSchema: quantPositionOutputSchema,
			Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
			ValidateConfig: validateQuantPositionConfig,
		}, quantPositionAction{}},
		{sdk.NodeDescriptor{
			Type: "official.quant.output_signal", Version: "1.0.0", Kind: sdk.NodeKindAction,
			Branches: []string{"realtime", "unchanged"}, ConfigSchema: quantSeriesConfigSchema,
			UISchema:    json.RawMessage(`{"ui:order":["market","instrument","interval"]}`),
			InputSchema: quantOutputSignalInputSchema, OutputSchema: quantOutputSignalOutputSchema,
			Pool: sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
		}, quantOutputSignalAction{runtime: q}},
	}
	for _, node := range nodes {
		if err := registrar.Action(node.descriptor, node.handler); err != nil {
			return err
		}
	}
	return nil
}

func (quantPositionAction) Execute(_ context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config struct {
		Market, TargetMode, FixedTarget, DecimalField string
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
	config, err := parseQuantSeriesConfig(request.Config)
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
	if !decodeQuantStrict(request.Input, &input) || len(input.Candidates) == 0 {
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
	businessKey := fmt.Sprintf("quant:%s:%s:%s", config.Market, config.Instrument, request.NodeInstanceID)
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
				Config: request.Config, ExecutionMode: request.ExecutionMode,
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
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
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
