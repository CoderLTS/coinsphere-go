package official

import (
	"context"
	"encoding/json"
	"errors"

	"coinsphere/backend/internal/db"
	"coinsphere/backend/plugin/sdk"
)

var quantAssistantQuerySchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"resource":{"type":"string","enum":["instruments","backtests","signals","paper_accounts"]},"market":{"type":"string","enum":["spot","usdm"]},"instrument":{"type":"string","pattern":"^[A-Z0-9]{2,32}$"},"status":{"type":"string","maxLength":32},"limit":{"type":"integer","minimum":1,"maximum":50,"default":20}},"required":["resource"],"additionalProperties":false}`)

var notificationAssistantQuerySchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"channel":{"type":"string","enum":["in_app","dingtalk","qq","smtp"]},"status":{"type":"string","enum":["pending","delivered","failed"]},"limit":{"type":"integer","minimum":1,"maximum":50,"default":20}},"additionalProperties":false}`)

type assistantDomainQuery struct {
	Resource   string `json:"resource"`
	Market     string `json:"market"`
	Instrument string `json:"instrument"`
	Status     string `json:"status"`
	Channel    string `json:"channel"`
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
	case "instruments":
		var rows []struct {
			Market, Symbol, BaseAsset, QuoteAsset, Status string
		}
		query := q.db.WithContext(ctx).Model(&quantInstrument{}).Limit(request.Limit)
		if request.Market != "" {
			query = query.Where("market = ?", request.Market)
		}
		if request.Instrument != "" {
			query = query.Where("symbol = ?", request.Instrument)
		}
		if request.Status != "" {
			query = query.Where("status = ?", request.Status)
		}
		if err := query.Order("symbol").Find(&rows).Error; err != nil {
			return nil, errors.New("query Quant instruments failed")
		}
		result = map[string]any{"items": rows}
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
	case "paper_accounts":
		var rows []struct {
			ID, WorkflowID                              int64
			Status, InitialBalance, CashBalance, Equity string
		}
		if request.Market != "" || request.Instrument != "" {
			return nil, errors.New("market and instrument are not supported for Paper accounts")
		}
		query := q.db.WithContext(ctx).Model(&quantPaperAccount{}).Limit(request.Limit)
		if request.Status != "" {
			query = query.Where("status = ?", request.Status)
		}
		if err := query.Order("updated_at DESC, id DESC").Find(&rows).Error; err != nil {
			return nil, errors.New("query Quant Paper accounts failed")
		}
		result = map[string]any{"items": rows}
	default:
		return nil, errors.New("unsupported Quant assistant resource")
	}
	return json.Marshal(result)
}

func (n *notificationRuntime) assistantQuery(ctx context.Context, input json.RawMessage, scope sdk.SystemScope) (json.RawMessage, error) {
	if scope.PluginID != notificationPluginID || scope.UserID <= 0 {
		return nil, errors.New("invalid Notification assistant scope")
	}
	var request assistantDomainQuery
	if json.Unmarshal(input, &request) != nil {
		return nil, errors.New("invalid Notification assistant query")
	}
	query := n.db.WithContext(ctx).Model(&db.NotificationDelivery{}).Limit(assistantQueryLimit(request.Limit))
	if request.Channel != "" {
		query = query.Where("channel = ?", request.Channel)
	}
	if request.Status != "" {
		query = query.Where("status = ?", request.Status)
	}
	var rows []struct {
		ID           int64  `json:"id"`
		Channel      string `json:"channel"`
		SubjectKey   string `json:"subjectKey"`
		Title        string `json:"title"`
		Status       string `json:"status"`
		AttemptCount int    `json:"attemptCount"`
	}
	if err := query.Select("id, channel, subject_key, title, status, attempt_count").Order("created_at DESC, id DESC").Find(&rows).Error; err != nil {
		return nil, errors.New("query notification deliveries failed")
	}
	return json.Marshal(map[string]any{"items": rows})
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
