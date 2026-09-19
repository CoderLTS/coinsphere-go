package quant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
)

const (
	quantIndicatorScale = int32(32)
)

var (
	quantHundred        = decimal.NewFromInt(100)
	quantFifty          = decimal.NewFromInt(50)
	quantDecimalPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)
)

var quantIndicatorInputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"context":{"type":"object","title":"行情上下文"},"closeTime":{"type":"string","format":"date-time"}},"additionalProperties":false}`)
var quantIndicatorOutputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"available":{"type":"boolean"},"matched":{"type":["boolean","null"]},"evaluatedAt":{"type":"string"},"source":{"type":"string"},"summary":{"type":"string"},"venue":{"type":"string"},"market":{"type":"string"},"instrument":{"type":"string"},"interval":{"type":"string"},"indicator":{"type":"string"},"candleCloseTime":{"type":"string"},"value":{"type":"object","additionalProperties":{"type":"string"}}},"required":["available","matched","evaluatedAt","source","summary","venue","market","instrument","interval","indicator","candleCloseTime","value"],"additionalProperties":false}`)

type quantIndicatorDefinition struct {
	Indicator string
	Title     string
	Icon      string
}

var quantIndicatorDefinitions = []quantIndicatorDefinition{
	{Indicator: "volume_spike", Title: "放量判断", Icon: "chart-column-big"},
	{Indicator: "price_change", Title: "价格波动判断", Icon: "trending-up"},
	{Indicator: "macd", Title: "MACD 判断", Icon: "chart-no-axes-combined"},
	{Indicator: "kdj", Title: "KDJ 判断", Icon: "chart-spline"},
	{Indicator: "rsi", Title: "RSI 判断", Icon: "gauge"},
	{Indicator: "bollinger", Title: "布林带判断", Icon: "chart-candlestick"},
}

var quantIndicatorParameterSchemas = map[string]string{
	"volume_spike": `{"type":"object","title":"参数","properties":{"lookback":{"type":"integer","title":"均量周期","minimum":1,"maximum":500,"default":20},"multiplier":{"type":"string","title":"放量倍数","pattern":"^[0-9]+(?:\\.[0-9]+)?$","default":"2","x-coinsphere-decimal":true}},"required":["lookback","multiplier"],"additionalProperties":false,"default":{"lookback":20,"multiplier":"2"}}`,
	"price_change": `{"type":"object","title":"参数","properties":{"lookback":{"type":"integer","title":"K 线数量","minimum":1,"maximum":500,"default":1},"mode":{"type":"string","title":"判断方式","enum":["rise","fall","absolute","amplitude","since_day_start"],"enumLabels":["上涨","下跌","绝对涨跌幅","最高最低振幅","当日涨跌幅(UTC+0)"],"default":"absolute"},"threshold":{"type":"string","title":"阈值（%）","pattern":"^[0-9]+(?:\\.[0-9]+)?$","default":"1","x-coinsphere-decimal":true}},"required":["lookback","mode","threshold"],"additionalProperties":false,"default":{"lookback":1,"mode":"absolute","threshold":"1"}}`,
	"macd":         `{"type":"object","title":"参数","properties":{"fastPeriod":{"type":"integer","title":"快线周期","minimum":1,"maximum":100,"default":12},"slowPeriod":{"type":"integer","title":"慢线周期","minimum":2,"maximum":200,"default":26},"signalPeriod":{"type":"integer","title":"信号周期","minimum":1,"maximum":100,"default":9},"signal":{"type":"string","title":"判断规则","enum":["golden_cross","death_cross","dif_above_zero","dif_below_zero"],"enumLabels":["金叉","死叉","DIF 位于零轴上方","DIF 位于零轴下方"],"default":"golden_cross"}},"required":["fastPeriod","slowPeriod","signalPeriod","signal"],"additionalProperties":false,"default":{"fastPeriod":12,"slowPeriod":26,"signalPeriod":9,"signal":"golden_cross"}}`,
	"kdj":          `{"type":"object","title":"参数","properties":{"period":{"type":"integer","title":"周期","minimum":2,"maximum":200,"default":9},"kSmoothing":{"type":"integer","title":"K 平滑周期","minimum":1,"maximum":50,"default":3},"dSmoothing":{"type":"integer","title":"D 平滑周期","minimum":1,"maximum":50,"default":3},"signal":{"type":"string","title":"判断规则","enum":["golden_cross","death_cross","k_above","k_below","d_above","d_below","j_above","j_below"],"enumLabels":["K/D 金叉","K/D 死叉","K 高于阈值","K 低于阈值","D 高于阈值","D 低于阈值","J 高于阈值","J 低于阈值"],"default":"golden_cross"},"threshold":{"type":"string","title":"阈值","x-visible-when":{"signal":["k_above","k_below","d_above","d_below","j_above","j_below"]},"pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","default":"80","x-coinsphere-decimal":true}},"required":["period","kSmoothing","dSmoothing","signal"],"additionalProperties":false,"default":{"period":9,"kSmoothing":3,"dSmoothing":3,"signal":"golden_cross","threshold":"80"}}`,
	"rsi":          `{"type":"object","title":"参数","properties":{"period":{"type":"integer","title":"周期","minimum":2,"maximum":200,"default":14},"direction":{"type":"string","title":"判断规则","enum":["above","below"],"enumLabels":["高于阈值","低于阈值"],"default":"below"},"threshold":{"type":"string","title":"阈值","pattern":"^[0-9]+(?:\\.[0-9]+)?$","default":"30","x-coinsphere-decimal":true}},"required":["period","direction","threshold"],"additionalProperties":false,"default":{"period":14,"direction":"below","threshold":"30"}}`,
	"bollinger":    `{"type":"object","title":"参数","properties":{"period":{"type":"integer","title":"周期","minimum":2,"maximum":500,"default":20},"multiplier":{"type":"string","title":"标准差倍数","pattern":"^[0-9]+(?:\\.[0-9]+)?$","default":"2","x-coinsphere-decimal":true},"signal":{"type":"string","title":"判断规则","enum":["close_above_upper","close_below_lower"],"enumLabels":["收盘价突破上轨","收盘价跌破下轨"],"default":"close_above_upper"}},"required":["period","multiplier","signal"],"additionalProperties":false,"default":{"period":20,"multiplier":"2","signal":"close_above_upper"}}`,
}

func quantIndicatorConfigSchema() json.RawMessage {
	variants := make([]any, 0, len(quantIndicatorDefinitions))
	for _, definition := range quantIndicatorDefinitions {
		variants = append(variants, map[string]any{
			"if":   map[string]any{"properties": map[string]any{"indicator": map[string]any{"const": definition.Indicator}}, "required": []string{"indicator"}},
			"then": map[string]any{"properties": map[string]any{"parameters": json.RawMessage(quantIndicatorParameterSchemas[definition.Indicator])}},
		})
	}
	return mustMarshal(map[string]any{"$schema": "https://json-schema.org/draft/2020-12/schema", "type": "object",
		"properties": map[string]any{"indicator": map[string]any{"type": "string", "title": "指标", "enum": []string{"volume_spike", "price_change", "macd", "kdj", "rsi", "bollinger"}, "enumLabels": []string{"放量", "价格波动", "MACD", "KDJ", "RSI", "布林带"}, "default": "rsi"}, "parameters": map[string]any{"type": "object", "title": "指标参数", "default": map[string]any{"period": 14, "direction": "below", "threshold": "30"}}},
		"required":   []string{"indicator", "parameters"}, "additionalProperties": false, "allOf": variants})
}

type quantIndicatorAction struct {
	runtime *quantRuntime
}

type quantIndicatorConfig struct {
	Source string
	Leaf   *quantIndicatorLeaf
}

type quantIndicatorLeaf struct {
	Name       string
	Interval   string
	Indicator  string
	Parameters quantIndicatorParameters
}

type quantIndicatorParameters struct {
	Lookback          int
	Multiplier        decimal.Decimal
	Mode              string
	Threshold         decimal.Decimal
	FastPeriod        int
	SlowPeriod        int
	SignalPeriod      int
	Period            int
	KSmoothing        int
	DSmoothing        int
	Signal            string
	StandardDeviation decimal.Decimal
}

type quantIndicatorPoint struct {
	Ready           bool
	Matched         bool
	CandleCloseTime string
	Values          map[string]string
	Summary         string
}

func (a quantIndicatorAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	config, err := parseQuantIndicatorConfig(request.Config)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		Context   *quantMarketContext `json:"context"`
		CloseTime string              `json:"closeTime"`
	}
	if !decodeQuantStrict(request.Input, &input) {
		return sdk.ActionResult{}, errors.New("invalid market context")
	}
	series, err := resolveQuantMarketProfile(ctx, request.Profiles, request.ProfileBindings, "market")
	if err != nil {
		return sdk.ActionResult{}, err
	}
	evaluatedAt := request.TriggeredAt
	if input.Context != nil && input.Context.AsOf != "" {
		evaluatedAt, err = parseQuantUTCTime(input.Context.AsOf)
	} else if input.CloseTime != "" {
		evaluatedAt, err = parseQuantUTCTime(input.CloseTime)
	}
	if err != nil {
		return sdk.ActionResult{}, err
	}
	config.Leaf.Interval = series.Interval
	lookback := quantIndicatorLookback(config.Leaf)
	candles, err := a.runtime.loadQuantCandlesThroughClose(ctx, series, evaluatedAt, lookback+1)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	point := quantIndicatorPoint{Values: map[string]string{}}
	problem := ""
	if problem == "" && (len(candles) == 0 || !quantLatestCandleAvailable(candles, evaluatedAt, series.Interval)) {
		problem = "应有闭合 K 线尚未到达"
	}
	if problem == "" {
		if err := validateStrategyCandles(sdk.EvaluateRequest{Market: series.Market, Instrument: series.Instrument, Interval: series.Interval, Candles: quantSDKCandles(candles), EvaluatedAt: evaluatedAt}); err != nil {
			problem = "行情存在缺口"
		} else {
			point, err = evaluateQuantIndicatorLeaf(config.Leaf, candles)
			if err != nil {
				return sdk.ActionResult{}, err
			}
			if !point.Ready {
				problem = "历史数据不足"
			}
		}
	}
	if problem != "" && request.ExecutionMode != sdk.ExecutionModeBacktestFrame {
		deadline := request.TriggeredAt.Add(30 * time.Second)
		if time.Now().UTC().Before(deadline) {
			return sdk.ActionResult{Output: quantIndicatorOutput(config, series, evaluatedAt, point, problem), Wait: &sdk.WaitRequest{BlockFollowingRuns: true, Key: request.OperationKey, Until: deadline, WakeAt: time.Now().UTC().Add(time.Second), Data: json.RawMessage(`{}`)}}, nil
		}
	}
	port := "false"
	if problem != "" || !point.Ready {
		port = "unavailable"
	} else if point.Matched {
		port = "true"
	}
	return sdk.ActionResult{Port: port, Output: quantIndicatorOutput(config, series, evaluatedAt, point, problem)}, nil
}

func quantIndicatorOutput(config quantIndicatorConfig, series quantSeriesConfig, asOf time.Time, point quantIndicatorPoint, problem string) json.RawMessage {
	var matched any = point.Matched
	if problem != "" || !point.Ready {
		matched = nil
		point.Ready = false
		point.Summary = config.Source + ": " + problem
	}
	if point.Values == nil {
		point.Values = map[string]string{}
	}
	return mustMarshal(map[string]any{"available": point.Ready, "matched": matched, "evaluatedAt": asOf.Format(time.RFC3339Nano), "source": config.Source, "summary": point.Summary, "venue": series.Venue, "market": series.Market, "instrument": series.Instrument, "interval": series.Interval, "indicator": config.Leaf.Indicator, "candleCloseTime": point.CandleCloseTime, "value": point.Values})
}

func parseQuantIndicatorConfig(raw json.RawMessage) (quantIndicatorConfig, error) {
	var payload struct {
		Indicator  string          `json:"indicator"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if !decodeQuantStrict(raw, &payload) || payload.Indicator == "" {
		return quantIndicatorConfig{}, errors.New("invalid indicator configuration")
	}
	parameters, err := parseQuantIndicatorParameters(payload.Indicator, payload.Parameters)
	if err != nil {
		return quantIndicatorConfig{}, err
	}
	return quantIndicatorConfig{Source: "market", Leaf: &quantIndicatorLeaf{Name: payload.Indicator, Indicator: payload.Indicator, Parameters: parameters}}, nil
}

func parseQuantIndicatorParameters(indicator string, raw json.RawMessage) (quantIndicatorParameters, error) {
	parameters := quantIndicatorParameters{}
	switch indicator {
	case "volume_spike":
		value := struct {
			Lookback   int    `json:"lookback"`
			Multiplier string `json:"multiplier"`
		}{Lookback: 20, Multiplier: "2"}
		if !decodeQuantStrict(raw, &value) || value.Lookback < 1 || value.Lookback > 500 {
			return parameters, errors.New("volume spike parameters are invalid")
		}
		multiplier, err := parseQuantConditionDecimal(value.Multiplier, decimal.Zero, decimal.NewFromInt(1000), false)
		if err != nil {
			return parameters, errors.New("volume spike multiplier is invalid")
		}
		parameters.Lookback, parameters.Multiplier = value.Lookback, multiplier
	case "price_change":
		value := struct {
			Lookback  int    `json:"lookback"`
			Mode      string `json:"mode"`
			Threshold string `json:"threshold"`
		}{Lookback: 5, Mode: "absolute", Threshold: "5"}
		if !decodeQuantStrict(raw, &value) || value.Lookback < 1 || value.Lookback > 500 ||
			value.Mode != "rise" && value.Mode != "fall" && value.Mode != "absolute" && value.Mode != "amplitude" && value.Mode != "since_day_start" {
			return parameters, errors.New("price change parameters are invalid")
		}
		threshold, err := parseQuantConditionDecimal(value.Threshold, decimal.Zero, decimal.NewFromInt(10000), true)
		if err != nil {
			return parameters, errors.New("price change threshold is invalid")
		}
		parameters.Lookback, parameters.Mode, parameters.Threshold = value.Lookback, value.Mode, threshold
	case "macd":
		value := struct {
			FastPeriod   int    `json:"fastPeriod"`
			SlowPeriod   int    `json:"slowPeriod"`
			SignalPeriod int    `json:"signalPeriod"`
			Signal       string `json:"signal"`
		}{FastPeriod: 12, SlowPeriod: 26, SignalPeriod: 9, Signal: "golden_cross"}
		if !decodeQuantStrict(raw, &value) || value.FastPeriod < 1 || value.FastPeriod > 100 || value.SlowPeriod <= value.FastPeriod || value.SlowPeriod > 200 || value.SignalPeriod < 1 || value.SignalPeriod > 100 ||
			value.Signal != "golden_cross" && value.Signal != "death_cross" && value.Signal != "dif_above_zero" && value.Signal != "dif_below_zero" {
			return parameters, errors.New("mACD parameters are invalid")
		}
		parameters.FastPeriod, parameters.SlowPeriod, parameters.SignalPeriod, parameters.Signal = value.FastPeriod, value.SlowPeriod, value.SignalPeriod, value.Signal
	case "kdj":
		value := struct {
			Period     int    `json:"period"`
			KSmoothing int    `json:"kSmoothing"`
			DSmoothing int    `json:"dSmoothing"`
			Signal     string `json:"signal"`
			Threshold  string `json:"threshold"`
		}{Period: 9, KSmoothing: 3, DSmoothing: 3, Signal: "golden_cross", Threshold: "80"}
		if !decodeQuantStrict(raw, &value) || value.Period < 2 || value.Period > 200 || value.KSmoothing < 1 || value.KSmoothing > 50 || value.DSmoothing < 1 || value.DSmoothing > 50 || !quantKDJSignal(value.Signal) {
			return parameters, errors.New("kDJ parameters are invalid")
		}
		threshold, err := parseQuantConditionDecimal(value.Threshold, decimal.NewFromInt(-1000), decimal.NewFromInt(1000), true)
		if err != nil {
			return parameters, errors.New("kDJ threshold is invalid")
		}
		parameters.Period, parameters.KSmoothing, parameters.DSmoothing = value.Period, value.KSmoothing, value.DSmoothing
		parameters.Signal, parameters.Threshold = value.Signal, threshold
	case "rsi":
		value := struct {
			Period    int    `json:"period"`
			Direction string `json:"direction"`
			Threshold string `json:"threshold"`
		}{Period: 14, Direction: "below", Threshold: "30"}
		if !decodeQuantStrict(raw, &value) || value.Period < 2 || value.Period > 200 || value.Direction != "above" && value.Direction != "below" {
			return parameters, errors.New("rSI parameters are invalid")
		}
		threshold, err := parseQuantConditionDecimal(value.Threshold, decimal.Zero, quantHundred, true)
		if err != nil {
			return parameters, errors.New("rSI threshold is invalid")
		}
		parameters.Period, parameters.Mode, parameters.Threshold = value.Period, value.Direction, threshold
	case "bollinger":
		value := struct {
			Period     int    `json:"period"`
			Multiplier string `json:"multiplier"`
			Signal     string `json:"signal"`
		}{Period: 20, Multiplier: "2", Signal: "close_above_upper"}
		if !decodeQuantStrict(raw, &value) || value.Period < 2 || value.Period > 500 || value.Signal != "close_above_upper" && value.Signal != "close_below_lower" {
			return parameters, errors.New("bollinger parameters are invalid")
		}
		multiplier, err := parseQuantConditionDecimal(value.Multiplier, decimal.Zero, decimal.NewFromInt(20), false)
		if err != nil {
			return parameters, errors.New("bollinger multiplier is invalid")
		}
		parameters.Period, parameters.StandardDeviation, parameters.Signal = value.Period, multiplier, value.Signal
	default:
		return parameters, errors.New("quant indicator type is unsupported")
	}
	return parameters, nil
}

func decodeQuantStrict(raw json.RawMessage, destination any) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil {
		return false
	}
	return errors.Is(decoder.Decode(&struct{}{}), io.EOF)
}

func parseQuantConditionDecimal(raw string, minimum, maximum decimal.Decimal, allowMinimum bool) (decimal.Decimal, error) {
	if !quantDecimalPattern.MatchString(raw) {
		return decimal.Zero, errors.New("invalid Decimal")
	}
	value, err := decimal.NewFromString(raw)
	if err != nil || value.GreaterThan(maximum) || allowMinimum && value.LessThan(minimum) || !allowMinimum && !value.GreaterThan(minimum) {
		return decimal.Zero, errors.New("decimal is outside limits")
	}
	return value, nil
}

func quantKDJSignal(signal string) bool {
	switch signal {
	case "golden_cross", "death_cross", "k_above", "k_below", "d_above", "d_below", "j_above", "j_below":
		return true
	default:
		return false
	}
}

func quantIndicatorLookback(leaf *quantIndicatorLeaf) int {
	p := leaf.Parameters
	switch leaf.Indicator {
	case "volume_spike":
		return p.Lookback + 1
	case "price_change":
		if p.Mode == "since_day_start" {
			duration := quantIntervals[leaf.Interval]
			if duration <= 0 || duration > 24*time.Hour {
				return 1
			}
			return int((24*time.Hour + duration - 1) / duration)
		}
		return p.Lookback
	case "macd":
		if p.Signal == "golden_cross" || p.Signal == "death_cross" {
			return p.SlowPeriod + p.SignalPeriod
		}
		return p.SlowPeriod
	case "kdj":
		lookback := p.Period + max(p.KSmoothing, p.DSmoothing)
		if p.Signal == "golden_cross" || p.Signal == "death_cross" {
			lookback++
		}
		return lookback
	case "rsi":
		return p.Period + 1
	case "bollinger":
		return p.Period
	default:
		return 1
	}
}

func evaluateQuantIndicatorLeaf(leaf *quantIndicatorLeaf, candles []quantCandle) (quantIndicatorPoint, error) {
	point := quantIndicatorPoint{Values: map[string]string{}, Summary: leaf.Name + "：历史数据不足"}
	if len(candles) < quantIndicatorLookback(leaf) {
		return point, nil
	}
	point.Ready = true
	point.CandleCloseTime = candles[len(candles)-1].CloseTime.UTC().Format(time.RFC3339Nano)
	p := leaf.Parameters
	switch leaf.Indicator {
	case "volume_spike":
		previous := candles[len(candles)-p.Lookback-1 : len(candles)-1]
		average := decimal.Zero
		for _, candle := range previous {
			average = average.Add(candle.Volume)
		}
		average = average.DivRound(decimal.NewFromInt(int64(len(previous))), quantIndicatorScale)
		ratio := decimal.Zero
		if average.Sign() > 0 {
			ratio = candles[len(candles)-1].Volume.DivRound(average, quantIndicatorScale)
		}
		point.Matched = ratio.GreaterThanOrEqual(p.Multiplier)
		point.Values = map[string]string{"volume": candles[len(candles)-1].Volume.String(), "averageVolume": average.String(), "ratio": ratio.String()}
		point.Summary = fmt.Sprintf("%s：成交量倍数 %s，阈值 %s", leaf.Name, ratio.String(), p.Multiplier.String())
	case "price_change":
		if p.Mode == "since_day_start" {
			last := candles[len(candles)-1]
			openDay := last.OpenTime.UTC()
			dayStart := time.Date(openDay.Year(), openDay.Month(), openDay.Day(), 0, 0, 0, 0, time.UTC)
			start := 0
			for index, candle := range candles {
				if !candle.OpenTime.UTC().Before(dayStart) {
					start = index
					break
				}
			}
			first := candles[start]
			dayChange := last.Close.Sub(first.Open).DivRound(first.Open, quantIndicatorScale).Mul(quantHundred).Round(2)
			intervalChange := last.Close.Sub(last.Open).DivRound(last.Open, quantIndicatorScale).Mul(quantHundred).Round(2)
			point.Matched = dayChange.Abs().GreaterThanOrEqual(p.Threshold)
			point.Values = map[string]string{"changePercent": dayChange.String(), "intervalChangePercent": intervalChange.String()}
			point.Summary = fmt.Sprintf("%s：本周期涨跌幅 %s%%，当日涨跌幅(UTC+0) %s%%", leaf.Name, intervalChange.String(), dayChange.String())
			break
		}
		first, last := candles[0], candles[len(candles)-1]
		change := last.Close.Sub(first.Open).DivRound(first.Open, quantIndicatorScale).Mul(quantHundred)
		high, low := candles[0].High, candles[0].Low
		for _, candle := range candles[1:] {
			high = decimal.Max(high, candle.High)
			low = decimal.Min(low, candle.Low)
		}
		amplitude := high.Sub(low).DivRound(low, quantIndicatorScale).Mul(quantHundred)
		switch p.Mode {
		case "rise":
			point.Matched = change.GreaterThanOrEqual(p.Threshold)
		case "fall":
			point.Matched = change.LessThanOrEqual(p.Threshold.Neg())
		case "absolute":
			point.Matched = change.Abs().GreaterThanOrEqual(p.Threshold)
		case "amplitude":
			point.Matched = amplitude.GreaterThanOrEqual(p.Threshold)
		}
		point.Values = map[string]string{"changePercent": change.String(), "amplitudePercent": amplitude.String()}
		point.Summary = fmt.Sprintf("%s：涨跌幅 %s%%，振幅 %s%%", leaf.Name, change.String(), amplitude.String())
	case "macd":
		differences, signals := quantMACD(candles, p.FastPeriod, p.SlowPeriod, p.SignalPeriod)
		if len(differences) == 0 {
			return quantIndicatorPoint{}, errors.New("mACD lookback is invalid")
		}
		dif := differences[len(differences)-1]
		dea := decimal.Zero
		if len(signals) > 0 {
			dea = signals[len(signals)-1]
		}
		switch p.Signal {
		case "golden_cross":
			point.Matched = len(signals) >= 2 && differences[len(differences)-2].LessThanOrEqual(signals[len(signals)-2]) && dif.GreaterThan(dea)
		case "death_cross":
			point.Matched = len(signals) >= 2 && differences[len(differences)-2].GreaterThanOrEqual(signals[len(signals)-2]) && dif.LessThan(dea)
		case "dif_above_zero":
			point.Matched = dif.Sign() > 0
		case "dif_below_zero":
			point.Matched = dif.Sign() < 0
		}
		point.Values = map[string]string{"dif": dif.String(), "dea": dea.String()}
		point.Summary = fmt.Sprintf("%s：DIF %s，DEA %s", leaf.Name, dif.String(), dea.String())
	case "kdj":
		ks, ds, js := quantKDJ(candles, p.Period, p.KSmoothing, p.DSmoothing)
		if len(ks) == 0 {
			return quantIndicatorPoint{}, errors.New("kDJ lookback is invalid")
		}
		k, d, j := ks[len(ks)-1], ds[len(ds)-1], js[len(js)-1]
		switch p.Signal {
		case "golden_cross":
			point.Matched = len(ks) >= 2 && ks[len(ks)-2].LessThanOrEqual(ds[len(ds)-2]) && k.GreaterThan(d)
		case "death_cross":
			point.Matched = len(ks) >= 2 && ks[len(ks)-2].GreaterThanOrEqual(ds[len(ds)-2]) && k.LessThan(d)
		case "k_above":
			point.Matched = k.GreaterThan(p.Threshold)
		case "k_below":
			point.Matched = k.LessThan(p.Threshold)
		case "d_above":
			point.Matched = d.GreaterThan(p.Threshold)
		case "d_below":
			point.Matched = d.LessThan(p.Threshold)
		case "j_above":
			point.Matched = j.GreaterThan(p.Threshold)
		case "j_below":
			point.Matched = j.LessThan(p.Threshold)
		}
		point.Values = map[string]string{"k": k.String(), "d": d.String(), "j": j.String()}
		point.Summary = fmt.Sprintf("%s：K %s，D %s，J %s", leaf.Name, k.String(), d.String(), j.String())
	case "rsi":
		rsi := quantWilderRSI(candles, p.Period)
		point.Matched = p.Mode == "above" && rsi.GreaterThan(p.Threshold) || p.Mode == "below" && rsi.LessThan(p.Threshold)
		point.Values = map[string]string{"rsi": rsi.String()}
		point.Summary = fmt.Sprintf("%s：RSI %s，阈值 %s", leaf.Name, rsi.String(), p.Threshold.String())
	case "bollinger":
		middle := candleCloseAverage(quantSDKCandles(candles))
		variance := decimal.Zero
		for _, candle := range candles {
			difference := candle.Close.Sub(middle)
			variance = variance.Add(difference.Mul(difference))
		}
		variance = variance.DivRound(decimal.NewFromInt(int64(len(candles))), quantIndicatorScale)
		deviation := quantDecimalSqrt(variance)
		upper := middle.Add(deviation.Mul(p.StandardDeviation))
		lower := middle.Sub(deviation.Mul(p.StandardDeviation))
		closeValue := candles[len(candles)-1].Close
		point.Matched = p.Signal == "close_above_upper" && closeValue.GreaterThan(upper) || p.Signal == "close_below_lower" && closeValue.LessThan(lower)
		point.Values = map[string]string{"close": closeValue.String(), "middle": middle.String(), "upper": upper.String(), "lower": lower.String()}
		point.Summary = fmt.Sprintf("%s：收盘 %s，上轨 %s，下轨 %s", leaf.Name, closeValue.String(), upper.String(), lower.String())
	default:
		return quantIndicatorPoint{}, errors.New("quant indicator is unsupported")
	}
	return point, nil
}

func quantEMAAligned(values []decimal.Decimal, period int) []decimal.Decimal {
	result := make([]decimal.Decimal, len(values))
	if len(values) < period {
		return result
	}
	current := decimal.Zero
	for _, value := range values[:period] {
		current = current.Add(value)
	}
	current = current.DivRound(decimal.NewFromInt(int64(period)), quantIndicatorScale)
	result[period-1] = current
	alpha := decimal.NewFromInt(2).DivRound(decimal.NewFromInt(int64(period+1)), quantIndicatorScale)
	for index := period; index < len(values); index++ {
		current = values[index].Sub(current).Mul(alpha).Add(current).Round(quantIndicatorScale)
		result[index] = current
	}
	return result
}

func quantMACD(candles []quantCandle, fastPeriod, slowPeriod, signalPeriod int) ([]decimal.Decimal, []decimal.Decimal) {
	closes := make([]decimal.Decimal, len(candles))
	for index, candle := range candles {
		closes[index] = candle.Close
	}
	fast, slow := quantEMAAligned(closes, fastPeriod), quantEMAAligned(closes, slowPeriod)
	differences := make([]decimal.Decimal, 0, len(closes)-slowPeriod+1)
	for index := slowPeriod - 1; index < len(closes); index++ {
		differences = append(differences, fast[index].Sub(slow[index]))
	}
	if len(differences) < signalPeriod {
		return differences, nil
	}
	aligned := quantEMAAligned(differences, signalPeriod)
	return differences[signalPeriod-1:], aligned[signalPeriod-1:]
}

func quantKDJ(candles []quantCandle, period, kSmoothing, dSmoothing int) ([]decimal.Decimal, []decimal.Decimal, []decimal.Decimal) {
	if len(candles) < period {
		return nil, nil, nil
	}
	k, d := quantFifty, quantFifty
	ks, ds, js := make([]decimal.Decimal, 0, len(candles)-period+1), make([]decimal.Decimal, 0, len(candles)-period+1), make([]decimal.Decimal, 0, len(candles)-period+1)
	for index := period - 1; index < len(candles); index++ {
		window := candles[index-period+1 : index+1]
		high, low := window[0].High, window[0].Low
		for _, candle := range window[1:] {
			high = decimal.Max(high, candle.High)
			low = decimal.Min(low, candle.Low)
		}
		rsv := quantFifty
		if !high.Equal(low) {
			rsv = candles[index].Close.Sub(low).DivRound(high.Sub(low), quantIndicatorScale).Mul(quantHundred)
		}
		k = k.Mul(decimal.NewFromInt(int64(kSmoothing-1))).Add(rsv).DivRound(decimal.NewFromInt(int64(kSmoothing)), quantIndicatorScale)
		d = d.Mul(decimal.NewFromInt(int64(dSmoothing-1))).Add(k).DivRound(decimal.NewFromInt(int64(dSmoothing)), quantIndicatorScale)
		j := k.Mul(decimal.NewFromInt(3)).Sub(d.Mul(decimal.NewFromInt(2)))
		ks, ds, js = append(ks, k), append(ds, d), append(js, j)
	}
	return ks, ds, js
}

func quantWilderRSI(candles []quantCandle, period int) decimal.Decimal {
	gain, loss := decimal.Zero, decimal.Zero
	for index := 1; index <= period; index++ {
		change := candles[index].Close.Sub(candles[index-1].Close)
		if change.Sign() > 0 {
			gain = gain.Add(change)
		} else {
			loss = loss.Add(change.Abs())
		}
	}
	divisor := decimal.NewFromInt(int64(period))
	averageGain, averageLoss := gain.DivRound(divisor, quantIndicatorScale), loss.DivRound(divisor, quantIndicatorScale)
	for index := period + 1; index < len(candles); index++ {
		change := candles[index].Close.Sub(candles[index-1].Close)
		currentGain, currentLoss := decimal.Zero, decimal.Zero
		if change.Sign() > 0 {
			currentGain = change
		} else {
			currentLoss = change.Abs()
		}
		averageGain = averageGain.Mul(decimal.NewFromInt(int64(period-1))).Add(currentGain).DivRound(divisor, quantIndicatorScale)
		averageLoss = averageLoss.Mul(decimal.NewFromInt(int64(period-1))).Add(currentLoss).DivRound(divisor, quantIndicatorScale)
	}
	if averageGain.IsZero() && averageLoss.IsZero() {
		return quantFifty
	}
	if averageLoss.IsZero() {
		return quantHundred
	}
	rs := averageGain.DivRound(averageLoss, quantIndicatorScale)
	return quantHundred.Sub(quantHundred.DivRound(quantOne.Add(rs), quantIndicatorScale))
}

func quantDecimalSqrt(value decimal.Decimal) decimal.Decimal {
	if value.Sign() <= 0 {
		return decimal.Zero
	}
	estimate := value
	if value.LessThan(quantOne) {
		estimate = quantOne
	}
	two, epsilon := decimal.NewFromInt(2), decimal.New(1, -28)
	for range 64 {
		next := estimate.Add(value.DivRound(estimate, quantIndicatorScale)).DivRound(two, quantIndicatorScale)
		if next.Sub(estimate).Abs().LessThanOrEqual(epsilon) {
			return next
		}
		estimate = next
	}
	return estimate
}

var _ sdk.ActionHandler = quantIndicatorAction{}
