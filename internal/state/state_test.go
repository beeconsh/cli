package state

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	st := &State{
		Version:   1,
		Resources: map[string]*ResourceRecord{"service.api": {ResourceID: "service.api", Managed: true, Status: StatusMatched}},
		Audit: []AuditEvent{{
			ID:        "aud-1",
			Timestamp: time.Now().UTC(),
			Type:      "TEST",
			Message:   "ok",
		}},
		Approvals:   map[string]*ApprovalRequest{},
		Runs:        map[string]*RunRecord{},
		Actions:     map[string]*PlanAction{},
		Connections: map[string]ProviderConnection{},
	}

	if err := s.Save(st); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if loaded.Resources["service.api"] == nil {
		t.Fatalf("missing resource after load")
	}
	if loaded.Resources["service.api"].Status != StatusMatched {
		t.Fatalf("unexpected status: %s", loaded.Resources["service.api"].Status)
	}
	if loaded.LastModified.IsZero() {
		t.Fatalf("expected LastModified to be set")
	}

	wantPath := filepath.Join(dir, ".beecon", "state.json")
	if s.path != wantPath {
		t.Fatalf("unexpected state path: %s", s.path)
	}
}

func TestLoadMissingReturnsFreshState(t *testing.T) {
	s := NewStore(t.TempDir())
	st, err := s.Load()
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if st.Version != 1 {
		t.Fatalf("unexpected version: %d", st.Version)
	}
	if st.Resources == nil || st.Actions == nil || st.Approvals == nil {
		t.Fatalf("expected initialized maps")
	}
}

func TestHashMapDeterministic(t *testing.T) {
	a := map[string]interface{}{"x": 1, "y": "z"}
	b := map[string]interface{}{"y": "z", "x": 1}
	if HashMap(a) != HashMap(b) {
		t.Fatalf("hash should be deterministic regardless of map order")
	}
}
