package official

import (
	"context"
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
	"gorm.io/gorm/clause"
)

const maxQuantResponseBytes = 32 << 20

type quantCandleRealtimeTrigger struct{ runtime *quantRuntime }
type quantCandleBackfillAction struct{ runtime *quantRuntime }

func (t quantCandleRealtimeTrigger) Run(ctx context.Context, request sdk.TriggerRequest, emitter sdk.Emitter) error {
	config, err := parseQuantCandleStreamConfig(request.Config)
	if err != nil {
		return err
	}
	return t.runtime.hub.subscribe(ctx, config, emitter)
}

func (a quantCandleBackfillAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	now := time.Now().UTC()
	config, err := parseQuantCandleBackfillConfig(request.Config, now)
	if err != nil {
		return sdk.ActionResult{}, err
	}
	fetchedCount, insertedCount := 0, int64(0)
	for _, interval := range config.Intervals {
		series := quantSeriesConfig{Market: config.Market, Instrument: config.Instrument, Interval: interval}
		candles, err := a.runtime.fetchQuantKlinesBefore(ctx, series, config.EndTime, config.CandleCount)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		fetchedCount += len(candles)
		inserted, err := a.runtime.persistQuantCandles(ctx, candles)
		if err != nil {
			return sdk.ActionResult{}, err
		}
		insertedCount += inserted
	}
	completedAt := time.Now().UTC()
	request.Logger.Info("Binance K 线补数完成",
		"market", config.Market, "instrument", config.Instrument, "intervals", strings.Join(config.Intervals, ","),
		"requested_count_per_interval", config.CandleCount, "fetched_count", fetchedCount, "inserted_count", insertedCount,
	)
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"market": config.Market, "instrument": config.Instrument, "intervals": config.Intervals,
		"requestedCountPerInterval": config.CandleCount, "fetchedCount": fetchedCount,
		"insertedCount": insertedCount, "completedAt": completedAt.Format(time.RFC3339Nano),
	})}, nil
}

type quantCandleHub struct {
	runtime        *quantRuntime
	mu             sync.Mutex
	nextSubscriber uint64
	subscriptions  map[string]*quantCandleSubscription
}

type quantCandleSubscription struct {
	config      quantCandleStreamConfig
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

func (h *quantCandleHub) subscribe(ctx context.Context, config quantCandleStreamConfig, emitter sdk.Emitter) error {
	key := config.Market + ":" + config.Instrument + ":" + strings.Join(config.Intervals, ",")
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
	var workers sync.WaitGroup
	for _, interval := range subscription.config.Intervals {
		workers.Add(1)
		go func(interval string) {
			defer workers.Done()
			for subscription.ctx.Err() == nil {
				_ = h.runtime.streamQuantCandles(subscription.ctx, subscription, interval)
				select {
				case <-subscription.ctx.Done():
					return
				case <-time.After(time.Second):
				}
			}
		}(interval)
	}
	workers.Wait()
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
	_, err := q.persistQuantCandles(ctx, []quantCandle{candle})
	return err
}

func (q *quantRuntime) persistQuantCandles(ctx context.Context, candles []quantCandle) (int64, error) {
	if len(candles) == 0 {
		return 0, nil
	}
	receivedAt := time.Now().UTC()
	for index := range candles {
		candles[index].ReceivedAt = receivedAt
	}
	result := q.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).CreateInBatches(&candles, 500)
	if result.Error != nil {
		return 0, errors.New("persist Quant candles failed")
	}
	return result.RowsAffected, nil
}

func (q *quantRuntime) fetchQuantKlinesBefore(ctx context.Context, config quantSeriesConfig, end time.Time, count int) ([]quantCandle, error) {
	result := make([]quantCandle, 0, count)
	cursor := end.UTC()
	for len(result) < count {
		limit := count - len(result)
		if limit > 1000 {
			limit = 1000
		}
		page, err := q.fetchQuantKlinePage(ctx, config, cursor, limit)
		if err != nil {
			return nil, err
		}
		if len(page) == 0 {
			break
		}
		for _, candle := range page {
			if !candle.CloseTime.After(end) {
				result = append(result, candle)
			}
		}
		if len(result) >= count || len(page) < limit {
			break
		}
		cursor = page[0].OpenTime.Add(-time.Millisecond)
	}
	return result, nil
}

func (q *quantRuntime) fetchQuantKlinePage(ctx context.Context, config quantSeriesConfig, end time.Time, limit int) ([]quantCandle, error) {
	base, path := "https://data-api.binance.vision", "/api/v3/klines"
	if config.Market == "usdm" {
		base, path = "https://fapi.binance.com", "/fapi/v1/klines"
	}
	values := url.Values{
		"symbol": {config.Instrument}, "interval": {config.Interval},
		"endTime": {strconv.FormatInt(end.UnixMilli(), 10)}, "limit": {strconv.Itoa(limit)},
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

func (q *quantRuntime) streamQuantCandles(ctx context.Context, subscription *quantCandleSubscription, interval string) error {
	config := subscription.config
	host := "data-stream.binance.vision:9443"
	if config.Market == "usdm" {
		host = "fstream.binance.com"
	}
	target, _ := url.Parse("wss://" + host + "/ws/" + strings.ToLower(config.Instrument) + "@kline_" + interval)
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
		if message.Symbol != config.Instrument || message.Kline.Interval != interval {
			return errors.New("binance WebSocket kline payload is invalid")
		}
		if !message.Kline.Closed {
			continue
		}
		raw := mustMarshal([]any{
			message.Kline.OpenTime, message.Kline.Open, message.Kline.High, message.Kline.Low,
			message.Kline.Close, message.Kline.Volume, message.Kline.CloseTime,
		})
		series := quantSeriesConfig{Market: config.Market, Instrument: config.Instrument, Interval: interval}
		candle, err := parseQuantKline(series, raw)
		if err != nil {
			return errors.New("binance WebSocket kline payload is invalid")
		}
		if err := q.broadcastQuantCandle(subscription, candle); err != nil {
			return err
		}
	}
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

var _ sdk.TriggerHandler = quantCandleRealtimeTrigger{}
var _ sdk.ActionHandler = quantCandleBackfillAction{}
