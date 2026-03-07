package resolver

import (
	"path/filepath"
	"testing"

	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/parser"
	"github.com/terracotta-ai/beecon/internal/state"
)

func TestBuildPlanDependencyOrder(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "sample.beecon")
	f, err := parser.ParseFile(path)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	g, err := ir.Build(f, path)
	if err != nil {
		t.Fatalf("ir build failed: %v", err)
	}
	p, err := BuildPlan(g, &state.State{Resources: map[string]*state.ResourceRecord{}})
	if err != nil {
		t.Fatalf("plan failed: %v", err)
	}

	pos := map[string]int{}
	for i, a := range p.Actions {
		pos[a.NodeID] = i
	}
	if pos["store.postgres"] > pos["service.api"] {
		t.Fatalf("expected store before service")
	}
}
