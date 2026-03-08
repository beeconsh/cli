package compliance

import (
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/parser"
)

func buildGraph(t *testing.T, src string) *ir.Graph {
	t.Helper()
	f, err := parser.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := ir.Build(f, "test")
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestEnforceHIPAAMutation(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa]
}
store db {
  engine = postgres
  kms_key = arn:aws:kms:us-east-1:123:key/abc
}
`)
	report, err := Enforce(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Mutations) == 0 {
		t.Fatal("expected compliance mutations for HIPAA")
	}

	// Check that backup_retention, multi_az, deletion_protection were set
	node := g.Nodes[0]
	if node.Intent["backup_retention"] != "7" {
		t.Errorf("expected backup_retention=7, got %q", node.Intent["backup_retention"])
	}
	if node.Intent["multi_az"] != "true" {
		t.Errorf("expected multi_az=true, got %q", node.Intent["multi_az"])
	}
	if node.Intent["deletion_protection"] != "true" {
		t.Errorf("expected deletion_protection=true, got %q", node.Intent["deletion_protection"])
	}
}

func TestEnforceHIPAAViolation(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa]
}
store db {
  engine = postgres
  multi_az = false
  kms_key = arn:aws:kms:us-east-1:123:key/abc
}
`)
	report, err := Enforce(g)
	if err == nil {
		t.Fatal("expected violation error")
	}
	if !report.HasViolations() {
		t.Fatal("expected violations in report")
	}
	// multi_az = false violates HIPAA
	found := false
	for _, v := range report.Violations {
		if v.Field == "multi_az" {
			found = true
		}
	}
	if !found {
		t.Error("expected multi_az violation")
	}
}

func TestEnforceComplianceOverride(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa]
}
store db {
  engine = postgres
  multi_az = false
  kms_key = arn:aws:kms:us-east-1:123:key/abc
  compliance_override = [multi_az]
}
`)
	_, err := Enforce(g)
	if err != nil {
		t.Fatalf("expected override to suppress violation, got: %v", err)
	}
}

func TestEnforceNoCompliance(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store db {
  engine = postgres
}
`)
	report, err := Enforce(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Mutations) != 0 {
		t.Error("expected no mutations without compliance declaration")
	}
}

func TestEnforceSOC2(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [soc2]
}
store db {
  engine = postgres
}
`)
	report, err := Enforce(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Mutations) == 0 {
		t.Fatal("expected SOC2 mutations")
	}
	// SOC2 requires backup >= 1 day
	node := g.Nodes[0]
	if node.Intent["backup_retention"] != "1" {
		t.Errorf("expected backup_retention=1 for SOC2, got %q", node.Intent["backup_retention"])
	}
}

func TestEnforceMultiFrameworkStrictestWins(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [soc2, hipaa]
}
store db {
  engine = postgres
  kms_key = arn:aws:kms:us-east-1:123:key/abc
}
`)
	report, err := Enforce(g)
	// With multi-framework resolution: strictest defaults win.
	// encryption: cmk (HIPAA) > true (SOC2) → cmk applied
	// backup_retention: 7 (HIPAA) > 1 (SOC2) → 7 applied
	// No violations because the strictest defaults satisfy all frameworks.
	if err != nil {
		t.Fatalf("expected no error with strictest-wins resolution, got: %v", err)
	}
	node := g.Nodes[0]
	if node.Intent["encryption"] != "cmk" {
		t.Errorf("expected encryption=cmk (HIPAA strictest), got %q", node.Intent["encryption"])
	}
	if node.Intent["backup_retention"] != "7" {
		t.Errorf("expected backup_retention=7 (HIPAA strictest), got %q", node.Intent["backup_retention"])
	}
	if len(report.Mutations) == 0 {
		t.Error("expected mutations from multi-framework enforcement")
	}
}

func TestEnforceUnknownFramework(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [pci_dss]
}
store db {
  engine = postgres
}
`)
	_, err := Enforce(g)
	if err == nil {
		t.Fatal("expected error for unknown framework")
	}
	if !strings.Contains(err.Error(), "unknown compliance framework") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestEnforceHIPAAMissingKMSKey(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa]
}
store db {
  engine = postgres
}
`)
	report, err := Enforce(g)
	if err == nil {
		t.Fatal("expected violation for missing kms_key under HIPAA")
	}
	found := false
	for _, v := range report.Violations {
		if v.Field == "kms_key" {
			found = true
		}
	}
	if !found {
		t.Error("expected kms_key violation")
	}
}

func TestEnforceBackupRetentionViolatesMin(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa]
}
store db {
  engine = postgres
  backup_retention = 3
  kms_key = arn:aws:kms:us-east-1:123:key/abc
}
`)
	report, err := Enforce(g)
	if err == nil {
		t.Fatal("expected violation for backup_retention < 7")
	}
	found := false
	for _, v := range report.Violations {
		if v.Field == "backup_retention" {
			found = true
		}
	}
	if !found {
		t.Error("expected backup_retention violation")
	}
	_ = report
}

func TestEnforceInvalidComplianceOverride(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa]
}
store db {
  engine = postgres
  kms_key = arn:aws:kms:us-east-1:123:key/abc
  compliance_override = [nonexistent_field]
}
`)
	report, err := Enforce(g)
	if err == nil {
		t.Fatal("expected violation for invalid compliance_override field")
	}
	found := false
	for _, v := range report.Violations {
		if v.Field == "nonexistent_field" && strings.Contains(v.Reason, "unknown field") {
			found = true
		}
	}
	if !found {
		t.Error("expected violation mentioning unknown field in compliance_override")
	}
}

func TestEnforceNonNumericBackupRetention(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa]
}
store db {
  engine = postgres
  backup_retention = never
  kms_key = arn:aws:kms:us-east-1:123:key/abc
}
`)
	report, err := Enforce(g)
	if err == nil {
		t.Fatal("expected violation for non-numeric backup_retention")
	}
	found := false
	for _, v := range report.Violations {
		if v.Field == "backup_retention" {
			found = true
		}
	}
	if !found {
		t.Error("expected backup_retention violation for non-numeric value")
	}
	_ = report
}

func TestEnforceOverrideTracked(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
  compliance = [hipaa]
}
store db {
  engine = postgres
  multi_az = false
  kms_key = arn:aws:kms:us-east-1:123:key/abc
  compliance_override = [multi_az]
}
`)
	report, err := Enforce(g)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(report.Overrides) == 0 {
		t.Error("expected overrides to be tracked in report")
	}
	found := false
	for _, o := range report.Overrides {
		if o.Field == "multi_az" {
			found = true
		}
	}
	if !found {
		t.Error("expected multi_az override in report")
	}
}

