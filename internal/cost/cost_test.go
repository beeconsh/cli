package cost

import (
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/parser"
	"github.com/terracotta-ai/beecon/internal/resolver"
	"github.com/terracotta-ai/beecon/internal/state"
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

func TestParseBudget(t *testing.T) {
	tests := []struct {
		input  string
		amount float64
		period string
	}{
		{"5000/mo", 5000, "mo"},
		{"$5000/mo", 5000, "mo"},
		{"60000/yr", 60000, "yr"},
		{"1000/month", 1000, "mo"},
		{"12000/annual", 12000, "yr"},
	}
	for _, tt := range tests {
		b, err := ParseBudget(tt.input)
		if err != nil {
			t.Errorf("ParseBudget(%q) error: %v", tt.input, err)
			continue
		}
		if b.Amount != tt.amount {
			t.Errorf("ParseBudget(%q).Amount = %v, want %v", tt.input, b.Amount, tt.amount)
		}
		if b.Period != tt.period {
			t.Errorf("ParseBudget(%q).Period = %v, want %v", tt.input, b.Period, tt.period)
		}
	}
}

func TestParseBudgetEmpty(t *testing.T) {
	b, err := ParseBudget("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if b != nil {
		t.Error("expected nil for empty input")
	}
}

func TestParseBudgetInvalid(t *testing.T) {
	invalids := []string{"notabudget", "5000", "abc/mo", "-100/mo", "100/days"}
	for _, s := range invalids {
		_, err := ParseBudget(s)
		if err == nil {
			t.Errorf("expected error for %q", s)
		}
	}
}

func TestParseBudgetRejectsZero(t *testing.T) {
	_, err := ParseBudget("$0/mo")
	if err == nil {
		t.Error("expected error for zero budget")
	}
}

func TestBudgetMonthlyAmount(t *testing.T) {
	b := &Budget{Amount: 12000, Period: "yr"}
	if b.MonthlyAmount() != 1000 {
		t.Errorf("expected 1000, got %v", b.MonthlyAmount())
	}
	b2 := &Budget{Amount: 500, Period: "mo"}
	if b2.MonthlyAmount() != 500 {
		t.Errorf("expected 500, got %v", b2.MonthlyAmount())
	}
}

func TestLookupInstancePrice(t *testing.T) {
	price, ok := LookupInstancePrice("db.t3.micro")
	if !ok {
		t.Fatal("expected price for db.t3.micro")
	}
	if price != 15 {
		t.Errorf("expected 15, got %v", price)
	}

	_, ok = LookupInstancePrice("db.z99.mega")
	if ok {
		t.Error("expected no price for unknown instance type")
	}
}

func TestSuggestCheaper(t *testing.T) {
	alt, savings, ok := SuggestCheaper("db.r6g.xlarge")
	if !ok {
		t.Fatal("expected cheaper alternative")
	}
	if alt != "db.r6g.large" {
		t.Errorf("expected db.r6g.large, got %q", alt)
	}
	if savings != 200 {
		t.Errorf("expected savings of 200, got %v", savings)
	}
}

func TestSuggestCheaperSmallest(t *testing.T) {
	_, _, ok := SuggestCheaper("db.t3.micro")
	if ok {
		t.Error("expected no cheaper alternative for smallest instance")
	}
}

func TestSuggestCheaperUnknown(t *testing.T) {
	_, _, ok := SuggestCheaper("db.unknown.big")
	if ok {
		t.Error("expected no suggestion for unknown instance")
	}
}

func TestEvaluateCostReport(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store db {
  engine = postgres
  instance_type = db.r6g.xlarge
}
service api {
  runtime = container(from: ./Dockerfile)
}
`)
	st := &state.State{Resources: map[string]*state.ResourceRecord{}}
	plan, err := resolver.BuildPlan(g, st)
	if err != nil {
		t.Fatal(err)
	}

	budget, _ := ParseBudget("500/mo")
	report := Evaluate(plan, g, st, budget)

	if report.TotalMonthlyCost == 0 {
		t.Error("expected non-zero cost")
	}

	// db.r6g.xlarge = $400, so should find cheaper alternative
	if len(report.Alternatives) == 0 {
		t.Error("expected cheaper alternative for db.r6g.xlarge")
	}

	// Budget exceeded: $400 + ECS cost > $500? depends on ECS estimate
	// The db alone is $400, api is ~$33 (Fargate), total ~$433 < $500
	// Actually we don't need the budget to be exceeded for this test
}

func TestEvaluateNoBudget(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
service api {
  runtime = container(from: ./Dockerfile)
}
`)
	st := &state.State{Resources: map[string]*state.ResourceRecord{}}
	plan, err := resolver.BuildPlan(g, st)
	if err != nil {
		t.Fatal(err)
	}

	report := Evaluate(plan, g, st, nil)
	if report.BudgetExceeded {
		t.Error("budget should not be exceeded without a budget")
	}
}

func TestEvaluateBudgetExceeded(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store db {
  engine = postgres
  instance_type = db.r6g.xlarge
}
`)
	st := &state.State{Resources: map[string]*state.ResourceRecord{}}
	plan, err := resolver.BuildPlan(g, st)
	if err != nil {
		t.Fatal(err)
	}

	budget, _ := ParseBudget("100/mo")
	report := Evaluate(plan, g, st, budget)

	if !report.BudgetExceeded {
		t.Error("expected budget to be exceeded (db.r6g.xlarge=$400 > $100)")
	}
}

func TestEvaluateNilPlan(t *testing.T) {
	budget, _ := ParseBudget("1000/mo")
	report := Evaluate(nil, nil, nil, budget)
	if report == nil {
		t.Fatal("expected non-nil report")
	}
	if report.TotalMonthlyCost != 0 {
		t.Errorf("expected zero cost for nil plan, got %v", report.TotalMonthlyCost)
	}
	if report.BudgetExceeded {
		t.Error("nil plan should not exceed budget")
	}
}

func TestFormatDelta(t *testing.T) {
	report := &CostReport{TotalMonthlyCost: 440}
	s := FormatDelta(report)
	if s != "+$440/mo (estimated)" {
		t.Errorf("unexpected format: %q", s)
	}
}

func TestFormatDeltaZero(t *testing.T) {
	s := FormatDelta(nil)
	if s != "$0/mo" {
		t.Errorf("unexpected format: %q", s)
	}
}

func TestEstimateFargateCost(t *testing.T) {
	cost := EstimateFargateCost(0.25, 0.5, 1)
	if cost <= 0 {
		t.Errorf("expected positive cost, got %v", cost)
	}
	// 0.25 * 0.04048 * 730 + 0.5 * 0.004445 * 730 ≈ 7.4 + 1.6 ≈ 9
	if cost < 5 || cost > 15 {
		t.Errorf("expected ~9, got %v", cost)
	}
}

func TestFormatAlternatives(t *testing.T) {
	alts := []Alternative{
		{NodeName: "db", CurrentInstance: "db.r6g.xlarge", CurrentCost: 400, SuggestedInstance: "db.r6g.large", SuggestedCost: 200, MonthlySavings: 200},
	}
	lines := FormatAlternatives(alts)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "db.r6g.large") {
		t.Errorf("expected alternative mention, got %q", lines[0])
	}
}
