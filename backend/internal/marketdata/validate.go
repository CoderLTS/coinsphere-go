package marketdata

import (
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	decimalTextPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
	assetCodePattern   = regexp.MustCompile(`^[A-Z0-9][A-Z0-9._-]*$`)
)

// ParseDecimal 只接受交易所 JSON 字符串中的固定十进制文本，避免浮点和指数语义进入领域层。
func ParseDecimal(text string) (decimal.Decimal, error) {
	if !decimalTextPattern.MatchString(text) {
		return decimal.Zero, errors.New("invalid decimal text")
	}

	integer, fraction, hasFraction := strings.Cut(text, ".")
	if len(integer) > 20 || hasFraction && len(fraction) > 18 {
		return decimal.Zero, errors.New("decimal exceeds numeric(38,18)")
	}

	value, err := decimal.NewFromString(text)
	if err != nil {
		return decimal.Zero, errors.New("invalid decimal text")
	}
	return value, nil
}

// ValidateUUIDv7 拒绝 nil、非 RFC 4122 variant 或非 v7 的持久化资源 ID。
func ValidateUUIDv7(id uuid.UUID) error {
	if id == uuid.Nil || id.Version() != uuid.Version(7) || id.Variant() != uuid.RFC4122 {
		return errors.New("instrument ID must be UUIDv7")
	}
	return nil
}

// ValidateSourceError 约束适配器创建的错误分类和 RetryAfter 语义，避免把重试策略下沉到来源层。
func ValidateSourceError(errorValue SourceError) error {
	switch errorValue.Kind {
	case SourceErrorInvalidRequest, SourceErrorRateLimited, SourceErrorUnavailable, SourceErrorProtocol:
	default:
		return errors.New("invalid source error kind")
	}
	if errorValue.RetryAfter < 0 || errorValue.Kind != SourceErrorRateLimited && errorValue.RetryAfter != 0 {
		return errors.New("invalid retry after")
	}
	return nil
}

// CandleIntervalDuration 返回冻结 interval 的 UTC 固定时长；日线固定为 24 小时。
func CandleIntervalDuration(interval CandleInterval) (time.Duration, bool) {
	switch interval {
	case CandleInterval1m:
		return time.Minute, true
	case CandleInterval5m:
		return 5 * time.Minute, true
	case CandleInterval15m:
		return 15 * time.Minute, true
	case CandleInterval1h:
		return time.Hour, true
	case CandleInterval4h:
		return 4 * time.Hour, true
	case CandleInterval1d:
		return 24 * time.Hour, true
	default:
		return 0, false
	}
}

// ValidateInstrumentMetadata 在外部元数据进入领域层时固定共享枚举、代码和 Decimal 边界。
func ValidateInstrumentMetadata(metadata InstrumentMetadata) error {
	if !validVenue(metadata.Venue) || !validMarketType(metadata.MarketType) || !validInstrumentStatus(metadata.Status) {
		return errors.New("invalid instrument enum")
	}
	if !validCode(metadata.NativeSymbol, 64) || !validCode(metadata.BaseAsset, 32) || !validCode(metadata.QuoteAsset, 32) {
		return errors.New("invalid instrument code")
	}
	if err := validateDecimal(metadata.PriceTick, false); err != nil {
		return errors.New("invalid price tick")
	}
	if err := validateDecimal(metadata.QuantityStep, false); err != nil {
		return errors.New("invalid quantity step")
	}
	if err := validateDecimal(metadata.MinQuantity, false); err != nil {
		return errors.New("invalid minimum quantity")
	}
	if err := validateDecimal(metadata.MinNotional, false); err != nil {
		return errors.New("invalid minimum notional")
	}
	if err := validateUTC(metadata.UpdatedAt); err != nil {
		return errors.New("invalid instrument update time")
	}
	return nil
}

// ValidateInstrument 额外确认持续化边界传入的 ID 符合 UUIDv7。
func ValidateInstrument(instrument Instrument) error {
	if err := ValidateUUIDv7(instrument.ID); err != nil {
		return err
	}
	return ValidateInstrumentMetadata(InstrumentMetadata{
		Venue:        instrument.Venue,
		MarketType:   instrument.MarketType,
		NativeSymbol: instrument.NativeSymbol,
		BaseAsset:    instrument.BaseAsset,
		QuoteAsset:   instrument.QuoteAsset,
		Status:       instrument.Status,
		PriceTick:    instrument.PriceTick,
		QuantityStep: instrument.QuantityStep,
		MinQuantity:  instrument.MinQuantity,
		MinNotional:  instrument.MinNotional,
		UpdatedAt:    instrument.UpdatedAt,
	})
}

// ValidateCandle 约束排他收盘边界与 OHLC，防止畸形外部载荷进入后续存储。
func ValidateCandle(candle Candle) error {
	if !validVenue(candle.Venue) || !validInterval(candle.Interval) {
		return errors.New("invalid candle enum")
	}
	if err := ValidateUUIDv7(candle.InstrumentID); err != nil {
		return err
	}
	if err := validateUTC(candle.OpenTime); err != nil {
		return errors.New("invalid candle open time")
	}
	if err := validateUTC(candle.CloseTime); err != nil {
		return errors.New("invalid candle close time")
	}
	duration, _ := CandleIntervalDuration(candle.Interval)
	if !isIntervalBoundary(candle.OpenTime, duration) || !candle.CloseTime.Equal(candle.OpenTime.Add(duration)) {
		return errors.New("invalid candle time boundary")
	}
	for _, value := range []decimal.Decimal{candle.Open, candle.High, candle.Low, candle.Close} {
		if err := validateDecimal(value, false); err != nil {
			return errors.New("invalid candle price")
		}
	}
	if err := validateDecimal(candle.BaseVolume, true); err != nil {
		return errors.New("invalid candle volume")
	}
	if candle.Low.GreaterThan(candle.Open) || candle.Low.GreaterThan(candle.Close) || candle.High.LessThan(candle.Open) || candle.High.LessThan(candle.Close) {
		return errors.New("invalid candle OHLC")
	}
	return nil
}

// ValidateTicker 固定快照的 UTC 事件时间和买卖价边界。
func ValidateTicker(ticker Ticker) error {
	if !validVenue(ticker.Venue) {
		return errors.New("invalid ticker venue")
	}
	if err := ValidateUUIDv7(ticker.InstrumentID); err != nil {
		return err
	}
	if err := validateUTC(ticker.OccurredAt); err != nil {
		return errors.New("invalid ticker time")
	}
	for _, value := range []decimal.Decimal{ticker.LastPrice, ticker.BestBidPrice, ticker.BestAskPrice} {
		if err := validateDecimal(value, false); err != nil {
			return errors.New("invalid ticker price")
		}
	}
	if ticker.BestBidPrice.GreaterThan(ticker.BestAskPrice) {
		return errors.New("invalid ticker spread")
	}
	return nil
}

// ValidateCandlePageRequest 保证分页窗口及 cursor 在请求源边界就保持可重放语义。
func ValidateCandlePageRequest(request CandlePageRequest) error {
	if err := ValidateInstrument(request.Instrument); err != nil {
		return err
	}
	duration, ok := CandleIntervalDuration(request.Interval)
	if !ok {
		return errors.New("invalid candle interval")
	}
	if err := validateUTC(request.StartTime); err != nil {
		return errors.New("invalid candle start time")
	}
	if err := validateUTC(request.EndTime); err != nil {
		return errors.New("invalid candle end time")
	}
	if !isIntervalBoundary(request.StartTime, duration) || !isIntervalBoundary(request.EndTime, duration) || !request.StartTime.Before(request.EndTime) {
		return errors.New("invalid candle window")
	}
	if request.Limit < 1 || request.Limit > 300 {
		return errors.New("invalid candle limit")
	}
	if request.Cursor == "" {
		return nil
	}
	cursor, err := parseCursor(request.Cursor)
	if err != nil || !isIntervalBoundary(cursor, duration) || cursor.Before(request.StartTime) || !cursor.Before(request.EndTime) {
		return errors.New("invalid candle cursor")
	}
	return nil
}

// ValidateCandlePage 在适配器输出边界检查顺序、窗口、游标推进和逻辑键唯一性。
func ValidateCandlePage(request CandlePageRequest, page CandlePage) error {
	if err := ValidateCandlePageRequest(request); err != nil {
		return err
	}
	if len(page.Candles) > request.Limit {
		return errors.New("candle page exceeds limit")
	}
	if len(page.Candles) == 0 {
		if page.NextCursor != "" {
			return errors.New("empty candle page has cursor")
		}
		return nil
	}

	lowerBound := request.StartTime
	if request.Cursor != "" {
		lowerBound, _ = parseCursor(request.Cursor)
	}
	for index, candle := range page.Candles {
		if err := ValidateCandle(candle); err != nil {
			return err
		}
		if candle.Venue != request.Instrument.Venue || candle.InstrumentID != request.Instrument.ID || candle.Interval != request.Interval {
			return errors.New("candle does not match request instrument")
		}
		if candle.OpenTime.Before(lowerBound) || !candle.OpenTime.Before(request.EndTime) {
			return errors.New("candle is outside request window")
		}
		if index > 0 && !page.Candles[index-1].OpenTime.Before(candle.OpenTime) {
			return errors.New("candle page is not strictly ordered")
		}
	}
	if request.Cursor != "" && !page.Candles[0].OpenTime.Equal(lowerBound) {
		return errors.New("candle page skipped cursor")
	}
	if page.NextCursor == "" {
		return nil
	}

	duration, _ := CandleIntervalDuration(request.Interval)
	next, err := parseCursor(page.NextCursor)
	if err != nil || !isIntervalBoundary(next, duration) || next.Before(request.StartTime) || !next.Before(request.EndTime) {
		return errors.New("invalid next candle cursor")
	}
	if !next.Equal(page.Candles[len(page.Candles)-1].OpenTime.Add(duration)) {
		return errors.New("next candle cursor does not advance")
	}
	if len(page.Candles) < request.Limit {
		return errors.New("short candle page has cursor")
	}
	return nil
}

func validVenue(value Venue) bool {
	return value == VenueBinance
}

func validMarketType(value MarketType) bool {
	return value == MarketTypeSpot || value == MarketTypeUSDM
}

func validInstrumentStatus(value InstrumentStatus) bool {
	return value == InstrumentStatusTrading || value == InstrumentStatusSuspended
}

func validInterval(value CandleInterval) bool {
	_, ok := CandleIntervalDuration(value)
	return ok
}

func validCode(value string, limit int) bool {
	return len(value) <= limit && assetCodePattern.MatchString(value)
}

func validateDecimal(value decimal.Decimal, allowZero bool) error {
	if value.Sign() < 0 || !allowZero && value.Sign() == 0 {
		return errors.New("decimal sign is invalid")
	}
	_, err := ParseDecimal(value.String())
	return err
}

func validateUTC(value time.Time) error {
	if value.IsZero() || value.Location() != time.UTC {
		return errors.New("time must be UTC")
	}
	return nil
}

func isIntervalBoundary(value time.Time, duration time.Duration) bool {
	return value.Nanosecond() == 0 && value.Unix()%int64(duration/time.Second) == 0
}

func parseCursor(cursor CandleCursor) (time.Time, error) {
	text := string(cursor)
	if !strings.HasSuffix(text, "Z") {
		return time.Time{}, errors.New("cursor must be UTC")
	}
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || value.Location() != time.UTC || value.Format(time.RFC3339Nano) != text {
		return time.Time{}, errors.New("invalid cursor")
	}
	return value, nil
}
