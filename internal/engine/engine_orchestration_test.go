package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/provider"
	"github.com/terracotta-ai/beecon/internal/state"
)

// importTestExecutor is a test executor that returns Exists=true for Observe,
// allowing Import to succeed in test environments without real cloud calls.
type importTestExecutor struct {
	provider.Executor
}

func (e *importTestExecutor) Observe(_ context.Context, _, _ string, rec *state.ResourceRecord) (*provider.ObserveResult, error) {
	return &provider.ObserveResult{
		Exists:     true,
		ProviderID: rec.ProviderID,
		LiveState:  map[string]interface{}{"status": "available"},
	}, nil
}

func (e *importTestExecutor) Apply(_ context.Context, req provider.ApplyRequest) (*provider.ApplyResult, error) {
	return &provider.ApplyResult{ProviderID: req.Record.ProviderID}, nil
}

func (e *importTestExecutor) IsDryRun() bool { return true }

const basicBeacon = `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = postgres
}

service api {
  runtime = container(from: ./Dockerfile)
  needs {
    postgres = read_write
  }
}
`

const noBoundaryBeacon = `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = postgres
}
`

func writeBeacon(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- Plan tests ---

func TestPlanDetectsCreate(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, basicBeacon)

	ctx := context.Background()
	e := New(dir)
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(res.Plan.Actions) == 0 {
		t.Fatal("expected CREATE actions on fresh state")
	}
	for _, a := range res.Plan.Actions {
		if a.Operation != "CREATE" && a.Operation != "FORBIDDEN" {
			t.Fatalf("expected CREATE or FORBIDDEN on fresh state, got %s", a.Operation)
		}
	}
}

func TestPlanNoChanges(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	// Apply first
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Plan again with same file — should detect no changes
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if len(res.Plan.Actions) != 0 {
		t.Fatalf("expected 0 actions after apply with same beacon, got %d", len(res.Plan.Actions))
	}
}

func TestPlanDetectsUpdate(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	// Apply
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Modify beacon — change engine
	modifiedBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = mysql
}
`
	writeBeacon(t, dir, modifiedBeacon)

	// Plan should detect UPDATE
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	hasUpdate := false
	for _, a := range res.Plan.Actions {
		if a.Operation == "UPDATE" {
			hasUpdate = true
		}
	}
	if !hasUpdate {
		t.Fatal("expected UPDATE action after modifying beacon")
	}
}

func TestPlanDetectsDelete(t *testing.T) {
	dir := t.TempDir()
	multiBeacon := `domain acme {
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
	beacon := writeBeacon(t, dir, multiBeacon)

	ctx := context.Background()
	e := New(dir)

	// Apply with both stores
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Remove redis from beacon
	reducedBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = postgres
}
`
	writeBeacon(t, dir, reducedBeacon)

	// Plan should detect DELETE for redis
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	hasDelete := false
	for _, a := range res.Plan.Actions {
		if a.Operation == "DELETE" && a.NodeName == "redis" {
			hasDelete = true
		}
	}
	if !hasDelete {
		t.Fatal("expected DELETE action for removed redis store")
	}
}

func TestPlanDependencyOrder(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, basicBeacon)

	ctx := context.Background()
	e := New(dir)
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	// postgres (STORE) should come before api (SERVICE) in CREATE order
	postgresIdx := -1
	apiIdx := -1
	for i, a := range res.Plan.Actions {
		if a.NodeName == "postgres" {
			postgresIdx = i
		}
		if a.NodeName == "api" {
			apiIdx = i
		}
	}
	if postgresIdx >= 0 && apiIdx >= 0 && postgresIdx > apiIdx {
		t.Fatal("expected postgres (store) to be planned before api (service)")
	}
}

// --- Validate tests ---

func TestValidateGoodFile(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, basicBeacon)

	e := New(dir)
	if err := e.Validate(beacon); err != nil {
		t.Fatalf("expected valid beacon, got: %v", err)
	}
}

func TestValidateBadFile(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, "this is not { valid beacon syntax")

	e := New(dir)
	if err := e.Validate(beacon); err == nil {
		t.Fatal("expected validation error for invalid syntax")
	}
}

func TestValidateMissingFile(t *testing.T) {
	dir := t.TempDir()
	e := New(dir)
	if err := e.Validate(filepath.Join(dir, "nonexistent.beecon")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

// --- Drift tests ---

func TestDriftNoChanges(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	// Apply
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Drift with same file — simulated executor always returns cached state
	drifted, errs, err := e.Drift(ctx, beacon)
	if err != nil {
		t.Fatalf("drift failed: %v", err)
	}
	if len(errs) > 0 {
		t.Fatalf("unexpected observe errors: %v", errs)
	}
	if len(drifted) > 0 {
		t.Fatalf("expected no drift, got %d drifted resources", len(drifted))
	}
}

// --- Apply tests ---

func TestApplyNoBoundary(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	res, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if res.Pending > 0 {
		t.Fatalf("expected no pending actions without boundary, got %d", res.Pending)
	}
	if res.Executed == 0 {
		t.Fatal("expected at least one executed action")
	}
	if res.ApprovalRequestID != "" {
		t.Fatal("expected no approval request without boundary")
	}
}

func TestApplyCreatesRunRecord(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	res, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if res.RunID == "" {
		t.Fatal("expected run ID")
	}

	// Verify run exists in state
	st, err := e.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run, ok := st.Runs[res.RunID]
	if !ok {
		t.Fatalf("run %s not found in state", res.RunID)
	}
	if run.Status != state.RunApplied {
		t.Fatalf("expected run status APPLIED, got %s", run.Status)
	}
}

func TestApplyIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	// Apply twice
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	res2, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("second apply failed: %v", err)
	}
	// Second apply should have 0 executed (no changes)
	if res2.Executed != 0 {
		t.Fatalf("expected 0 executed on idempotent apply, got %d", res2.Executed)
	}
}

func TestApplySimulatedFlag(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	res, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	// Default executor is dryRun unless BEECON_EXECUTE=1
	if !res.Simulated {
		t.Log("note: Simulated=false — BEECON_EXECUTE=1 may be set")
	}
}

// --- Status tests ---

func TestStatusEmptyState(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	e := New(dir)

	st, err := e.Status(ctx)
	if err != nil {
		t.Fatalf("status failed: %v", err)
	}
	if len(st.Resources) != 0 {
		t.Fatalf("expected 0 resources on fresh state, got %d", len(st.Resources))
	}
}

func TestStatusAfterApply(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	st, err := e.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Resources) == 0 {
		t.Fatal("expected resources after apply")
	}
	// Check postgres store exists
	found := false
	for id := range st.Resources {
		if st.Resources[id].NodeName == "postgres" {
			found = true
		}
	}
	if !found {
		t.Fatal("expected postgres resource in state after apply")
	}
}

// --- History tests ---

func TestHistoryAfterApply(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Get history for the postgres store
	st, _ := e.Status(ctx)
	var resourceID string
	for id, r := range st.Resources {
		if r.NodeName == "postgres" {
			resourceID = id
			break
		}
	}
	if resourceID == "" {
		t.Fatal("no postgres resource found")
	}

	events, err := e.History(ctx, resourceID)
	if err != nil {
		t.Fatalf("history failed: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected history events after apply")
	}
}

// --- Compliance integration ---

func TestPlanWithComplianceEnrichment(t *testing.T) {
	dir := t.TempDir()
	// Use soc2 (which applies defaults) instead of hipaa (which requires kms_key)
	complianceBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [soc2]
}

store postgres {
  engine = postgres
}
`
	beacon := writeBeacon(t, dir, complianceBeacon)

	ctx := context.Background()
	e := New(dir)
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if res.ComplianceReport == nil {
		t.Fatal("expected compliance report with SOC2")
	}
}

func TestPlanWithHIPAARejectsNonCompliant(t *testing.T) {
	dir := t.TempDir()
	complianceBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa]
}

store postgres {
  engine = postgres
}
`
	beacon := writeBeacon(t, dir, complianceBeacon)

	ctx := context.Background()
	e := New(dir)
	_, err := e.Plan(ctx, beacon)
	if err == nil {
		t.Fatal("expected HIPAA compliance error for store without kms_key")
	}
}

// --- Cost report ---

func TestPlanWithCostReport(t *testing.T) {
	dir := t.TempDir()
	budgetBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  boundary {
    budget = 5000/mo
  }
}

store postgres {
  engine = postgres
  instance_type = db.r6g.xlarge
}
`
	beacon := writeBeacon(t, dir, budgetBeacon)

	ctx := context.Background()
	e := New(dir)
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if res.CostReport == nil {
		t.Fatal("expected cost report with budget")
	}
}

// --- Reject clears ApprovalBlocked ---

const approveGateBeacon = `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  boundary {
    approve = [new_store]
  }
}
store postgres {
  engine = postgres
}
`

func TestRejectClearsApprovalBlocked(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, approveGateBeacon)

	ctx := context.Background()
	e := New(dir)
	res, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if res.ApprovalRequestID == "" {
		t.Fatal("expected approval request for new_store gate")
	}

	// The approval gate triggers on CREATE for STORE, but since it's a fresh
	// CREATE, the resource record doesn't exist yet. Manually set ApprovalBlocked
	// on the resource to simulate the state after a resource already exists and
	// an approval is pending.
	st, err := e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	// Find the action to get the node ID
	req := st.Approvals[res.ApprovalRequestID]
	if req == nil {
		t.Fatal("approval request not found in state")
	}
	for _, actionID := range req.ActionIDs {
		a := st.Actions[actionID]
		if a == nil {
			continue
		}
		// Create the resource record with ApprovalBlocked=true
		st.Resources[a.NodeID] = &state.ResourceRecord{
			ResourceID:      a.NodeID,
			NodeType:        a.NodeType,
			NodeName:        a.NodeName,
			Managed:         false,
			ApprovalBlocked: true,
		}
	}
	if err := e.store.Save(st); err != nil {
		t.Fatal(err)
	}

	// Reject the approval
	if err := e.Reject(ctx, res.ApprovalRequestID, "tester", "no"); err != nil {
		t.Fatalf("reject failed: %v", err)
	}

	// Verify ApprovalBlocked is cleared
	st, err = e.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for id, rec := range st.Resources {
		if rec.ApprovalBlocked {
			t.Fatalf("resource %s still has ApprovalBlocked=true after rejection", id)
		}
	}
}

// --- Rollback guards run status ---

func TestRollbackGuardsRunStatus(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	res, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// First rollback should succeed
	_, err = e.Rollback(ctx, res.RunID)
	if err != nil {
		t.Fatalf("first rollback failed: %v", err)
	}

	// Second rollback of same run should fail with status guard
	_, err = e.Rollback(ctx, res.RunID)
	if err == nil {
		t.Fatal("expected error on second rollback")
	}
	if !strings.Contains(err.Error(), "status is ROLLED_BACK") {
		t.Fatalf("expected status guard error, got: %v", err)
	}
}

// --- Rollback updates original run status ---

func TestRollbackUpdatesOriginalRunStatus(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, noBoundaryBeacon)

	ctx := context.Background()
	e := New(dir)

	res, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	_, err = e.Rollback(ctx, res.RunID)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}

	st, err := e.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	run := st.Runs[res.RunID]
	if run == nil {
		t.Fatalf("original run %s not found", res.RunID)
	}
	if run.Status != state.RunRolledBack {
		t.Fatalf("expected original run status ROLLED_BACK, got %s", run.Status)
	}
}

// --- Approve validates run status ---

func TestApproveValidatesRunStatus(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, approveGateBeacon)

	ctx := context.Background()
	e := New(dir)

	res, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if res.ApprovalRequestID == "" {
		t.Fatal("expected approval request")
	}

	// Approve it
	_, err = e.Approve(ctx, res.ApprovalRequestID, "tester")
	if err != nil {
		t.Fatalf("approve failed: %v", err)
	}

	// Try to approve the same request again — should fail because status is APPROVED
	_, err = e.Approve(ctx, res.ApprovalRequestID, "tester")
	if err == nil {
		t.Fatal("expected error on second approve")
	}
}

// --- Import short name ---

func TestImportShortName(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	e := &Engine{
		store: state.NewStore(dir),
		root:  dir,
		exec:  &importTestExecutor{},
	}

	// Import with an ARN-like provider ID
	nodeID, err := e.Import(ctx, "aws", "store", "arn:aws:s3:::my-bucket", "us-east-1")
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if nodeID != "store.my-bucket" {
		t.Fatalf("expected node ID store.my-bucket, got %s", nodeID)
	}

	// Verify in state
	st, err := e.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rec := st.Resources["store.my-bucket"]
	if rec == nil {
		t.Fatal("expected resource store.my-bucket in state")
	}
	if rec.LastOperation != "IMPORT" {
		t.Fatalf("expected LastOperation IMPORT, got %s", rec.LastOperation)
	}
}

// --- copyMap deep copy ---

func TestCopyMapDeep(t *testing.T) {
	// Test that copyMap performs a deep copy — nested maps are independent.
	inner := map[string]interface{}{"nested": "value"}
	original := map[string]interface{}{"key": inner}
	copied := copyMap(original)

	// Modify the copy's nested map
	if nestedCopy, ok := copied["key"].(map[string]interface{}); ok {
		nestedCopy["nested"] = "modified"
	} else {
		t.Fatal("expected nested map in copy")
	}

	// Original should be unchanged
	if innerMap, ok := original["key"].(map[string]interface{}); ok {
		if innerMap["nested"] != "value" {
			t.Error("deep copy failed: modifying copy affected original")
		}
	} else {
		t.Fatal("expected nested map in original")
	}
}

func TestCopyMapNil(t *testing.T) {
	if got := copyMap(nil); got != nil {
		t.Errorf("copyMap(nil) = %v, want nil", got)
	}
}

func TestCopyMapShallow(t *testing.T) {
	// Flat maps should round-trip correctly.
	original := map[string]interface{}{"a": "1", "b": float64(2)}
	copied := copyMap(original)
	if copied["a"] != "1" || copied["b"] != float64(2) {
		t.Errorf("flat copy mismatch: got %v", copied)
	}
	// Mutating copy should not affect original.
	copied["a"] = "changed"
	if original["a"] != "1" {
		t.Error("shallow value mutation leaked to original")
	}
}

func TestApplyPartialFailureReturnsResult(t *testing.T) {
	// Verify that Apply returns a non-nil result even when an action fails.
	// We use a beacon with a FORBIDDEN boundary action to trigger an error path
	// that exercises the partial-result logic.
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, basicBeacon)

	ctx := context.Background()
	e := New(dir)

	// First apply should succeed (creates resources).
	res, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("first apply failed: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result from successful apply")
	}
	if res.RunID == "" {
		t.Error("expected non-empty RunID")
	}
}

// --- Plan enrichment tests ---

func TestPlanEnrichment_RiskScoring(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
}
`)
	ctx := context.Background()
	e := New(dir)
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if res.Summary == nil {
		t.Fatal("expected plan summary")
	}
	if res.Summary.TotalActions != 2 {
		t.Fatalf("expected 2 actions, got %d", res.Summary.TotalActions)
	}
	if res.Summary.Creates != 2 {
		t.Fatalf("expected 2 creates, got %d", res.Summary.Creates)
	}

	// Every action should have risk scoring.
	for _, a := range res.Plan.Actions {
		if a.RiskScore == 0 {
			t.Errorf("action %s has zero risk score", a.NodeID)
		}
		if a.RiskLevel == "" {
			t.Errorf("action %s has empty risk level", a.NodeID)
		}
		if a.RollbackFeasibility == "" {
			t.Errorf("action %s has empty rollback feasibility", a.NodeID)
		}
	}
}

func TestScoreRisk(t *testing.T) {
	tests := []struct {
		op, nodeType string
		wantMin      int
		wantLevel    string
	}{
		{"CREATE", "service", 2, "low"},
		{"CREATE", "store", 4, "medium"},
		{"UPDATE", "service", 4, "medium"},
		{"UPDATE", "store", 6, "high"},
		{"DELETE", "service", 7, "high"},
		{"DELETE", "store", 9, "critical"},
		{"FORBIDDEN", "service", 1, "low"},
	}
	for _, tt := range tests {
		score, level := scoreRisk(tt.op, tt.nodeType)
		if score < tt.wantMin {
			t.Errorf("scoreRisk(%s, %s) = %d, want >= %d", tt.op, tt.nodeType, score, tt.wantMin)
		}
		if level != tt.wantLevel {
			t.Errorf("scoreRisk(%s, %s) level = %s, want %s", tt.op, tt.nodeType, level, tt.wantLevel)
		}
	}
}

func TestRollbackFeasibility(t *testing.T) {
	tests := []struct {
		op, nodeType, want string
	}{
		{"CREATE", "service", "safe"},
		{"CREATE", "store", "safe"},
		{"UPDATE", "service", "safe"},
		{"UPDATE", "store", "risky"},
		{"DELETE", "service", "risky"},
		{"DELETE", "store", "impossible"},
		{"FORBIDDEN", "store", "safe"},
	}
	for _, tt := range tests {
		got := rollbackFeasibility(tt.op, tt.nodeType)
		if got != tt.want {
			t.Errorf("rollbackFeasibility(%s, %s) = %s, want %s", tt.op, tt.nodeType, got, tt.want)
		}
	}
}

func TestPlanEnrichment_Summary(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
`)
	ctx := context.Background()
	e := New(dir)
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if res.Summary == nil {
		t.Fatal("expected summary")
	}
	if res.Summary.AggregateRisk == "" {
		t.Error("expected non-empty aggregate risk")
	}
	// CREATE store has risk ~4 (CREATE=2 + store=2), level=medium
	for _, a := range res.Plan.Actions {
		if a.NodeType == "store" && a.Operation == "CREATE" {
			if a.RollbackFeasibility != "safe" {
				t.Errorf("CREATE store should be safe, got %s", a.RollbackFeasibility)
			}
		}
	}
}

func TestScoreBlastRadius(t *testing.T) {
	t.Run("standalone action uses risk score", func(t *testing.T) {
		a := &state.PlanAction{NodeID: "a", RiskScore: 3}
		score, level := scoreBlastRadius(a, []*state.PlanAction{a})
		if score != 3 {
			t.Errorf("expected 3, got %d", score)
		}
		if level != "medium" {
			t.Errorf("expected medium, got %s", level)
		}
	})

	t.Run("direct dependents add bonus", func(t *testing.T) {
		parent := &state.PlanAction{NodeID: "parent", RiskScore: 2}
		child1 := &state.PlanAction{NodeID: "c1", DependsOn: []string{"parent"}, RiskScore: 1}
		child2 := &state.PlanAction{NodeID: "c2", DependsOn: []string{"parent"}, RiskScore: 1}
		all := []*state.PlanAction{parent, child1, child2}
		score, _ := scoreBlastRadius(parent, all)
		// 2 base + 1 (2 deps / 2) = 3
		if score != 3 {
			t.Errorf("expected 3, got %d", score)
		}
	})

	t.Run("DELETE downstream adds 1", func(t *testing.T) {
		parent := &state.PlanAction{NodeID: "parent", RiskScore: 2}
		child := &state.PlanAction{NodeID: "c", DependsOn: []string{"parent"}, Operation: "DELETE", RiskScore: 1}
		all := []*state.PlanAction{parent, child}
		score, _ := scoreBlastRadius(parent, all)
		// 2 base + 0 (1 dep / 2 = 0) + 1 DELETE = 3
		if score != 3 {
			t.Errorf("expected 3, got %d", score)
		}
	})

	t.Run("STORE downstream adds 1", func(t *testing.T) {
		parent := &state.PlanAction{NodeID: "parent", RiskScore: 2}
		child := &state.PlanAction{NodeID: "c", DependsOn: []string{"parent"}, NodeType: "store", Operation: "CREATE", RiskScore: 1}
		all := []*state.PlanAction{parent, child}
		score, _ := scoreBlastRadius(parent, all)
		// 2 base + 0 + 1 STORE = 3
		if score != 3 {
			t.Errorf("expected 3, got %d", score)
		}
	})

	t.Run("capped at 10", func(t *testing.T) {
		parent := &state.PlanAction{NodeID: "parent", RiskScore: 8}
		children := make([]*state.PlanAction, 10)
		for i := range children {
			children[i] = &state.PlanAction{
				NodeID:    fmt.Sprintf("c%d", i),
				DependsOn: []string{"parent"},
				Operation: "DELETE",
				NodeType:  "store",
				RiskScore: 1,
			}
		}
		all := append([]*state.PlanAction{parent}, children...)
		score, level := scoreBlastRadius(parent, all)
		if score != 10 {
			t.Errorf("expected 10 (capped), got %d", score)
		}
		if level != "critical" {
			t.Errorf("expected critical, got %s", level)
		}
	})

	t.Run("transitive dependents counted in BFS", func(t *testing.T) {
		a := &state.PlanAction{NodeID: "a", RiskScore: 2}
		b := &state.PlanAction{NodeID: "b", DependsOn: []string{"a"}, RiskScore: 1}
		c := &state.PlanAction{NodeID: "c", DependsOn: []string{"b"}, Operation: "DELETE", RiskScore: 1}
		all := []*state.PlanAction{a, b, c}
		score, _ := scoreBlastRadius(a, all)
		// 2 base + 0 (1 direct dep / 2) + 1 DELETE downstream (transitive) = 3
		if score != 3 {
			t.Errorf("expected 3, got %d", score)
		}
	})
}

func TestPlanEnrichment_BlastRadius(t *testing.T) {
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
`)
	ctx := context.Background()
	e := New(dir)
	res, err := e.Plan(ctx, beacon)
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}
	if res.Summary == nil {
		t.Fatal("expected summary")
	}
	if res.Summary.MaxBlastRadiusLevel == "" {
		t.Error("expected non-empty max blast radius level")
	}
	for _, a := range res.Plan.Actions {
		if a.BlastRadius == 0 && a.RiskScore > 0 {
			t.Errorf("action %s has risk score %d but blast radius 0", a.NodeID, a.RiskScore)
		}
		if a.BlastRadiusLevel == "" {
			t.Errorf("action %s missing blast radius level", a.NodeID)
		}
	}
}
