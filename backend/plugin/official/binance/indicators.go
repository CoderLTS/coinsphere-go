package binance

import (
	"fmt"
	"time"

	"coinsphere/backend/plugin/sdk"
	"github.com/shopspring/decimal"
)

const indicatorScale int32 = 18

type binanceIndicatorConfig struct {
	MAPeriods                         [3]int
	EMAPeriods                        [3]int
	BollPeriod                        int
	BollMultiplier                    decimal.Decimal
	MACDFast, MACDSlow, MACDSignal    int
	RSIPeriod                         int
	KDJPeriod, KDJKSmooth, KDJDSmooth int
	WRPeriod                          int
}

var defaultBinanceIndicatorConfig = binanceIndicatorConfig{
	MAPeriods: [3]int{7, 25, 99}, EMAPeriods: [3]int{7, 25, 99}, BollPeriod: 20,
	BollMultiplier: decimal.NewFromInt(2), MACDFast: 12, MACDSlow: 26, MACDSignal: 9,
	RSIPeriod: 14, KDJPeriod: 9, KDJKSmooth: 3, KDJDSmooth: 3, WRPeriod: 14,
}

func calculateBinanceIndicators(candles []sdk.Candle, config binanceIndicatorConfig) []map[string]any {
	closes := make([]decimal.Decimal, len(candles))
	for i := range candles {
		closes[i] = candles[i].Close
	}
	ma := [3][]*decimal.Decimal{alignedMA(closes, config.MAPeriods[0]), alignedMA(closes, config.MAPeriods[1]), alignedMA(closes, config.MAPeriods[2])}
	ema := [3][]*decimal.Decimal{alignedEMA(closes, config.EMAPeriods[0]), alignedEMA(closes, config.EMAPeriods[1]), alignedEMA(closes, config.EMAPeriods[2])}
	bollMiddle, bollUpper, bollLower := alignedBollinger(candles, config.BollPeriod, config.BollMultiplier)
	dif, dea, macdHist := alignedMACD(closes, config.MACDFast, config.MACDSlow, config.MACDSignal)
	rsi := alignedRSI(closes, config.RSIPeriod)
	k, d, j := alignedKDJ(candles, config.KDJPeriod, config.KDJKSmooth, config.KDJDSmooth)
	obv, wr := alignedOBV(candles), alignedWR(candles, config.WRPeriod)
	result := make([]map[string]any, len(candles))
	for i, candle := range candles {
		main := map[string]any{
			"bollMiddle": decimalOrNil(bollMiddle[i]), "bollUpper": decimalOrNil(bollUpper[i]), "bollLower": decimalOrNil(bollLower[i]),
		}
		for index, period := range config.MAPeriods {
			main[fmt.Sprintf("ma%d", period)] = decimalOrNil(ma[index][i])
		}
		for index, period := range config.EMAPeriods {
			main[fmt.Sprintf("ema%d", period)] = decimalOrNil(ema[index][i])
		}
		result[i] = map[string]any{
			"openTime": candle.OpenTime.UTC().Format(time.RFC3339Nano),
			"main":     main,
			"sub": map[string]any{
				"volume": candle.Volume.String(),
				"macd":   decimalOrNil(macdHist[i]), "dif": decimalOrNil(dif[i]), "dea": decimalOrNil(dea[i]),
				"rsi": decimalOrNil(rsi[i]), "k": decimalOrNil(k[i]), "d": decimalOrNil(d[i]), "j": decimalOrNil(j[i]),
				"obv": decimalOrNil(obv[i]), "wr": decimalOrNil(wr[i]),
			},
		}
	}
	return result
}

func decimalOrNil(value *decimal.Decimal) any {
	if value == nil {
		return nil
	}
	return value.String()
}

func alignedMA(values []decimal.Decimal, period int) []*decimal.Decimal {
	result := make([]*decimal.Decimal, len(values))
	if period < 1 {
		return result
	}
	var sum decimal.Decimal
	for i, value := range values {
		sum = sum.Add(value)
		if i >= period {
			sum = sum.Sub(values[i-period])
		}
		if i >= period-1 {
			v := sum.DivRound(decimal.NewFromInt(int64(period)), indicatorScale)
			result[i] = &v
		}
	}
	return result
}

func alignedEMA(values []decimal.Decimal, period int) []*decimal.Decimal {
	result := make([]*decimal.Decimal, len(values))
	if period < 1 || len(values) < period {
		return result
	}
	seed := decimal.Zero
	for _, value := range values[:period] {
		seed = seed.Add(value)
	}
	current := seed.DivRound(decimal.NewFromInt(int64(period)), indicatorScale)
	result[period-1] = &current
	alpha := decimal.NewFromInt(2).DivRound(decimal.NewFromInt(int64(period+1)), indicatorScale)
	for i := period; i < len(values); i++ {
		current = values[i].Sub(current).Mul(alpha).Add(current).Round(indicatorScale)
		v := current
		result[i] = &v
	}
	return result
}

func alignedBollinger(candles []sdk.Candle, period int, multiplier decimal.Decimal) ([]*decimal.Decimal, []*decimal.Decimal, []*decimal.Decimal) {
	middle, upper, lower := make([]*decimal.Decimal, len(candles)), make([]*decimal.Decimal, len(candles)), make([]*decimal.Decimal, len(candles))
	for i := range candles {
		if i < period-1 {
			continue
		}
		window := candles[i-period+1 : i+1]
		avg := decimal.Zero
		for _, candle := range window {
			avg = avg.Add(candle.Close)
		}
		avg = avg.DivRound(decimal.NewFromInt(int64(period)), indicatorScale)
		variance := decimal.Zero
		for _, candle := range window {
			delta := candle.Close.Sub(avg)
			variance = variance.Add(delta.Mul(delta))
		}
		deviation := decimalSqrt(variance.DivRound(decimal.NewFromInt(int64(period)), indicatorScale))
		m, u, l := avg, avg.Add(deviation.Mul(multiplier)), avg.Sub(deviation.Mul(multiplier))
		middle[i], upper[i], lower[i] = &m, &u, &l
	}
	return middle, upper, lower
}

func alignedMACD(values []decimal.Decimal, fast, slow, signal int) ([]*decimal.Decimal, []*decimal.Decimal, []*decimal.Decimal) {
	dif, dea, hist := make([]*decimal.Decimal, len(values)), make([]*decimal.Decimal, len(values)), make([]*decimal.Decimal, len(values))
	fastValues, slowValues := alignedEMA(values, fast), alignedEMA(values, slow)
	capacity := len(values) - slow + 1
	if capacity < 0 {
		capacity = 0
	}
	macdValues := make([]decimal.Decimal, 0, capacity)
	for i := slow - 1; i < len(values); i++ {
		if fastValues[i] == nil || slowValues[i] == nil {
			continue
		}
		value := fastValues[i].Sub(*slowValues[i])
		macdValues = append(macdValues, value)
		v := value
		dif[i] = &v
	}
	deaValues := alignedEMA(macdValues, signal)
	start := slow - 1
	for i, value := range deaValues {
		if value == nil {
			continue
		}
		index := start + i
		v := *value
		dea[index] = &v
		if dif[index] != nil {
			h := dif[index].Sub(v)
			hist[index] = &h
		}
	}
	return dif, dea, hist
}

func alignedRSI(values []decimal.Decimal, period int) []*decimal.Decimal {
	result := make([]*decimal.Decimal, len(values))
	if period < 1 || len(values) <= period {
		return result
	}
	gain, loss := decimal.Zero, decimal.Zero
	for i := 1; i <= period; i++ {
		change := values[i].Sub(values[i-1])
		if change.Sign() > 0 {
			gain = gain.Add(change)
		} else {
			loss = loss.Add(change.Abs())
		}
	}
	divisor := decimal.NewFromInt(int64(period))
	avgGain, avgLoss := gain.DivRound(divisor, indicatorScale), loss.DivRound(divisor, indicatorScale)
	result[period] = rsiValue(avgGain, avgLoss)
	for i := period + 1; i < len(values); i++ {
		change := values[i].Sub(values[i-1])
		currentGain, currentLoss := decimal.Zero, decimal.Zero
		if change.Sign() > 0 {
			currentGain = change
		} else {
			currentLoss = change.Abs()
		}
		avgGain = avgGain.Mul(decimal.NewFromInt(int64(period-1))).Add(currentGain).DivRound(divisor, indicatorScale)
		avgLoss = avgLoss.Mul(decimal.NewFromInt(int64(period-1))).Add(currentLoss).DivRound(divisor, indicatorScale)
		result[i] = rsiValue(avgGain, avgLoss)
	}
	return result
}

func rsiValue(gain, loss decimal.Decimal) *decimal.Decimal {
	value := decimal.NewFromInt(50)
	if gain.IsZero() && loss.IsZero() {
		return &value
	}
	if loss.IsZero() {
		value = decimal.NewFromInt(100)
		return &value
	}
	rs := gain.DivRound(loss, indicatorScale)
	value = decimal.NewFromInt(100).Sub(decimal.NewFromInt(100).DivRound(decimal.NewFromInt(1).Add(rs), indicatorScale))
	return &value
}

func alignedKDJ(candles []sdk.Candle, period, ksmooth, dsmooth int) ([]*decimal.Decimal, []*decimal.Decimal, []*decimal.Decimal) {
	k, d, j := make([]*decimal.Decimal, len(candles)), make([]*decimal.Decimal, len(candles)), make([]*decimal.Decimal, len(candles))
	currentK, currentD := decimal.NewFromInt(50), decimal.NewFromInt(50)
	for i := period - 1; i < len(candles); i++ {
		high, low := candles[i].High, candles[i].Low
		for _, candle := range candles[i-period+1 : i] {
			high = decimal.Max(high, candle.High)
			low = decimal.Min(low, candle.Low)
		}
		rsv := decimal.NewFromInt(50)
		if !high.Equal(low) {
			rsv = candles[i].Close.Sub(low).DivRound(high.Sub(low), indicatorScale).Mul(decimal.NewFromInt(100))
		}
		currentK = currentK.Mul(decimal.NewFromInt(int64(ksmooth-1))).Add(rsv).DivRound(decimal.NewFromInt(int64(ksmooth)), indicatorScale)
		currentD = currentD.Mul(decimal.NewFromInt(int64(dsmooth-1))).Add(currentK).DivRound(decimal.NewFromInt(int64(dsmooth)), indicatorScale)
		currentJ := currentK.Mul(decimal.NewFromInt(3)).Sub(currentD.Mul(decimal.NewFromInt(2)))
		kv, dv, jv := currentK, currentD, currentJ
		k[i], d[i], j[i] = &kv, &dv, &jv
	}
	return k, d, j
}

func alignedOBV(candles []sdk.Candle) []*decimal.Decimal {
	result := make([]*decimal.Decimal, len(candles))
	current := decimal.Zero
	for i, candle := range candles {
		if i > 0 {
			if candle.Close.GreaterThan(candles[i-1].Close) {
				current = current.Add(candle.Volume)
			} else if candle.Close.LessThan(candles[i-1].Close) {
				current = current.Sub(candle.Volume)
			}
		}
		value := current
		result[i] = &value
	}
	return result
}

func alignedWR(candles []sdk.Candle, period int) []*decimal.Decimal {
	result := make([]*decimal.Decimal, len(candles))
	for i := period - 1; i < len(candles); i++ {
		high, low := candles[i].High, candles[i].Low
		for _, candle := range candles[i-period+1 : i] {
			high = decimal.Max(high, candle.High)
			low = decimal.Min(low, candle.Low)
		}
		value := decimal.NewFromInt(-50)
		if !high.Equal(low) {
			value = candles[i].Close.Sub(high).DivRound(high.Sub(low), indicatorScale).Mul(decimal.NewFromInt(100))
		}
		result[i] = &value
	}
	return result
}

func decimalSqrt(value decimal.Decimal) decimal.Decimal {
	if value.Sign() <= 0 {
		return decimal.Zero
	}
	estimate := value
	if value.LessThan(decimal.NewFromInt(1)) {
		estimate = decimal.NewFromInt(1)
	}
	two := decimal.NewFromInt(2)
	for i := 0; i < 64; i++ {
		next := estimate.Add(value.DivRound(estimate, indicatorScale)).DivRound(two, indicatorScale)
		if next.Sub(estimate).Abs().LessThan(decimal.New(1, -16)) {
			return next
		}
		estimate = next
	}
	return estimate
}
