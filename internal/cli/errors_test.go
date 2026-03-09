package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
)

func TestNewCLIError_Classification(t *testing.T) {
	cases := []struct {
		name     string
		errMsg   string
		wantCode ErrorCode
		wantCat  ErrorCategory
		wantSafe bool
	}{
		{"parse error", "parse error at line 5: unexpected token", CodeInvalidBeacon, ErrCatValidation, false},
		{"syntax error", "syntax error in infra.beecon", CodeInvalidBeacon, ErrCatValidation, false},
		{"missing required", "field 'engine' is required", CodeMissingRequired, ErrCatValidation, false},
		{"profile not found", "profile 'staging' not found", CodeInvalidProfile, ErrCatNotFound, false},
		{"state not found", "no state found for project", CodeStateNotFound, ErrCatNotFound, false},
		{"run not found", "run abc-123 not found", CodeRunNotFound, ErrCatNotFound, false},
		{"approval not found", "approval xyz not found", CodeApprovalNotFound, ErrCatNotFound, false},
		{"resource not found", "resource does not exist", CodeResourceNotFound, ErrCatNotFound, false},
		{"credentials", "invalid credentials for AWS", CodeProviderAuth, ErrCatAuth, false},
		{"access denied", "access denied: insufficient permissions", CodeProviderAuth, ErrCatAuth, false},
		{"rate limit", "rate limit exceeded, try again later", CodeProviderQuota, ErrCatQuota, true},
		{"quota", "quota exceeded for gcp compute", CodeProviderQuota, ErrCatQuota, true},
		{"timeout", "context deadline exceeded", CodeProviderTransient, ErrCatTransient, true},
		{"503 error", "service returned 503", CodeProviderTransient, ErrCatTransient, true},
		{"already exists", "bucket already exists", CodeProviderError, ErrCatConflict, false},
		{"partial apply", "partial apply: 2 of 3 resources created", CodePartialApply, ErrCatProvider, false},
		{"approval required", "approval required for destructive actions", CodeApprovalRequired, ErrCatPermission, false},
		{"policy denied", "policy denied: DELETE not allowed", CodePolicyDenied, ErrCatPermission, false},
		{"aws error", "aws rds: instance not available", CodeProviderError, ErrCatProvider, false},
		{"gcp error", "gcp cloud sql: creation failed", CodeProviderError, ErrCatProvider, false},
		{"unknown", "something went wrong", CodeUnknown, ErrCatInternal, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ce := NewCLIError("test-cmd", errors.New(tc.errMsg))
			if ce.Code != tc.wantCode {
				t.Errorf("code: got %q, want %q", ce.Code, tc.wantCode)
			}
			if ce.Category != tc.wantCat {
				t.Errorf("category: got %q, want %q", ce.Category, tc.wantCat)
			}
			if ce.RetrySafe != tc.wantSafe {
				t.Errorf("retry_safe: got %v, want %v", ce.RetrySafe, tc.wantSafe)
			}
			if ce.Command != "test-cmd" {
				t.Errorf("command: got %q, want %q", ce.Command, "test-cmd")
			}
			if ce.Message != tc.errMsg {
				t.Errorf("message: got %q, want %q", ce.Message, tc.errMsg)
			}
		})
	}
}

func TestCLIError_ErrorInterface(t *testing.T) {
	ce := &CLIError{Message: "test error"}
	if ce.Error() != "test error" {
		t.Errorf("Error() = %q, want %q", ce.Error(), "test error")
	}
}

func TestWriteJSONError(t *testing.T) {
	ce := &CLIError{
		Code:      CodeInvalidBeacon,
		Category:  ErrCatValidation,
		Message:   "parse error at line 5",
		Recovery:  "Fix the beacon file syntax and retry",
		RetrySafe: false,
		Command:   "validate",
	}

	var buf bytes.Buffer
	if err := WriteJSONError(&buf, ce); err != nil {
		t.Fatalf("WriteJSONError: %v", err)
	}

	var result struct {
		Error struct {
			Code      string `json:"code"`
			Category  string `json:"category"`
			Message   string `json:"message"`
			Recovery  string `json:"recovery"`
			RetrySafe bool   `json:"retry_safe"`
			Command   string `json:"command"`
		} `json:"error"`
	}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("JSON unmarshal: %v", err)
	}

	if result.Error.Code != "INVALID_BEACON" {
		t.Errorf("code: got %q, want INVALID_BEACON", result.Error.Code)
	}
	if result.Error.Category != "validation" {
		t.Errorf("category: got %q, want validation", result.Error.Category)
	}
	if result.Error.Recovery == "" {
		t.Error("expected non-empty recovery")
	}
	if result.Error.Command != "validate" {
		t.Errorf("command: got %q, want validate", result.Error.Command)
	}
}

func TestFormatError(t *testing.T) {
	ce := &CLIError{
		Message:  "parse error at line 5",
		Recovery: "Fix the beacon file syntax and retry",
	}
	formatted := FormatError(ce)
	if formatted == "" {
		t.Error("expected non-empty formatted error")
	}
	if !bytes.Contains([]byte(formatted), []byte("recovery:")) {
		t.Error("expected recovery hint in formatted output")
	}
}

func TestNewCLIError_Nil(t *testing.T) {
	ce := NewCLIError("cmd", nil)
	if ce != nil {
		t.Error("expected nil for nil error")
	}
}

func TestCLIError_RecoveryAlwaysSet(t *testing.T) {
	// Every classified error should have a recovery hint
	testErrors := []string{
		"parse error in file",
		"credentials invalid",
		"rate limit exceeded",
		"context deadline exceeded",
		"bucket already exists",
		"approval required",
	}
	for _, msg := range testErrors {
		ce := NewCLIError("test", errors.New(msg))
		if ce.Recovery == "" {
			t.Errorf("error %q should have a recovery hint", msg)
		}
	}
}
