package quant

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIndicatorCalculations(t *testing.T) {
	candles := generateMockCandles(100)
	now := time.Now()

	t.Run("RSI", func(t *testing.T) {
		params := map[string]interface{}{
			"period":    14.0,
			"threshold": "30",
			"direction": "below",
		}

		result, err := calculateRSI(params, candles, now)
		require.NoError(t, err)
		assert.True(t, result.Ready)
		t.Logf("RSI result: %+v", result)
	})

	t.Run("MACD", func(t *testing.T) {
		params := map[string]interface{}{
			"fastPeriod":   12.0,
			"slowPeriod":   26.0,
			"signalPeriod": 9.0,
			"signal":       "golden_cross",
		}

		result, err := calculateMACD(params, candles, now)
		require.NoError(t, err)
		assert.True(t, result.Ready)
		t.Logf("MACD result: %+v", result)
	})

	t.Run("VolumeSpike", func(t *testing.T) {
		params := map[string]interface{}{
			"lookback":   20.0,
			"multiplier": "2.0",
		}

		result, err := calculateVolumeSpike(params, candles, now)
		require.NoError(t, err)
		assert.True(t, result.Ready)
		t.Logf("VolumeSpike result: %+v", result)
	})

	t.Run("Bollinger", func(t *testing.T) {
		params := map[string]interface{}{
			"period":     20.0,
			"multiplier": "2.0",
			"signal":     "close_above_upper",
		}

		result, err := calculateBollinger(params, candles, now)
		require.NoError(t, err)
		assert.True(t, result.Ready)
		t.Logf("Bollinger result: %+v", result)
	})
}

// generateMockCandles 生成模拟 K 线数据
func generateMockCandles(count int) []quantCandle {
	candles := make([]quantCandle, count)
	basePrice := decimal.NewFromInt(50000)
	baseVolume := decimal.NewFromInt(1000)
	now := time.Now()

	for i := 0; i < count; i++ {
		// 模拟价格波动
		priceChange := decimal.NewFromInt(int64(i % 20)).Sub(decimal.NewFromInt(10))
		price := basePrice.Add(priceChange.Mul(decimal.NewFromInt(100)))

		candles[i] = quantCandle{
			OpenTime:  now.Add(time.Duration(-count+i) * time.Minute),
			CloseTime: now.Add(time.Duration(-count+i+1) * time.Minute),
			Open:      price,
			High:      price.Add(decimal.NewFromInt(50)),
			Low:       price.Sub(decimal.NewFromInt(50)),
			Close:     price.Add(decimal.NewFromInt(int64(i % 10))),
			Volume:    baseVolume.Add(decimal.NewFromInt(int64(i % 100))),
		}
	}

	return candles
}
