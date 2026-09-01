package quant

import (
	"context"
	"encoding/json"
	"errors"

	"coinsphere/backend/plugin/sdk"
)

var quantAssistantQuerySchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"resource":{"type":"string","enum":["backtests","signals"]},"venue":{"type":"string","pattern":"^[a-z][a-z0-9_-]{1,31}$"},"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"status":{"type":"string","maxLength":32},"limit":{"type":"integer","minimum":1,"maximum":50,"default":20}},"required":["resource"],"additionalProperties":false}`)

type assistantDomainQuery struct {
	Resource   string `json:"resource"`
	Venue      string `json:"venue"`
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Status     string `json:"status"`
	Limit      int    `json:"limit"`
}

func (q *quantRuntime) assistantQuery(ctx context.Context, input json.RawMessage, scope sdk.SystemScope) (json.RawMessage, error) {
	if scope.PluginID != quantPluginID || scope.UserID <= 0 {
		return nil, errors.New("invalid Quant assistant scope")
	}
	var request assistantDomainQuery
	if json.Unmarshal(input, &request) != nil {
		return nil, errors.New("invalid Quant assistant query")
	}
	request.Limit = assistantQueryLimit(request.Limit)

	var result any
	switch request.Resource {
	case "backtests":
		var rows []struct {
			ID                                       int64
			StrategyID, Market, Instrument, Interval string
			FinalEquity, TotalReturn, MaxDrawdown    string
			TradeCount, CandleCount                  int
		}
		if request.Status != "" {
			return nil, errors.New("status is not supported for Quant backtests")
		}
		query := q.db.WithContext(ctx).Model(&quantBacktest{}).Limit(request.Limit)
		if request.Market != "" {
			query = query.Where("market = ?", request.Market)
		}
		if request.Venue != "" {
			query = query.Where("venue = ?", request.Venue)
		}
		if request.Instrument != "" {
			query = query.Where("instrument = ?", request.Instrument)
		}
		if err := query.Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
			return nil, errors.New("query Quant backtests failed")
		}
		result = map[string]any{"items": rows}
	case "signals":
		var rows []struct {
			ID, WorkflowID                                 int64
			StrategyID, Market, Instrument, Target, Status string
		}
		query := q.db.WithContext(ctx).Model(&quantSignal{}).Limit(request.Limit)
		if request.Market != "" {
			query = query.Where("market = ?", request.Market)
		}
		if request.Venue != "" {
			query = query.Where("venue = ?", request.Venue)
		}
		if request.Instrument != "" {
			query = query.Where("instrument = ?", request.Instrument)
		}
		if request.Status != "" {
			query = query.Where("status = ?", request.Status)
		}
		if err := query.Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
			return nil, errors.New("query Quant signals failed")
		}
		result = map[string]any{"items": rows}
	default:
		return nil, errors.New("unsupported Quant assistant resource")
	}
	return json.Marshal(result)
}

func assistantQueryLimit(value int) int {
	if value < 1 {
		return 20
	}
	if value > 50 {
		return 50
	}
	return value
}
