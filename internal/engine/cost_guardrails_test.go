package engine

import (
	"os"
	"testing"

	"github.com/terracotta-ai/beecon/internal/cost"
	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/resolver"
	"github.com/terracotta-ai/beecon/internal/state"
)

func TestCostDeltaCalculation(t *testing.T) {
	tests := []struct {
		name      string
		actions   []*state.PlanAction
		estimates []cost.CostEstimate
		state     *state.State
		wantDelta float64
	}{
		{
			name: "create_adds_cost",
			actions: []*state.PlanAction{
				{ID: "a1", NodeID: "service.api", Operation: "CREATE", NodeType: "SERVICE"},
			},
			estimates: []cost.CostEstimate{
				{NodeID: "service.api", MonthlyCost: 50},
			},
			state:     newTestState(),
			wantDelta: 50,
		},
		{
			name: "delete_removes_cost",
			actions: []*state.PlanAction{
				{ID: "a1", NodeID: "store.db", Operation: "DELETE", NodeType: "STORE"},
			},
			estimates: nil,
			state: &state.State{
				Resources: map[string]*state.ResourceRecord{
					"store.db": {ResourceID: "store.db", EstimatedCost: 200},
				},
			},
			wantDelta: -200,
		},
		{
			name: "create_and_delete_net_delta",
			actions: []*state.PlanAction{
				{ID: "a1", NodeID: "service.api", Operation: "CREATE", NodeType: "SERVICE"},
				{ID: "a2", NodeID: "service.old", Operation: "DELETE", NodeType: "SERVICE"},
			},
			estimates: []cost.CostEstimate{
				{NodeID: "service.api", MonthlyCost: 100},
			},
			state: &state.State{
				Resources: map[string]*state.ResourceRecord{
					"service.old": {ResourceID: "service.old", EstimatedCost: 60},
				},
			},
			wantDelta: 40,
		},
		{
			name: "update_no_delta",
			actions: []*state.PlanAction{
				{ID: "a1", NodeID: "service.api", Operation: "UPDATE", NodeType: "SERVICE"},
			},
			estimates: []cost.CostEstimate{
				{NodeID: "service.api", MonthlyCost: 50},
			},
			state:     newTestState(),
			wantDelta: 0,
		},
		{
			name: "nil_state_delete_no_crash",
			actions: []*state.PlanAction{
				{ID: "a1", NodeID: "store.db", Operation: "DELETE", NodeType: "STORE"},
			},
			estimates: nil,
			state:     nil,
			wantDelta: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := &resolver.Plan{Actions: tt.actions}
			cr := &cost.CostReport{Estimates: tt.estimates}
			summary := enrichPlanActions(plan, cr, nil, tt.state, nil)
			if summary.CostDelta != tt.wantDelta {
				t.Errorf("CostDelta = %v, want %v", summary.CostDelta, tt.wantDelta)
			}
		})
	}
}

func TestBudgetUtilization(t *testing.T) {
	tests := []struct {
		name             string
		totalMonthlyCost float64
		budgetAmount     float64
		budgetPeriod     string
		wantUtilization  float64
		wantWarning      string
	}{
		{
			name:             "under_80_percent",
			totalMonthlyCost: 700,
			budgetAmount:     1000,
			budgetPeriod:     "mo",
			wantUtilization:  70,
			wantWarning:      "",
		},
		{
			name:             "at_80_percent_approaching",
			totalMonthlyCost: 800,
			budgetAmount:     1000,
			budgetPeriod:     "mo",
			wantUtilization:  80,
			wantWarning:      "approaching",
		},
		{
			name:             "at_95_percent_approaching",
			totalMonthlyCost: 950,
			budgetAmount:     1000,
			budgetPeriod:     "mo",
			wantUtilization:  95,
			wantWarning:      "approaching",
		},
		{
			name:             "above_95_percent_exceeded",
			totalMonthlyCost: 960,
			budgetAmount:     1000,
			budgetPeriod:     "mo",
			wantUtilization:  96,
			wantWarning:      "exceeded",
		},
		{
			name:             "yearly_budget_converted",
			totalMonthlyCost: 450,
			budgetAmount:     6000,
			budgetPeriod:     "yr",
			wantUtilization:  90,
			wantWarning:      "approaching",
		},
		{
			name:             "no_budget",
			totalMonthlyCost: 500,
			budgetAmount:     0,
			wantUtilization:  0,
			wantWarning:      "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := &cost.CostReport{TotalMonthlyCost: tt.totalMonthlyCost}
			if tt.budgetAmount > 0 {
				cr.Budget = &cost.Budget{Amount: tt.budgetAmount, Period: tt.budgetPeriod}
			}
			plan := &resolver.Plan{Actions: []*state.PlanAction{
				{ID: "a1", NodeID: "service.api", Operation: "CREATE", NodeType: "SERVICE"},
			}}
			summary := enrichPlanActions(plan, cr, nil, newTestState(), nil)
			if summary.BudgetUtilization != tt.wantUtilization {
				t.Errorf("BudgetUtilization = %v, want %v", summary.BudgetUtilization, tt.wantUtilization)
			}
			if summary.BudgetWarning != tt.wantWarning {
				t.Errorf("BudgetWarning = %q, want %q", summary.BudgetWarning, tt.wantWarning)
			}
		})
	}
}

func TestAutoApproveWithinPolicy(t *testing.T) {
	tests := []struct {
		name        string
		costDelta   float64
		riskLevel   string
		maxCost     string
		maxRisk     string
		wantPending bool
	}{
		{
			name:        "within_both_bounds",
			costDelta:   50,
			maxCost:     "100",
			maxRisk:     "medium",
			wantPending: false,
		},
		{
			name:        "at_cost_boundary",
			costDelta:   100,
			maxCost:     "100",
			maxRisk:     "high",
			wantPending: false,
		},
		{
			name:        "risk_only_within",
			costDelta:   0,
			maxCost:     "",
			maxRisk:     "medium",
			wantPending: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			domain := &ir.DomainNode{
				AutoApprove: map[string]string{},
				Boundary:    map[string][]string{"approve": {"new_store"}},
			}
			if tt.maxCost != "" {
				domain.AutoApprove["max_cost_delta"] = tt.maxCost
			}
			if tt.maxRisk != "" {
				domain.AutoApprove["max_risk"] = tt.maxRisk
			}

			action := &state.PlanAction{
				ID: "a1", NodeID: "store.db", Operation: "CREATE", NodeType: "STORE",
				RequiresApproval: true,
			}
			plan := &resolver.Plan{Actions: []*state.PlanAction{action}}
			cr := &cost.CostReport{
				Estimates: []cost.CostEstimate{{NodeID: "store.db", MonthlyCost: tt.costDelta}},
			}

			summary := enrichPlanActions(plan, cr, nil, newTestState(), domain)
			if action.RequiresApproval != tt.wantPending {
				t.Errorf("RequiresApproval = %v, want %v", action.RequiresApproval, tt.wantPending)
			}
			if tt.wantPending && summary.PendingApproval == 0 {
				t.Error("PendingApproval should be > 0")
			}
			if !tt.wantPending && summary.PendingApproval != 0 {
				t.Errorf("PendingApproval = %d, want 0", summary.PendingApproval)
			}
		})
	}
}

func TestAutoApproveExceedsPolicy(t *testing.T) {
	tests := []struct {
		name       string
		operation  string
		nodeType   string
		createCost float64
		deleteCost float64
		maxCost    string
		maxRisk    string
	}{
		{
			name:       "cost_exceeds_create",
			operation:  "CREATE",
			nodeType:   "STORE",
			createCost: 200,
			maxCost:    "100",
			maxRisk:    "critical",
		},
		{
			name:       "risk_exceeds",
			operation:  "DELETE",
			nodeType:   "STORE",
			deleteCost: 10,
			maxCost:    "100",
			maxRisk:    "low",
		},
		{
			name:       "both_exceed",
			operation:  "CREATE",
			nodeType:   "STORE",
			createCost: 500,
			maxCost:    "100",
			maxRisk:    "low",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			domain := &ir.DomainNode{
				AutoApprove: map[string]string{},
			}
			if tt.maxCost != "" {
				domain.AutoApprove["max_cost_delta"] = tt.maxCost
			}
			if tt.maxRisk != "" {
				domain.AutoApprove["max_risk"] = tt.maxRisk
			}

			action := &state.PlanAction{
				ID: "a1", NodeID: "store.db", Operation: tt.operation, NodeType: tt.nodeType,
				RequiresApproval: true,
			}
			plan := &resolver.Plan{Actions: []*state.PlanAction{action}}
			cr := &cost.CostReport{
				Estimates:        []cost.CostEstimate{{NodeID: "store.db", MonthlyCost: tt.createCost}},
				TotalMonthlyCost: tt.createCost,
			}

			st := &state.State{
				Resources: map[string]*state.ResourceRecord{
					"store.db": {ResourceID: "store.db", EstimatedCost: tt.deleteCost},
				},
			}

			enrichPlanActions(plan, cr, nil, st, domain)
			if !action.RequiresApproval {
				t.Error("expected RequiresApproval to remain true when exceeding policy")
			}
		})
	}
}

func TestApprovalRequestPopulated(t *testing.T) {
	tests := []struct {
		name            string
		costDelta       float64
		aggregateRisk   string
		pendingCount    int
		wantCostDelta   string
		wantBlastRadius string
	}{
		{
			name:            "positive_delta",
			costDelta:       12.5,
			aggregateRisk:   "medium",
			pendingCount:    3,
			wantCostDelta:   "+$12.50/mo",
			wantBlastRadius: "3 actions, medium risk",
		},
		{
			name:            "negative_delta",
			costDelta:       -5,
			aggregateRisk:   "low",
			pendingCount:    1,
			wantCostDelta:   "-$5.00/mo",
			wantBlastRadius: "1 actions, low risk",
		},
		{
			name:            "zero_delta",
			costDelta:       0,
			aggregateRisk:   "high",
			pendingCount:    2,
			wantCostDelta:   "+$0.00/mo",
			wantBlastRadius: "2 actions, high risk",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := &PlanSummary{
				CostDelta:     tt.costDelta,
				AggregateRisk: tt.aggregateRisk,
			}
			gotDelta := formatCostDelta(summary)
			if gotDelta != tt.wantCostDelta {
				t.Errorf("formatCostDelta = %q, want %q", gotDelta, tt.wantCostDelta)
			}
			gotRadius := formatBlastRadius(summary, tt.pendingCount)
			if gotRadius != tt.wantBlastRadius {
				t.Errorf("formatBlastRadius = %q, want %q", gotRadius, tt.wantBlastRadius)
			}
		})
	}
}

func TestRiskAtOrBelow(t *testing.T) {
	tests := []struct {
		level     string
		threshold string
		want      bool
	}{
		{"low", "low", true},
		{"low", "medium", true},
		{"medium", "medium", true},
		{"high", "medium", false},
		{"critical", "high", false},
		{"critical", "critical", true},
		{"unknown", "medium", false},
		{"low", "unknown", false},
	}
	for _, tt := range tests {
		t.Run(tt.level+"_vs_"+tt.threshold, func(t *testing.T) {
			got := riskAtOrBelow(tt.level, tt.threshold)
			if got != tt.want {
				t.Errorf("riskAtOrBelow(%q, %q) = %v, want %v", tt.level, tt.threshold, got, tt.want)
			}
		})
	}
}

func TestFormatCostDeltaNilSummary(t *testing.T) {
	got := formatCostDelta(nil)
	if got != "$0.00/mo" {
		t.Errorf("formatCostDelta(nil) = %q, want %q", got, "$0.00/mo")
	}
}

func TestFormatBlastRadiusNilSummary(t *testing.T) {
	got := formatBlastRadius(nil, 5)
	if got != "5 actions" {
		t.Errorf("formatBlastRadius(nil, 5) = %q, want %q", got, "5 actions")
	}
}

func newTestState() *state.State {
	return &state.State{
		Resources: map[string]*state.ResourceRecord{},
	}
}

func TestAutoApproveParserIntegration(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantMap map[string]string
	}{
		{
			name: "auto_approve_parsed",
			input: `domain myapp {
  cloud = aws
  owner = team
  auto_approve {
    max_cost_delta = 100
    max_risk = medium
  }
}
service api {
  runtime = container
}
`,
			wantMap: map[string]string{"max_cost_delta": "100", "max_risk": "medium"},
		},
		{
			name: "no_auto_approve",
			input: `domain myapp {
  cloud = aws
  owner = team
}
service api {
  runtime = container
}
`,
			wantMap: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := buildTestGraph(t, tt.input)
			if tt.wantMap == nil {
				if g.Domain.AutoApprove != nil {
					t.Errorf("expected nil AutoApprove, got %v", g.Domain.AutoApprove)
				}
				return
			}
			if g.Domain.AutoApprove == nil {
				t.Fatal("expected AutoApprove to be set")
			}
			for k, want := range tt.wantMap {
				got, ok := g.Domain.AutoApprove[k]
				if !ok {
					t.Errorf("missing key %q in AutoApprove", k)
				} else if got != want {
					t.Errorf("AutoApprove[%q] = %q, want %q", k, got, want)
				}
			}
		})
	}
}

func buildTestGraph(t *testing.T, src string) *ir.Graph {
	t.Helper()
	tmp := t.TempDir()
	path := tmp + "/test.beecon"
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	g, err := parseAndBuildGraph(path, "")
	if err != nil {
		t.Fatalf("parseAndBuildGraph: %v", err)
	}
	return g
}
