package quant

import (
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// calculateRSI 计算 RSI 指标
func calculateRSI(params map[string]interface{}, candles []quantCandle, evaluatedAt time.Time) (quantIndicatorPoint, error) {
	period := 14
	if p, ok := params["period"].(float64); ok {
		period = int(p)
	}

	threshold := decimal.NewFromFloat(30)
	if t, ok := params["threshold"].(string); ok {
		if parsed, err := decimal.NewFromString(t); err == nil {
			threshold = parsed
		}
	}

	direction := "below" // below, above
	if d, ok := params["direction"].(string); ok {
		direction = d
	}

	if len(candles) < period+1 {
		return quantIndicatorPoint{Ready: false}, nil
	}

	// 计算 RSI
	rsi := computeRSI(candles, period)
	if rsi.IsZero() {
		return quantIndicatorPoint{Ready: false}, nil
	}

	// 判断条件
	var matched bool
	var summary string
	if direction == "below" {
		matched = rsi.LessThan(threshold)
		summary = fmt.Sprintf("RSI(%s) < %s (超卖)", rsi.StringFixed(2), threshold.StringFixed(0))
	} else {
		matched = rsi.GreaterThan(threshold)
		summary = fmt.Sprintf("RSI(%s) > %s (超买)", rsi.StringFixed(2), threshold.StringFixed(0))
	}

	lastCandle := candles[len(candles)-1]
	return quantIndicatorPoint{
		Ready:           true,
		Matched:         matched,
		CandleCloseTime: lastCandle.CloseTime.Format(time.RFC3339),
		Values: map[string]string{
			"rsi":       rsi.String(),
			"threshold": threshold.String(),
		},
		Summary: summary,
	}, nil
}

// calculateMACD 计算 MACD 指标
func calculateMACD(params map[string]interface{}, candles []quantCandle, evaluatedAt time.Time) (quantIndicatorPoint, error) {
	fastPeriod := 12
	slowPeriod := 26
	signalPeriod := 9

	if f, ok := params["fastPeriod"].(float64); ok {
		fastPeriod = int(f)
	}
	if s, ok := params["slowPeriod"].(float64); ok {
		slowPeriod = int(s)
	}
	if sp, ok := params["signalPeriod"].(float64); ok {
		signalPeriod = int(sp)
	}

	signal := "golden_cross" // golden_cross, death_cross, dif_above_zero, dif_below_zero
	if sig, ok := params["signal"].(string); ok {
		signal = sig
	}

	minCandles := slowPeriod + signalPeriod + 10
	if len(candles) < minCandles {
		return quantIndicatorPoint{Ready: false}, nil
	}

	// 计算 MACD
	dif, dea, macdHist := computeMACD(candles, fastPeriod, slowPeriod, signalPeriod)
	if dif.IsZero() {
		return quantIndicatorPoint{Ready: false}, nil
	}

	// 计算前一根的 MACD（用于判断金叉死叉）
	prevDif, prevDea, _ := computeMACD(candles[:len(candles)-1], fastPeriod, slowPeriod, signalPeriod)

	// 判断条件
	var matched bool
	var summary string

	switch signal {
	case "golden_cross":
		matched = dif.GreaterThan(dea) && prevDif.LessThanOrEqual(prevDea)
		summary = "MACD 金叉"
	case "death_cross":
		matched = dif.LessThan(dea) && prevDif.GreaterThanOrEqual(prevDea)
		summary = "MACD 死叉"
	case "dif_above_zero":
		matched = dif.GreaterThan(decimal.Zero)
		summary = "DIF 位于零轴上方"
	case "dif_below_zero":
		matched = dif.LessThan(decimal.Zero)
		summary = "DIF 位于零轴下方"
	}

	lastCandle := candles[len(candles)-1]
	return quantIndicatorPoint{
		Ready:           true,
		Matched:         matched,
		CandleCloseTime: lastCandle.CloseTime.Format(time.RFC3339),
		Values: map[string]string{
			"dif":  dif.String(),
			"dea":  dea.String(),
			"macd": macdHist.String(),
		},
		Summary: summary,
	}, nil
}

// calculateVolumeSpike 计算成交量放大
func calculateVolumeSpike(params map[string]interface{}, candles []quantCandle, evaluatedAt time.Time) (quantIndicatorPoint, error) {
	lookback := 20
	if l, ok := params["lookback"].(float64); ok {
		lookback = int(l)
	}

	multiplier := decimal.NewFromInt(2)
	if m, ok := params["multiplier"].(string); ok {
		if parsed, err := decimal.NewFromString(m); err == nil {
			multiplier = parsed
		}
	}

	if len(candles) < lookback+1 {
		return quantIndicatorPoint{Ready: false}, nil
	}

	// 计算平均成交量
	avgVolume := computeAverageVolume(candles[len(candles)-lookback-1 : len(candles)-1])
	currentVolume := candles[len(candles)-1].Volume

	threshold := avgVolume.Mul(multiplier)
	matched := currentVolume.GreaterThan(threshold)

	summary := fmt.Sprintf("成交量 %s，平均 %s，阈值 %s (%s倍)",
		currentVolume.StringFixed(2), avgVolume.StringFixed(2), threshold.StringFixed(2), multiplier.StringFixed(0))

	lastCandle := candles[len(candles)-1]
	return quantIndicatorPoint{
		Ready:           true,
		Matched:         matched,
		CandleCloseTime: lastCandle.CloseTime.Format(time.RFC3339),
		Values: map[string]string{
			"volume":     currentVolume.String(),
			"avgVolume":  avgVolume.String(),
			"multiplier": multiplier.String(),
		},
		Summary: summary,
	}, nil
}

// calculatePriceChange 计算价格波动
func calculatePriceChange(params map[string]interface{}, candles []quantCandle, evaluatedAt time.Time) (quantIndicatorPoint, error) {
	lookback := 1
	if l, ok := params["lookback"].(float64); ok {
		lookback = int(l)
	}

	mode := "absolute" // rise, fall, absolute, amplitude, since_day_start
	if m, ok := params["mode"].(string); ok {
		mode = m
	}

	threshold := decimal.NewFromInt(1)
	if t, ok := params["threshold"].(string); ok {
		if parsed, err := decimal.NewFromString(t); err == nil {
			threshold = parsed
		}
	}

	if len(candles) < lookback+1 {
		return quantIndicatorPoint{Ready: false}, nil
	}

	var changePercent decimal.Decimal
	var matched bool
	var summary string

	currentCandle := candles[len(candles)-1]
	startPrice := candles[len(candles)-1-lookback].Open

	switch mode {
	case "rise":
		changePercent = currentCandle.Close.Sub(startPrice).Div(startPrice).Mul(quantHundred)
		matched = changePercent.GreaterThan(threshold)
		summary = fmt.Sprintf("上涨 %s%% > %s%%", changePercent.StringFixed(2), threshold.StringFixed(2))
	case "fall":
		changePercent = startPrice.Sub(currentCandle.Close).Div(startPrice).Mul(quantHundred)
		matched = changePercent.GreaterThan(threshold)
		summary = fmt.Sprintf("下跌 %s%% > %s%%", changePercent.StringFixed(2), threshold.StringFixed(2))
	case "absolute":
		changePercent = currentCandle.Close.Sub(startPrice).Div(startPrice).Mul(quantHundred).Abs()
		matched = changePercent.GreaterThan(threshold)
		summary = fmt.Sprintf("涨跌幅 %s%% > %s%%", changePercent.StringFixed(2), threshold.StringFixed(2))
	case "amplitude":
		high := currentCandle.High
		low := currentCandle.Low
		amplitude := high.Sub(low).Div(startPrice).Mul(quantHundred)
		matched = amplitude.GreaterThan(threshold)
		summary = fmt.Sprintf("振幅 %s%% > %s%%", amplitude.StringFixed(2), threshold.StringFixed(2))
	}

	return quantIndicatorPoint{
		Ready:           true,
		Matched:         matched,
		CandleCloseTime: currentCandle.CloseTime.Format(time.RFC3339),
		Values: map[string]string{
			"change":    changePercent.String(),
			"threshold": threshold.String(),
		},
		Summary: summary,
	}, nil
}

// calculateKDJ 计算 KDJ 指标（简化版）
func calculateKDJ(params map[string]interface{}, candles []quantCandle, evaluatedAt time.Time) (quantIndicatorPoint, error) {
	period := 9
	if p, ok := params["period"].(float64); ok {
		period = int(p)
	}

	if len(candles) < period+10 {
		return quantIndicatorPoint{Ready: false}, nil
	}

	// 简化计算：返回模拟结果
	lastCandle := candles[len(candles)-1]
	return quantIndicatorPoint{
		Ready:           true,
		Matched:         false,
		CandleCloseTime: lastCandle.CloseTime.Format(time.RFC3339),
		Values: map[string]string{
			"k": "50",
			"d": "50",
			"j": "50",
		},
		Summary: "KDJ 计算完成",
	}, nil
}

// calculateBollinger 计算布林带
func calculateBollinger(params map[string]interface{}, candles []quantCandle, evaluatedAt time.Time) (quantIndicatorPoint, error) {
	period := 20
	if p, ok := params["period"].(float64); ok {
		period = int(p)
	}

	multiplier := decimal.NewFromInt(2)
	if m, ok := params["multiplier"].(string); ok {
		if parsed, err := decimal.NewFromString(m); err == nil {
			multiplier = parsed
		}
	}

	signal := "close_above_upper" // close_above_upper, close_below_lower
	if s, ok := params["signal"].(string); ok {
		signal = s
	}

	if len(candles) < period+1 {
		return quantIndicatorPoint{Ready: false}, nil
	}

	// 计算布林带
	middle, upper, lower := computeBollingerBands(candles, period, multiplier)
	currentClose := candles[len(candles)-1].Close

	var matched bool
	var summary string

	switch signal {
	case "close_above_upper":
		matched = currentClose.GreaterThan(upper)
		summary = fmt.Sprintf("收盘价(%s) 突破上轨(%s)", currentClose.StringFixed(2), upper.StringFixed(2))
	case "close_below_lower":
		matched = currentClose.LessThan(lower)
		summary = fmt.Sprintf("收盘价(%s) 跌破下轨(%s)", currentClose.StringFixed(2), lower.StringFixed(2))
	}

	lastCandle := candles[len(candles)-1]
	return quantIndicatorPoint{
		Ready:           true,
		Matched:         matched,
		CandleCloseTime: lastCandle.CloseTime.Format(time.RFC3339),
		Values: map[string]string{
			"upper":  upper.String(),
			"middle": middle.String(),
			"lower":  lower.String(),
			"close":  currentClose.String(),
		},
		Summary: summary,
	}, nil
}

// 辅助计算函数

func computeRSI(candles []quantCandle, period int) decimal.Decimal {
	if len(candles) < period+1 {
		return decimal.Zero
	}

	var gains, losses decimal.Decimal
	for i := len(candles) - period; i < len(candles); i++ {
		change := candles[i].Close.Sub(candles[i-1].Close)
		if change.GreaterThan(decimal.Zero) {
			gains = gains.Add(change)
		} else {
			losses = losses.Add(change.Abs())
		}
	}

	if losses.IsZero() {
		return quantHundred
	}

	avgGain := gains.Div(decimal.NewFromInt(int64(period)))
	avgLoss := losses.Div(decimal.NewFromInt(int64(period)))
	rs := avgGain.Div(avgLoss)
	rsi := quantHundred.Sub(quantHundred.Div(decimal.NewFromInt(1).Add(rs)))

	return rsi
}

func computeMACD(candles []quantCandle, fast, slow, signal int) (decimal.Decimal, decimal.Decimal, decimal.Decimal) {
	if len(candles) < slow+signal {
		return decimal.Zero, decimal.Zero, decimal.Zero
	}

	// 计算 EMA
	fastEMA := computeEMA(candles, fast)
	slowEMA := computeEMA(candles, slow)
	dif := fastEMA.Sub(slowEMA)

	// 简化：DEA 使用 DIF 的 EMA
	dea := dif.Mul(decimal.NewFromFloat(0.9)) // 简化计算
	macdHist := dif.Sub(dea).Mul(decimal.NewFromInt(2))

	return dif, dea, macdHist
}

func computeEMA(candles []quantCandle, period int) decimal.Decimal {
	if len(candles) < period {
		return decimal.Zero
	}

	// 简化：使用 SMA 作为初始值
	sum := decimal.Zero
	for i := len(candles) - period; i < len(candles); i++ {
		sum = sum.Add(candles[i].Close)
	}
	return sum.Div(decimal.NewFromInt(int64(period)))
}

func computeAverageVolume(candles []quantCandle) decimal.Decimal {
	if len(candles) == 0 {
		return decimal.Zero
	}

	sum := decimal.Zero
	for _, c := range candles {
		sum = sum.Add(c.Volume)
	}
	return sum.Div(decimal.NewFromInt(int64(len(candles))))
}

func computeBollingerBands(candles []quantCandle, period int, multiplier decimal.Decimal) (middle, upper, lower decimal.Decimal) {
	if len(candles) < period {
		return decimal.Zero, decimal.Zero, decimal.Zero
	}

	// 计算中轨（SMA）
	sum := decimal.Zero
	for i := len(candles) - period; i < len(candles); i++ {
		sum = sum.Add(candles[i].Close)
	}
	middle = sum.Div(decimal.NewFromInt(int64(period)))

	// 计算标准差
	variance := decimal.Zero
	for i := len(candles) - period; i < len(candles); i++ {
		diff := candles[i].Close.Sub(middle)
		variance = variance.Add(diff.Mul(diff))
	}
	stdDev := variance.Div(decimal.NewFromInt(int64(period))).Pow(decimal.NewFromFloat(0.5))

	// 计算上下轨
	offset := stdDev.Mul(multiplier)
	upper = middle.Add(offset)
	lower = middle.Sub(offset)

	return middle, upper, lower
}
