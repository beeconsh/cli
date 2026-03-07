package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/terracotta-ai/beecon/internal/state"
)

func TestRejectApproval(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	content := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  boundary {
    approve = [new_store]
  }
}
store postgres {
  engine = postgres
  username = admin
  password = secret123
}
`
	if err := os.WriteFile(beacon, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	e := New(dir)
	res, err := e.Apply(beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if res.ApprovalRequestID == "" {
		t.Fatalf("expected approval request")
	}
	if err := e.Reject(res.ApprovalRequestID, "tester", "no"); err != nil {
		t.Fatalf("reject failed: %v", err)
	}
	st, err := e.Status()
	if err != nil {
		t.Fatal(err)
	}
	req := st.Approvals[res.ApprovalRequestID]
	if req == nil || req.Status != state.ApprovalRejected {
		t.Fatalf("expected rejected approval")
	}
	run := st.Runs[res.RunID]
	if run == nil || run.Status != state.RunFailed {
		t.Fatalf("expected run failed after rejection")
	}
}

func TestApprovalExpiry(t *testing.T) {
	dir := t.TempDir()
	store := state.NewStore(dir)
	st := &state.State{
		Version:     1,
		Resources:   map[string]*state.ResourceRecord{},
		Audit:       []state.AuditEvent{},
		Approvals:   map[string]*state.ApprovalRequest{},
		Runs:        map[string]*state.RunRecord{},
		Actions:     map[string]*state.PlanAction{},
		Connections: map[string]state.ProviderConnection{},
	}
	runID := "run-1"
	st.Runs[runID] = &state.RunRecord{ID: runID, CreatedAt: time.Now().UTC(), Status: state.RunPendingApproval}
	st.Approvals["apr-1"] = &state.ApprovalRequest{
		ID:        "apr-1",
		RunID:     runID,
		Status:    state.ApprovalPending,
		ExpiresAt: time.Now().UTC().Add(-1 * time.Minute),
	}
	if err := store.Save(st); err != nil {
		t.Fatal(err)
	}
	e := &Engine{store: store, root: dir}
	if err := e.expireApprovals(); err != nil {
		t.Fatalf("expire failed: %v", err)
	}
	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if after.Approvals["apr-1"].Status != state.ApprovalRejected {
		t.Fatalf("expected approval rejected by expiry")
	}
	if after.Runs[runID].Status != state.RunFailed {
		t.Fatalf("expected run failed by expiry")
	}
}
