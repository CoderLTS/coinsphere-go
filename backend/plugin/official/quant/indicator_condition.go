package quant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

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

var quantIndicatorInputSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"eventTime":{"type":"string","title":"Event time","format":"date-time"},"pathEntered":{"type":"boolean","title":"Upstream path entered","default":false}},"required":["eventTime"],"additionalProperties":false}`)

var quantIndicatorOutputSchema = json.RawMessage(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema",
  "type":"object",
  "properties":{
    "ready":{"type":"boolean"},"matched":{"type":"boolean"},"previousMatched":{"type":"boolean"},
    "branch":{"type":"string","enum":["true","false"]},"entered":{"type":"boolean"},"triggered":{"type":"boolean"},
    "evaluatedAt":{"type":"string","format":"date-time"},"previousEvaluatedAt":{"type":"string","format":"date-time"},
    "businessKey":{"type":"string","minLength":1,"maxLength":256},"summary":{"type":"string","minLength":1,"maxLength":2000},"formula":{"type":"string","minLength":1,"maxLength":2000},
    "market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},
    "indicator":{"type":"string"},"interval":{"type":"string"},
    "candleCloseTime":{"type":"string"},"previousCandleCloseTime":{"type":"string"},
    "value":{"type":"object","additionalProperties":{"type":"string"}},"previousValue":{"type":"object","additionalProperties":{"type":"string"}}
  },
  "required":["ready","matched","previousMatched","branch","entered","triggered","evaluatedAt","previousEvaluatedAt","businessKey","summary","formula","market","instrument","indicator","interval","candleCloseTime","previousCandleCloseTime","value","previousValue"],
  "additionalProperties":false
}`)

type quantIndicatorDefinition struct {
	NodeType  string
	Indicator string
}

var quantIndicatorDefinitions = []quantIndicatorDefinition{
	{NodeType: "official.quant.volume_spike_condition", Indicator: "volume_spike"},
	{NodeType: "official.quant.price_change_condition", Indicator: "price_change"},
	{NodeType: "official.quant.macd_condition", Indicator: "macd"},
	{NodeType: "official.quant.kdj_condition", Indicator: "kdj"},
	{NodeType: "official.quant.rsi_condition", Indicator: "rsi"},
	{NodeType: "official.quant.bollinger_condition", Indicator: "bollinger"},
}

var quantIndicatorParameterSchemas = map[string]string{
	"volume_spike": `{"type":"object","title":"Parameters","properties":{"lookback":{"type":"integer","title":"Average candles","minimum":1,"maximum":500,"default":20},"multiplier":{"type":"string","title":"Volume multiplier","pattern":"^[0-9]+(?:\\.[0-9]+)?$","default":"2","x-coinsphere-decimal":true}},"required":["lookback","multiplier"],"additionalProperties":false,"default":{"lookback":20,"multiplier":"2"}}`,
	"price_change": `{"type":"object","title":"Parameters","properties":{"lookback":{"type":"integer","title":"Candles","minimum":1,"maximum":500,"default":1},"mode":{"type":"string","title":"Mode","enum":["rise","fall","absolute","amplitude"],"enumLabels":["Rise","Fall","Absolute change","High-low amplitude"],"default":"absolute"},"threshold":{"type":"string","title":"Threshold (%)","pattern":"^[0-9]+(?:\\.[0-9]+)?$","default":"1","x-coinsphere-decimal":true}},"required":["lookback","mode","threshold"],"additionalProperties":false,"default":{"lookback":1,"mode":"absolute","threshold":"1"}}`,
	"macd":         `{"type":"object","title":"Parameters","properties":{"fastPeriod":{"type":"integer","title":"Fast period","minimum":1,"maximum":100,"default":12},"slowPeriod":{"type":"integer","title":"Slow period","minimum":2,"maximum":200,"default":26},"signalPeriod":{"type":"integer","title":"Signal period","minimum":1,"maximum":100,"default":9},"signal":{"type":"string","title":"Rule","enum":["golden_cross","death_cross","dif_above_zero","dif_below_zero"],"enumLabels":["Golden cross","Death cross","DIF above zero","DIF below zero"],"default":"golden_cross"}},"required":["fastPeriod","slowPeriod","signalPeriod","signal"],"additionalProperties":false,"default":{"fastPeriod":12,"slowPeriod":26,"signalPeriod":9,"signal":"golden_cross"}}`,
	"kdj":          `{"type":"object","title":"Parameters","properties":{"period":{"type":"integer","title":"Period","minimum":2,"maximum":200,"default":9},"kSmoothing":{"type":"integer","title":"K smoothing","minimum":1,"maximum":50,"default":3},"dSmoothing":{"type":"integer","title":"D smoothing","minimum":1,"maximum":50,"default":3},"signal":{"type":"string","title":"Rule","enum":["golden_cross","death_cross","k_above","k_below","d_above","d_below","j_above","j_below"],"enumLabels":["K/D golden cross","K/D death cross","K above threshold","K below threshold","D above threshold","D below threshold","J above threshold","J below threshold"],"default":"golden_cross"},"threshold":{"type":"string","title":"Threshold","pattern":"^-?[0-9]+(?:\\.[0-9]+)?$","default":"80","x-coinsphere-decimal":true}},"required":["period","kSmoothing","dSmoothing","signal","threshold"],"additionalProperties":false,"default":{"period":9,"kSmoothing":3,"dSmoothing":3,"signal":"golden_cross","threshold":"80"}}`,
	"rsi":          `{"type":"object","title":"Parameters","properties":{"period":{"type":"integer","title":"Period","minimum":2,"maximum":200,"default":14},"direction":{"type":"string","title":"Rule","enum":["above","below"],"enumLabels":["Above threshold","Below threshold"],"default":"below"},"threshold":{"type":"string","title":"Threshold","pattern":"^[0-9]+(?:\\.[0-9]+)?$","default":"30","x-coinsphere-decimal":true}},"required":["period","direction","threshold"],"additionalProperties":false,"default":{"period":14,"direction":"below","threshold":"30"}}`,
	"bollinger":    `{"type":"object","title":"Parameters","properties":{"period":{"type":"integer","title":"Period","minimum":2,"maximum":500,"default":20},"multiplier":{"type":"string","title":"Standard deviations","pattern":"^[0-9]+(?:\\.[0-9]+)?$","default":"2","x-coinsphere-decimal":true},"signal":{"type":"string","title":"Rule","enum":["close_above_upper","close_below_lower"],"enumLabels":["Close above upper band","Close below lower band"],"default":"close_above_upper"}},"required":["period","multiplier","signal"],"additionalProperties":false,"default":{"period":20,"multiplier":"2","signal":"close_above_upper"}}`,
}

func quantIndicatorConfigSchema(indicator string) json.RawMessage {
	return json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"market":{"type":"string","title":"Market","enum":["spot","usdm"],"default":"spot"},"instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$","default":"BTCUSDT"},"checkInterval":{"type":"string","title":"Check interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"],"default":"1m"},"name":{"type":"string","title":"Condition name","minLength":1,"maxLength":80},"interval":{"type":"string","title":"Candle interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"],"default":"1m"},"parameters":` + quantIndicatorParameterSchemas[indicator] + `},"required":["market","instrument","checkInterval","name","interval","parameters"],"additionalProperties":false}`)
}

type quantIndicatorAction struct {
	runtime   *quantRuntime
	indicator string
}

type quantIndicatorConfig struct {
	Market        string
	Instrument    string
	CheckInterval string
	Leaf          *quantIndicatorLeaf
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
	config, err := parseQuantIndicatorConfig(request.Config, a.indicator)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	var input struct {
		EventTime   string `json:"eventTime"`
		PathEntered bool   `json:"pathEntered"`
	}
	if !decodeQuantStrict(request.Input, &input) {
		return sdk.ActionResult{}, errors.New("quant indicator condition input is invalid")
	}
	evaluatedAt, err := parseQuantUTCTime(input.EventTime)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	previousAt := evaluatedAt.Add(-quantIntervals[config.CheckInterval])
	leaf, lookback := config.Leaf, quantIndicatorLookback(config.Leaf)
	duration := quantIntervals[leaf.Interval]
	extra := int((quantIntervals[config.CheckInterval] + duration - 1) / duration)
	candles, err := a.runtime.loadQuantCandlesThroughClose(ctx, quantSeriesConfig{
		Market: config.Market, Instrument: config.Instrument, Interval: leaf.Interval,
	}, evaluatedAt, lookback+extra+2)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	if len(candles) > 0 {
		if err := validateStrategyCandles(sdk.EvaluateRequest{
			Market: config.Market, Instrument: config.Instrument, Interval: leaf.Interval,
			Candles: quantSDKCandles(candles), EvaluatedAt: evaluatedAt,
		}); err != nil {
			return sdk.ActionResult{}, err
		}
		if !candles[len(candles)-1].CloseTime.Add(duration).After(evaluatedAt) {
			return sdk.ActionResult{}, errors.New("quant indicator candles are stale")
		}
	}
	current, err := evaluateQuantIndicatorLeaf(leaf, quantCandlesAt(candles, evaluatedAt, lookback))
	if err != nil {
		return sdk.ActionResult{}, err
	}
	previous, err := evaluateQuantIndicatorLeaf(leaf, quantCandlesAt(candles, previousAt, lookback))
	if err != nil {
		return sdk.ActionResult{}, err
	}
	ready := current.Ready && previous.Ready
	matched := ready && current.Matched
	previousMatched := previous.Ready && previous.Matched
	branch, previousBranch := "false", "false"
	if matched {
		branch = "true"
	}
	if previousMatched {
		previousBranch = "true"
	}
	entered := branch != previousBranch || input.PathEntered
	triggered := matched && entered
	formula := leaf.Name
	summary := fmt.Sprintf("%s %s 未命中：%s", strings.ToUpper(config.Market), config.Instrument, formula)
	if matched {
		summary = fmt.Sprintf("%s %s 命中：%s", strings.ToUpper(config.Market), config.Instrument, formula)
	} else if !ready {
		summary = fmt.Sprintf("%s %s 历史数据不足：%s", strings.ToUpper(config.Market), config.Instrument, formula)
	}
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"ready": ready, "matched": matched, "previousMatched": previousMatched,
		"branch": branch, "entered": entered, "triggered": triggered,
		"evaluatedAt": evaluatedAt.Format(time.RFC3339Nano), "previousEvaluatedAt": previousAt.Format(time.RFC3339Nano),
		"businessKey": fmt.Sprintf("quant:%s:%s:%s", config.Market, config.Instrument, request.NodeInstanceID),
		"summary":     summary, "formula": formula, "market": config.Market, "instrument": config.Instrument,
		"indicator": leaf.Indicator, "interval": leaf.Interval,
		"candleCloseTime": current.CandleCloseTime, "previousCandleCloseTime": previous.CandleCloseTime,
		"value": current.Values, "previousValue": previous.Values,
	})}, nil
}

func parseQuantIndicatorConfig(raw json.RawMessage, indicator string) (quantIndicatorConfig, error) {
	var payload struct {
		Market        string          `json:"market"`
		Instrument    string          `json:"instrument"`
		CheckInterval string          `json:"checkInterval"`
		Name          string          `json:"name"`
		Interval      string          `json:"interval"`
		Parameters    json.RawMessage `json:"parameters"`
	}
	if !decodeQuantStrict(raw, &payload) {
		return quantIndicatorConfig{}, errors.New("quant indicator condition configuration is invalid")
	}
	series, err := parseQuantSeriesConfig(mustMarshal(map[string]any{
		"market": payload.Market, "instrument": payload.Instrument, "interval": payload.CheckInterval,
	}))
	if err != nil {
		return quantIndicatorConfig{}, err
	}
	payload.Name = strings.TrimSpace(payload.Name)
	if payload.Name == "" || utf8.RuneCountInString(payload.Name) > 80 {
		return quantIndicatorConfig{}, errors.New("quant indicator condition name is invalid")
	}
	if _, ok := quantIntervals[payload.Interval]; !ok {
		return quantIndicatorConfig{}, errors.New("quant indicator interval is unsupported")
	}
	parameters, err := parseQuantIndicatorParameters(indicator, payload.Parameters)
	if err != nil {
		return quantIndicatorConfig{}, err
	}
	leaf := &quantIndicatorLeaf{Name: payload.Name, Interval: payload.Interval, Indicator: indicator, Parameters: parameters}
	return quantIndicatorConfig{Market: series.Market, Instrument: series.Instrument, CheckInterval: series.Interval, Leaf: leaf}, nil
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
			value.Mode != "rise" && value.Mode != "fall" && value.Mode != "absolute" && value.Mode != "amplitude" {
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
			return parameters, errors.New("MACD parameters are invalid")
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
			return parameters, errors.New("KDJ parameters are invalid")
		}
		threshold, err := parseQuantConditionDecimal(value.Threshold, decimal.NewFromInt(-1000), decimal.NewFromInt(1000), true)
		if err != nil {
			return parameters, errors.New("KDJ threshold is invalid")
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
			return parameters, errors.New("RSI parameters are invalid")
		}
		threshold, err := parseQuantConditionDecimal(value.Threshold, decimal.Zero, quantHundred, true)
		if err != nil {
			return parameters, errors.New("RSI threshold is invalid")
		}
		parameters.Period, parameters.Mode, parameters.Threshold = value.Period, value.Direction, threshold
	case "bollinger":
		value := struct {
			Period     int    `json:"period"`
			Multiplier string `json:"multiplier"`
			Signal     string `json:"signal"`
		}{Period: 20, Multiplier: "2", Signal: "close_above_upper"}
		if !decodeQuantStrict(raw, &value) || value.Period < 2 || value.Period > 500 || value.Signal != "close_above_upper" && value.Signal != "close_below_lower" {
			return parameters, errors.New("Bollinger parameters are invalid")
		}
		multiplier, err := parseQuantConditionDecimal(value.Multiplier, decimal.Zero, decimal.NewFromInt(20), false)
		if err != nil {
			return parameters, errors.New("Bollinger multiplier is invalid")
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
		return decimal.Zero, errors.New("Decimal is outside limits")
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

func quantCandlesAt(candles []quantCandle, cutoff time.Time, lookback int) []quantCandle {
	end := sort.Search(len(candles), func(index int) bool { return candles[index].CloseTime.After(cutoff) })
	if end < lookback {
		return nil
	}
	return candles[end-lookback : end]
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
			return quantIndicatorPoint{}, errors.New("MACD lookback is invalid")
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
			return quantIndicatorPoint{}, errors.New("KDJ lookback is invalid")
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
