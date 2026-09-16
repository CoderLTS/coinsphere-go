package sdk

import (
	"errors"
	"fmt"
	"strings"
)

// VisualCondition 可视化条件定义
type VisualCondition struct {
	Type string `json:"type"` // "comparison", "logic", "threshold", "indicator"

	// 比较条件
	Comparison *ComparisonCondition `json:"comparison,omitempty"`

	// 逻辑条件
	Logic *LogicCondition `json:"logic,omitempty"`

	// 阈值条件
	Threshold *ThresholdCondition `json:"threshold,omitempty"`

	// 指标条件
	Indicator *IndicatorCondition `json:"indicator,omitempty"`
}

// ComparisonCondition 比较条件
type ComparisonCondition struct {
	Left     ValueSource `json:"left"`
	Operator string      `json:"operator"` // ">", "<", "==", ">=", "<=", "!=", "contains"
	Right    ValueSource `json:"right"`
}

// LogicCondition 逻辑条件
type LogicCondition struct {
	Operator   string            `json:"operator"` // "AND", "OR", "NOT"
	Conditions []VisualCondition `json:"conditions"`
}

// ThresholdCondition 阈值条件
type ThresholdCondition struct {
	Field     string `json:"field"`           // 字段名
	Direction string `json:"direction"`       // "above", "below", "between"
	Value     string `json:"value"`           // 阈值
	Upper     string `json:"upper,omitempty"` // 上限（between 模式）
}

// IndicatorCondition 指标条件
type IndicatorCondition struct {
	Indicator  string                 `json:"indicator"` // "rsi", "macd", "volume"
	Signal     string                 `json:"signal"`    // "oversold", "golden_cross", "spike"
	Parameters map[string]interface{} `json:"parameters,omitempty"`
}

// ValueSource 值来源
type ValueSource struct {
	Kind  string      `json:"kind"` // "field", "constant", "indicator", "expression"
	Field string      `json:"field,omitempty"`
	Value interface{} `json:"value,omitempty"`
	Expr  string      `json:"expr,omitempty"`
}

// CompileToCEL 编译为 CEL 表达式
func (vc VisualCondition) CompileToCEL() (string, error) {
	switch vc.Type {
	case "comparison":
		return vc.compileComparison()
	case "logic":
		return vc.compileLogic()
	case "threshold":
		return vc.compileThreshold()
	case "indicator":
		return vc.compileIndicator()
	default:
		return "", fmt.Errorf("unknown condition type: %s", vc.Type)
	}
}

func (vc VisualCondition) compileComparison() (string, error) {
	if vc.Comparison == nil {
		return "", errors.New("comparison is nil")
	}

	left, err := vc.Comparison.Left.toCEL()
	if err != nil {
		return "", fmt.Errorf("compile left value: %w", err)
	}

	right, err := vc.Comparison.Right.toCEL()
	if err != nil {
		return "", fmt.Errorf("compile right value: %w", err)
	}

	// 处理 Decimal 比较（如果字段名包含 decimal 或 price/amount 等）
	if vc.needsDecimalComparison(vc.Comparison.Left, vc.Comparison.Right) {
		switch vc.Comparison.Operator {
		case ">":
			return fmt.Sprintf("decimalGt(%s, %s)", left, right), nil
		case "<":
			return fmt.Sprintf("decimalLt(%s, %s)", left, right), nil
		case "==":
			return fmt.Sprintf("decimalEq(%s, %s)", left, right), nil
		case ">=":
			return fmt.Sprintf("decimalGte(%s, %s)", left, right), nil
		case "<=":
			return fmt.Sprintf("decimalLte(%s, %s)", left, right), nil
		case "!=":
			return fmt.Sprintf("!decimalEq(%s, %s)", left, right), nil
		}
	}

	// 普通比较
	return fmt.Sprintf("%s %s %s", left, vc.Comparison.Operator, right), nil
}

func (vc VisualCondition) needsDecimalComparison(left, right ValueSource) bool {
	// 检查字段名是否涉及金额、价格等需要 Decimal 比较的类型
	decimalFields := []string{"price", "amount", "target", "position", "decimal", "value"}

	checkField := func(field string) bool {
		lowerField := strings.ToLower(field)
		for _, df := range decimalFields {
			if strings.Contains(lowerField, df) {
				return true
			}
		}
		return false
	}

	if left.Kind == "field" && checkField(left.Field) {
		return true
	}
	if right.Kind == "field" && checkField(right.Field) {
		return true
	}
	return false
}

func (vc VisualCondition) compileLogic() (string, error) {
	if vc.Logic == nil || len(vc.Logic.Conditions) == 0 {
		return "", errors.New("logic conditions empty")
	}

	var parts []string
	for _, cond := range vc.Logic.Conditions {
		cel, err := cond.CompileToCEL()
		if err != nil {
			return "", err
		}
		parts = append(parts, fmt.Sprintf("(%s)", cel))
	}

	switch vc.Logic.Operator {
	case "AND":
		return strings.Join(parts, " && "), nil
	case "OR":
		return strings.Join(parts, " || "), nil
	case "NOT":
		if len(parts) != 1 {
			return "", errors.New("NOT operator requires exactly one condition")
		}
		return fmt.Sprintf("!(%s)", parts[0]), nil
	default:
		return "", fmt.Errorf("unknown logic operator: %s", vc.Logic.Operator)
	}
}

func (vc VisualCondition) compileThreshold() (string, error) {
	if vc.Threshold == nil {
		return "", errors.New("threshold is nil")
	}

	field := vc.Threshold.Field
	value := vc.Threshold.Value

	switch vc.Threshold.Direction {
	case "above":
		return fmt.Sprintf("%s > %s", field, value), nil
	case "below":
		return fmt.Sprintf("%s < %s", field, value), nil
	case "between":
		if vc.Threshold.Upper == "" {
			return "", errors.New("between direction requires upper value")
		}
		return fmt.Sprintf("(%s >= %s) && (%s <= %s)", field, value, field, vc.Threshold.Upper), nil
	default:
		return "", fmt.Errorf("unknown threshold direction: %s", vc.Threshold.Direction)
	}
}

func (vc VisualCondition) compileIndicator() (string, error) {
	if vc.Indicator == nil {
		return "", errors.New("indicator is nil")
	}

	// 根据指标类型和信号生成 CEL
	switch vc.Indicator.Indicator {
	case "rsi":
		return vc.compileRSISignal()
	case "macd":
		return vc.compileMACDSignal()
	case "volume":
		return vc.compileVolumeSignal()
	case "bollinger":
		return vc.compileBollingerSignal()
	default:
		return "", fmt.Errorf("unknown indicator: %s", vc.Indicator.Indicator)
	}
}

func (vc VisualCondition) compileRSISignal() (string, error) {
	switch vc.Indicator.Signal {
	case "oversold":
		threshold := "30"
		if t, ok := vc.Indicator.Parameters["threshold"].(string); ok {
			threshold = t
		}
		return fmt.Sprintf("rsi < %s", threshold), nil
	case "overbought":
		threshold := "70"
		if t, ok := vc.Indicator.Parameters["threshold"].(string); ok {
			threshold = t
		}
		return fmt.Sprintf("rsi > %s", threshold), nil
	default:
		return "", fmt.Errorf("unknown RSI signal: %s", vc.Indicator.Signal)
	}
}

func (vc VisualCondition) compileMACDSignal() (string, error) {
	switch vc.Indicator.Signal {
	case "golden_cross":
		return "macd.dif > macd.dea && macd.prevDif <= macd.prevDea", nil
	case "death_cross":
		return "macd.dif < macd.dea && macd.prevDif >= macd.prevDea", nil
	case "dif_above_zero":
		return "macd.dif > 0", nil
	case "dif_below_zero":
		return "macd.dif < 0", nil
	default:
		return "", fmt.Errorf("unknown MACD signal: %s", vc.Indicator.Signal)
	}
}

func (vc VisualCondition) compileVolumeSignal() (string, error) {
	switch vc.Indicator.Signal {
	case "spike":
		multiplier := "2"
		if m, ok := vc.Indicator.Parameters["multiplier"].(string); ok {
			multiplier = m
		}
		return fmt.Sprintf("volume > avgVolume * %s", multiplier), nil
	default:
		return "", fmt.Errorf("unknown volume signal: %s", vc.Indicator.Signal)
	}
}

func (vc VisualCondition) compileBollingerSignal() (string, error) {
	switch vc.Indicator.Signal {
	case "break_upper":
		return "close > bollingerBands.upper", nil
	case "break_lower":
		return "close < bollingerBands.lower", nil
	default:
		return "", fmt.Errorf("unknown Bollinger signal: %s", vc.Indicator.Signal)
	}
}

func (vs ValueSource) toCEL() (string, error) {
	switch vs.Kind {
	case "field":
		if vs.Field == "" {
			return "", errors.New("field name is empty")
		}
		return vs.Field, nil
	case "constant":
		if vs.Value == nil {
			return "", errors.New("constant value is nil")
		}
		// 字符串需要加引号
		if s, ok := vs.Value.(string); ok {
			return fmt.Sprintf("%q", s), nil
		}
		return fmt.Sprintf("%v", vs.Value), nil
	case "expression":
		if vs.Expr == "" {
			return "", errors.New("expression is empty")
		}
		return vs.Expr, nil
	default:
		return "", fmt.Errorf("unknown value source kind: %s", vs.Kind)
	}
}

// Validate 验证可视化条件
func (vc VisualCondition) Validate() error {
	switch vc.Type {
	case "comparison":
		if vc.Comparison == nil {
			return errors.New("comparison condition is required")
		}
		if vc.Comparison.Operator == "" {
			return errors.New("comparison operator is required")
		}
	case "logic":
		if vc.Logic == nil {
			return errors.New("logic condition is required")
		}
		if len(vc.Logic.Conditions) == 0 {
			return errors.New("logic conditions cannot be empty")
		}
		for i, cond := range vc.Logic.Conditions {
			if err := cond.Validate(); err != nil {
				return fmt.Errorf("logic condition[%d]: %w", i, err)
			}
		}
	case "threshold":
		if vc.Threshold == nil {
			return errors.New("threshold condition is required")
		}
		if vc.Threshold.Field == "" {
			return errors.New("threshold field is required")
		}
	case "indicator":
		if vc.Indicator == nil {
			return errors.New("indicator condition is required")
		}
		if vc.Indicator.Indicator == "" {
			return errors.New("indicator type is required")
		}
	default:
		return fmt.Errorf("unknown condition type: %s", vc.Type)
	}
	return nil
}
