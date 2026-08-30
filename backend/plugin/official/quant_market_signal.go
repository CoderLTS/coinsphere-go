package official

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"coinsphere/backend/plugin/sdk"
	"gorm.io/gorm"
)

var quantMarketSignalInputSchema = json.RawMessage(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema","type":"object",
  "properties":{
    "market":{"type":"string","title":"Market","enum":["spot","usdm"],"x-coinsphere-field-source":true},
    "instrument":{"type":"string","title":"Instrument","pattern":"^[A-Z0-9]{2,32}$","x-coinsphere-field-source":true},
    "interval":{"type":"string","title":"Interval","enum":["1m","3m","5m","15m","30m","1h","2h","4h","6h","8h","12h","1d","3d","1w"],"x-coinsphere-field-source":true},
    "name":{"type":"string","title":"Signal name","minLength":1,"maxLength":80,"x-coinsphere-field-source":true},
    "indicator":{"type":"string","title":"Indicator","minLength":1,"maxLength":64,"x-coinsphere-field-source":true},
    "candleCloseTime":{"type":"string","title":"Candle close time","format":"date-time","x-coinsphere-field-source":true},
    "summary":{"type":"string","title":"Summary","minLength":1,"maxLength":2000,"x-coinsphere-field-source":true},
    "values":{"type":"object","title":"Values","additionalProperties":{"type":"string"},"x-coinsphere-field-source":true}
  },
  "required":["market","instrument","interval","name","indicator","candleCloseTime","summary","values"],
  "additionalProperties":false
}`)

var quantMarketSignalOutputSchema = json.RawMessage(`{
  "$schema":"https://json-schema.org/draft/2020-12/schema","type":"object",
  "properties":{"signalId":{"type":"integer","minimum":1},"market":{"type":"string"},"instrument":{"type":"string"},"interval":{"type":"string"},"name":{"type":"string"},"indicator":{"type":"string"},"candleCloseTime":{"type":"string","format":"date-time"}},
  "required":["signalId","market","instrument","interval","name","indicator","candleCloseTime"],"additionalProperties":false
}`)

type quantMarketSignalAction struct{ runtime *quantRuntime }

func (q *quantRuntime) registerMarketSignals(registrar sdk.Registrar) error {
	return registrar.Action(sdk.NodeDescriptor{
		Type: "official.quant.market_signal", Version: "1.0.0", Kind: sdk.NodeKindAction,
		ConfigSchema: emptyObjectSchema, UISchema: json.RawMessage(`{"ui:order":[]}`),
		InputSchema: quantMarketSignalInputSchema, OutputSchema: quantMarketSignalOutputSchema,
		Pool: sdk.PoolStream, SideEffect: sdk.SideEffectData, State: sdk.StateStateless,
	}, quantMarketSignalAction{runtime: q})
}

func (a quantMarketSignalAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	var input struct {
		Market, Instrument, Interval, Name, Indicator, CandleCloseTime, Summary string
		Values map[string]string `json:"values"`
	}
	if !decodeQuantStrict(request.Input, &input) {
		return sdk.ActionResult{}, errors.New("quant market signal input is invalid")
	}
	series, err := parseQuantSeriesConfig(mustMarshal(map[string]any{
		"market": input.Market, "instrument": input.Instrument, "interval": input.Interval,
	}))
	if err != nil {
		return sdk.ActionResult{}, err
	}
	input.Name, input.Indicator, input.Summary = strings.TrimSpace(input.Name), strings.TrimSpace(input.Indicator), strings.TrimSpace(input.Summary)
	candleCloseTime, timeErr := parseQuantUTCTime(input.CandleCloseTime)
	workflowID, workflowErr := quantInt64(request.Revision.WorkflowID)
	revisionID, revisionErr := quantInt64(request.Revision.RevisionID)
	if timeErr != nil || workflowErr != nil || revisionErr != nil || input.Values == nil ||
		!quantMarketSignalIndicator(input.Indicator) || utf8.RuneCountInString(input.Name) < 1 || utf8.RuneCountInString(input.Name) > 80 ||
		utf8.RuneCountInString(input.Summary) < 1 || utf8.RuneCountInString(input.Summary) > 2000 {
		return sdk.ActionResult{}, errors.New("quant market signal identity is invalid")
	}
	if existing, ok, err := a.runtime.loadQuantMarketSignalByOperation(ctx, request.OperationKey); err != nil {
		return sdk.ActionResult{}, err
	} else if ok {
		return quantMarketSignalResult(existing), nil
	}
	values, err := json.Marshal(input.Values)
	if err != nil {
		return sdk.ActionResult{}, errors.New("encode Quant market signal values failed")
	}
	row := quantMarketSignal{
		OperationKey: request.OperationKey, WorkflowID: workflowID, RevisionID: revisionID,
		NodeInstanceID: request.NodeInstanceID, Market: series.Market, Instrument: series.Instrument,
		Interval: series.Interval, Name: input.Name, Indicator: input.Indicator,
		CandleCloseTime: candleCloseTime, Summary: input.Summary, Values: string(values), CreatedAt: time.Now().UTC(),
	}
	if err := a.runtime.db.WithContext(ctx).Create(&row).Error; err != nil {
		if existing, ok, loadErr := a.runtime.loadQuantMarketSignalByOperation(ctx, request.OperationKey); loadErr == nil && ok {
			return quantMarketSignalResult(existing), nil
		}
		return sdk.ActionResult{}, errors.New("persist Quant market signal failed")
	}
	return quantMarketSignalResult(row), nil
}

func quantMarketSignalIndicator(value string) bool {
	for _, definition := range quantIndicatorDefinitions {
		if definition.Indicator == value {
			return true
		}
	}
	return false
}

func (q *quantRuntime) loadQuantMarketSignalByOperation(ctx context.Context, operationKey string) (quantMarketSignal, bool, error) {
	var signal quantMarketSignal
	if err := q.db.WithContext(ctx).Where("operation_key = ?", operationKey).First(&signal).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return quantMarketSignal{}, false, nil
		}
		return quantMarketSignal{}, false, errors.New("load Quant market signal failed")
	}
	return signal, true, nil
}

func quantMarketSignalResult(signal quantMarketSignal) sdk.ActionResult {
	return sdk.ActionResult{Output: mustMarshal(map[string]any{
		"signalId": signal.ID, "market": signal.Market, "instrument": signal.Instrument,
		"interval": signal.Interval, "name": signal.Name, "indicator": signal.Indicator,
		"candleCloseTime": signal.CandleCloseTime.UTC().Format(time.RFC3339Nano),
	})}
}

func (q *quantRuntime) handleQuantMarketSignals(w http.ResponseWriter, r *http.Request, scope sdk.RouteScope) {
	if !validQuantSystemScope(scope) || !quantQueryKeys(r, "market", "instrument", "interval", "startTime", "endTime", "limit") {
		writeQuantProblem(w, http.StatusBadRequest, "invalid Quant market signal query")
		return
	}
	series, err := parseQuantSeriesConfig(mustMarshal(map[string]any{
		"market": r.URL.Query().Get("market"), "instrument": r.URL.Query().Get("instrument"), "interval": r.URL.Query().Get("interval"),
	}))
	if err != nil {
		writeQuantProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	limit, err := quantQueryLimit(r, 200, 500)
	if err != nil {
		writeQuantProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	query := q.db.WithContext(r.Context()).Where(
		"market = ? AND instrument = ? AND interval = ?", series.Market, series.Instrument, series.Interval,
	)
	start, startSet, err := quantMarketSignalQueryTime(r, "startTime")
	if err != nil {
		writeQuantProblem(w, http.StatusBadRequest, err.Error())
		return
	}
	end, endSet, err := quantMarketSignalQueryTime(r, "endTime")
	if err != nil || startSet && endSet && start.After(end) {
		writeQuantProblem(w, http.StatusBadRequest, "Quant market signal time range is invalid")
		return
	}
	if startSet {
		query = query.Where("candle_close_time >= ?", start)
	}
	if endSet {
		query = query.Where("candle_close_time <= ?", end)
	}
	var signals []quantMarketSignal
	if err := query.Order("candle_close_time DESC, id DESC").Limit(limit).Find(&signals).Error; err != nil {
		writeQuantProblem(w, http.StatusInternalServerError, "list Quant market signals failed")
		return
	}
	items := make([]map[string]any, len(signals))
	for index, signal := range signals {
		items[index] = map[string]any{
			"id": signal.ID, "market": signal.Market, "instrument": signal.Instrument,
			"interval": signal.Interval, "name": signal.Name, "indicator": signal.Indicator,
			"candleCloseTime": signal.CandleCloseTime.UTC().Format(time.RFC3339Nano),
			"summary": signal.Summary, "values": json.RawMessage(signal.Values),
			"createdAt": signal.CreatedAt.UTC().Format(time.RFC3339Nano),
		}
	}
	writeQuantOK(w, map[string]any{"items": items})
}

func quantMarketSignalQueryTime(r *http.Request, key string) (time.Time, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return time.Time{}, false, nil
	}
	value, err := parseQuantUTCTime(raw)
	if err != nil {
		return time.Time{}, false, errors.New("Quant market signal time must use RFC3339 UTC")
	}
	return value, true, nil
}

var _ sdk.ActionHandler = quantMarketSignalAction{}
