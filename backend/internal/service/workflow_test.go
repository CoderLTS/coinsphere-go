package service

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestBlankWorkflowGraphAndLifecycle(t *testing.T) {
	graph, err := validateWorkflowGraph(json.RawMessage(blankWorkflowGraph))
	if err != nil || graph.mainTriggerID != "manual-trigger" || !json.Valid([]byte(graph.nodeVersionsJSON)) {
		t.Fatalf("blank graph = %#v, err = %v", graph, err)
	}
	for name, raw := range map[string]string{
		"malformed":         `{`,
		"unknown node":      strings.Replace(blankWorkflowGraph, "core.end", "official.unknown", 1),
		"duplicate trigger": strings.Replace(blankWorkflowGraph, "core.end", "core.manual", 1),
		"missing edge":      strings.Replace(blankWorkflowGraph, `"edges": [`, `"edges": [null,`, 1),
		"unknown field":     strings.Replace(blankWorkflowGraph, `"schemaVersion": 1,`, `"schemaVersion": 1,"unknown":true,`, 1),
		"core config":       strings.Replace(blankWorkflowGraph, `"config":{}`, `"config":{"unknown":true}`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateWorkflowGraph(json.RawMessage(raw)); err == nil {
				t.Fatal("invalid graph was accepted")
			}
		})
	}

	status := WorkflowStatusPaused
	for _, action := range []string{"start", "pause", "archive"} {
		status, err = nextWorkflowStatus(status, action)
		if err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	if status != WorkflowStatusArchived {
		t.Fatalf("final status = %q", status)
	}
	if _, err := nextWorkflowStatus(status, "start"); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived start error = %v", err)
	}
}
