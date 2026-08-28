package official

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"coinsphere/backend/plugin/sdk"
	cloudevents "github.com/cloudevents/sdk-go/v2"
	"github.com/gorilla/websocket"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const maxQuantResponseBytes = 32 << 20

type quantCandleTrigger struct{ runtime *quantRuntime }

func (t quantCandleTrigger) Run(ctx context.Context, request sdk.TriggerRequest, emitter sdk.Emitter) error {
	config, err := parseQuantSeriesConfig(request.Config)
	if err != nil {
		return err
	}
	return t.runtime.hub.subscribe(ctx, config, emitter)
}

type quantCandleHub struct {
	runtime        *quantRuntime
	mu             sync.Mutex
	nextSubscriber uint64
	subscriptions  map[string]*quantCandleSubscription
}

type quantCandleSubscription struct {
	config      quantSeriesConfig
	ctx         context.Context
	cancel      context.CancelFunc
	subscribers map[uint64]quantCandleSubscriber
}

type quantCandleSubscriber struct {
	ctx     context.Context
	emitter sdk.Emitter
}

func newQuantCandleHub(runtime *quantRuntime) *quantCandleHub {
	return &quantCandleHub{runtime: runtime, subscriptions: map[string]*quantCandleSubscription{}}
}

func (h *quantCandleHub) subscribe(ctx context.Context, config quantSeriesConfig, emitter sdk.Emitter) error {
	key := config.Market + ":" + config.Instrument + ":" + config.Interval
	h.mu.Lock()
	subscription := h.subscriptions[key]
	if subscription == nil {
		workerCtx, cancel := context.WithCancel(context.Background())
		subscription = &quantCandleSubscription{
			config: config, ctx: workerCtx, cancel: cancel, subscribers: map[uint64]quantCandleSubscriber{},
		}
		h.subscriptions[key] = subscription
		go h.run(key, subscription)
	}
	h.nextSubscriber++
	subscriberID := h.nextSubscriber
	subscription.subscribers[subscriberID] = quantCandleSubscriber{ctx: ctx, emitter: emitter}
	h.mu.Unlock()

	<-ctx.Done()
	h.mu.Lock()
	if current := h.subscriptions[key]; current == subscription {
		delete(subscription.subscribers, subscriberID)
		if len(subscription.subscribers) == 0 {
			delete(h.subscriptions, key)
			subscription.cancel()
		}
	}
	h.mu.Unlock()
	return ctx.Err()
}

func (h *quantCandleHub) run(key string, subscription *quantCandleSubscription) {
	for subscription.ctx.Err() == nil {
		if err := h.runtime.syncQuantInstruments(subscription.ctx, subscription.config.Market); err == nil {
			if err = h.runtime.backfillQuantCandles(subscription.ctx, subscription); err == nil {
				_ = h.runtime.streamQuantCandles(subscription.ctx, subscription)
			}
		}
		select {
		case <-subscription.ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}
	h.mu.Lock()
	if h.subscriptions[key] == subscription && len(subscription.subscribers) == 0 {
		delete(h.subscriptions, key)
	}
	h.mu.Unlock()
}

func (q *quantRuntime) broadcastQuantCandle(subscription *quantCandleSubscription, candle quantCandle) error {
	if err := q.persistQuantCandle(subscription.ctx, candle); err != nil {
		return err
	}
	event := cloudevents.NewEvent()
	event.SetID(candle.SourceEventID)
	event.SetSource("urn:coinsphere:plugin:official.quant")
	event.SetType("market.candle.closed")
	event.SetSubject("binance:" + candle.Market + ":" + candle.Instrument + ":" + candle.Interval)
	event.SetTime(candle.CloseTime.UTC())
	event.SetExtension("partitionkey", "binance:"+candle.Market+":"+candle.Instrument+":"+candle.Interval)
	if err := event.SetData(cloudevents.ApplicationJSON, quantCandleData(candle)); err != nil {
		return errors.New("encode Quant candle event failed")
	}

	q.hub.mu.Lock()
	subscribers := make([]quantCandleSubscriber, 0, len(subscription.subscribers))
	for _, subscriber := range subscription.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	q.hub.mu.Unlock()
	for _, subscriber := range subscribers {
		if subscriber.ctx.Err() != nil {
			continue
		}
		if err := subscriber.emitter.Emit(subscriber.ctx, event); err != nil && subscriber.ctx.Err() == nil {
			return err
		}
	}
	return nil
}

func (q *quantRuntime) persistQuantCandle(ctx context.Context, candle quantCandle) error {
	candle.ReceivedAt = time.Now().UTC()
	if err := q.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&candle).Error; err != nil {
		return errors.New("persist Quant candle failed")
	}
	return nil
}

func (q *quantRuntime) backfillQuantCandles(ctx context.Context, subscription *quantCandleSubscription) error {
	config := subscription.config
	duration := quantIntervals[config.Interval]
	now := time.Now().UTC()
	start := now.Add(-500 * duration)
	var latest sql.NullTime
	err := q.db.WithContext(ctx).Model(&quantCandle{}).
		Where("market = ? AND instrument = ? AND interval = ?", config.Market, config.Instrument, config.Interval).
		Select("MAX(open_time)").Scan(&latest).Error
	if err != nil {
		return errors.New("load latest Quant candle failed")
	}
	if latest.Valid {
		start = latest.Time.UTC().Add(duration)
	}
	for ctx.Err() == nil {
		candles, err := q.fetchQuantKlines(ctx, config, start, 1000)
		if err != nil {
			return err
		}
		if len(candles) == 0 {
			return nil
		}
		closed := 0
		for _, candle := range candles {
			if candle.CloseTime.After(now) {
				continue
			}
			closed++
			if err := q.broadcastQuantCandle(subscription, candle); err != nil {
				return err
			}
		}
		last := candles[len(candles)-1].OpenTime
		if len(candles) < 1000 || closed < len(candles) || !last.Before(now) {
			return nil
		}
		start = last.Add(duration)
	}
	return ctx.Err()
}

func (q *quantRuntime) fetchQuantKlines(ctx context.Context, config quantSeriesConfig, start time.Time, limit int) ([]quantCandle, error) {
	base, path := "https://data-api.binance.vision", "/api/v3/klines"
	if config.Market == "usdm" {
		base, path = "https://fapi.binance.com", "/fapi/v1/klines"
	}
	values := url.Values{
		"symbol": {config.Instrument}, "interval": {config.Interval},
		"startTime": {strconv.FormatInt(start.UnixMilli(), 10)}, "limit": {strconv.Itoa(limit)},
	}
	var rows []json.RawMessage
	if err := q.getQuantJSON(ctx, base+path+"?"+values.Encode(), &rows); err != nil {
		return nil, err
	}
	candles := make([]quantCandle, 0, len(rows))
	for _, raw := range rows {
		candle, err := parseQuantKline(config, raw)
		if err != nil {
			return nil, err
		}
		candles = append(candles, candle)
	}
	return candles, nil
}

func parseQuantKline(config quantSeriesConfig, raw json.RawMessage) (quantCandle, error) {
	var fields []json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || len(fields) < 7 {
		return quantCandle{}, errors.New("binance kline payload is invalid")
	}
	var openMillis, closeMillis int64
	var values [5]string
	if json.Unmarshal(fields[0], &openMillis) != nil || json.Unmarshal(fields[6], &closeMillis) != nil ||
		json.Unmarshal(fields[1], &values[0]) != nil || json.Unmarshal(fields[2], &values[1]) != nil ||
		json.Unmarshal(fields[3], &values[2]) != nil || json.Unmarshal(fields[4], &values[3]) != nil ||
		json.Unmarshal(fields[5], &values[4]) != nil {
		return quantCandle{}, errors.New("binance kline fields are invalid")
	}
	decimals := make([]decimal.Decimal, len(values))
	for index, value := range values {
		parsed, err := decimal.NewFromString(value)
		if err != nil {
			return quantCandle{}, errors.New("binance kline Decimal is invalid")
		}
		decimals[index] = parsed
	}
	openTime, closeTime := time.UnixMilli(openMillis).UTC(), time.UnixMilli(closeMillis).UTC()
	if !closeTime.After(openTime) || decimals[0].Sign() <= 0 || decimals[1].Sign() <= 0 ||
		decimals[2].Sign() <= 0 || decimals[3].Sign() <= 0 || decimals[4].Sign() < 0 ||
		decimals[1].LessThan(decimal.Max(decimals[0], decimal.Max(decimals[2], decimals[3]))) ||
		decimals[2].GreaterThan(decimal.Min(decimals[0], decimal.Min(decimals[1], decimals[3]))) {
		return quantCandle{}, errors.New("binance kline values are invalid")
	}
	return quantCandle{
		Market: config.Market, Instrument: config.Instrument, Interval: config.Interval,
		OpenTime: openTime, CloseTime: closeTime, Open: decimals[0], High: decimals[1],
		Low: decimals[2], Close: decimals[3], Volume: decimals[4],
		SourceEventID: fmt.Sprintf("%s:%s:%s:%d", config.Market, config.Instrument, config.Interval, openMillis),
	}, nil
}

func (q *quantRuntime) streamQuantCandles(ctx context.Context, subscription *quantCandleSubscription) error {
	config := subscription.config
	host := "data-stream.binance.vision:9443"
	if config.Market == "usdm" {
		host = "fstream.binance.com"
	}
	target, _ := url.Parse("wss://" + host + "/ws/" + strings.ToLower(config.Instrument) + "@kline_" + config.Interval)
	if err := q.client.validateWebSocketURL(ctx, target, false); err != nil {
		return err
	}
	dialer := websocket.Dialer{Proxy: nil, NetDialContext: q.client.dialContext, HandshakeTimeout: 10 * time.Second}
	connection, response, err := dialer.DialContext(ctx, target.String(), http.Header{"User-Agent": []string{"CoinSphere-Quant/1.0"}})
	if response != nil && response.Body != nil {
		response.Body.Close()
	}
	if err != nil {
		return err
	}
	connection.SetReadLimit(maxConnectorPayloadBytes)
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			connection.Close()
		case <-closed:
		}
	}()
	defer close(closed)
	defer connection.Close()
	for {
		var message struct {
			Symbol string `json:"s"`
			Kline  struct {
				OpenTime  int64  `json:"t"`
				CloseTime int64  `json:"T"`
				Interval  string `json:"i"`
				Open      string `json:"o"`
				High      string `json:"h"`
				Low       string `json:"l"`
				Close     string `json:"c"`
				Volume    string `json:"v"`
				Closed    bool   `json:"x"`
			} `json:"k"`
		}
		if err := connection.ReadJSON(&message); err != nil {
			return err
		}
		if !message.Kline.Closed {
			continue
		}
		raw := mustMarshal([]any{
			message.Kline.OpenTime, message.Kline.Open, message.Kline.High, message.Kline.Low,
			message.Kline.Close, message.Kline.Volume, message.Kline.CloseTime,
		})
		candle, err := parseQuantKline(config, raw)
		if err != nil || message.Symbol != config.Instrument || message.Kline.Interval != config.Interval {
			return errors.New("binance WebSocket kline payload is invalid")
		}
		if err := q.broadcastQuantCandle(subscription, candle); err != nil {
			return err
		}
	}
}

func (q *quantRuntime) syncQuantInstruments(ctx context.Context, market string) error {
	target := "https://data-api.binance.vision/api/v3/exchangeInfo"
	if market == "usdm" {
		target = "https://fapi.binance.com/fapi/v1/exchangeInfo"
	}
	var payload struct {
		Symbols []struct {
			Symbol, BaseAsset, QuoteAsset, Status string
			Filters                               []struct {
				FilterType, TickSize, StepSize, MinQty string
			} `json:"filters"`
		} `json:"symbols"`
	}
	if err := q.getQuantJSON(ctx, target, &payload); err != nil {
		return err
	}
	now := time.Now().UTC()
	return q.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, item := range payload.Symbols {
			instrument := quantInstrument{
				Market: market, Symbol: item.Symbol, BaseAsset: item.BaseAsset, QuoteAsset: item.QuoteAsset,
				Status: item.Status, UpdatedAt: now,
			}
			valid := true
			for _, filter := range item.Filters {
				switch filter.FilterType {
				case "PRICE_FILTER":
					value, parseErr := decimal.NewFromString(filter.TickSize)
					instrument.PriceTick, valid = value, valid && parseErr == nil
				case "LOT_SIZE":
					step, stepErr := decimal.NewFromString(filter.StepSize)
					minimum, minimumErr := decimal.NewFromString(filter.MinQty)
					instrument.QuantityStep, instrument.MinQuantity = step, minimum
					valid = valid && stepErr == nil && minimumErr == nil
				}
			}
			if !valid || !quantInstrumentPattern.MatchString(instrument.Symbol) ||
				!quantInstrumentPattern.MatchString(instrument.BaseAsset) || !quantInstrumentPattern.MatchString(instrument.QuoteAsset) ||
				strings.TrimSpace(instrument.Status) == "" || instrument.PriceTick.Sign() <= 0 ||
				instrument.QuantityStep.Sign() <= 0 || instrument.MinQuantity.Sign() < 0 {
				continue
			}
			if err := tx.Clauses(clause.OnConflict{
				Columns: []clause.Column{{Name: "market"}, {Name: "symbol"}},
				DoUpdates: clause.AssignmentColumns([]string{
					"base_asset", "quote_asset", "status", "price_tick", "quantity_step", "min_quantity", "updated_at",
				}),
			}).Create(&instrument).Error; err != nil {
				return errors.New("persist Binance instrument metadata failed")
			}
		}
		return nil
	})
}

func (q *quantRuntime) getQuantJSON(ctx context.Context, target string, destination any) error {
	callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(callCtx, http.MethodGet, target, nil)
	if err != nil {
		return errors.New("create Binance public request failed")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "CoinSphere-Quant/1.0")
	response, err := q.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("binance public response status %d", response.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxQuantResponseBytes+1))
	if err != nil || len(raw) > maxQuantResponseBytes {
		return errors.New("binance public response exceeds the 32 MiB limit")
	}
	if json.Unmarshal(raw, destination) != nil {
		return errors.New("decode Binance public response failed")
	}
	return nil
}

func quantCandleData(candle quantCandle) map[string]any {
	return map[string]any{
		"market": candle.Market, "instrument": candle.Instrument, "interval": candle.Interval,
		"openTime":  candle.OpenTime.UTC().Format(time.RFC3339Nano),
		"closeTime": candle.CloseTime.UTC().Format(time.RFC3339Nano),
		"open":      candle.Open.String(), "high": candle.High.String(), "low": candle.Low.String(),
		"close": candle.Close.String(), "volume": candle.Volume.String(),
	}
}

func quantSDKCandle(candle quantCandle) sdk.Candle {
	return sdk.Candle{
		OpenTime: candle.OpenTime.UTC(), CloseTime: candle.CloseTime.UTC(),
		Open: candle.Open, High: candle.High, Low: candle.Low, Close: candle.Close, Volume: candle.Volume,
	}
}

var _ sdk.TriggerHandler = quantCandleTrigger{}
