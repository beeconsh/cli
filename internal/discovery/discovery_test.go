package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverBeacons(t *testing.T) {
	dir := t.TempDir()
	mustWrite := func(rel string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("domain x {\n cloud = aws(region: us-east-1)\n owner = team(a)\n}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("infra.beecon")
	mustWrite("services/api/infra.beecon")
	mustWrite(".git/ignored.beecon")
	mustWrite(".beecon/ignored.beecon")
	mustWrite("node_modules/ignored.beecon")

	paths, err := DiscoverBeacons(dir)
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 beacons, got %d: %#v", len(paths), paths)
	}
}
