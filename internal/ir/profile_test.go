package ir

import (
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/parser"
)

func TestProfileInheritanceApplied(t *testing.T) {
	src := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
profile standard_service {
  runtime = container(from: ./Dockerfile)
  scaling = auto
  performance {
    latency = p95 < 200ms
  }
}
service api {
  apply = [standard_service]
  scaling = fixed:2
}
`
	f, err := parser.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(f, "inline")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(g.Nodes))
	}
	n := g.Nodes[0]
	if n.Intent["runtime"] == "" {
		t.Fatalf("expected runtime inherited from profile")
	}
	if n.Intent["scaling"] != "fixed:2" {
		t.Fatalf("expected local scaling override, got %q", n.Intent["scaling"])
	}
	if n.Performance["latency"] == "" {
		t.Fatalf("expected inherited performance latency")
	}
}
