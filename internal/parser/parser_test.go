package parser

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFile(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "sample.beecon")
	f, err := ParseFile(path)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if len(f.Blocks) != 4 {
		t.Fatalf("expected 4 top-level blocks, got %d", len(f.Blocks))
	}
}

func TestParseError(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "bad.beecon")
	if err := os.WriteFile(tmp, []byte("service api {\nfoo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(tmp); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestValidateSemanticError(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "bad2.beecon")
	content := `domain x {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
service api {
  needs {
    postgres = read_write
  }
}
`
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(tmp); err == nil {
		t.Fatal("expected semantic validation error for unknown dependency")
	}
}

func TestValidateUnknownProfileReference(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "bad3.beecon")
	content := `domain x {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
service api {
  apply = [standard_service]
}
`
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFile(tmp); err == nil {
		t.Fatal("expected validation error for unknown profile reference")
	}
}
