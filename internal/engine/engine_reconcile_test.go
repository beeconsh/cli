package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/terracotta-ai/beecon/internal/provider"
	"github.com/terracotta-ai/beecon/internal/state"
)

// driftTestExecutor simulates drift by returning modified LiveState from Observe.
type driftTestExecutor struct {
	provider.Executor
	driftedNodes map[string]map[string]interface{} // nodeID -> modified live state
}

func (e *driftTestExecutor) Observe(_ context.Context, _, _ string, rec *state.ResourceRecord) (*provider.ObserveResult, error) {
	if ls, ok := e.driftedNodes[rec.ResourceID]; ok {
		return &provider.ObserveResult{
			Exists:     true,
			ProviderID: rec.ProviderID,
			LiveState:  ls,
		}, nil
	}
	// Return the intent snapshot as live state (no drift).
	return &provider.ObserveResult{
		Exists:     true,
		ProviderID: rec.ProviderID,
		LiveState:  rec.IntentSnapshot,
	}, nil
}

func (e *driftTestExecutor) Apply(_ context.Context, req provider.ApplyRequest) (*provider.ApplyResult, error) {
	return &provider.ApplyResult{ProviderID: req.Record.ProviderID}, nil
}

func (e *driftTestExecutor) IsDryRun() bool { return true }

const reconcileBeacon = `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = postgres
}
`

// reconcileBeaconUpdated changes the engine field, causing intent hash to differ
// from what was stored during the original apply.
const reconcileBeaconUpdated = `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store postgres {
  engine = mysql
}
`

func setupReconcileEngine(t *testing.T, driftedNodes map[string]map[string]interface{}) (*Engine, string) {
	t.Helper()
	dir := t.TempDir()
	beacon := writeBeacon(t, dir, reconcileBeacon)

	e := New(dir)
	ctx := context.Background()

	// Apply once to establish state.
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}

	// Replace executor with drift-simulating executor.
	e.exec = &driftTestExecutor{driftedNodes: driftedNodes}

	return e, beacon
}

func TestReconcileNoDrift(t *testing.T) {
	e, beacon := setupReconcileEngine(t, nil) // no drift
	ctx := context.Background()

	result, err := e.DriftReconcile(ctx, beacon, false)
	if err != nil {
		t.Fatalf("DriftReconcile failed: %v", err)
	}
	if result.DriftedCount != 0 {
		t.Fatalf("expected 0 drifted, got %d", result.DriftedCount)
	}
	if result.ReconciledCount != 0 {
		t.Fatalf("expected 0 reconciled, got %d", result.ReconciledCount)
	}
	if result.FailedCount != 0 {
		t.Fatalf("expected 0 failed, got %d", result.FailedCount)
	}
	if len(result.Actions) != 0 {
		t.Fatalf("expected 0 actions, got %d", len(result.Actions))
	}
}

func TestReconcileDetectsDriftAndGeneratesPlan(t *testing.T) {
	// Apply with the original beacon, then change the beacon to cause
	// intent hash mismatch (simulating that the live state drifted from intent).
	e, beacon := setupReconcileEngine(t, nil)
	ctx := context.Background()

	// Overwrite the beacon with updated content (engine = mysql).
	// This makes Drift detect that the intent hash differs from stored hash.
	if err := os.WriteFile(beacon, []byte(reconcileBeaconUpdated), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := e.DriftReconcile(ctx, beacon, false)
	if err != nil {
		t.Fatalf("DriftReconcile failed: %v", err)
	}
	if result.DriftedCount == 0 {
		t.Fatal("expected drifted resources")
	}
	if len(result.Actions) == 0 {
		t.Fatal("expected reconciliation actions")
	}

	// All actions should be "pending" (plan-only mode).
	for _, a := range result.Actions {
		if a.Status != "pending" && a.Status != "skipped" {
			t.Fatalf("expected pending or skipped status in plan mode, got %q for %s", a.Status, a.Target)
		}
	}

	// Verify drift fields are populated for at least one action.
	foundFields := false
	for _, a := range result.Actions {
		if len(a.DriftFields) > 0 {
			foundFields = true
			break
		}
	}
	if !foundFields {
		t.Fatal("expected at least one action with drift fields populated")
	}
}

func TestReconcileWithApplyExecutesUpdate(t *testing.T) {
	e, beacon := setupReconcileEngine(t, nil)
	ctx := context.Background()

	// Overwrite beacon to cause drift.
	if err := os.WriteFile(beacon, []byte(reconcileBeaconUpdated), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := e.DriftReconcile(ctx, beacon, true)
	if err != nil {
		t.Fatalf("DriftReconcile with apply failed: %v", err)
	}
	if result.DriftedCount == 0 {
		t.Fatal("expected drifted resources")
	}
	if result.ReconciledCount == 0 {
		t.Fatal("expected at least one reconciled resource")
	}

	// Check that reconciled actions have status "reconciled".
	foundReconciled := false
	for _, a := range result.Actions {
		if a.Status == "reconciled" {
			foundReconciled = true
		}
	}
	if !foundReconciled {
		t.Fatal("expected at least one action with 'reconciled' status")
	}

	// Verify state was updated — the resource should now be MATCHED.
	st, err := e.store.Load()
	if err != nil {
		t.Fatalf("load state: %v", err)
	}
	rec := st.Resources["store.postgres"]
	if rec == nil {
		t.Fatal("expected store.postgres in state")
	}
	if rec.Status != state.StatusMatched {
		t.Fatalf("expected MATCHED after reconcile, got %s", rec.Status)
	}
}

func TestReconcileDriftFieldsAccuracy(t *testing.T) {
	e, beacon := setupReconcileEngine(t, nil)
	ctx := context.Background()

	// Overwrite beacon to cause drift on the engine field.
	if err := os.WriteFile(beacon, []byte(reconcileBeaconUpdated), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := e.DriftReconcile(ctx, beacon, false)
	if err != nil {
		t.Fatalf("DriftReconcile failed: %v", err)
	}

	// Find the postgres action.
	var postgresAction *ReconcileAction
	for i := range result.Actions {
		if result.Actions[i].Target == "store.postgres" {
			postgresAction = &result.Actions[i]
			break
		}
	}
	if postgresAction == nil {
		t.Fatal("expected reconcile action for store.postgres")
	}

	// The drift field should include "intent.engine" since the beacon changed
	// engine from "postgres" to "mysql".
	found := false
	for _, f := range postgresAction.DriftFields {
		if f == "intent.engine" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'intent.engine' in drift fields, got %v", postgresAction.DriftFields)
	}
}

func TestReconcileJSONOutput(t *testing.T) {
	e, beacon := setupReconcileEngine(t, nil)
	ctx := context.Background()

	// Overwrite beacon to cause drift.
	if err := os.WriteFile(beacon, []byte(reconcileBeaconUpdated), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := e.DriftReconcile(ctx, beacon, false)
	if err != nil {
		t.Fatalf("DriftReconcile failed: %v", err)
	}

	// Marshal to JSON and verify structure.
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("JSON marshal failed: %v", err)
	}

	var parsed ReconcileResult
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	if parsed.DriftedCount != result.DriftedCount {
		t.Fatalf("JSON round-trip: drifted_count mismatch: %d != %d", parsed.DriftedCount, result.DriftedCount)
	}
	if len(parsed.Actions) != len(result.Actions) {
		t.Fatalf("JSON round-trip: actions count mismatch: %d != %d", len(parsed.Actions), len(result.Actions))
	}

	// Verify JSON has expected fields.
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("JSON raw parse failed: %v", err)
	}
	for _, key := range []string{"drifted_count", "reconciled_count", "failed_count", "actions"} {
		if _, ok := raw[key]; !ok {
			t.Fatalf("expected key %q in JSON output", key)
		}
	}

	// Verify action structure.
	actionsRaw, ok := raw["actions"].([]interface{})
	if !ok {
		t.Fatal("expected actions to be an array")
	}
	if len(actionsRaw) > 0 {
		action, ok := actionsRaw[0].(map[string]interface{})
		if !ok {
			t.Fatal("expected action to be an object")
		}
		for _, key := range []string{"node_name", "target", "drift_fields", "status"} {
			if _, ok := action[key]; !ok {
				t.Fatalf("expected key %q in action JSON", key)
			}
		}
	}
}

func TestReconcileApplyWithNoDrift(t *testing.T) {
	// DriftReconcile with apply=true and no drift should succeed with empty result.
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(reconcileBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	ctx := context.Background()

	// Apply to establish state.
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// No drift, apply=true should succeed with empty result.
	result, err := e.DriftReconcile(ctx, beacon, true)
	if err != nil {
		t.Fatalf("DriftReconcile failed: %v", err)
	}
	if result.DriftedCount != 0 {
		t.Fatalf("expected 0 drifted, got %d", result.DriftedCount)
	}
}
