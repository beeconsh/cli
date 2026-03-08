package compliance

// Rule represents a single compliance enforcement rule.
type Rule struct {
	Field        string // intent field to check/set (e.g., "backup_retention")
	MinValue     string // minimum acceptable value (numeric comparison)
	DefaultValue string // value to set if field is missing
	Required     bool   // if true, field must be present (no default can fix it)
	Description  string // human-readable explanation
}

// Constraint is a collection of rules scoped to specific resource targets.
type Constraint struct {
	Framework string   // e.g., "hipaa", "soc2"
	Targets   []string // AWS target types this applies to (empty = all)
	Rules     []Rule
}

// Mutation records an auto-applied compliance default.
type Mutation struct {
	NodeID    string
	Field     string
	Value     string
	Framework string
	Reason    string
}

// Violation records an explicit user setting that violates compliance.
type Violation struct {
	NodeID    string
	Field     string
	Value     string
	Framework string
	Required  string
	Reason    string
}

// Override records a compliance rule that was explicitly bypassed by the user.
type Override struct {
	NodeID string
	Field  string
}

// ComplianceReport is the output of Enforce().
type ComplianceReport struct {
	Mutations  []Mutation
	Violations []Violation
	Overrides  []Override
}

// HasViolations returns true if the report contains blocking violations.
func (r *ComplianceReport) HasViolations() bool {
	return len(r.Violations) > 0
}
