package witness

import "strings"

// CandidateAction describes an infra action proposed from telemetry.
type CandidateAction struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// EvaluateBreach generates candidate resolver actions for a metric breach.
func EvaluateBreach(metric, observed, threshold string) []CandidateAction {
	m := strings.ToLower(metric)
	switch {
	case strings.Contains(m, "latency"):
		return []CandidateAction{
			{Action: "scale_out", Reason: "latency breach suggests capacity pressure"},
			{Action: "upgrade_instance", Reason: "higher compute tier can reduce p95 latency"},
			{Action: "add_cache", Reason: "cache can reduce backend round trips"},
		}
	case strings.Contains(m, "uptime"):
		return []CandidateAction{
			{Action: "increase_replicas", Reason: "availability breach indicates insufficient redundancy"},
			{Action: "multi_az", Reason: "single-zone failure risk detected"},
		}
	default:
		return []CandidateAction{{Action: "re_evaluate", Reason: "generic performance breach requires resolver diff"}}
	}
}
