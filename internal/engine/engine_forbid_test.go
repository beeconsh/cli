package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyFailsWhenDeleteStoreForbidden(t *testing.T) {
	dir := t.TempDir()
	initial := filepath.Join(dir, "infra1.beecon")
	updated := filepath.Join(dir, "infra2.beecon")

	content1 := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
`
	content2 := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  boundary {
    forbid = [delete_store]
  }
}
`
	if err := os.WriteFile(initial, []byte(content1), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(updated, []byte(content2), 0o644); err != nil {
		t.Fatal(err)
	}

	e := New(dir)
	if _, err := e.Apply(initial); err != nil {
		t.Fatalf("initial apply failed: %v", err)
	}
	_, err := e.Apply(updated)
	if err == nil {
		t.Fatalf("expected forbid policy error")
	}
	if !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("unexpected error: %v", err)
	}
}
