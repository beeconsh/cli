package compliance

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/terracotta-ai/beecon/internal/classify"
	"github.com/terracotta-ai/beecon/internal/ir"
)

// Enforce applies compliance framework rules to the intent graph.
// It mutates IntentNode.Intent in-place (fills missing defaults) and returns
// a report of mutations and violations.
//
// Multi-framework resolution: when multiple frameworks declare defaults for the
// same field, the strictest value wins (e.g., HIPAA's cmk beats SOC2's true for
// encryption). Defaults are collected across all frameworks first, then applied
// once, then validated — avoiding order-dependent conflicts.
func Enforce(g *ir.Graph) (*ComplianceReport, error) {
	if g.Domain == nil || len(g.Domain.Compliance) == 0 {
		return &ComplianceReport{}, nil
	}

	// Collect all applicable constraints from declared frameworks.
	var constraints []Constraint
	for _, fw := range g.Domain.Compliance {
		fw = strings.ToLower(strings.TrimSpace(fw))
		cs, ok := FrameworkConstraints[fw]
		if !ok {
			return nil, fmt.Errorf("unknown compliance framework %q (supported: hipaa, soc2)", fw)
		}
		constraints = append(constraints, cs...)
	}

	// Validate compliance_override fields against known rule fields.
	knownFields := collectKnownFields(constraints)

	report := &ComplianceReport{}

	for i := range g.Nodes {
		node := &g.Nodes[i]
		target := classify.ClassifyNode(string(node.Type), node.Intent)
		overrides := overrideSet(node.ComplianceOverrides)

		// Validate overrides reference known fields
		for field := range overrides {
			if !knownFields[field] {
				report.Violations = append(report.Violations, Violation{
					NodeID:   node.ID,
					Field:    field,
					Required: "valid compliance field",
					Reason:   fmt.Sprintf("compliance_override references unknown field %q; valid fields: %s", field, joinFields(knownFields)),
				})
			}
		}

		// Track overrides used for audit
		for _, field := range node.ComplianceOverrides {
			field = strings.TrimSpace(field)
			if knownFields[field] {
				report.Overrides = append(report.Overrides, Override{
					NodeID: node.ID,
					Field:  field,
				})
			}
		}

		// Phase 1: Collect strictest defaults across all frameworks for this node.
		// Key: field name → {value, framework, description}
		defaults := map[string]defaultEntry{}
		for _, c := range constraints {
			if !matchesTarget(c.Targets, target) {
				continue
			}
			for _, rule := range c.Rules {
				if overrides[rule.Field] || rule.DefaultValue == "" {
					continue
				}
				existing, has := defaults[rule.Field]
				if !has {
					defaults[rule.Field] = defaultEntry{
						value:     rule.DefaultValue,
						framework: c.Framework,
						reason:    rule.Description,
					}
				} else {
					// Pick the stricter value
					stricter := pickStricter(existing.value, rule.DefaultValue)
					if stricter != existing.value {
						defaults[rule.Field] = defaultEntry{
							value:     stricter,
							framework: c.Framework,
							reason:    rule.Description,
						}
					}
				}
			}
		}

		// Phase 2: Apply collected defaults for missing fields.
		for field, def := range defaults {
			if _, exists := node.Intent[field]; !exists {
				node.Intent[field] = def.value
				report.Mutations = append(report.Mutations, Mutation{
					NodeID:    node.ID,
					Field:     field,
					Value:     def.value,
					Framework: def.framework,
					Reason:    def.reason,
				})
			}
		}

		// Phase 3: Validate all rules against current state (user-set + auto-defaults).
		for _, c := range constraints {
			if !matchesTarget(c.Targets, target) {
				continue
			}
			for _, rule := range c.Rules {
				if overrides[rule.Field] {
					continue
				}
				current, exists := node.Intent[rule.Field]
				if !exists && rule.Required {
					report.Violations = append(report.Violations, Violation{
						NodeID:    node.ID,
						Field:     rule.Field,
						Framework: c.Framework,
						Required:  "present",
						Reason:    rule.Description,
					})
					continue
				}
				if exists && rule.MinValue != "" {
					if violatesMin(current, rule.MinValue) {
						report.Violations = append(report.Violations, Violation{
							NodeID:    node.ID,
							Field:     rule.Field,
							Value:     current,
							Framework: c.Framework,
							Required:  fmt.Sprintf(">= %s", rule.MinValue),
							Reason:    rule.Description,
						})
					}
					continue
				}
				if exists && rule.DefaultValue != "" {
					if violatesBoolean(current, rule.DefaultValue) {
						report.Violations = append(report.Violations, Violation{
							NodeID:    node.ID,
							Field:     rule.Field,
							Value:     current,
							Framework: c.Framework,
							Required:  rule.DefaultValue,
							Reason:    rule.Description,
						})
					}
				}
			}
		}
	}

	if report.HasViolations() {
		var msgs []string
		for _, v := range report.Violations {
			msgs = append(msgs, fmt.Sprintf("  %s: %s (field %q = %q, required %s)",
				v.NodeID, v.Reason, v.Field, v.Value, v.Required))
		}
		return report, fmt.Errorf("compliance violations:\n%s", strings.Join(msgs, "\n"))
	}

	return report, nil
}

// defaultEntry tracks a pending default value and its source.
type defaultEntry struct {
	value     string
	framework string
	reason    string
}

// pickStricter returns the stricter of two default values.
// Strictness rules: "cmk" > "true" > any numeric (higher wins).
func pickStricter(a, b string) string {
	if a == "cmk" || b == "cmk" {
		return "cmk"
	}
	// For numeric values, pick the higher one
	ai, aerr := strconv.Atoi(a)
	bi, berr := strconv.Atoi(b)
	if aerr == nil && berr == nil {
		if bi > ai {
			return b
		}
		return a
	}
	// For non-numeric (e.g., "true"), keep first
	return a
}

// collectKnownFields gathers all unique field names from all constraints.
func collectKnownFields(constraints []Constraint) map[string]bool {
	fields := make(map[string]bool)
	for _, c := range constraints {
		for _, r := range c.Rules {
			fields[r.Field] = true
		}
	}
	return fields
}

func joinFields(fields map[string]bool) string {
	names := make([]string, 0, len(fields))
	for f := range fields {
		names = append(names, f)
	}
	return strings.Join(names, ", ")
}

func matchesTarget(targets []string, target string) bool {
	if len(targets) == 0 {
		return true
	}
	for _, t := range targets {
		if t == target {
			return true
		}
	}
	return false
}

func overrideSet(overrides []string) map[string]bool {
	m := make(map[string]bool, len(overrides))
	for _, o := range overrides {
		m[strings.TrimSpace(o)] = true
	}
	return m
}

func violatesMin(current, min string) bool {
	c, err1 := strconv.Atoi(current)
	m, err2 := strconv.Atoi(min)
	if err2 != nil {
		return false // invalid min spec — not a violation
	}
	if err1 != nil {
		return true // non-numeric current value cannot satisfy a numeric minimum
	}
	return c < m
}

func violatesBoolean(current, required string) bool {
	if required == "true" && strings.ToLower(current) == "false" {
		return true
	}
	if required == "cmk" && strings.ToLower(current) != "cmk" {
		return true
	}
	return false
}
