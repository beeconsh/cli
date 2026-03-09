package engine

import (
	"context"
	"testing"

	"github.com/terracotta-ai/beecon/internal/state"
)

func TestWithAgentIDOption(t *testing.T) {
	o := applyDefaults([]ApplyOption{
		WithAgentID("claude-opus-4-6", "claude-opus-4-6"),
		WithForce(true),
	})
	if o.AgentID != "claude-opus-4-6" {
		t.Errorf("AgentID = %q, want %q", o.AgentID, "claude-opus-4-6")
	}
	if o.AgentModel != "claude-opus-4-6" {
		t.Errorf("AgentModel = %q, want %q", o.AgentModel, "claude-opus-4-6")
	}
	if !o.Force {
		t.Error("Force should be true")
	}
}

func TestAgentHistoryEmpty(t *testing.T) {
	dir := t.TempDir()
	e := New(dir)
	runs, err := e.AgentHistory(context.Background(), "nonexistent-agent")
	if err != nil {
		t.Fatalf("AgentHistory error: %v", err)
	}
	if len(runs) != 0 {
		t.Errorf("expected 0 runs, got %d", len(runs))
	}
}

func TestAddAuditWithAgent(t *testing.T) {
	st := &state.State{}
	addAuditWithAgent(st, "run-1", "TEST_EVENT", "res-1", "test message", nil, "claude-agent")
	if len(st.Audit) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(st.Audit))
	}
	ev := st.Audit[0]
	if ev.AgentID != "claude-agent" {
		t.Errorf("AgentID = %q, want %q", ev.AgentID, "claude-agent")
	}
	if ev.Type != "TEST_EVENT" {
		t.Errorf("Type = %q, want %q", ev.Type, "TEST_EVENT")
	}
}

func TestAddAuditWithoutAgent(t *testing.T) {
	st := &state.State{}
	addAudit(st, "run-1", "TEST_EVENT", "res-1", "test message", nil)
	if len(st.Audit) != 1 {
		t.Fatalf("expected 1 audit event, got %d", len(st.Audit))
	}
	ev := st.Audit[0]
	if ev.AgentID != "" {
		t.Errorf("AgentID should be empty, got %q", ev.AgentID)
	}
}

func TestAgentHistoryFiltering(t *testing.T) {
	dir := t.TempDir()
	e := New(dir)

	// Manually create state with runs from different agents.
	tx, err := e.store.LoadForUpdate()
	if err != nil {
		t.Fatal(err)
	}
	tx.State.Runs = map[string]*state.RunRecord{
		"run-1": {ID: "run-1", AgentID: "agent-a", Status: state.RunApplied},
		"run-2": {ID: "run-2", AgentID: "agent-b", Status: state.RunApplied},
		"run-3": {ID: "run-3", AgentID: "agent-a", Status: state.RunApplied},
		"run-4": {ID: "run-4", AgentID: "", Status: state.RunApplied},
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	runs, err := e.AgentHistory(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("AgentHistory error: %v", err)
	}
	if len(runs) != 2 {
		t.Errorf("expected 2 runs for agent-a, got %d", len(runs))
	}
	for _, r := range runs {
		if r.AgentID != "agent-a" {
			t.Errorf("run %s has AgentID %q, want agent-a", r.ID, r.AgentID)
		}
	}

	// No runs for unknown agent
	runs2, err := e.AgentHistory(context.Background(), "agent-c")
	if err != nil {
		t.Fatal(err)
	}
	if len(runs2) != 0 {
		t.Errorf("expected 0 runs for agent-c, got %d", len(runs2))
	}
}
