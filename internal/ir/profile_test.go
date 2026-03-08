package ir

import (
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/parser"
)

func TestDotVariantProfileResolution(t *testing.T) {
	src := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
profile standard {
  instance_type = db.t3.medium
  scaling = auto
}
profile standard.staging {
  instance_type = db.t3.micro
}
profile standard.production {
  instance_type = db.r6g.xlarge
}
store db {
  engine = postgres
  apply = [standard]
}
`
	f, err := parser.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}

	// With production profile: should use db.r6g.xlarge
	g, err := Build(f, "test", "production")
	if err != nil {
		t.Fatal(err)
	}
	if g.ActiveProfile != "production" {
		t.Fatalf("expected active profile 'production', got %q", g.ActiveProfile)
	}
	n := g.Nodes[0]
	if n.Intent["instance_type"] != "db.r6g.xlarge" {
		t.Fatalf("expected production instance_type, got %q", n.Intent["instance_type"])
	}
	// scaling should still come from base profile
	if n.Intent["scaling"] != "auto" {
		t.Fatalf("expected scaling from base profile, got %q", n.Intent["scaling"])
	}
}

func TestDotVariantProfileNoProfile(t *testing.T) {
	src := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
profile standard {
  instance_type = db.t3.medium
}
profile standard.production {
  instance_type = db.r6g.xlarge
}
store db {
  engine = postgres
  apply = [standard]
}
`
	f, err := parser.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}

	// Without active profile: should use base standard only
	g, err := Build(f, "test")
	if err != nil {
		t.Fatal(err)
	}
	n := g.Nodes[0]
	if n.Intent["instance_type"] != "db.t3.medium" {
		t.Fatalf("expected base instance_type without profile, got %q", n.Intent["instance_type"])
	}
}

func TestDotVariantProfileStagingOverride(t *testing.T) {
	src := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
profile standard {
  instance_type = db.t3.medium
}
profile standard.staging {
  instance_type = db.t3.micro
}
store db {
  engine = postgres
  apply = [standard]
}
`
	f, err := parser.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}

	g, err := Build(f, "test", "staging")
	if err != nil {
		t.Fatal(err)
	}
	n := g.Nodes[0]
	if n.Intent["instance_type"] != "db.t3.micro" {
		t.Fatalf("expected staging instance_type, got %q", n.Intent["instance_type"])
	}
}

func TestComplianceOverrideParsed(t *testing.T) {
	src := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store db {
  engine = postgres
  compliance_override = [multi_az, backup_retention]
}
`
	f, err := parser.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(f, "test")
	if err != nil {
		t.Fatal(err)
	}
	n := g.Nodes[0]
	if len(n.ComplianceOverrides) != 2 {
		t.Fatalf("expected 2 compliance overrides, got %d", len(n.ComplianceOverrides))
	}
	if n.ComplianceOverrides[0] != "multi_az" || n.ComplianceOverrides[1] != "backup_retention" {
		t.Fatalf("unexpected overrides: %v", n.ComplianceOverrides)
	}
}

func TestBudgetParsedFromDomain(t *testing.T) {
	src := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  budget = 5000/mo
}
service api {
  runtime = container(from: ./Dockerfile)
}
`
	f, err := parser.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(f, "test")
	if err != nil {
		t.Fatal(err)
	}
	if g.Domain.Budget != "5000/mo" {
		t.Fatalf("expected budget '5000/mo', got %q", g.Domain.Budget)
	}
}

func TestProfileNeedsDeduplicated(t *testing.T) {
	src := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
profile standard_service {
  runtime = container(from: ./Dockerfile)
  needs {
    db = read
  }
}
store db {
  engine = postgres
}
service api {
  apply = [standard_service]
  needs {
    db = write
  }
}
`
	f, err := parser.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := Build(f, "test")
	if err != nil {
		t.Fatal(err)
	}
	var apiNode *IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}
	// Should have only one dependency on "db", with the local mode "write" winning
	dbCount := 0
	for _, dep := range apiNode.Needs {
		if dep.Target == "db" {
			dbCount++
			if dep.Mode != "write" {
				t.Errorf("expected local mode 'write' to override profile 'read', got %q", dep.Mode)
			}
		}
	}
	if dbCount != 1 {
		t.Errorf("expected 1 dependency on 'db' after dedup, got %d", dbCount)
	}
}

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
