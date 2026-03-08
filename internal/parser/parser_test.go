package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/ast"
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

func TestEscapedQuoteInStripComment(t *testing.T) {
	// Escaped quotes inside a string should not confuse stripComment.
	// The string "has\"quote" should not cause # inside to be mis-detected.
	tmp := filepath.Join(t.TempDir(), "escape.beecon")
	content := `domain x {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
service api {
  value = "has\"quote" # this is a comment
}
`
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ParseFile(tmp)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	var svc *ast.Block
	for _, b := range f.Blocks {
		if b.Name == "api" {
			svc = b
		}
	}
	if svc == nil {
		t.Fatal("missing api block")
	}
	got := svc.Fields["value"].Raw
	if got != `has\"quote` {
		t.Fatalf("unexpected value: %q", got)
	}
}

func TestCommaInsideQuotedListItem(t *testing.T) {
	tmp := filepath.Join(t.TempDir(), "comma.beecon")
	content := `domain x {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
service api {
  tags = ["a,b", "c"]
}
`
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := ParseFile(tmp)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	var svc *ast.Block
	for _, b := range f.Blocks {
		if b.Name == "api" {
			svc = b
		}
	}
	if svc == nil {
		t.Fatal("missing api block")
	}
	list := svc.Fields["tags"].List
	if len(list) != 2 {
		t.Fatalf("expected 2 list items, got %d: %v", len(list), list)
	}
	if list[0] != "a,b" {
		t.Fatalf("expected first item 'a,b', got %q", list[0])
	}
	if list[1] != "c" {
		t.Fatalf("expected second item 'c', got %q", list[1])
	}
}

func TestParserRejectsDotInName(t *testing.T) {
	input := `service my.api {
  runtime = container
}`
	_, err := Parse(strings.NewReader(input))
	if err == nil {
		t.Error("expected error for dot in name, got nil")
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
