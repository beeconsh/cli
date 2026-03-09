package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ErrorCategory classifies errors for agent consumption.
type ErrorCategory string

const (
	ErrCatValidation  ErrorCategory = "validation"   // Invalid input, malformed beacon file
	ErrCatAuth        ErrorCategory = "auth"          // Authentication or authorization failure
	ErrCatNotFound    ErrorCategory = "not_found"     // Resource, file, or state not found
	ErrCatConflict    ErrorCategory = "conflict"      // State conflict, resource already exists
	ErrCatQuota       ErrorCategory = "quota"         // Rate limit or quota exceeded
	ErrCatTransient   ErrorCategory = "transient"     // Temporary failure, safe to retry
	ErrCatProvider    ErrorCategory = "provider"      // Cloud provider API error
	ErrCatInternal    ErrorCategory = "internal"      // Unexpected internal error
	ErrCatPermission  ErrorCategory = "permission"    // Operation not permitted (approval, policy)
)

// ErrorCode provides machine-parseable error identifiers.
type ErrorCode string

const (
	// Validation errors
	CodeInvalidBeacon     ErrorCode = "INVALID_BEACON"
	CodeInvalidFlag       ErrorCode = "INVALID_FLAG"
	CodeMissingRequired   ErrorCode = "MISSING_REQUIRED"
	CodeInvalidProfile    ErrorCode = "INVALID_PROFILE"

	// State errors
	CodeStateNotFound     ErrorCode = "STATE_NOT_FOUND"
	CodeResourceNotFound  ErrorCode = "RESOURCE_NOT_FOUND"
	CodeRunNotFound       ErrorCode = "RUN_NOT_FOUND"
	CodeApprovalNotFound  ErrorCode = "APPROVAL_NOT_FOUND"

	// Provider errors
	CodeProviderError     ErrorCode = "PROVIDER_ERROR"
	CodeProviderTransient ErrorCode = "PROVIDER_TRANSIENT"
	CodeProviderAuth      ErrorCode = "PROVIDER_AUTH"
	CodeProviderQuota     ErrorCode = "PROVIDER_QUOTA"

	// Permission errors
	CodeApprovalRequired  ErrorCode = "APPROVAL_REQUIRED"
	CodePolicyDenied      ErrorCode = "POLICY_DENIED"

	// Apply errors
	CodeApplyFailed       ErrorCode = "APPLY_FAILED"
	CodePartialApply      ErrorCode = "PARTIAL_APPLY"

	// Generic
	CodeInternal          ErrorCode = "INTERNAL_ERROR"
	CodeUnknown           ErrorCode = "UNKNOWN_ERROR"
)

// CLIError is a structured error response for --format json output.
// It enables agents to programmatically classify and recover from errors.
type CLIError struct {
	Code       ErrorCode     `json:"code"`
	Category   ErrorCategory `json:"category"`
	Message    string        `json:"message"`
	Detail     string        `json:"detail,omitempty"`
	Recovery   string        `json:"recovery,omitempty"`    // Suggested remediation
	RetrySafe  bool          `json:"retry_safe"`            // True if the operation can be safely retried
	Command    string        `json:"command,omitempty"`      // The CLI command that failed
}

// Error implements the error interface.
func (e *CLIError) Error() string {
	return e.Message
}

// WriteJSONError writes a CLIError as JSON to the writer.
func WriteJSONError(w io.Writer, e *CLIError) error {
	wrapper := struct {
		Error *CLIError `json:"error"`
	}{Error: e}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(wrapper)
}

// NewCLIError creates a CLIError from a raw error, attempting to classify it.
func NewCLIError(cmd string, err error) *CLIError {
	if err == nil {
		return nil
	}

	msg := err.Error()
	ce := &CLIError{
		Code:     CodeUnknown,
		Category: ErrCatInternal,
		Message:  msg,
		Command:  cmd,
	}

	// Classify based on error message patterns
	classifyError(ce, msg)
	return ce
}

// classifyError sets the code, category, recovery, and retry_safe fields
// based on error message content.
func classifyError(ce *CLIError, msg string) {
	lower := strings.ToLower(msg)

	switch {
	// Validation errors
	case strings.Contains(lower, "parse error") ||
		strings.Contains(lower, "syntax error") ||
		strings.Contains(lower, "validation failed"):
		ce.Code = CodeInvalidBeacon
		ce.Category = ErrCatValidation
		ce.Recovery = "Fix the beacon file syntax and retry"

	case strings.Contains(lower, "unsupported format") ||
		strings.Contains(lower, "invalid flag"):
		ce.Code = CodeInvalidFlag
		ce.Category = ErrCatValidation
		ce.Recovery = "Check command usage with --help"

	case strings.Contains(lower, "missing required") ||
		strings.Contains(lower, "is required"):
		ce.Code = CodeMissingRequired
		ce.Category = ErrCatValidation
		ce.Recovery = "Provide the required field or flag"

	case strings.Contains(lower, "profile") && strings.Contains(lower, "not found"):
		ce.Code = CodeInvalidProfile
		ce.Category = ErrCatNotFound
		ce.Recovery = "Check available profiles with beecon status"

	// State/resource not found
	case strings.Contains(lower, "no state found") ||
		strings.Contains(lower, "state file") && strings.Contains(lower, "not found"):
		ce.Code = CodeStateNotFound
		ce.Category = ErrCatNotFound
		ce.Recovery = "Run beecon apply first to create initial state"

	case strings.Contains(lower, "run") && strings.Contains(lower, "not found"):
		ce.Code = CodeRunNotFound
		ce.Category = ErrCatNotFound
		ce.Recovery = "Check available runs with beecon list-runs"

	case strings.Contains(lower, "approval") && strings.Contains(lower, "not found"):
		ce.Code = CodeApprovalNotFound
		ce.Category = ErrCatNotFound
		ce.Recovery = "Check pending approvals with beecon list-approvals"

	case strings.Contains(lower, "not found") || strings.Contains(lower, "does not exist"):
		ce.Code = CodeResourceNotFound
		ce.Category = ErrCatNotFound
		ce.Recovery = "Run beecon refresh to sync state, then retry"

	// Auth errors
	case strings.Contains(lower, "credentials") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "access denied") ||
		strings.Contains(lower, "authentication"):
		ce.Code = CodeProviderAuth
		ce.Category = ErrCatAuth
		ce.Recovery = "Check cloud provider credentials and permissions"

	// Quota/rate limit
	case strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "quota") ||
		strings.Contains(lower, "too many requests") ||
		strings.Contains(lower, "throttl"):
		ce.Code = CodeProviderQuota
		ce.Category = ErrCatQuota
		ce.RetrySafe = true
		ce.Recovery = "Wait and retry; consider requesting a quota increase"

	// Transient errors
	case strings.Contains(lower, "temporarily unavailable") ||
		strings.Contains(lower, "timeout") ||
		strings.Contains(lower, "deadline exceeded") ||
		strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "503") ||
		strings.Contains(lower, "502"):
		ce.Code = CodeProviderTransient
		ce.Category = ErrCatTransient
		ce.RetrySafe = true
		ce.Recovery = "Retry the operation; the error is likely transient"

	// Conflict
	case strings.Contains(lower, "already exists") ||
		strings.Contains(lower, "conflict") ||
		strings.Contains(lower, "duplicate"):
		ce.Code = CodeProviderError
		ce.Category = ErrCatConflict
		ce.Recovery = "Run beecon refresh to sync state, then re-plan"

	// Partial apply
	case strings.Contains(lower, "partial"):
		ce.Code = CodePartialApply
		ce.Category = ErrCatProvider
		ce.Recovery = "Run beecon refresh to capture partial state, then re-plan and re-apply"

	// Approval/permission
	case strings.Contains(lower, "approval required") ||
		strings.Contains(lower, "pending approval"):
		ce.Code = CodeApprovalRequired
		ce.Category = ErrCatPermission
		ce.Recovery = "Approve the pending run with beecon approve <run-id>"

	case strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "policy") && strings.Contains(lower, "denied"):
		ce.Code = CodePolicyDenied
		ce.Category = ErrCatPermission
		ce.Recovery = "The operation is blocked by a boundary policy"

	// Provider errors (catch-all for cloud errors)
	case strings.Contains(lower, "aws") ||
		strings.Contains(lower, "gcp") ||
		strings.Contains(lower, "azure"):
		ce.Code = CodeProviderError
		ce.Category = ErrCatProvider
		ce.Recovery = "Check the error detail and cloud provider console"

	// Apply failure
	case strings.Contains(lower, "apply failed") ||
		strings.Contains(lower, "execution failed"):
		ce.Code = CodeApplyFailed
		ce.Category = ErrCatProvider
		ce.Recovery = "Run beecon refresh to sync state, then re-plan"
	}
}

// FormatError returns a human-readable error string for text output.
func FormatError(ce *CLIError) string {
	var b strings.Builder
	fmt.Fprintf(&b, "error: %s", ce.Message)
	if ce.Recovery != "" {
		fmt.Fprintf(&b, "\n  recovery: %s", ce.Recovery)
	}
	return b.String()
}
