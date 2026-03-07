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

func TestHashMapFloat64IntEquivalence(t *testing.T) {
	a := map[string]interface{}{"count": 42}
	b := map[string]interface{}{"count": float64(42)}
	if HashMap(a) != HashMap(b) {
		t.Fatalf("hash should be equal for int and float64 of same whole number")
	}
}

func TestLoadForUpdateCommit(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	tx, err := s.LoadForUpdate()
	if err != nil {
		t.Fatalf("LoadForUpdate failed: %v", err)
	}
	tx.State.Resources["test.svc"] = &ResourceRecord{ResourceID: "test.svc", Managed: true, Status: StatusMatched}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load after commit failed: %v", err)
	}
	if loaded.Resources["test.svc"] == nil {
		t.Fatal("committed resource should be persisted")
	}
}

func TestLoadForUpdateRollback(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)

	tx, err := s.LoadForUpdate()
	if err != nil {
		t.Fatalf("LoadForUpdate failed: %v", err)
	}
	tx.State.Resources["test.svc"] = &ResourceRecord{ResourceID: "test.svc", Managed: true}
	tx.Rollback()

	loaded, err := s.Load()
	if err != nil {
		t.Fatalf("Load after rollback failed: %v", err)
	}
	if loaded.Resources["test.svc"] != nil {
		t.Fatal("rolled back resource should not be persisted")
	}
}

func TestDoubleCommitIsNoop(t *testing.T) {
	s := NewStore(t.TempDir())
	tx, err := s.LoadForUpdate()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	// Second commit should be a no-op, not panic
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestDoubleRollbackIsNoop(t *testing.T) {
	s := NewStore(t.TempDir())
	tx, err := s.LoadForUpdate()
	if err != nil {
		t.Fatal(err)
	}
	tx.Rollback()
	// Second rollback should be a no-op, not panic
	tx.Rollback()
}
