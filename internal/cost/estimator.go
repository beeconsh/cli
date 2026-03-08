package cost

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/terracotta-ai/beecon/internal/classify"
	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/resolver"
	"github.com/terracotta-ai/beecon/internal/state"
)

// CostEstimate holds the estimated cost for a single resource.
type CostEstimate struct {
	NodeID       string
	NodeName     string
	ResourceType string
	InstanceType string
	MonthlyCost  float64
	IsEstimated  bool // true if from static table; false if unknown
}

// Alternative suggests a cheaper instance option.
type Alternative struct {
	NodeID           string
	NodeName         string
	CurrentInstance  string
	CurrentCost      float64
	SuggestedInstance string
	SuggestedCost    float64
	MonthlySavings   float64
}

// CostReport is the output of Evaluate().
type CostReport struct {
	TotalMonthlyCost float64
	Estimates        []CostEstimate
	Alternatives     []Alternative
	Warnings         []string
	BudgetExceeded   bool
	Budget           *Budget
}

// Evaluate computes cost estimates for a plan against the current graph and state.
func Evaluate(p *resolver.Plan, g *ir.Graph, st *state.State, budget *Budget) *CostReport {
	report := &CostReport{Budget: budget}
	if p == nil {
		return report
	}
	nodesByID := g.NodesByID()

	for _, action := range p.Actions {
		if action.Operation == "DELETE" || action.Operation == "FORBIDDEN" {
			continue
		}

		node, ok := nodesByID[action.NodeID]
		if !ok {
			continue
		}

		est := estimateNode(node)
		report.Estimates = append(report.Estimates, est)
		report.TotalMonthlyCost += est.MonthlyCost

		if !est.IsEstimated && est.MonthlyCost == 0 {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("%s: unknown instance type %q, cost not estimated", est.NodeID, est.InstanceType))
		}

		// Check for cheaper alternatives
		if est.InstanceType != "" {
			if alt, savings, ok := SuggestCheaper(est.InstanceType); ok {
				altPrice, _ := LookupInstancePrice(alt)
				report.Alternatives = append(report.Alternatives, Alternative{
					NodeID:            est.NodeID,
					NodeName:          est.NodeName,
					CurrentInstance:   est.InstanceType,
					CurrentCost:       est.MonthlyCost,
					SuggestedInstance: alt,
					SuggestedCost:     altPrice,
					MonthlySavings:    savings,
				})
			}
		}
	}

	if budget != nil {
		monthlyBudget := budget.MonthlyAmount()
		if report.TotalMonthlyCost > monthlyBudget {
			report.BudgetExceeded = true
		}
	}

	return report
}

// FormatDelta returns a human-readable cost delta string.
func FormatDelta(report *CostReport) string {
	if report == nil || report.TotalMonthlyCost == 0 {
		return "$0/mo"
	}
	return fmt.Sprintf("+$%.0f/mo (estimated)", report.TotalMonthlyCost)
}

func estimateNode(node ir.IntentNode) CostEstimate {
	target := classify.ClassifyNode(string(node.Type), node.Intent)
	instanceType := node.Intent["instance_type"]

	est := CostEstimate{
		NodeID:       node.ID,
		NodeName:     node.Name,
		ResourceType: target,
		InstanceType: instanceType,
	}

	// Try instance-based pricing first
	if instanceType != "" {
		if price, ok := LookupInstancePrice(instanceType); ok {
			est.MonthlyCost = price
			est.IsEstimated = true
			return est
		}
		// Unknown instance type
		return est
	}

	// Estimate by resource type
	switch target {
	case "rds", "rds_aurora_serverless":
		est.MonthlyCost = 200 // default RDS estimate
		est.IsEstimated = true
	case "elasticache":
		est.MonthlyCost = 50
		est.IsEstimated = true
	case "s3":
		est.MonthlyCost = 5
		est.IsEstimated = true
	case "ecs":
		est.MonthlyCost = estimateECS(node)
		est.IsEstimated = true
	case "lambda":
		est.MonthlyCost = 10 // typical low-traffic lambda
		est.IsEstimated = true
	case "alb":
		est.MonthlyCost = albBaseMonthlyCost
		est.IsEstimated = true
	case "sqs":
		est.MonthlyCost = 2
		est.IsEstimated = true
	case "sns":
		est.MonthlyCost = 1
		est.IsEstimated = true
	case "vpc":
		est.MonthlyCost = 0
		est.IsEstimated = true
	case "security_group":
		est.MonthlyCost = 0
		est.IsEstimated = true
	default:
		est.MonthlyCost = 40 // fallback for unknown types
		est.IsEstimated = true
	}

	return est
}

func estimateECS(node ir.IntentNode) float64 {
	vcpus := parseFloat(node.Intent["cpu"], 0.25)
	memGB := parseFloat(node.Intent["memory"], 0.5)
	count := parseInt(node.Intent["desired_count"], 1)

	// Convert CPU units (256 = 0.25 vCPU) if specified in AWS units
	if vcpus > 4 {
		vcpus = vcpus / 1024
	}
	// Convert memory in MiB to GB
	if memGB > 30 {
		memGB = memGB / 1024
	}

	return EstimateFargateCost(vcpus, memGB, count)
}

func parseFloat(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return v
}

func parseInt(s string, def int) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return v
}
