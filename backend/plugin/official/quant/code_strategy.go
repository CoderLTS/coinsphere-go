package quant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"time"

	"cel.dev/cel-go/cel"
	"cel.dev/cel-go/common/operators"
	"cel.dev/cel-go/common/types"
	"cel.dev/cel-go/common/types/ref"
	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

const maxQuantCodeBytes = 4096

var quantCodeNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,63}$`)

type quantCodeSeries struct {
	Alias      string `json:"alias"`
	Venue      string `json:"venue"`
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Interval   string `json:"interval"`
	Lookback   int    `json:"lookback"`
}

type quantCodeStrategyConfig struct {
	Series         []quantCodeSeries `json:"series"`
	Parameters     map[string]any    `json:"parameters"`
	Source         string            `json:"source"`
	BooleanOutputs []string          `json:"booleanOutputs"`
	DecimalOutputs []string          `json:"decimalOutputs"`
	BranchField    string            `json:"branchField"`
}

type quantCodeStrategyAction struct {
	runtime  *quantRuntime
	programs sync.Map
}

func validateQuantCodeStrategyConfig(raw json.RawMessage) error {
	config, err := parseQuantCodeStrategyConfig(raw)
	if err != nil {
		return err
	}
	_, ast, err := compileQuantCodeStrategy(config.Source)
	if err != nil {
		return err
	}
	return validateQuantCodeStrategyOutputs(config, ast)
}

func parseQuantCodeStrategyConfig(raw json.RawMessage) (quantCodeStrategyConfig, error) {
	var config quantCodeStrategyConfig
	if !decodeQuantStrict(raw, &config) || len(config.Series) < 1 || len(config.Series) > 8 ||
		len(strings.TrimSpace(config.Source)) < 1 || len(config.Source) > maxQuantCodeBytes {
		return config, errors.New("code strategy configuration is invalid")
	}
	aliases, outputs := map[string]bool{}, map[string]bool{}
	for index := range config.Series {
		series := &config.Series[index]
		series.Alias = strings.TrimSpace(series.Alias)
		if !quantCodeNamePattern.MatchString(series.Alias) || aliases[series.Alias] || series.Lookback < 1 || series.Lookback > 500 {
			return config, errors.New("code strategy series declaration is invalid")
		}
		if _, err := parseQuantSeriesConfig(mustMarshal(quantSeriesConfig{Venue: series.Venue, Market: series.Market, Instrument: series.Instrument, Interval: series.Interval})); err != nil {
			return config, err
		}
		aliases[series.Alias] = true
	}
	for _, name := range append(append([]string{}, config.BooleanOutputs...), config.DecimalOutputs...) {
		if !quantCodeNamePattern.MatchString(name) || outputs[name] {
			return config, errors.New("code strategy output declaration is invalid")
		}
		outputs[name] = true
	}
	if len(config.BooleanOutputs) == 0 || len(config.BooleanOutputs)+len(config.DecimalOutputs) > 64 || !containsQuantString(config.BooleanOutputs, config.BranchField) {
		return config, errors.New("code strategy branchField must reference a Boolean output")
	}
	if quantContainsJSONNumber(config.Parameters) {
		return config, errors.New("code strategy parameters must use Decimal strings instead of JSON numbers")
	}
	return config, nil
}

func (a *quantCodeStrategyAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	config, err := parseQuantCodeStrategyConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		EventTime   string `json:"eventTime"`
		PathEntered bool   `json:"pathEntered"`
	}
	if !decodeQuantStrict(request.Input, &input) {
		return sdk.ActionResult{}, errors.New("code strategy input is invalid")
	}
	evaluatedAt, err := parseQuantUTCTime(input.EventTime)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	ohlcv := map[string]any{}
	ready := true
	for _, declaration := range config.Series {
		candles, err := a.runtime.loadQuantCandlesThroughClose(ctx, quantSeriesConfig{
			Venue: declaration.Venue, Market: declaration.Market, Instrument: declaration.Instrument, Interval: declaration.Interval,
		}, evaluatedAt, declaration.Lookback)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		if len(candles) != declaration.Lookback {
			ready = false
			continue
		}
		if err := validateStrategyCandles(sdk.EvaluateRequest{
			Market: declaration.Market, Instrument: declaration.Instrument, Interval: declaration.Interval,
			Candles: quantSDKCandles(candles), EvaluatedAt: evaluatedAt,
		}); err != nil {
			return sdk.ActionResult{}, err
		}
		ohlcv[declaration.Alias] = quantCodeCandleSeries(candles)
	}
	booleans, decimals := map[string]bool{}, map[string]string{}
	branch := "false"
	if ready {
		program, err := a.program(config.Source)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		value, _, err := program.Eval(map[string]any{"ohlcv": ohlcv, "params": config.Parameters})
		if err != nil {
			return sdk.ActionResult{}, fmt.Errorf("evaluate code strategy CEL failed: %w", err)
		}
		booleans, decimals, err = quantCodeStrategyValues(config, value)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		if booleans[config.BranchField] {
			branch = "true"
		}
	}
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"booleans": booleans, "decimals": decimals, "ready": ready, "branch": branch,
		"entered": branch == "true", "triggered": branch == "true",
		"evaluatedAt": evaluatedAt.UTC().Format(time.RFC3339Nano),
	})}, nil
}

func (a *quantCodeStrategyAction) program(source string) (cel.Program, error) {
	if cached, ok := a.programs.Load(source); ok {
		return cached.(cel.Program), nil
	}
	env, ast, err := compileQuantCodeStrategy(source)
	if err != nil {
		return nil, err
	}
	program, err := env.Program(ast)
	if err != nil {
		return nil, errors.New("create code strategy CEL program failed")
	}
	actual, _ := a.programs.LoadOrStore(source, program)
	return actual.(cel.Program), nil
}

func validateQuantCodeStrategyOutputs(config quantCodeStrategyConfig, ast *cel.Ast) error {
	checked, err := cel.AstToCheckedExpr(ast)
	if err != nil {
		return errors.New("inspect code strategy CEL output failed")
	}
	aliases := make(map[string]bool, len(config.Series))
	for _, series := range config.Series {
		aliases[series.Alias] = true
	}
	if !quantCELReferencesValid(checked.Expr, aliases, config.Parameters) {
		return errors.New("code strategy CEL references an undeclared series, field, or parameter")
	}
	object := checked.Expr.GetStructExpr()
	if object == nil || object.GetMessageName() != "" {
		return errors.New("code strategy CEL must return an object literal")
	}
	type output struct {
		valueType *exprpb.Type
		expr      *exprpb.Expr
	}
	outputs := make(map[string]output, len(object.Entries))
	for _, entry := range object.Entries {
		key := entry.GetMapKey().GetConstExpr()
		if key == nil {
			return errors.New("code strategy CEL output names must be string literals")
		}
		name := key.GetStringValue()
		if name == "" {
			return errors.New("code strategy CEL output names must be string literals")
		}
		outputs[name] = output{valueType: checked.TypeMap[entry.Value.Id], expr: entry.Value}
	}
	for _, name := range config.BooleanOutputs {
		item := outputs[name]
		if item.valueType == nil || item.valueType.GetDyn() == nil && item.valueType.GetPrimitive() != exprpb.Type_BOOL ||
			item.valueType.GetDyn() != nil && !quantCELDynamicOutputCompatible(item.expr, config.Parameters, false) {
			return fmt.Errorf("code strategy Boolean output %q is missing or has an incompatible type", name)
		}
	}
	for _, name := range config.DecimalOutputs {
		item := outputs[name]
		if item.valueType == nil || item.valueType.GetDyn() == nil && item.valueType.GetPrimitive() != exprpb.Type_STRING ||
			item.valueType.GetDyn() != nil && !quantCELDynamicOutputCompatible(item.expr, config.Parameters, true) {
			return fmt.Errorf("code strategy Decimal output %q is missing or has an incompatible type", name)
		}
	}
	return nil
}

func quantCELDynamicOutputCompatible(expr *exprpb.Expr, parameters map[string]any, decimalOutput bool) bool {
	selection := expr.GetSelectExpr()
	if selection == nil || selection.Operand.GetIdentExpr().GetName() != "params" {
		return false
	}
	value, exists := parameters[selection.Field]
	if !exists {
		return false
	}
	if decimalOutput {
		text, ok := value.(string)
		_, err := decimal.NewFromString(text)
		return ok && err == nil
	}
	_, ok := value.(bool)
	return ok
}

func quantCodeStrategyValues(config quantCodeStrategyConfig, value ref.Val) (map[string]bool, map[string]string, error) {
	native, err := value.ConvertToNative(reflect.TypeOf(map[string]any{}))
	if err != nil {
		return nil, nil, errors.New("code strategy CEL must return an object")
	}
	result, _ := native.(map[string]any)
	booleans := make(map[string]bool, len(config.BooleanOutputs))
	decimals := make(map[string]string, len(config.DecimalOutputs))
	for _, name := range config.BooleanOutputs {
		item, ok := result[name].(bool)
		if !ok {
			return nil, nil, fmt.Errorf("code strategy Boolean output %q is missing or invalid", name)
		}
		booleans[name] = item
	}
	for _, name := range config.DecimalOutputs {
		item, ok := result[name].(string)
		decimalValue, decimalErr := decimal.NewFromString(item)
		if !ok || decimalErr != nil {
			return nil, nil, fmt.Errorf("code strategy Decimal output %q is missing or invalid", name)
		}
		decimals[name] = decimalValue.String()
	}
	return booleans, decimals, nil
}

func compileQuantCodeStrategy(source string) (*cel.Env, *cel.Ast, error) {
	env, err := cel.NewEnv(
		cel.Variable("ohlcv", cel.MapType(cel.StringType, cel.DynType)),
		cel.Variable("params", cel.MapType(cel.StringType, cel.DynType)),
		quantDecimalFunction("decimalAdd", func(left, right decimal.Decimal) decimal.Decimal { return left.Add(right) }),
		quantDecimalFunction("decimalSub", func(left, right decimal.Decimal) decimal.Decimal { return left.Sub(right) }),
		quantDecimalFunction("decimalMul", func(left, right decimal.Decimal) decimal.Decimal { return left.Mul(right) }),
		quantDecimalFunction("decimalDiv", func(left, right decimal.Decimal) decimal.Decimal {
			if right.IsZero() {
				return decimal.Zero
			}
			return left.DivRound(right, quantIndicatorScale)
		}),
		quantDecimalCompareFunction("decimalGt", func(value int) bool { return value > 0 }),
		quantDecimalCompareFunction("decimalGte", func(value int) bool { return value >= 0 }),
		quantDecimalCompareFunction("decimalLt", func(value int) bool { return value < 0 }),
		quantDecimalCompareFunction("decimalLte", func(value int) bool { return value <= 0 }),
		quantDecimalCompareFunction("decimalEq", func(value int) bool { return value == 0 }),
		cel.Function("sma", cel.Overload("quant_sma_string_list_int", []*cel.Type{cel.ListType(cel.StringType), cel.IntType}, cel.StringType,
			cel.BinaryBinding(quantCELSMA))),
		cel.Function("last", cel.Overload("quant_last_string_list", []*cel.Type{cel.ListType(cel.StringType)}, cel.StringType,
			cel.UnaryBinding(quantCELLast))),
	)
	if err != nil {
		return nil, nil, err
	}
	ast, issues := env.Compile(strings.TrimSpace(source))
	if issues != nil && issues.Err() != nil {
		return nil, nil, issues.Err()
	}
	checked, err := cel.AstToCheckedExpr(ast)
	if err != nil || quantCELUnsafe(checked.Expr) {
		return nil, nil, errors.New("code strategy CEL must not use floating point or native arithmetic")
	}
	return env, ast, nil
}

func quantDecimalFunction(name string, operation func(decimal.Decimal, decimal.Decimal) decimal.Decimal) cel.EnvOption {
	return cel.Function(name, cel.Overload("quant_"+name+"_string_string", []*cel.Type{cel.StringType, cel.StringType}, cel.StringType,
		cel.BinaryBinding(func(left, right ref.Val) ref.Val {
			leftValue, leftErr := decimal.NewFromString(string(left.(types.String)))
			rightValue, rightErr := decimal.NewFromString(string(right.(types.String)))
			if leftErr != nil || rightErr != nil || name == "decimalDiv" && rightValue.IsZero() {
				return types.NewErr("invalid Decimal operands")
			}
			return types.String(operation(leftValue, rightValue).String())
		})))
}

func quantDecimalCompareFunction(name string, predicate func(int) bool) cel.EnvOption {
	return cel.Function(name, cel.Overload("quant_"+name+"_string_string", []*cel.Type{cel.StringType, cel.StringType}, cel.BoolType,
		cel.BinaryBinding(func(left, right ref.Val) ref.Val {
			leftValue, leftErr := decimal.NewFromString(string(left.(types.String)))
			rightValue, rightErr := decimal.NewFromString(string(right.(types.String)))
			if leftErr != nil || rightErr != nil {
				return types.NewErr("invalid Decimal operands")
			}
			return types.Bool(predicate(leftValue.Cmp(rightValue)))
		})))
}

func quantCELSMA(listValue, periodValue ref.Val) ref.Val {
	native, err := listValue.ConvertToNative(reflect.TypeOf([]string{}))
	period := int(periodValue.(types.Int))
	values, _ := native.([]string)
	if err != nil || period < 1 || len(values) < period {
		return types.NewErr("invalid SMA arguments")
	}
	total := decimal.Zero
	for _, raw := range values[len(values)-period:] {
		value, err := decimal.NewFromString(raw)
		if err != nil {
			return types.NewErr("invalid SMA Decimal value")
		}
		total = total.Add(value)
	}
	return types.String(total.DivRound(decimal.NewFromInt(int64(period)), quantIndicatorScale).String())
}

func quantCELLast(listValue ref.Val) ref.Val {
	native, err := listValue.ConvertToNative(reflect.TypeOf([]string{}))
	values, _ := native.([]string)
	if err != nil || len(values) == 0 {
		return types.NewErr("invalid last arguments")
	}
	return types.String(values[len(values)-1])
}

func quantCELUnsafe(expr *exprpb.Expr) bool {
	if expr == nil {
		return false
	}
	if constant := expr.GetConstExpr(); constant != nil {
		if _, ok := constant.ConstantKind.(*exprpb.Constant_DoubleValue); ok {
			return true
		}
	}
	if call := expr.GetCallExpr(); call != nil {
		if !quantCELAllowedCall(call.Function) {
			return true
		}
		if quantCELUnsafe(call.Target) {
			return true
		}
		for _, argument := range call.Args {
			if quantCELUnsafe(argument) {
				return true
			}
		}
	}
	if selection := expr.GetSelectExpr(); selection != nil {
		return quantCELUnsafe(selection.Operand)
	}
	if list := expr.GetListExpr(); list != nil {
		for _, element := range list.Elements {
			if quantCELUnsafe(element) {
				return true
			}
		}
	}
	if object := expr.GetStructExpr(); object != nil {
		for _, entry := range object.Entries {
			if quantCELUnsafe(entry.GetMapKey()) || quantCELUnsafe(entry.Value) {
				return true
			}
		}
	}
	if comprehension := expr.GetComprehensionExpr(); comprehension != nil {
		return true
	}
	return false
}

func quantCELAllowedCall(name string) bool {
	switch name {
	case "decimalAdd", "decimalSub", "decimalMul", "decimalDiv", "decimalGt", "decimalGte", "decimalLt", "decimalLte", "decimalEq", "sma", "last",
		operators.Conditional, operators.LogicalAnd, operators.LogicalOr, operators.LogicalNot, operators.Equals, operators.NotEquals,
		operators.Index, operators.In:
		return true
	default:
		return false
	}
}

func quantCELReferencesValid(expr *exprpb.Expr, aliases map[string]bool, parameters map[string]any) bool {
	if expr == nil {
		return true
	}
	if call := expr.GetCallExpr(); call != nil && call.Function == operators.Index && len(call.Args) == 2 {
		root := quantCELReferenceRoot(call.Args[0])
		key := call.Args[1].GetConstExpr()
		if (root == "ohlcv" || root == "params") && (key == nil || key.GetStringValue() == "") {
			return false
		}
	}
	if root, path, ok := quantCELReferencePath(expr); ok {
		if root == "ohlcv" {
			if len(path) < 1 || len(path) > 2 || !aliases[path[0]] {
				return false
			}
			if len(path) == 2 && !containsQuantString([]string{"open", "high", "low", "close", "volume", "closeTime"}, path[1]) {
				return false
			}
		} else if root == "params" {
			if len(path) != 1 {
				return false
			}
			if _, exists := parameters[path[0]]; !exists {
				return false
			}
		}
	}
	if call := expr.GetCallExpr(); call != nil {
		if !quantCELReferencesValid(call.Target, aliases, parameters) {
			return false
		}
		for _, argument := range call.Args {
			if !quantCELReferencesValid(argument, aliases, parameters) {
				return false
			}
		}
	}
	if selection := expr.GetSelectExpr(); selection != nil {
		return quantCELReferencesValid(selection.Operand, aliases, parameters)
	}
	if list := expr.GetListExpr(); list != nil {
		for _, element := range list.Elements {
			if !quantCELReferencesValid(element, aliases, parameters) {
				return false
			}
		}
	}
	if object := expr.GetStructExpr(); object != nil {
		for _, entry := range object.Entries {
			if !quantCELReferencesValid(entry.GetMapKey(), aliases, parameters) || !quantCELReferencesValid(entry.Value, aliases, parameters) {
				return false
			}
		}
	}
	return expr.GetComprehensionExpr() == nil
}

func quantCELReferencePath(expr *exprpb.Expr) (string, []string, bool) {
	path := []string{}
	current := expr
	for current != nil {
		if selection := current.GetSelectExpr(); selection != nil {
			path = append([]string{selection.Field}, path...)
			current = selection.Operand
			continue
		}
		call := current.GetCallExpr()
		if call == nil || call.Function != operators.Index || len(call.Args) != 2 {
			break
		}
		key := call.Args[1].GetConstExpr()
		if key == nil || key.GetStringValue() == "" {
			return "", nil, false
		}
		path = append([]string{key.GetStringValue()}, path...)
		current = call.Args[0]
	}
	ident := current.GetIdentExpr()
	if ident == nil || ident.Name != "ohlcv" && ident.Name != "params" || len(path) == 0 {
		return "", nil, false
	}
	return ident.Name, path, true
}

func quantCELReferenceRoot(expr *exprpb.Expr) string {
	current := expr
	for current != nil {
		if selection := current.GetSelectExpr(); selection != nil {
			current = selection.Operand
			continue
		}
		call := current.GetCallExpr()
		if call == nil || call.Function != operators.Index || len(call.Args) != 2 {
			break
		}
		current = call.Args[0]
	}
	return current.GetIdentExpr().GetName()
}

func quantCodeCandleSeries(candles []quantCandle) map[string]any {
	result := map[string]any{"open": []string{}, "high": []string{}, "low": []string{}, "close": []string{}, "volume": []string{}, "closeTime": []string{}}
	for _, candle := range candles {
		result["open"] = append(result["open"].([]string), candle.Open.String())
		result["high"] = append(result["high"].([]string), candle.High.String())
		result["low"] = append(result["low"].([]string), candle.Low.String())
		result["close"] = append(result["close"].([]string), candle.Close.String())
		result["volume"] = append(result["volume"].([]string), candle.Volume.String())
		result["closeTime"] = append(result["closeTime"].([]string), candle.CloseTime.UTC().Format(time.RFC3339Nano))
	}
	return result
}

func quantContainsJSONNumber(value any) bool {
	switch typed := value.(type) {
	case float64:
		return true
	case []any:
		for _, item := range typed {
			if quantContainsJSONNumber(item) {
				return true
			}
		}
	case map[string]any:
		for _, item := range typed {
			if quantContainsJSONNumber(item) {
				return true
			}
		}
	}
	return false
}

func containsQuantString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

var _ sdk.ActionHandler = (*quantCodeStrategyAction)(nil)
