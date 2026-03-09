package state

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateV1toV2(t *testing.T) {
	// Create a v1 state (no DriftFirstDetected/DriftCount fields)
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".beecon")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	v1State := map[string]interface{}{
		"version":   1,
		"resources": map[string]interface{}{},
		"audit":     []interface{}{},
		"approvals": map[string]interface{}{},
		"runs":      map[string]interface{}{},
		"actions":   map[string]interface{}{},
	}
	data, _ := json.Marshal(v1State)
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewStore(dir)
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Version != CurrentVersion {
		t.Errorf("expected version %d, got %d", CurrentVersion, st.Version)
	}
}

func TestNewStateUsesCurrentVersion(t *testing.T) {
	st := newState()
	if st.Version != CurrentVersion {
		t.Errorf("expected version %d, got %d", CurrentVersion, st.Version)
	}
}

func TestRunMigrationsIdempotent(t *testing.T) {
	st := &State{Version: CurrentVersion}
	if err := runMigrations(st); err != nil {
		t.Fatal(err)
	}
	if st.Version != CurrentVersion {
		t.Errorf("expected version %d after idempotent migration, got %d", CurrentVersion, st.Version)
	}
}

func TestDriftFieldsAvailableAfterMigration(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".beecon")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// v1 state with a resource that has no drift fields
	v1State := map[string]interface{}{
		"version": 1,
		"resources": map[string]interface{}{
			"service.api": map[string]interface{}{
				"resource_id": "service.api",
				"node_type":   "SERVICE",
				"node_name":   "api",
				"managed":     true,
				"status":      "MATCHED",
			},
		},
		"audit":     []interface{}{},
		"approvals": map[string]interface{}{},
		"runs":      map[string]interface{}{},
		"actions":   map[string]interface{}{},
	}
	data, _ := json.Marshal(v1State)
	if err := os.WriteFile(filepath.Join(stateDir, "state.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}

	store := NewStore(dir)
	st, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	r := st.Resources["service.api"]
	if r == nil {
		t.Fatal("resource not found after migration")
	}
	if r.DriftFirstDetected != nil {
		t.Error("DriftFirstDetected should be nil for migrated resource")
	}
	if r.DriftCount != 0 {
		t.Error("DriftCount should be 0 for migrated resource")
	}
}
