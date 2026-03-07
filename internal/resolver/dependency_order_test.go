package resolver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/parser"
	"github.com/terracotta-ai/beecon/internal/state"
)

func TestBuildPlanPreservesDependencyOrderWithinType(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "infra.beecon")
	content := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store z {
  engine = postgres
}
service b {
  runtime = container(from: ./Dockerfile)
  needs {
    z = read_write
  }
}
service a {
  runtime = container(from: ./Dockerfile)
  needs {
    b = invoke
  }
}
`
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := parser.ParseFile(p)
	if err != nil {
		t.Fatal(err)
	}
	g, err := ir.Build(f, p)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(g, &state.State{Resources: map[string]*state.ResourceRecord{}})
	if err != nil {
		t.Fatal(err)
	}

	idx := map[string]int{}
	for i, a := range plan.Actions {
		idx[a.NodeID] = i
	}
	if idx["service.b"] >= idx["service.a"] {
		t.Fatalf("expected service.b before service.a, got order %#v", idx)
	}
}
