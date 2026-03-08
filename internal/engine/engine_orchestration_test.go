package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/terracotta-ai/beecon/internal/state"
)

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
