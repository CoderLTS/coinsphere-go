package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"coinsphere/backend/internal/db"
	"github.com/google/uuid"
)

func TestBacktestValidationKeepsDecimalStringsUTCAndCanonicalJSON(t *testing.T) {
	schema, err := validateParameterSchema(map[string]json.RawMessage{
		"threshold": json.RawMessage(`{"type":"decimal","minimum":"-1","maximum":"1"}`),
	})
	if err != nil {
		t.Fatalf("validate parameter schema: %v", err)
	}
	version := db.StrategyVersion{
		ID:     uuid.MustParse("019c2f6d-7c00-7000-8000-000000000020"),
		Market: "spot", ParameterSchemaJSON: string(schema),
	}
	payload := BacktestCreatePayload{
		StrategyVersionID: version.ID.String(),
		Parameters:        map[string]json.RawMessage{"threshold": json.RawMessage(`"0.2500"`)},
		StartTime:         "2026-08-07T00:00:00Z", EndTime: "2026-08-07T00:02:00Z",
		AllocationUSDT: "100.00", InitialEquity: "1000.00", FeeRate: "0.0010", SlippageRate: "0.0020",
	}
	validated, err := validateBacktestPayload(payload, version)
	if err != nil {
		t.Fatalf("validate backtest: %v", err)
	}
	if validated.ParametersJSON != `{"threshold":"0.2500"}` || validated.FundingRatesJSON != `[]` {
		t.Fatalf("canonical JSON = parameters:%s funding:%s", validated.ParametersJSON, validated.FundingRatesJSON)
	}
	if validated.StartTime.Location() != time.UTC || validated.AllocationUSDT.String() != "100" || validated.FeeRate.String() != "0.001" {
		t.Fatalf("validated backtest = %#v", validated)
	}

	payload.Parameters["threshold"] = json.RawMessage(`0.25`)
	if _, err := validateBacktestPayload(payload, version); !errors.Is(err, ErrInvalidStrategyRequest) {
		t.Fatalf("numeric Decimal parameter error = %v", err)
	}
	payload.Parameters["threshold"] = json.RawMessage(`"0.25"`)
	payload.AllocationUSDT = "100000000000000000000"
	if _, err := validateBacktestPayload(payload, version); !errors.Is(err, ErrInvalidStrategyRequest) {
		t.Fatalf("numeric(38,18) overflow error = %v", err)
	}
	payload.AllocationUSDT = "100"
	payload.StartTime = "2026-08-07T00:00:00+00:00"
	if _, err := validateBacktestPayload(payload, version); !errors.Is(err, ErrInvalidStrategyRequest) {
		t.Fatalf("non-Z UTC error = %v", err)
	}
}

func TestBacktestFundingBoundaryMatchesMarket(t *testing.T) {
	version := db.StrategyVersion{
		ID:     uuid.MustParse("019c2f6d-7c00-7000-8000-000000000021"),
		Market: "usd_m", ParameterSchemaJSON: `{}`,
	}
	payload := BacktestCreatePayload{
		StrategyVersionID: version.ID.String(), Parameters: map[string]json.RawMessage{},
		StartTime: "2026-08-07T00:00:00Z", EndTime: "2026-08-07T00:01:00Z",
		AllocationUSDT: "100", InitialEquity: "1000", FeeRate: "0", SlippageRate: "0",
	}
	if _, err := validateBacktestPayload(payload, version); !errors.Is(err, ErrInvalidStrategyRequest) {
		t.Fatalf("missing USD-M funding error = %v", err)
	}
	payload.FundingRates = []string{"-0.0001"}
	if _, err := validateBacktestPayload(payload, version); err != nil {
		t.Fatalf("valid USD-M funding: %v", err)
	}
	version.Market = "spot"
	if _, err := validateBacktestPayload(payload, version); !errors.Is(err, ErrInvalidStrategyRequest) {
		t.Fatalf("Spot funding error = %v", err)
	}
}

func TestIntegerParametersRequireJSONIntegers(t *testing.T) {
	schema, err := validateParameterSchema(map[string]json.RawMessage{
		"count": json.RawMessage(`{"type":"integer","minimum":"1","maximum":"10"}`),
	})
	if err != nil {
		t.Fatalf("validate integer schema: %v", err)
	}
	if _, err := validateParameters(
		map[string]json.RawMessage{"count": json.RawMessage(`"2"`)}, string(schema),
	); !errors.Is(err, ErrInvalidStrategyRequest) {
		t.Fatalf("integer string error = %v", err)
	}
	if _, err := validateParameters(
		map[string]json.RawMessage{"count": json.RawMessage(`2`)}, string(schema),
	); err != nil {
		t.Fatalf("JSON integer rejected: %v", err)
	}
}

func TestStrategyBoundaryRejectsBeforeDatabase(t *testing.T) {
	app := &App{}
	for _, payload := range []StrategyDraftPayload{
		{},
		{Name: " bad ", SourceCode: "def on_bar(): pass", Market: "spot", InstrumentID: "bad", Interval: "1m", LookbackBars: 1, ParameterSchema: map[string]json.RawMessage{}},
		{Name: "ok", SourceCode: "def on_bar(): pass", Market: "other", InstrumentID: "019c2f6d-7c00-7000-8000-000000000001", Interval: "1m", LookbackBars: 1, ParameterSchema: map[string]json.RawMessage{}},
		{Name: "ok", SourceCode: "def on_bar(): pass", Market: "spot", InstrumentID: "019c2f6d-7c00-7000-8000-000000000001", Interval: "2m", LookbackBars: 1, ParameterSchema: map[string]json.RawMessage{}},
	} {
		if _, err := app.validateStrategyDraft(t.Context(), payload); !errors.Is(err, ErrInvalidStrategyRequest) {
			t.Fatalf("payload %#v error = %v", payload, err)
		}
	}
}
