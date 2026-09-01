package quant

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"coinsphere/backend/plugin/sdk"
)

func quantPathInt64(value string) (int64, error) {
	number, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || number <= 0 {
		return 0, errors.New("positive integer path is required")
	}
	return number, nil
}

func quantCandleData(candle quantCandle) map[string]any {
	return map[string]any{
		"venue": candle.Venue, "market": candle.Market, "instrument": candle.Instrument, "interval": candle.Interval,
		"openTime": candle.OpenTime.UTC().Format(time.RFC3339Nano), "closeTime": candle.CloseTime.UTC().Format(time.RFC3339Nano),
		"open": candle.Open.String(), "high": candle.High.String(), "low": candle.Low.String(), "close": candle.Close.String(), "volume": candle.Volume.String(),
	}
}

func quantSDKCandle(candle quantCandle) sdk.Candle {
	return sdk.Candle{OpenTime: candle.OpenTime.UTC(), CloseTime: candle.CloseTime.UTC(), Open: candle.Open, High: candle.High, Low: candle.Low, Close: candle.Close, Volume: candle.Volume}
}
