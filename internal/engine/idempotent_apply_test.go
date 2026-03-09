package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/terracotta-ai/beecon/internal/provider"
	"github.com/terracotta-ai/beecon/internal/state"
)

// failingExecutor fails on Apply for specific node names (used for partial failure tests).
type failingExecutor struct {
	failOn map[string]bool // NodeName -> should fail
	calls  []string        // track Apply calls made
}

func (f *failingExecutor) Apply(_ context.Context, req provider.ApplyRequest) (*provider.ApplyResult, error) {
	f.calls = append(f.calls, req.Action.NodeName+":"+req.Action.Operation)
	if f.failOn[req.Action.NodeName] {
		return nil, fmt.Errorf("simulated failure for %s", req.Action.NodeName)
	}
	return &provider.ApplyResult{
		ProviderID: "provider-" + req.Action.NodeName,
		LiveState:  map[string]interface{}{"status": "active"},
	}, nil
}

func (f *failingExecutor) Observe(_ context.Context, _, _ string, rec *state.ResourceRecord) (*provider.ObserveResult, error) {
	return &provider.ObserveResult{Exists: true, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
}

func (f *failingExecutor) IsDryRun() bool { return true }

// trackingExecutor tracks Apply calls and always succeeds.
type trackingExecutor struct {
	calls []string
}

func (t *trackingExecutor) Apply(_ context.Context, req provider.ApplyRequest) (*provider.ApplyResult, error) {
	t.calls = append(t.calls, req.Action.NodeName+":"+req.Action.Operation)
	return &provider.ApplyResult{
		ProviderID: "provider-" + req.Action.NodeName,
		LiveState:  map[string]interface{}{"status": "active"},
	}, nil
}

func (t *trackingExecutor) Observe(_ context.Context, _, _ string, rec *state.ResourceRecord) (*provider.ObserveResult, error) {
	return &provider.ObserveResult{Exists: true, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
}

func (t *trackingExecutor) IsDryRun() bool { return true }

const idempotentBeacon = `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = postgres
}
`

const multiResourceBeacon = `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = postgres
}

store redis {
  engine = redis
}

service api {
  runtime = container(from: ./Dockerfile)
  needs {
    postgres = read_write
    redis = read
  }
}
`

func TestIdempotentCreateSkipsExistingResource(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(idempotentBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	tracker := &trackingExecutor{}
	e := &Engine{
		store: state.NewStore(dir),
		root:  dir,
		exec:  tracker,
	}

	// First apply — should execute the CREATE.
	res1, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if res1.Executed != 1 {
		t.Fatalf("expected 1 executed on first apply, got %d", res1.Executed)
	}
	firstCallCount := len(tracker.calls)
	if firstCallCount == 0 {
		t.Fatal("expected at least one Apply call on first run")
	}

	// Second apply — CREATE should be skipped because resource already has ProviderID.
	tracker.calls = nil
	res2, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("second apply failed: %v", err)
	}

	// The resolver should produce 0 actions since the intent hash matches.
	// But if it does produce a CREATE (e.g., different code path), the
	// idempotency check should skip it.
	if res2.Executed > 0 {
		// If the resolver generated no actions, executed==0 and skipped==0 is fine.
		// If the resolver DID generate a CREATE, it should have been skipped.
		t.Logf("second apply: executed=%d, skipped=%d", res2.Executed, res2.Skipped)
	}
	// No Apply calls should have been made to the executor on re-apply.
	if len(tracker.calls) > 0 {
		t.Fatalf("expected no executor Apply calls on re-apply, got %v", tracker.calls)
	}
}

func TestIdempotentCreateRetriesFailedResource(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(idempotentBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// First apply with a failing executor — CREATE should fail.
	failing := &failingExecutor{failOn: map[string]bool{"postgres": true}}
	e := &Engine{
		store: state.NewStore(dir),
		root:  dir,
		exec:  failing,
	}

	res1, err := e.Apply(ctx, beacon)
	if err == nil {
		t.Fatal("expected error from failing executor")
	}
	if res1 == nil {
		t.Fatal("expected partial result on failure")
	}

	// Verify the resource is marked as FAILED in state.
	st, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := st.Resources["store.postgres"]
	if rec == nil {
		t.Fatal("expected resource record for store.postgres after failed CREATE")
	}
	if rec.Status != state.StatusFailed {
		t.Fatalf("expected FAILED status, got %s", rec.Status)
	}

	// Second apply with a working executor — should retry the CREATE.
	tracker := &trackingExecutor{}
	e.exec = tracker

	res2, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("retry apply failed: %v", err)
	}
	if res2.Executed != 1 {
		t.Fatalf("expected 1 executed on retry, got %d", res2.Executed)
	}
	if len(tracker.calls) == 0 {
		t.Fatal("expected Apply call on retry of FAILED resource")
	}
	hasCreate := false
	for _, c := range tracker.calls {
		if c == "postgres:CREATE" {
			hasCreate = true
		}
	}
	if !hasCreate {
		t.Fatalf("expected CREATE call for postgres, got %v", tracker.calls)
	}
}

func TestIdempotentDeleteSkipsRemovedResource(t *testing.T) {
	dir := t.TempDir()
	initial := filepath.Join(dir, "infra1.beecon")
	reduced := filepath.Join(dir, "infra2.beecon")

	initialContent := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = postgres
}

store redis {
  engine = redis
}
`
	reducedContent := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = postgres
}
`
	if err := os.WriteFile(initial, []byte(initialContent), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reduced, []byte(reducedContent), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	tracker := &trackingExecutor{}
	e := &Engine{
		store: state.NewStore(dir),
		root:  dir,
		exec:  tracker,
	}

	// Apply initial (creates both stores).
	if _, err := e.Apply(ctx, initial); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	// Apply reduced (deletes redis).
	tracker.calls = nil
	if _, err := e.Apply(ctx, reduced); err != nil {
		t.Fatalf("delete apply failed: %v", err)
	}

	// Verify redis was deleted.
	st, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	rec := st.Resources["store.redis"]
	if rec == nil {
		t.Fatal("expected resource record for store.redis")
	}
	if rec.Managed {
		t.Fatal("expected redis to be unmanaged after delete")
	}

	// Apply reduced again — the DELETE for redis should be a no-op (resolver
	// won't even generate a DELETE since it only deletes managed resources).
	tracker.calls = nil
	res3, err := e.Apply(ctx, reduced)
	if err != nil {
		t.Fatalf("re-apply after delete failed: %v", err)
	}

	// No DELETE calls should reach the executor.
	for _, c := range tracker.calls {
		if c == "redis:DELETE" {
			t.Fatal("unexpected DELETE call for already-removed redis")
		}
	}
	_ = res3
}

func TestPartialApplyRecovery(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(multiResourceBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()

	// First apply: fail on "api" (service), which comes after postgres and redis
	// in dependency order.
	failing := &failingExecutor{failOn: map[string]bool{"api": true}}
	e := &Engine{
		store: state.NewStore(dir),
		root:  dir,
		exec:  failing,
	}

	res1, applyErr := e.Apply(ctx, beacon)
	if applyErr == nil {
		t.Fatal("expected error from partial apply")
	}
	if res1 == nil {
		t.Fatal("expected partial result")
	}
	// Stores should have been created, service should have failed.
	if res1.Executed < 1 {
		t.Fatalf("expected at least 1 executed action before failure, got %d", res1.Executed)
	}

	// Verify CompletedActions were saved to the run record.
	st, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	run := st.Runs[res1.RunID]
	if run == nil {
		t.Fatal("expected run record")
	}
	if len(run.CompletedActions) == 0 {
		t.Fatal("expected CompletedActions to be non-empty after partial apply")
	}
	if run.Status != state.RunFailed {
		t.Fatalf("expected FAILED run status, got %s", run.Status)
	}

	// Second apply: all succeed. Previously completed actions should be skipped.
	tracker := &trackingExecutor{}
	e.exec = tracker

	res2, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("retry apply failed: %v", err)
	}

	// The resolver will still generate CREATE actions for all 3 resources
	// (since the FAILED resource hasn't been fully provisioned). The stores
	// that already have ProviderID should be skipped by idempotency, and the
	// api (which failed) should be retried.
	//
	// Alternatively, the resolver may see the stores as already matching
	// (intent hash matches) and only generate CREATE for the failed one.
	// In either case, postgres and redis should NOT reach the executor.
	t.Logf("retry result: executed=%d, skipped=%d, actions=%d",
		res2.Executed, res2.Skipped, len(res2.Actions))

	// api should have been executed (it was the one that failed).
	hasAPI := false
	for _, c := range tracker.calls {
		if c == "api:CREATE" {
			hasAPI = true
		}
	}
	if !hasAPI {
		t.Fatalf("expected CREATE call for api on retry, got %v", tracker.calls)
	}

	// postgres and redis should NOT have been re-executed.
	for _, c := range tracker.calls {
		if c == "postgres:CREATE" || c == "redis:CREATE" {
			t.Fatalf("unexpected re-execution of already-provisioned resource: %s", c)
		}
	}
}

func TestFullReApplyIsNoOp(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(multiResourceBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	tracker := &trackingExecutor{}
	e := &Engine{
		store: state.NewStore(dir),
		root:  dir,
		exec:  tracker,
	}

	// First apply — all resources created.
	res1, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if res1.Executed == 0 {
		t.Fatal("expected actions on first apply")
	}

	// Second apply — should be a no-op (resolver produces 0 actions when
	// intent hashes match).
	tracker.calls = nil
	res2, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("re-apply failed: %v", err)
	}

	// No executor calls should have been made.
	if len(tracker.calls) > 0 {
		t.Fatalf("expected no executor calls on re-apply, got %v", tracker.calls)
	}

	// All counts should be zero (no actions generated by resolver).
	if res2.Executed != 0 {
		t.Fatalf("expected 0 executed on re-apply, got %d", res2.Executed)
	}
	if res2.Skipped != 0 {
		t.Fatalf("expected 0 skipped on re-apply (resolver produces no actions), got %d", res2.Skipped)
	}
}

// TestIdempotentCreateSkipsViaCompletedSet verifies that the completedSet from
// a prior failed run causes actions to be skipped even when state records don't
// have a ProviderID (e.g., if the action succeeded but the provider ID wasn't
// stored due to a bug or the action was a side-effect).
func TestPartialRecoveryUsesCompletedSet(t *testing.T) {
	// This is a direct unit test of the completedSet mechanism, verifying
	// that findCompletedSetForBeacon correctly picks up prior run data.
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(idempotentBeacon), 0o644); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs(beacon)

	st := &state.State{
		Version:   1,
		Resources: map[string]*state.ResourceRecord{},
		Runs: map[string]*state.RunRecord{
			"run-1": {
				ID:               "run-1",
				BeaconPath:       abs,
				Status:           state.RunFailed,
				CompletedActions: []string{"postgres:CREATE"},
			},
		},
		Actions:     map[string]*state.PlanAction{},
		Approvals:   map[string]*state.ApprovalRequest{},
		Connections: map[string]state.ProviderConnection{},
	}

	completedSet := findCompletedSetForBeacon(st, abs)
	if !completedSet["postgres:CREATE"] {
		t.Fatal("expected postgres:CREATE in completed set")
	}
	if completedSet["redis:CREATE"] {
		t.Fatal("unexpected redis:CREATE in completed set")
	}
}

func TestIsAlreadyAppliedUnit(t *testing.T) {
	tests := []struct {
		name         string
		action       *state.PlanAction
		resources    map[string]*state.ResourceRecord
		completedSet map[string]bool
		wantSkip     bool
	}{
		{
			name:   "CREATE with no existing record",
			action: &state.PlanAction{NodeID: "store.pg", NodeName: "pg", Operation: "CREATE"},
			resources: map[string]*state.ResourceRecord{},
			completedSet: map[string]bool{},
			wantSkip: false,
		},
		{
			name:   "CREATE with existing ProviderID",
			action: &state.PlanAction{NodeID: "store.pg", NodeName: "pg", Operation: "CREATE"},
			resources: map[string]*state.ResourceRecord{
				"store.pg": {ProviderID: "rds-123", Status: state.StatusMatched},
			},
			completedSet: map[string]bool{},
			wantSkip: true,
		},
		{
			name:   "CREATE with FAILED status retries",
			action: &state.PlanAction{NodeID: "store.pg", NodeName: "pg", Operation: "CREATE"},
			resources: map[string]*state.ResourceRecord{
				"store.pg": {ProviderID: "", Status: state.StatusFailed},
			},
			completedSet: map[string]bool{},
			wantSkip: false,
		},
		{
			name:   "DELETE with no record",
			action: &state.PlanAction{NodeID: "store.pg", NodeName: "pg", Operation: "DELETE"},
			resources: map[string]*state.ResourceRecord{},
			completedSet: map[string]bool{},
			wantSkip: true,
		},
		{
			name:   "DELETE with unmanaged resource",
			action: &state.PlanAction{NodeID: "store.pg", NodeName: "pg", Operation: "DELETE"},
			resources: map[string]*state.ResourceRecord{
				"store.pg": {Managed: false, ProviderID: "", LastOperation: "DELETE"},
			},
			completedSet: map[string]bool{},
			wantSkip: true,
		},
		{
			name:   "DELETE with managed resource proceeds",
			action: &state.PlanAction{NodeID: "store.pg", NodeName: "pg", Operation: "DELETE"},
			resources: map[string]*state.ResourceRecord{
				"store.pg": {Managed: true, ProviderID: "rds-123"},
			},
			completedSet: map[string]bool{},
			wantSkip: false,
		},
		{
			name:   "action in completed set is skipped",
			action: &state.PlanAction{NodeID: "store.pg", NodeName: "pg", Operation: "CREATE"},
			resources: map[string]*state.ResourceRecord{},
			completedSet: map[string]bool{"pg:CREATE": true},
			wantSkip: true,
		},
		{
			name:   "UPDATE is not skipped by default",
			action: &state.PlanAction{NodeID: "store.pg", NodeName: "pg", Operation: "UPDATE"},
			resources: map[string]*state.ResourceRecord{
				"store.pg": {ProviderID: "rds-123", Managed: true, Status: state.StatusMatched},
			},
			completedSet: map[string]bool{},
			wantSkip: false,
		},
		{
			name:   "UPDATE in completed set is skipped",
			action: &state.PlanAction{NodeID: "store.pg", NodeName: "pg", Operation: "UPDATE"},
			resources: map[string]*state.ResourceRecord{
				"store.pg": {ProviderID: "rds-123", Managed: true, Status: state.StatusMatched},
			},
			completedSet: map[string]bool{"pg:UPDATE": true},
			wantSkip: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &state.State{Resources: tc.resources}
			skip, reason := isAlreadyApplied(tc.action, st, tc.completedSet)
			if skip != tc.wantSkip {
				t.Errorf("isAlreadyApplied() skip=%v, want %v (reason: %s)", skip, tc.wantSkip, reason)
			}
			if skip && reason == "" {
				t.Error("expected non-empty reason when skip=true")
			}
		})
	}
}
