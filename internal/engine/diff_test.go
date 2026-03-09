package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/state"
)

const testBeacon = `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store api-db {
  engine = postgres
  instance_class = db.t3.micro
}

service api {
  runtime = container(from: ./Dockerfile)
  needs {
    api-db = read_write
  }
}
`

func TestDiff_NoState_AllAdded(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(testBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	result, err := e.Diff(context.Background(), beacon)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}

	if len(result.Added) != 2 {
		t.Fatalf("expected 2 added, got %d", len(result.Added))
	}
	if len(result.Removed) != 0 {
		t.Fatalf("expected 0 removed, got %d", len(result.Removed))
	}
	if len(result.Modified) != 0 {
		t.Fatalf("expected 0 modified, got %d", len(result.Modified))
	}
	if result.Unchanged != 0 {
		t.Fatalf("expected 0 unchanged, got %d", result.Unchanged)
	}

	// Verify node names are present (sorted).
	names := make([]string, len(result.Added))
	for i, e := range result.Added {
		names[i] = e.NodeName
	}
	if names[0] != "api" || names[1] != "api-db" {
		t.Fatalf("unexpected added names: %v", names)
	}
}

func TestDiff_NoChanges(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(testBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	// First apply to populate state.
	e := New(dir)
	ctx := context.Background()
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Diff should show no changes.
	result, err := e.Diff(ctx, beacon)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}

	if len(result.Added) != 0 {
		t.Fatalf("expected 0 added, got %d", len(result.Added))
	}
	if len(result.Removed) != 0 {
		t.Fatalf("expected 0 removed, got %d", len(result.Removed))
	}
	if len(result.Modified) != 0 {
		t.Fatalf("expected 0 modified, got %d", len(result.Modified))
	}
	if result.Unchanged != 2 {
		t.Fatalf("expected 2 unchanged, got %d", result.Unchanged)
	}
}

func TestDiff_ResourceAdded(t *testing.T) {
	dir := t.TempDir()
	initialBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store api-db {
  engine = postgres
}
`
	expandedBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store api-db {
  engine = postgres
}

service api {
  runtime = container(from: ./Dockerfile)
  needs {
    api-db = read_write
  }
}
`
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(initialBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	ctx := context.Background()
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Write expanded beacon with a new service.
	if err := os.WriteFile(beacon, []byte(expandedBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := e.Diff(ctx, beacon)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}

	if len(result.Added) != 1 {
		t.Fatalf("expected 1 added, got %d", len(result.Added))
	}
	if result.Added[0].NodeName != "api" {
		t.Fatalf("expected added node 'api', got %q", result.Added[0].NodeName)
	}
	if result.Added[0].NodeType != "SERVICE" {
		t.Fatalf("expected type SERVICE, got %q", result.Added[0].NodeType)
	}
	if result.Unchanged != 1 {
		t.Fatalf("expected 1 unchanged, got %d", result.Unchanged)
	}
}

func TestDiff_ResourceRemoved(t *testing.T) {
	dir := t.TempDir()
	fullBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store api-db {
  engine = postgres
}

service api {
  runtime = container(from: ./Dockerfile)
  needs {
    api-db = read_write
  }
}
`
	reducedBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store api-db {
  engine = postgres
}
`

	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(fullBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	ctx := context.Background()
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Write reduced beacon.
	if err := os.WriteFile(beacon, []byte(reducedBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := e.Diff(ctx, beacon)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}

	if len(result.Removed) != 1 {
		t.Fatalf("expected 1 removed, got %d", len(result.Removed))
	}
	if result.Removed[0].NodeName != "api" {
		t.Fatalf("expected removed node 'api', got %q", result.Removed[0].NodeName)
	}
	if result.Unchanged != 1 {
		t.Fatalf("expected 1 unchanged, got %d", result.Unchanged)
	}
}

func TestDiff_ResourceModified(t *testing.T) {
	dir := t.TempDir()
	originalBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store api-db {
  engine = postgres
  instance_class = db.t3.micro
}
`
	modifiedBeacon := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}

store api-db {
  engine = postgres
  instance_class = db.t3.large
}
`

	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(originalBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	ctx := context.Background()
	_, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}

	// Write modified beacon.
	if err := os.WriteFile(beacon, []byte(modifiedBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := e.Diff(ctx, beacon)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}

	if len(result.Modified) != 1 {
		t.Fatalf("expected 1 modified, got %d", len(result.Modified))
	}
	mod := result.Modified[0]
	if mod.NodeName != "api-db" {
		t.Fatalf("expected modified node 'api-db', got %q", mod.NodeName)
	}
	diff, ok := mod.Changes["intent.instance_class"]
	if !ok {
		t.Fatalf("expected change on intent.instance_class, got changes: %v", mod.Changes)
	}
	if diff.Old != "db.t3.micro" {
		t.Fatalf("expected old value 'db.t3.micro', got %v", diff.Old)
	}
	if diff.New != "db.t3.large" {
		t.Fatalf("expected new value 'db.t3.large', got %v", diff.New)
	}
}

func TestDiff_JSONOutput(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	if err := os.WriteFile(beacon, []byte(testBeacon), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	result, err := e.Diff(context.Background(), beacon)
	if err != nil {
		t.Fatalf("diff failed: %v", err)
	}

	// Verify JSON marshaling is properly structured.
	b, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("json marshal failed: %v", err)
	}

	var decoded DiffResult
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}

	if len(decoded.Added) != 2 {
		t.Fatalf("expected 2 added after JSON round-trip, got %d", len(decoded.Added))
	}
	if decoded.Unchanged != 0 {
		t.Fatalf("expected 0 unchanged after JSON round-trip, got %d", decoded.Unchanged)
	}

	// Verify JSON has the expected top-level fields.
	var raw map[string]interface{}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("json unmarshal to map failed: %v", err)
	}
	for _, field := range []string{"added", "removed", "modified", "unchanged"} {
		if _, ok := raw[field]; !ok {
			t.Fatalf("JSON missing field %q", field)
		}
	}
}

func TestComputeDiff_Pure(t *testing.T) {
	// Test the pure comparison function directly with crafted data.
	t.Run("empty graph and state", func(t *testing.T) {
		g := &ir.Graph{Domain: &ir.DomainNode{Cloud: "aws"}}
		st := &state.State{Resources: map[string]*state.ResourceRecord{}}
		result := computeDiff(g, st)
		if len(result.Added) != 0 || len(result.Removed) != 0 || len(result.Modified) != 0 {
			t.Fatal("expected empty diff for empty inputs")
		}
	})

	t.Run("unmanaged resources ignored", func(t *testing.T) {
		g := &ir.Graph{Domain: &ir.DomainNode{Cloud: "aws"}}
		st := &state.State{Resources: map[string]*state.ResourceRecord{
			"store.imported": {
				ResourceID: "store.imported",
				NodeName:   "imported",
				NodeType:   "STORE",
				Managed:    false,
			},
		}}
		result := computeDiff(g, st)
		// Unmanaged resources should NOT appear as removed.
		if len(result.Removed) != 0 {
			t.Fatalf("expected 0 removed (unmanaged), got %d", len(result.Removed))
		}
	})
}

func TestScrubDiffResult_RedactsSensitiveFields(t *testing.T) {
	result := &DiffResult{
		Added:   []DiffEntry{},
		Removed: []DiffEntry{},
		Modified: []DiffEntry{
			{
				NodeName: "my-db",
				NodeType: "STORE",
				Changes: map[string]FieldDiff{
					"intent.instance_class": {Old: "db.t3.micro", New: "db.t3.large"},
					"intent.password":       {Old: "oldpass", New: "newpass"},
					"intent.api_key":        {Old: "key123", New: "key456"},
					"intent.secret":         {Old: nil, New: "newsecret"},
					"intent.token":          {Old: "oldtoken", New: nil},
				},
			},
		},
	}

	ScrubDiffResult(result)

	changes := result.Modified[0].Changes

	// Non-sensitive field should be untouched.
	if changes["intent.instance_class"].Old != "db.t3.micro" ||
		changes["intent.instance_class"].New != "db.t3.large" {
		t.Fatal("non-sensitive field was incorrectly scrubbed")
	}

	// Sensitive fields should be redacted.
	for _, k := range []string{"intent.password", "intent.api_key"} {
		fd := changes[k]
		if fd.Old != "**REDACTED**" || fd.New != "**REDACTED**" {
			t.Fatalf("sensitive field %q not scrubbed: old=%v new=%v", k, fd.Old, fd.New)
		}
	}

	// Sensitive field added (Old was nil) should preserve nil.
	secretFD := changes["intent.secret"]
	if secretFD.Old != nil {
		t.Fatalf("expected nil Old for added sensitive field, got %v", secretFD.Old)
	}
	if secretFD.New != "**REDACTED**" {
		t.Fatalf("expected redacted New for added sensitive field, got %v", secretFD.New)
	}

	// Sensitive field removed (New was nil) should preserve nil.
	tokenFD := changes["intent.token"]
	if tokenFD.Old != "**REDACTED**" {
		t.Fatalf("expected redacted Old for removed sensitive field, got %v", tokenFD.Old)
	}
	if tokenFD.New != nil {
		t.Fatalf("expected nil New for removed sensitive field, got %v", tokenFD.New)
	}
}

func TestValuesEqual_MapOrdering(t *testing.T) {
	// Maps with the same keys/values but different insertion order must be equal.
	a := map[string]interface{}{"x": "1", "y": "2", "z": "3"}
	b := map[string]interface{}{"z": "3", "x": "1", "y": "2"}
	if !valuesEqual(a, b) {
		t.Fatal("valuesEqual should return true for maps with same content regardless of order")
	}

	// Maps with different values must not be equal.
	c := map[string]interface{}{"x": "1", "y": "99"}
	if valuesEqual(a, c) {
		t.Fatal("valuesEqual should return false for maps with different values")
	}
}

func TestCompareSnapshots(t *testing.T) {
	tests := []struct {
		name     string
		old      map[string]interface{}
		new      map[string]interface{}
		wantKeys []string
	}{
		{
			name:     "identical",
			old:      map[string]interface{}{"a": "1", "b": "2"},
			new:      map[string]interface{}{"a": "1", "b": "2"},
			wantKeys: nil,
		},
		{
			name:     "value changed",
			old:      map[string]interface{}{"a": "1"},
			new:      map[string]interface{}{"a": "2"},
			wantKeys: []string{"a"},
		},
		{
			name:     "field added",
			old:      map[string]interface{}{"a": "1"},
			new:      map[string]interface{}{"a": "1", "b": "2"},
			wantKeys: []string{"b"},
		},
		{
			name:     "field removed",
			old:      map[string]interface{}{"a": "1", "b": "2"},
			new:      map[string]interface{}{"a": "1"},
			wantKeys: []string{"b"},
		},
		{
			name:     "numeric normalization",
			old:      map[string]interface{}{"count": float64(5)},
			new:      map[string]interface{}{"count": float64(5)},
			wantKeys: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changes := compareSnapshots(tt.old, tt.new)
			if len(changes) != len(tt.wantKeys) {
				t.Fatalf("expected %d changes, got %d: %v", len(tt.wantKeys), len(changes), changes)
			}
			for _, k := range tt.wantKeys {
				if _, ok := changes[k]; !ok {
					t.Fatalf("expected change for key %q, not found", k)
				}
			}
		})
	}
}
