package engine

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyDeleteManagedResource(t *testing.T) {
	dir := t.TempDir()
	initial := filepath.Join(dir, "infra1.beecon")
	updated := filepath.Join(dir, "infra2.beecon")

	content1 := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store bucket {
  engine = s3
}
`
	content2 := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
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
	if _, err := e.Apply(updated); err != nil {
		t.Fatalf("delete apply failed: %v", err)
	}
	st, err := e.Status()
	if err != nil {
		t.Fatal(err)
	}
	rec := st.Resources["store.bucket"]
	if rec == nil {
		t.Fatalf("expected record for store.bucket")
	}
	if rec.Managed {
		t.Fatalf("expected managed=false after delete")
	}
}
