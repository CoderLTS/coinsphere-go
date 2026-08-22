package service

import (
	"testing"

	"coinsphere/backend/internal/db"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

func TestWorkflowGraphV2DataEdgeContract(t *testing.T) {
	graph := M{
		"schemaVersion": 2,
		"nodes": []any{
			M{"id": "start", "type": "start.manual", "config": M{"entryKey": "manual.default"}},
			M{"id": "condition", "type": "condition.branch", "config": M{"operator": "truthy"}},
			M{"id": "yes", "type": "end", "config": M{}},
			M{"id": "no", "type": "end", "config": M{}},
		},
		"edges": []any{
			M{"id": "flow-start", "kind": "flow", "source": "start", "target": "condition"},
			M{"id": "flow-yes", "kind": "flow", "source": "condition", "target": "yes", "branch": "true"},
			M{"id": "flow-no", "kind": "flow", "source": "condition", "target": "no", "branch": "false"},
			M{"id": "data-value", "kind": "data", "source": "start", "target": "condition", "sourcePort": "inputs", "targetPort": "value", "sourcePointer": "/enabled"},
		},
	}
	if err := validateWorkflowGraph(graph); err != nil {
		t.Fatalf("valid Graph V2 rejected: %v", err)
	}

	graph["schemaVersion"] = 1
	if err := validateWorkflowGraph(graph); err == nil {
		t.Fatal("Graph V1 was accepted")
	}
}

func TestWorkflowGraphV2RejectsNonAncestorDataSource(t *testing.T) {
	graph := M{
		"schemaVersion": 2,
		"nodes": []any{
			M{"id": "left", "type": "start.manual", "config": M{"entryKey": "manual.left"}},
			M{"id": "right", "type": "start.manual", "config": M{"entryKey": "manual.right"}},
			M{"id": "condition", "type": "condition.branch", "config": M{"operator": "truthy"}},
			M{"id": "yes", "type": "end", "config": M{}},
			M{"id": "no", "type": "end", "config": M{}},
			M{"id": "right-end", "type": "end", "config": M{}},
		},
		"edges": []any{
			M{"id": "flow-left", "kind": "flow", "source": "left", "target": "condition"},
			M{"id": "flow-right", "kind": "flow", "source": "right", "target": "right-end"},
			M{"id": "flow-yes", "kind": "flow", "source": "condition", "target": "yes", "branch": "true"},
			M{"id": "flow-no", "kind": "flow", "source": "condition", "target": "no", "branch": "false"},
			M{"id": "data-cross", "kind": "data", "source": "right", "target": "condition", "sourcePort": "inputs", "targetPort": "value"},
		},
	}
	if err := validateWorkflowGraph(graph); err == nil {
		t.Fatal("non-ancestor data source was accepted")
	}
}

func TestWorkflowPortTypesAndJSONPointers(t *testing.T) {
	decimal := M{"type": "string", "format": "decimal"}
	if workflowSchemasCompatible(M{"type": "number"}, decimal) {
		t.Fatal("number was accepted as a Decimal string")
	}
	if !workflowSchemasCompatible(decimal, decimal) {
		t.Fatal("matching Decimal strings were rejected")
	}
	tokens, err := decodeWorkflowJSONPointer("/a~1b/~0value")
	if err != nil || len(tokens) != 2 || tokens[0] != "a/b" || tokens[1] != "~value" {
		t.Fatalf("RFC 6901 decode = %#v, %v", tokens, err)
	}
	if _, err := decodeWorkflowJSONPointer("a/b"); err == nil {
		t.Fatal("pointer without leading slash was accepted")
	}
}

func TestTradingRiskExpansionClassification(t *testing.T) {
	one := uuid.MustParse("019c2f6d-7c00-7000-8000-000000000001")
	two := uuid.MustParse("019c2f6d-7c00-7000-8000-000000000002")
	limit := decimal.NewFromInt(100)
	quoteAge := 30
	leverage := 3
	current := db.TradingAccount{
		MaxTotalNotional: &limit, MaxSymbolNotional: &limit, MaxOrderNotional: &limit,
		MaxDailyLoss: &limit, MaxDrawdown: &limit, MaxQuoteAgeSeconds: &quoteAge, Leverage: &leverage,
	}
	lower := decimal.NewFromInt(90)
	lowerAge := 20
	lowerLeverage := 2
	restrictive := validatedTradingRisk{
		InstrumentIDs: []uuid.UUID{one}, MaxTotalNotional: &lower, MaxSymbolNotional: &lower,
		MaxOrderNotional: &lower, MaxDailyLoss: &lower, MaxDrawdown: &lower,
		MaxQuoteAgeSeconds: &lowerAge, Leverage: &lowerLeverage,
	}
	if tradingRiskExpands(current, []uuid.UUID{one, two}, restrictive) {
		t.Fatal("restrictive risk update was classified as expanding")
	}
	expanded := restrictive
	expanded.InstrumentIDs = []uuid.UUID{one, uuid.MustParse("019c2f6d-7c00-7000-8000-000000000003")}
	if !tradingRiskExpands(current, []uuid.UUID{one, two}, expanded) {
		t.Fatal("expanded whitelist was not classified as expanding")
	}
	expanded = restrictive
	expanded.MaxOrderNotional = nil
	if !tradingRiskExpands(current, []uuid.UUID{one, two}, expanded) {
		t.Fatal("removed risk limit was not classified as expanding")
	}
}
