package wiring

import (
	"fmt"

	"github.com/terracotta-ai/beecon/internal/classify"
)


// SGRule represents an inferred security group ingress rule.
type SGRule struct {
	SourceNodeID string
	TargetNodeID string
	Port         int
	Protocol     string
	Description  string
}

// InferSGRules generates security group rules for a dependency between two
// VPC-resident resources. Returns nil if either resource is not VPC-resident.
func InferSGRules(sourceNodeID, targetNodeID, sourceTarget, targetTarget string, targetIntent map[string]string) []SGRule {
	if !classify.IsVPCResident(sourceTarget) || !classify.IsVPCResident(targetTarget) {
		return nil
	}

	engine := classify.FieldVal(targetIntent, "engine")
	port := classify.DefaultPortForEngine(engine)
	if port == 0 {
		port = classify.DefaultPort(targetTarget)
	}

	// For ECS targets, use the container_port if specified
	if targetTarget == "ecs" {
		if cp := classify.FieldVal(targetIntent, "container_port"); cp != "" {
			if p := parsePort(cp); p > 0 {
				port = p
			}
		}
	}

	if port == 0 {
		return nil
	}

	return []SGRule{
		{
			SourceNodeID: sourceNodeID,
			TargetNodeID: targetNodeID,
			Port:         port,
			Protocol:     "tcp",
			Description:  fmt.Sprintf("Allow %s → %s on port %d", sourceNodeID, targetNodeID, port),
		},
	}
}

// InferGCPFirewallRules generates firewall rules for a dependency between two
// GCP VPC-resident resources. Returns nil if either resource is not VPC-resident.
func InferGCPFirewallRules(sourceNodeID, targetNodeID, sourceTarget, targetTarget string, targetIntent map[string]string) []SGRule {
	if !classify.IsGCPVPCResident(sourceTarget) || !classify.IsGCPVPCResident(targetTarget) {
		return nil
	}

	engine := classify.FieldVal(targetIntent, "engine")
	port := classify.GCPDefaultPortForEngine(engine)
	if port == 0 {
		port = classify.GCPDefaultPort(targetTarget)
	}

	// For Cloud Run targets, use the container_port if specified
	if targetTarget == "cloud_run" {
		if cp := classify.FieldVal(targetIntent, "container_port"); cp != "" {
			if p := parsePort(cp); p > 0 {
				port = p
			}
		}
	}

	if port == 0 {
		return nil
	}

	return []SGRule{
		{
			SourceNodeID: sourceNodeID,
			TargetNodeID: targetNodeID,
			Port:         port,
			Protocol:     "tcp",
			Description:  fmt.Sprintf("Allow %s → %s on port %d", sourceNodeID, targetNodeID, port),
		},
	}
}

func parsePort(s string) int {
	var p int
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0
		}
		p = p*10 + int(ch-'0')
		if p > 65535 {
			return 0
		}
	}
	return p
}
