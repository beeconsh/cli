package wiring

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/terracotta-ai/beecon/internal/classify"
	"github.com/terracotta-ai/beecon/internal/ir"
)

// WiringResult contains artifacts produced by WireGraph.
type WiringResult struct {
	InferredEnvVars   map[string]map[string]string // nodeID → env vars added
	InferredPolicies  map[string]string            // nodeID → inline policy JSON
	InferredSGRules   []SGRule
	Warnings          []string                     // non-fatal warnings surfaced during wiring
}

// WireGraph infers IAM policies, environment variables, and security group rules
// from dependency declarations in the intent graph. It mutates IntentNode.Env
// and IntentNode.Intent in-place.
func WireGraph(g *ir.Graph) (*WiringResult, error) {
	result := &WiringResult{
		InferredEnvVars:  make(map[string]map[string]string),
		InferredPolicies: make(map[string]string),
	}

	nodeByName := make(map[string]*ir.IntentNode, len(g.Nodes))
	for i := range g.Nodes {
		nodeByName[g.Nodes[i].Name] = &g.Nodes[i]
	}

	for i := range g.Nodes {
		node := &g.Nodes[i]
		if len(node.Needs) == 0 {
			continue
		}

		sourceTarget := classify.ClassifyNode(string(node.Type), node.Intent)
		var allActions []string

		for _, dep := range node.Needs {
			target, ok := nodeByName[dep.Target]
			if !ok {
				return nil, fmt.Errorf("node %q declares dependency on %q but no such node exists in the graph", node.Name, dep.Target)
			}

			targetTarget := classify.ClassifyNode(string(target.Type), target.Intent)

			// Validate and normalize mode
			mode, err := NormalizeMode(dep.Mode)
			if err != nil {
				return nil, fmt.Errorf("node %q needs %q: %w", node.Name, dep.Target, err)
			}

			if !IsValidMode(targetTarget, mode) {
				return nil, fmt.Errorf("node %q: invalid mode %q for %s target %q",
					node.Name, mode, targetTarget, dep.Target)
			}

			if mode == ModeAdmin {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("node %q: admin mode on %q grants wildcard IAM actions; review before deploying", node.Name, dep.Target))
			}

			// Infer IAM actions
			actions, err := IAMActionsFor(targetTarget, mode)
			if err == nil {
				allActions = append(allActions, actions...)
			} else if targetTarget != "" {
				// Known target type with no IAM mapping — surface as warning
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("node %q: no IAM actions defined for %s with mode %s; service may lack permissions", node.Name, targetTarget, mode))
			}

			// Infer env vars (only if not already explicitly set)
			envVars := InferEnvVars(dep.Target, targetTarget, target.Intent)
			for k, v := range envVars.Vars {
				if _, exists := node.Env[k]; !exists {
					node.Env[k] = v
					if result.InferredEnvVars[node.ID] == nil {
						result.InferredEnvVars[node.ID] = make(map[string]string)
					}
					result.InferredEnvVars[node.ID][k] = v
				}
			}

			// Infer SG rules
			sgRules := InferSGRules(node.ID, target.ID, sourceTarget, targetTarget, target.Intent)
			result.InferredSGRules = append(result.InferredSGRules, sgRules...)
		}

		// Build inline policy from collected IAM actions
		if len(allActions) > 0 {
			sort.Strings(allActions)
			allActions = dedup(allActions)
			policy := buildInlinePolicy(node.Name, allActions)
			policyJSON, err := json.Marshal(policy)
			if err != nil {
				return nil, fmt.Errorf("marshal inline policy for %s: %w", node.Name, err)
			}
			node.Intent["inline_policy"] = string(policyJSON)
			result.InferredPolicies[node.ID] = string(policyJSON)
			result.Warnings = append(result.Warnings,
				fmt.Sprintf("node %q: IAM policy uses Resource \"*\"; scope to specific ARNs in production", node.Name))
		}
	}

	// Inject inferred SG rules into security_group NETWORK nodes.
	// Use graph edges to scope rules: only inject into the SG that a related
	// node (source or target of the rule) depends on. If no edge-based match
	// is found, fall back to injecting into all SG nodes.
	if len(result.InferredSGRules) > 0 {
		sgNodesByID := make(map[string]*ir.IntentNode)
		for i := range g.Nodes {
			if g.Nodes[i].Type == ir.NodeNetwork && g.Nodes[i].Intent["topology"] == "security_group" {
				sgNodesByID[g.Nodes[i].ID] = &g.Nodes[i]
			}
		}
		if len(sgNodesByID) > 0 {
			// Build a mapping: nodeID → set of security_group IDs it depends on via graph edges.
			nodeToSGs := make(map[string]map[string]bool)
			for _, edge := range g.Edges {
				if _, ok := sgNodesByID[edge.From]; ok {
					// edge.From is an SG, edge.To depends on it
					if nodeToSGs[edge.To] == nil {
						nodeToSGs[edge.To] = make(map[string]bool)
					}
					nodeToSGs[edge.To][edge.From] = true
				}
			}

			// Track which SG nodes received at least one scoped rule.
			injected := make(map[string][]string) // sgNodeID → rule strings
			unscoped := false

			for _, r := range result.InferredSGRules {
				ruleStr := fmt.Sprintf("%s:%d:0.0.0.0/0", r.Protocol, r.Port)

				// Find SGs associated with either source or target of the rule.
				matched := make(map[string]bool)
				for _, nid := range []string{r.SourceNodeID, r.TargetNodeID} {
					for sgID := range nodeToSGs[nid] {
						matched[sgID] = true
					}
				}

				if len(matched) > 0 {
					for sgID := range matched {
						injected[sgID] = append(injected[sgID], ruleStr)
					}
				} else {
					// No edge-based match — fall back to all SG nodes.
					unscoped = true
					for sgID := range sgNodesByID {
						injected[sgID] = append(injected[sgID], ruleStr)
					}
				}
			}

			for sgID, rules := range injected {
				sgNode := sgNodesByID[sgID]
				inferred := strings.Join(rules, ",")
				existing := sgNode.Intent["ingress"]
				if existing != "" {
					sgNode.Intent["ingress"] = existing + "," + inferred
				} else {
					sgNode.Intent["ingress"] = inferred
				}
			}

			if unscoped {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("injected %d inferred SG ingress rule(s) into all security_group nodes (no edge-based scoping); scope to specific CIDRs in production", len(result.InferredSGRules)))
			} else {
				result.Warnings = append(result.Warnings,
					fmt.Sprintf("injected %d inferred SG ingress rule(s) scoped by graph edges using 0.0.0.0/0; scope to specific CIDRs in production", len(result.InferredSGRules)))
			}
		}
	}

	return result, nil
}

// inlinePolicy represents an AWS IAM inline policy document.
type inlinePolicy struct {
	Version   string            `json:"Version"`
	Statement []policyStatement `json:"Statement"`
}

type policyStatement struct {
	Effect   string   `json:"Effect"`
	Action   []string `json:"Action"`
	Resource string   `json:"Resource"`
}

func buildInlinePolicy(nodeName string, actions []string) inlinePolicy {
	return inlinePolicy{
		Version: "2012-10-17",
		Statement: []policyStatement{
			{
				Effect:   "Allow",
				Action:   actions,
				Resource: "*", // TODO: scope to specific ARNs from state records
			},
		},
	}
}

func dedup(sorted []string) []string {
	if len(sorted) == 0 {
		return sorted
	}
	out := []string{sorted[0]}
	for _, s := range sorted[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// WiringMetadata tracks what was inferred for a resource (informational).
type WiringMetadata struct {
	InferredEnvVars  map[string]string `json:"inferred_env_vars,omitempty"`
	InferredPolicy   string            `json:"inferred_policy,omitempty"`
	InferredSGRules  []string          `json:"inferred_sg_rules,omitempty"`
}

// BuildMetadata constructs WiringMetadata for a node from WiringResult.
func BuildMetadata(nodeID string, result *WiringResult) *WiringMetadata {
	if result == nil {
		return nil
	}
	m := &WiringMetadata{}
	if envs, ok := result.InferredEnvVars[nodeID]; ok {
		m.InferredEnvVars = envs
	}
	if policy, ok := result.InferredPolicies[nodeID]; ok {
		m.InferredPolicy = policy
	}
	for _, sg := range result.InferredSGRules {
		if sg.SourceNodeID == nodeID || sg.TargetNodeID == nodeID {
			m.InferredSGRules = append(m.InferredSGRules, sg.Description)
		}
	}
	if len(m.InferredEnvVars) == 0 && m.InferredPolicy == "" && len(m.InferredSGRules) == 0 {
		return nil
	}
	return m
}

// FormatSGRules returns human-readable SG rule descriptions.
func FormatSGRules(rules []SGRule) []string {
	out := make([]string, len(rules))
	for i, r := range rules {
		out[i] = fmt.Sprintf("%s → %s (%s/%d): %s",
			r.SourceNodeID, r.TargetNodeID,
			r.Protocol, r.Port,
			r.Description)
	}
	return out
}

