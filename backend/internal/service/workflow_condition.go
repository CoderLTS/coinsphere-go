package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
)

type workflowConditionConfig struct {
	Match string                  `json:"match"`
	Rules []workflowConditionRule `json:"rules"`
}

type workflowConditionRule struct {
	FieldPath []string        `json:"fieldPath"`
	Operator  string          `json:"operator"`
	Value     json.RawMessage `json:"value,omitempty"`
}

var errConditionUnavailable = errors.New("condition data unavailable")

func validateWorkflowCondition(config workflowConditionConfig) error {
	if config.Match != "all" && config.Match != "any" {
		return errors.New("condition match must be all or any")
	}
	if len(config.Rules) == 0 || len(config.Rules) > 64 {
		return errors.New("condition requires 1 to 64 rules")
	}
	for _, rule := range config.Rules {
		if len(rule.FieldPath) == 0 || len(rule.FieldPath) > 12 {
			return errors.New("condition field path is invalid")
		}
		for _, part := range rule.FieldPath {
			if strings.TrimSpace(part) == "" {
				return errors.New("condition field path is invalid")
			}
		}
		if !containsString([]string{"eq", "ne", "gt", "gte", "lt", "lte", "contains", "true", "false", "exists"}, rule.Operator) {
			return errors.New("condition operator is invalid")
		}
		if !containsString([]string{"true", "false", "exists"}, rule.Operator) && !json.Valid(rule.Value) {
			return errors.New("condition comparison value is required")
		}
	}
	return nil
}

func evaluateWorkflowCondition(config workflowConditionConfig, input map[string]any) (bool, error) {
	result := config.Match == "all"
	for _, rule := range config.Rules {
		value, exists := workflowFieldValue(input, rule.FieldPath)
		// 数据缺失与不满足是不同结果；组合条件必须等所有输入完成有效评估。
		if !exists || value == nil {
			return false, errConditionUnavailable
		}
		var expected any
		if len(rule.Value) > 0 {
			if err := json.Unmarshal(rule.Value, &expected); err != nil {
				return false, err
			}
		}
		var matched bool
		switch rule.Operator {
		case "exists":
			matched = true
		case "true", "false":
			boolean, ok := value.(bool)
			if !ok {
				return false, errConditionUnavailable
			}
			matched = boolean == (rule.Operator == "true")
		case "eq", "ne":
			matched = reflect.DeepEqual(value, expected)
			if rule.Operator == "ne" {
				matched = !matched
			}
		case "contains":
			text, ok := value.(string)
			needle, valid := expected.(string)
			if !ok || !valid {
				return false, errConditionUnavailable
			}
			matched = strings.Contains(text, needle)
		default:
			left, err := decimal.NewFromString(fmt.Sprint(value))
			if err != nil {
				return false, errConditionUnavailable
			}
			right, err := decimal.NewFromString(fmt.Sprint(expected))
			if err != nil {
				return false, errConditionUnavailable
			}
			cmp := left.Cmp(right)
			switch rule.Operator {
			case "gt":
				matched = cmp > 0
			case "gte":
				matched = cmp >= 0
			case "lt":
				matched = cmp < 0
			case "lte":
				matched = cmp <= 0
			}
		}
		if config.Match == "all" {
			result = result && matched
		} else {
			result = result || matched
		}
	}
	return result, nil
}

func executeWorkflowCondition(_ context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var config workflowConditionConfig
	var input map[string]any
	if json.Unmarshal(request.Config, &config) != nil || validateWorkflowCondition(config) != nil || json.Unmarshal(request.Input, &input) != nil {
		return sdk.ActionResult{}, errors.New("invalid condition")
	}
	matched, err := evaluateWorkflowCondition(config, input)
	if errors.Is(err, errConditionUnavailable) {
		return sdk.ActionResult{Port: "unavailable", Output: mustJSON(map[string]any{"available": false, "matched": nil})}, nil
	}
	if err != nil {
		return sdk.ActionResult{}, err
	}
	port := "false"
	if matched {
		port = "true"
	}
	return sdk.ActionResult{Port: port, Output: mustJSON(map[string]any{"available": true, "matched": matched})}, nil
}
func workflowOutputPorts(desc sdk.NodeDescriptor) []string {
	_, ports := workflowPorts(desc)
	if len(ports) == 0 {
		return []string{"out"}
	}
	return ports
}

// executeWorkflowControl keeps stateful routing concerns explicit in nodes
// instead of encoding them in edge expressions.
func executeWorkflowControl(ctx context.Context, nodeType string, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var input map[string]any
	if err := json.Unmarshal(request.Input, &input); err != nil || input == nil {
		return sdk.ActionResult{}, errors.New("control input is invalid")
	}
	readPath := func(path []string) (any, bool) {
		if len(path) == 0 {
			if value, ok := input["matched"]; ok {
				return value, true
			}
			return input["value"], input["value"] != nil
		}
		return workflowFieldValue(input, path)
	}
	state, err := request.State.Load(ctx)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var previous map[string]any
	if len(state) > 0 && json.Unmarshal(state, &previous) != nil {
		return sdk.ActionResult{}, errors.New("control state is invalid")
	}
	if previous == nil {
		previous = map[string]any{}
	}
	save := func(value map[string]any) error { return request.State.Save(ctx, mustJSON(value)) }

	switch nodeType {
	case "core.switch":
		var config struct {
			FieldPath []string `json:"fieldPath"`
			Cases     []struct {
				Value json.RawMessage `json:"value"`
				Port  string          `json:"port"`
			} `json:"cases"`
			DefaultPort string `json:"defaultPort"`
		}
		if json.Unmarshal(request.Config, &config) != nil || len(config.Cases) == 0 {
			return sdk.ActionResult{}, errors.New("switch config is invalid")
		}
		value, ok := readPath(config.FieldPath)
		if !ok {
			return sdk.ActionResult{Port: "default", Output: mustJSON(map[string]any{"available": false, "value": nil})}, nil
		}
		for _, item := range config.Cases {
			var expected any
			if json.Unmarshal(item.Value, &expected) == nil && reflect.DeepEqual(value, expected) {
				return sdk.ActionResult{Port: item.Port, Output: mustJSON(map[string]any{"available": true, "value": value})}, nil
			}
		}
		port := config.DefaultPort
		if port == "" {
			port = "default"
		}
		return sdk.ActionResult{Port: port, Output: mustJSON(map[string]any{"available": true, "value": value})}, nil
	case "core.join":
		merged := make(map[string]any, len(request.Incoming))
		for _, incoming := range request.Incoming {
			var value any
			if json.Unmarshal(incoming.Output, &value) != nil {
				return sdk.ActionResult{}, errors.New("join input is invalid")
			}
			merged[incoming.NodeInstanceID] = value
		}
		return sdk.ActionResult{Output: mustJSON(merged)}, nil
	case "core.transition":
		var config struct {
			FieldPath []string `json:"fieldPath"`
			Direction string   `json:"direction"`
		}
		if json.Unmarshal(request.Config, &config) != nil {
			return sdk.ActionResult{}, errors.New("transition config is invalid")
		}
		value, ok := readPath(config.FieldPath)
		if !ok {
			return sdk.ActionResult{Port: "unavailable", Output: mustJSON(map[string]any{"available": false})}, nil
		}
		current, isBool := value.(bool)
		if !isBool {
			return sdk.ActionResult{}, errors.New("transition value must be boolean")
		}
		old, hadOld := previous["value"].(bool)
		changed := !hadOld || old != current
		if err := save(map[string]any{"value": current, "evaluatedAt": request.TriggeredAt.UTC()}); err != nil {
			return sdk.ActionResult{}, err
		}
		port := "false"
		if config.Direction == "changed" && changed {
			port = "changed"
		} else if current {
			port = "true"
		}
		if config.Direction == "false_to_true" && !(changed && !old && current) {
			return sdk.ActionResult{Port: "false", Skip: true, Output: mustJSON(map[string]any{"changed": changed, "value": current})}, nil
		}
		if config.Direction == "true_to_false" && !(changed && old && !current) {
			return sdk.ActionResult{Port: "false", Skip: true, Output: mustJSON(map[string]any{"changed": changed, "value": current})}, nil
		}
		if config.Direction == "first" && hadOld {
			return sdk.ActionResult{Port: "false", Skip: true, Output: mustJSON(map[string]any{"changed": false, "value": current})}, nil
		}
		return sdk.ActionResult{Port: port, Output: mustJSON(map[string]any{"changed": changed, "value": current})}, nil
	case "core.debounce":
		var config struct {
			FieldPath []string `json:"fieldPath"`
			Count     int      `json:"count"`
		}
		if json.Unmarshal(request.Config, &config) != nil || config.Count < 1 {
			return sdk.ActionResult{}, errors.New("debounce config is invalid")
		}
		value, ok := readPath(config.FieldPath)
		matched, isBool := value.(bool)
		if !ok || !isBool {
			return sdk.ActionResult{Skip: true, Output: mustJSON(map[string]any{"available": false})}, nil
		}
		count, _ := previous["count"].(float64)
		if matched {
			count++
		} else {
			count = 0
		}
		if err := save(map[string]any{"count": count}); err != nil {
			return sdk.ActionResult{}, err
		}
		return sdk.ActionResult{Skip: int(count) < config.Count, Output: mustJSON(map[string]any{"matched": matched, "count": int(count)})}, nil
	case "core.throttle":
		var config struct {
			WindowSeconds int `json:"windowSeconds"`
		}
		if json.Unmarshal(request.Config, &config) != nil || config.WindowSeconds < 1 {
			return sdk.ActionResult{}, errors.New("throttle config is invalid")
		}
		last, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(previous["lastAt"]))
		if !last.IsZero() && request.TriggeredAt.Sub(last) < time.Duration(config.WindowSeconds)*time.Second {
			return sdk.ActionResult{Skip: true, Output: mustJSON(map[string]any{"allowed": false, "lastAt": last})}, nil
		}
		if err := save(map[string]any{"lastAt": request.TriggeredAt.UTC().Format(time.RFC3339Nano)}); err != nil {
			return sdk.ActionResult{}, err
		}
		return sdk.ActionResult{Output: mustJSON(map[string]any{"allowed": true})}, nil
	case "core.time_window":
		var config struct {
			Start string `json:"start"`
			End   string `json:"end"`
		}
		if json.Unmarshal(request.Config, &config) != nil {
			return sdk.ActionResult{}, errors.New("time window config is invalid")
		}
		start, e1 := time.Parse("15:04", config.Start)
		end, e2 := time.Parse("15:04", config.End)
		if e1 != nil || e2 != nil {
			return sdk.ActionResult{}, errors.New("time window requires HH:mm")
		}
		current := request.TriggeredAt.UTC()
		minute := current.Hour()*60 + current.Minute()
		begin := start.Hour()*60 + start.Minute()
		finish := end.Hour()*60 + end.Minute()
		allowed := minute >= begin && minute <= finish
		if begin > finish {
			allowed = minute >= begin || minute <= finish
		}
		port := "false"
		if allowed {
			port = "true"
		}
		return sdk.ActionResult{Port: port, Output: mustJSON(map[string]any{"allowed": allowed, "evaluatedAt": current})}, nil
	case "core.deduplicate":
		var config struct {
			FieldPath []string `json:"fieldPath"`
		}
		if json.Unmarshal(request.Config, &config) != nil {
			return sdk.ActionResult{}, errors.New("deduplicate config is invalid")
		}
		value, ok := readPath(config.FieldPath)
		if !ok {
			return sdk.ActionResult{Skip: true, Output: mustJSON(map[string]any{"deduplicated": false})}, nil
		}
		key := fmt.Sprint(value)
		if previous["key"] == key {
			return sdk.ActionResult{Skip: true, Output: mustJSON(map[string]any{"deduplicated": true, "key": key})}, nil
		}
		if err := save(map[string]any{"key": key}); err != nil {
			return sdk.ActionResult{}, err
		}
		return sdk.ActionResult{Output: mustJSON(map[string]any{"deduplicated": false, "key": key})}, nil
	default:
		return sdk.ActionResult{}, errors.New("unknown workflow control node")
	}
}

func workflowConditionDescriptors() []sdk.NodeDescriptor {
	object := json.RawMessage(`{"type":"object"}`)
	return []sdk.NodeDescriptor{
		{Type: "core.condition", Version: "1.0.0", Title: "条件", Description: "组合字段判断，区分满足、不满足和数据不可用", Category: "control", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless, Branches: []string{"true", "false", "unavailable"},
			Capabilities: sdk.NodeCapabilities{Deterministic: true, FrameSafe: true}, Color: "#d97706", Icon: "split", Width: 220, Height: 72,
			InputSchema: object, OutputSchema: json.RawMessage(`{"type":"object","properties":{"available":{"type":"boolean"},"matched":{"type":["boolean","null"]}},"required":["available","matched"],"additionalProperties":false}`),
			ConfigSchema: json.RawMessage(`{"type":"object","properties":{"match":{"type":"string","title":"组合方式","enum":["all","any"],"default":"all"},"rules":{"type":"array","title":"规则","minItems":1,"maxItems":64,"items":{"type":"object","properties":{"fieldPath":{"type":"array","items":{"type":"string"},"minItems":1},"operator":{"type":"string","enum":["eq","ne","gt","gte","lt","lte","contains","true","false","exists"]},"value":{}},"required":["fieldPath","operator"],"additionalProperties":false}}},"required":["match","rules"],"additionalProperties":false}`), UISchema: json.RawMessage(`{"ui:order":["match","rules"]}`),
			ValidateConfig: func(raw json.RawMessage) error {
				var config workflowConditionConfig
				if err := json.Unmarshal(raw, &config); err != nil {
					return err
				}
				return validateWorkflowCondition(config)
			}},
		{Type: "core.expression", Version: "1.0.0", Title: "高级表达式", Description: "使用 CEL 计算自定义值", Category: "data", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless,
			Capabilities: sdk.NodeCapabilities{Deterministic: true, Stateless: true, FrameSafe: true}, Color: "#0891b2", Icon: "code", Width: 220, Height: 72,
			InputSchema: object, OutputSchema: json.RawMessage(`{"type":"object","properties":{"value":{}},"required":["value"]}`),
			ConfigSchema: json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string","title":"CEL 表达式","minLength":1,"maxLength":4096}},"required":["expression"],"additionalProperties":false}`), UISchema: json.RawMessage(`{"expression":{"ui:widget":"textarea"}}`),
			ValidateConfig: func(raw json.RawMessage) error {
				var config struct {
					Expression string `json:"expression"`
				}
				if err := json.Unmarshal(raw, &config); err != nil {
					return err
				}
				_, err := compileWorkflowCEL(config.Expression)
				return err
			}},
		{Type: "core.switch", Version: "1.0.0", Title: "分流", Description: "按结构化值选择一条路径", Category: "control", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless, Branches: []string{"case1", "case2", "default"}, Capabilities: sdk.NodeCapabilities{Deterministic: true, Stateless: true, FrameSafe: true}, Color: "#d97706", Icon: "git-branch", Width: 220, Height: 72,
			InputSchema: object, OutputSchema: json.RawMessage(`{"type":"object","properties":{"available":{"type":"boolean"},"value":{}},"required":["available"]}`), ConfigSchema: json.RawMessage(`{"type":"object","properties":{"fieldPath":{"type":"array","items":{"type":"string"}},"cases":{"type":"array","minItems":1,"maxItems":2,"items":{"type":"object","properties":{"value":{},"port":{"type":"string","enum":["case1","case2"]}},"required":["value","port"],"additionalProperties":false}},"defaultPort":{"type":"string","enum":["default"]}},"required":["cases"],"additionalProperties":false}`), UISchema: json.RawMessage(`{"ui:order":["fieldPath","cases","defaultPort"]}`)},
		{Type: "core.join", Version: "1.0.0", Title: "汇聚", Description: "在当前 Run 内合并上游结果", Category: "control", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless, Capabilities: sdk.NodeCapabilities{Deterministic: true, Stateless: true, FrameSafe: true}, Color: "#d97706", Icon: "git-merge", Width: 220, Height: 72,
			InputSchema: object, OutputSchema: object, ConfigSchema: json.RawMessage(`{"type":"object","properties":{"mode":{"type":"string","enum":["all","any"],"default":"all"}},"additionalProperties":false}`), UISchema: json.RawMessage(`{"ui:order":["mode"]}`)},
		{Type: "core.transition", Version: "1.0.0", Title: "状态变化", Description: "只在指定状态变化时放行", Category: "control", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StatePersistent, Branches: []string{"true", "false", "changed", "unavailable"}, Capabilities: sdk.NodeCapabilities{Deterministic: true, FrameSafe: true}, Color: "#d97706", Icon: "arrow-right-left", Width: 220, Height: 72,
			InputSchema: object, OutputSchema: object, ConfigSchema: json.RawMessage(`{"type":"object","properties":{"fieldPath":{"type":"array","items":{"type":"string"}},"direction":{"type":"string","enum":["first","false_to_true","true_to_false","changed"],"default":"false_to_true"}},"required":["direction"],"additionalProperties":false}`), UISchema: json.RawMessage(`{"ui:order":["fieldPath","direction"]}`)},
		{Type: "core.debounce", Version: "1.0.0", Title: "连续命中", Description: "连续达到指定次数后放行", Category: "control", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StatePersistent, Capabilities: sdk.NodeCapabilities{Deterministic: true, FrameSafe: true}, Color: "#d97706", Icon: "list-checks", Width: 220, Height: 72,
			InputSchema: object, OutputSchema: object, ConfigSchema: json.RawMessage(`{"type":"object","properties":{"fieldPath":{"type":"array","items":{"type":"string"}},"count":{"type":"integer","minimum":1,"maximum":1000,"default":2}},"required":["count"],"additionalProperties":false}`), UISchema: json.RawMessage(`{"ui:order":["fieldPath","count"]}`)},
		{Type: "core.throttle", Version: "1.0.0", Title: "节流", Description: "在时间窗口内最多放行一次", Category: "control", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StatePersistent, Capabilities: sdk.NodeCapabilities{Deterministic: true, FrameSafe: true}, Color: "#d97706", Icon: "timer-reset", Width: 220, Height: 72,
			InputSchema: object, OutputSchema: object, ConfigSchema: json.RawMessage(`{"type":"object","properties":{"windowSeconds":{"type":"integer","minimum":1,"maximum":31536000,"default":3600}},"required":["windowSeconds"],"additionalProperties":false}`), UISchema: json.RawMessage(`{"ui:order":["windowSeconds"]}`)},
		{Type: "core.time_window", Version: "1.0.0", Title: "时间窗口", Description: "按 UTC 时间段放行", Category: "control", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StateStateless, Branches: []string{"true", "false"}, Capabilities: sdk.NodeCapabilities{Deterministic: true, Stateless: true, FrameSafe: true}, Color: "#d97706", Icon: "calendar-clock", Width: 220, Height: 72,
			InputSchema: object, OutputSchema: object, ConfigSchema: json.RawMessage(`{"type":"object","properties":{"start":{"type":"string","pattern":"^[0-2][0-9]:[0-5][0-9]$"},"end":{"type":"string","pattern":"^[0-2][0-9]:[0-5][0-9]$"}},"required":["start","end"],"additionalProperties":false}`), UISchema: json.RawMessage(`{"ui:order":["start","end"]}`)},
		{Type: "core.deduplicate", Version: "1.0.0", Title: "去重", Description: "根据业务键阻止重复事件", Category: "control", Kind: sdk.NodeKindAction, Pool: sdk.PoolCompute, SideEffect: sdk.SideEffectNone, State: sdk.StatePersistent, Capabilities: sdk.NodeCapabilities{Deterministic: true, FrameSafe: true}, Color: "#d97706", Icon: "copy-check", Width: 220, Height: 72,
			InputSchema: object, OutputSchema: object, ConfigSchema: json.RawMessage(`{"type":"object","properties":{"fieldPath":{"type":"array","items":{"type":"string"},"minItems":1}},"required":["fieldPath"],"additionalProperties":false}`), UISchema: json.RawMessage(`{"ui:order":["fieldPath"]}`)},
	}
}
