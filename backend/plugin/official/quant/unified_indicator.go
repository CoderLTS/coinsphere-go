package quant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"coinsphere/backend/plugin/sdk"
)

// quantUnifiedIndicatorAction 统一技术指标节点
type quantUnifiedIndicatorAction struct {
	runtime *quantRuntime
}

// UnifiedIndicatorConfig 统一指标配置
type UnifiedIndicatorConfig struct {
	// 数据源配置
	DataSource struct {
		Mode       string `json:"mode"` // inherit, query
		Venue      string `json:"venue"`
		Market     string `json:"market"`
		Instrument string `json:"instrument"`
		Interval   string `json:"interval"`
	} `json:"dataSource"`

	// 监控配置
	Monitoring struct {
		CheckInterval string `json:"checkInterval"`
		Name          string `json:"name"`
	} `json:"monitoring"`

	// 指标配置
	Indicator struct {
		Type       string                 `json:"type"`
		Parameters map[string]interface{} `json:"parameters"`
	} `json:"indicator"`
}

var unifiedIndicatorBasicSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "properties": {
    "dataSource": {
      "type": "object",
      "title": "数据源",
      "properties": {
        "mode": {"type": "string", "enum": ["query"], "default": "query"},
        "venue": {"type": "string", "title": "交易所", "default": "binance"},
        "market": {"type": "string", "title": "市场", "default": "spot"},
        "instrument": {"type": "string", "title": "交易对", "default": "BTCUSDT"},
        "interval": {"type": "string", "title": "周期", "enum": ["1m","5m","15m","1h","4h","1d"], "default": "1h"}
      },
      "required": ["mode", "venue", "market", "instrument", "interval"]
    },
    "monitoring": {
      "type": "object",
      "title": "监控",
      "properties": {
        "checkInterval": {"type": "string", "title": "检查周期", "enum": ["1m","5m","15m","1h","4h","1d"], "default": "1h"},
        "name": {"type": "string", "title": "名称", "maxLength": 80}
      },
      "required": ["checkInterval", "name"]
    },
    "indicator": {
      "type": "object",
      "title": "指标",
      "properties": {
        "type": {"type": "string", "title": "类型", "enum": ["rsi", "macd", "volume_spike", "price_change", "bollinger"], "default": "rsi"},
        "parameters": {"type": "object", "title": "参数"}
      },
      "required": ["type", "parameters"]
    }
  },
  "required": ["dataSource", "monitoring", "indicator"],
  "additionalProperties": false
}`)

func (a quantUnifiedIndicatorAction) Execute(ctx context.Context, request sdk.ActionRequest) (sdk.ActionResult, error) {
	// 解析配置
	var config UnifiedIndicatorConfig
	if err := json.Unmarshal(request.Config, &config); err != nil {
		return sdk.ActionResult{}, fmt.Errorf("invalid config: %w", err)
	}

	// 解析输入
	var input struct {
		EventTime   string `json:"eventTime"`
		PathEntered bool   `json:"pathEntered"`
	}
	if err := json.Unmarshal(request.Input, &input); err != nil {
		return sdk.ActionResult{}, fmt.Errorf("invalid input: %w", err)
	}

	evaluatedAt, err := parseQuantUTCTime(input.EventTime)
	if err != nil {
		return sdk.ActionResult{}, err
	}

	previousAt := evaluatedAt.Add(-quantIntervals[config.Monitoring.CheckInterval])

	// 确定数据查询参数
	seriesConfig := quantSeriesConfig{
		Venue:      config.DataSource.Venue,
		Market:     config.DataSource.Market,
		Instrument: config.DataSource.Instrument,
		Interval:   config.DataSource.Interval,
	}

	// 计算需要的 K 线数量
	lookback := calculateLookback(config.Indicator.Type, config.Indicator.Parameters)

	// 加载 K 线数据
	candles, err := a.runtime.loadQuantCandlesThroughClose(ctx, seriesConfig, evaluatedAt, lookback+10)
	if err != nil {
		return sdk.ActionResult{}, fmt.Errorf("failed to load candles: %w", err)
	}

	if len(candles) < lookback {
		return sdk.ActionResult{}, errors.New("insufficient candle data")
	}

	// 计算指标
	currentPoint, err := calculateIndicatorPoint(config.Indicator.Type, config.Indicator.Parameters, candles, evaluatedAt)
	if err != nil {
		return sdk.ActionResult{}, fmt.Errorf("failed to calculate indicator: %w", err)
	}

	previousCandles, _ := a.runtime.loadQuantCandlesThroughClose(ctx, seriesConfig, previousAt, lookback+10)
	previousPoint, _ := calculateIndicatorPoint(config.Indicator.Type, config.Indicator.Parameters, previousCandles, previousAt)

	// 构建输出
	output := map[string]interface{}{
		"ready":                   currentPoint.Ready,
		"matched":                 currentPoint.Matched,
		"previousMatched":         previousPoint.Matched,
		"branch":                  fmt.Sprintf("%t", currentPoint.Matched),
		"entered":                 input.PathEntered && currentPoint.Matched,
		"triggered":               !input.PathEntered && currentPoint.Matched,
		"evaluatedAt":             evaluatedAt.Format(time.RFC3339),
		"previousEvaluatedAt":     previousAt.Format(time.RFC3339),
		"businessKey":             fmt.Sprintf("%s:%s:%s:%s", seriesConfig.Venue, seriesConfig.Market, seriesConfig.Instrument, config.Indicator.Type),
		"summary":                 currentPoint.Summary,
		"formula":                 config.Monitoring.Name,
		"venue":                   seriesConfig.Venue,
		"market":                  seriesConfig.Market,
		"instrument":              seriesConfig.Instrument,
		"indicator":               config.Indicator.Type,
		"interval":                seriesConfig.Interval,
		"candleCloseTime":         currentPoint.CandleCloseTime,
		"previousCandleCloseTime": previousPoint.CandleCloseTime,
		"value":                   currentPoint.Values,
		"previousValue":           previousPoint.Values,
	}

	outputJSON, err := json.Marshal(output)
	if err != nil {
		return sdk.ActionResult{}, err
	}

	return sdk.ActionResult{
		Output: outputJSON,
	}, nil
}

func calculateLookback(indicatorType string, params map[string]interface{}) int {
	switch indicatorType {
	case "rsi":
		if p, ok := params["period"].(float64); ok {
			return int(p) + 10
		}
		return 24
	case "macd":
		slow := 26
		if p, ok := params["slowPeriod"].(float64); ok {
			slow = int(p)
		}
		signal := 9
		if p, ok := params["signalPeriod"].(float64); ok {
			signal = int(p)
		}
		return slow + signal + 10
	case "volume_spike":
		if p, ok := params["lookback"].(float64); ok {
			return int(p) + 5
		}
		return 25
	case "price_change":
		if p, ok := params["lookback"].(float64); ok {
			return int(p) + 2
		}
		return 3
	case "bollinger":
		if p, ok := params["period"].(float64); ok {
			return int(p) + 5
		}
		return 25
	default:
		return 50
	}
}

func calculateIndicatorPoint(indicatorType string, params map[string]interface{}, candles []quantCandle, evaluatedAt time.Time) (quantIndicatorPoint, error) {
	switch indicatorType {
	case "rsi":
		return calculateRSI(params, candles, evaluatedAt)
	case "macd":
		return calculateMACD(params, candles, evaluatedAt)
	case "volume_spike":
		return calculateVolumeSpike(params, candles, evaluatedAt)
	case "price_change":
		return calculatePriceChange(params, candles, evaluatedAt)
	case "bollinger":
		return calculateBollinger(params, candles, evaluatedAt)
	default:
		return quantIndicatorPoint{}, fmt.Errorf("unknown indicator type: %s", indicatorType)
	}
}
