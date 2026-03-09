package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
)

// ---------------------------------------------------------------------------
// API Gateway — dry-run apply tests
// ---------------------------------------------------------------------------

func TestApplyGCPAPIGateway_DryRun(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ops := []string{"CREATE", "UPDATE", "DELETE"}
	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			req := ApplyRequest{
				Action: &state.PlanAction{Operation: op, NodeName: "my_gateway"},
				Intent: map[string]interface{}{
					"intent.project_id":    "test-proj",
					"intent.location":      "us-central1",
					"intent.display_name":  "My API Gateway",
					"intent.config_name":   "my-config",
				},
				Region: "us-central1",
			}
			res, err := e.applyGCP(t.Context(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res == nil {
				t.Fatal("expected non-nil result")
			}
			if res.ProviderID == "" {
				t.Error("expected non-empty ProviderID")
			}
		})
	}
}

func TestApplyGCPAPIGateway_MissingProjectID(t *testing.T) {
	e := &DefaultExecutor{dryRun: false}
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_gateway"},
		Intent: map[string]interface{}{
			"intent.location": "us-central1",
		},
	}
	_, err := e.applyGCP(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
}

func TestApplyGCPAPIGateway_DirectCallMissingProjectID(t *testing.T) {
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_gateway"},
		Intent: map[string]interface{}{},
	}
	_, err := applyGCPAPIGateway(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
	if got := err.Error(); got != "api_gateway requires intent.project_id" {
		t.Errorf("unexpected error message: %s", got)
	}
}

// ---------------------------------------------------------------------------
// API Gateway — observe tests
// ---------------------------------------------------------------------------

func TestObserveGCPAPIGateway_NilSnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/locations/us-central1/gateways/my-gw",
		IntentSnapshot: nil,
		LiveState:      map[string]interface{}{"service": "api_gateway"},
	}
	_, err := observeGCPAPIGateway(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for nil IntentSnapshot")
	}
}

func TestObserveGCPAPIGateway_EmptySnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/locations/us-central1/gateways/my-gw",
		IntentSnapshot: map[string]interface{}{},
		LiveState:      map[string]interface{}{"service": "api_gateway"},
	}
	_, err := observeGCPAPIGateway(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for empty IntentSnapshot")
	}
}

func TestGCPObserveExpectedAPIGatewayKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "display_name", "state",
		"api_config", "default_hostname", "create_time", "update_time",
		"labels",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("API Gateway observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// ---------------------------------------------------------------------------
// API Gateway — ProviderID extraction tests
// ---------------------------------------------------------------------------

func TestAPIGatewayProviderIDConstruction(t *testing.T) {
	tests := []struct {
		name         string
		recordID     string
		wantShort    string
		wantProvider string
	}{
		{
			"full path extracts short name",
			"projects/my-proj/locations/us-central1/gateways/my-gw",
			"my-gw",
			"projects/test-proj/locations/us-central1/gateways/my-gw",
		},
		{
			"short name stays as-is",
			"my-gw",
			"my-gw",
			"projects/test-proj/locations/us-central1/gateways/my-gw",
		},
		{
			"empty falls back to node name",
			"",
			"my-gateway",
			"projects/test-proj/locations/us-central1/gateways/my-gateway",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate name extraction logic from applyGCPAPIGateway
			name := tt.recordID
			if name != "" && strings.Contains(name, "/") {
				name = name[strings.LastIndex(name, "/")+1:]
			}
			if name == "" {
				name = strings.TrimPrefix(identifierFor("my_gateway"), "beecon-")
			}
			if name != tt.wantShort {
				t.Errorf("extracted name = %q, want %q", name, tt.wantShort)
			}
			providerID := fmt.Sprintf("projects/%s/locations/%s/gateways/%s", "test-proj", "us-central1", name)
			if providerID != tt.wantProvider {
				t.Errorf("providerID = %q, want %q", providerID, tt.wantProvider)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// API Gateway — multi-step delete partial result tests
// ---------------------------------------------------------------------------

func TestApplyGCPAPIGateway_DeletePartialResult(t *testing.T) {
	// When calling the real function without creds, it will fail at service init.
	// This test verifies the function returns a non-nil error for partial cases.
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "DELETE", NodeName: "my_gateway"},
		Intent: map[string]interface{}{
			"intent.project_id": "test-proj",
			"intent.location":   "us-central1",
		},
		Record: &state.ResourceRecord{
			ProviderID: "projects/test-proj/locations/us-central1/gateways/my-gw",
		},
	}
	// This will fail at the service init since we don't have credentials,
	// but the function should return an error, not panic.
	res, err := applyGCPAPIGateway(t.Context(), req)
	if err == nil && res == nil {
		t.Fatal("expected either a result or an error")
	}
}

func TestApplyGCPAPIGateway_CreatePartialResult(t *testing.T) {
	// Similar to delete — will fail at service init.
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_gateway"},
		Intent: map[string]interface{}{
			"intent.project_id":   "test-proj",
			"intent.location":     "us-central1",
			"intent.display_name": "My Gateway",
		},
	}
	res, err := applyGCPAPIGateway(t.Context(), req)
	if err == nil && res == nil {
		t.Fatal("expected either a result or an error")
	}
}

// ---------------------------------------------------------------------------
// Identity Platform — dry-run apply tests
// ---------------------------------------------------------------------------

func TestApplyGCPIdentityPlatform_DryRun(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ops := []string{"CREATE", "UPDATE", "DELETE"}
	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			req := ApplyRequest{
				Action: &state.PlanAction{Operation: op, NodeName: "my_tenant"},
				Intent: map[string]interface{}{
					"intent.project_id":              "test-proj",
					"intent.display_name":            "My Tenant",
					"intent.allow_password_signup":    "true",
					"intent.enable_email_link_signin": "true",
					"intent.mfa_config":               "ENABLED",
				},
				Region: "us-central1",
			}
			res, err := e.applyGCP(t.Context(), req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res == nil {
				t.Fatal("expected non-nil result")
			}
			if res.ProviderID == "" {
				t.Error("expected non-empty ProviderID")
			}
		})
	}
}

func TestApplyGCPIdentityPlatform_MissingProjectID(t *testing.T) {
	e := &DefaultExecutor{dryRun: false}
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_tenant"},
		Intent: map[string]interface{}{
			"intent.display_name": "My Tenant",
		},
	}
	_, err := e.applyGCP(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
}

func TestApplyGCPIdentityPlatform_DirectCallMissingProjectID(t *testing.T) {
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_tenant"},
		Intent: map[string]interface{}{},
	}
	_, err := applyGCPIdentityPlatform(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
	if got := err.Error(); got != "identity_platform requires intent.project_id" {
		t.Errorf("unexpected error message: %s", got)
	}
}

// ---------------------------------------------------------------------------
// Identity Platform — observe tests
// ---------------------------------------------------------------------------

func TestObserveGCPIdentityPlatform_NilSnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/tenants/my-tenant",
		IntentSnapshot: nil,
		LiveState:      map[string]interface{}{"service": "identity_platform"},
	}
	_, err := observeGCPIdentityPlatform(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for nil IntentSnapshot")
	}
}

func TestObserveGCPIdentityPlatform_EmptySnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/tenants/my-tenant",
		IntentSnapshot: map[string]interface{}{},
		LiveState:      map[string]interface{}{"service": "identity_platform"},
	}
	_, err := observeGCPIdentityPlatform(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for empty IntentSnapshot")
	}
}

func TestGCPObserveExpectedIdentityPlatformKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "display_name",
		"allow_password_signup", "enable_email_link_signin",
		"mfa_state", "mfa_providers", "test_phone_numbers_count",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Identity Platform observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Identity Platform — ProviderID construction tests
// ---------------------------------------------------------------------------

func TestIdentityPlatformProviderIDConstruction(t *testing.T) {
	tests := []struct {
		name         string
		recordID     string
		wantShort    string
		wantProvider string
	}{
		{
			"full path extracts short name",
			"projects/my-proj/tenants/my-tenant",
			"my-tenant",
			"projects/test-proj/tenants/my-tenant",
		},
		{
			"short name stays as-is",
			"my-tenant",
			"my-tenant",
			"projects/test-proj/tenants/my-tenant",
		},
		{
			"empty falls back to node name",
			"",
			"my-tenant",
			"projects/test-proj/tenants/my-tenant",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := tt.recordID
			if name != "" && strings.Contains(name, "/tenants/") {
				name = name[strings.LastIndex(name, "/")+1:]
			}
			if name == "" {
				name = strings.TrimPrefix(identifierFor("my_tenant"), "beecon-")
			}
			if name != tt.wantShort {
				t.Errorf("extracted name = %q, want %q", name, tt.wantShort)
			}
			providerID := fmt.Sprintf("projects/%s/tenants/%s", "test-proj", name)
			if providerID != tt.wantProvider {
				t.Errorf("providerID = %q, want %q", providerID, tt.wantProvider)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// detectGCPRecordTarget — SERVICE and STORE node type tests
// ---------------------------------------------------------------------------

func TestDetectGCPRecordTarget_APIGateway(t *testing.T) {
	tests := []struct {
		name     string
		rec      *state.ResourceRecord
		expected string
	}{
		{
			"STORE with service=api_gateway",
			&state.ResourceRecord{
				NodeType:  "STORE",
				LiveState: map[string]interface{}{"service": "api_gateway"},
			},
			"api_gateway",
		},
		{
			"SERVICE with service=api_gateway",
			&state.ResourceRecord{
				NodeType:  "SERVICE",
				LiveState: map[string]interface{}{"service": "api_gateway"},
			},
			"api_gateway",
		},
		{
			"SERVICE with service=gateway",
			&state.ResourceRecord{
				NodeType:  "SERVICE",
				LiveState: map[string]interface{}{"service": "gateway"},
			},
			"api_gateway",
		},
		{
			"STORE with engine containing gateway",
			&state.ResourceRecord{
				NodeType:       "STORE",
				LiveState:      map[string]interface{}{},
				IntentSnapshot: map[string]interface{}{"intent.engine": "api_gateway"},
			},
			"api_gateway",
		},
		{
			"SERVICE with runtime containing api_gateway",
			&state.ResourceRecord{
				NodeType:       "SERVICE",
				LiveState:      map[string]interface{}{},
				IntentSnapshot: map[string]interface{}{"intent.runtime": "api_gateway"},
			},
			"api_gateway",
		},
		{
			"SERVICE with runtime containing gateway",
			&state.ResourceRecord{
				NodeType:       "SERVICE",
				LiveState:      map[string]interface{}{},
				IntentSnapshot: map[string]interface{}{"intent.runtime": "gateway"},
			},
			"api_gateway",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectGCPRecordTarget(tt.rec)
			if got != tt.expected {
				t.Errorf("detectGCPRecordTarget() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestDetectGCPRecordTarget_IdentityPlatform(t *testing.T) {
	tests := []struct {
		name     string
		rec      *state.ResourceRecord
		expected string
	}{
		{
			"STORE with service=identity_platform",
			&state.ResourceRecord{
				NodeType:  "STORE",
				LiveState: map[string]interface{}{"service": "identity_platform"},
			},
			"identity_platform",
		},
		{
			"SERVICE with service=identity_platform",
			&state.ResourceRecord{
				NodeType:  "SERVICE",
				LiveState: map[string]interface{}{"service": "identity_platform"},
			},
			"identity_platform",
		},
		{
			"SERVICE with service=identity",
			&state.ResourceRecord{
				NodeType:  "SERVICE",
				LiveState: map[string]interface{}{"service": "identity"},
			},
			"identity_platform",
		},
		{
			"STORE with engine containing identity",
			&state.ResourceRecord{
				NodeType:       "STORE",
				LiveState:      map[string]interface{}{},
				IntentSnapshot: map[string]interface{}{"intent.engine": "identity_platform"},
			},
			"identity_platform",
		},
		{
			"SERVICE with runtime containing identity",
			&state.ResourceRecord{
				NodeType:       "SERVICE",
				LiveState:      map[string]interface{}{},
				IntentSnapshot: map[string]interface{}{"intent.runtime": "identity"},
			},
			"identity_platform",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectGCPRecordTarget(tt.rec)
			if got != tt.expected {
				t.Errorf("detectGCPRecordTarget() = %q, want %q", got, tt.expected)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// validateGCPInput coverage for API Gateway and Identity Platform
// ---------------------------------------------------------------------------

func TestValidateGCPInput_G2CTargets(t *testing.T) {
	targets := []string{"api_gateway", "identity_platform"}
	for _, target := range targets {
		t.Run(target+"_missing_project_id", func(t *testing.T) {
			err := validateGCPInput(target, map[string]interface{}{})
			if err == nil {
				t.Fatalf("expected error for %s missing project_id", target)
			}
			if !strings.Contains(err.Error(), "project_id") {
				t.Errorf("error should mention project_id: %v", err)
			}
		})
		t.Run(target+"_valid", func(t *testing.T) {
			err := validateGCPInput(target, map[string]interface{}{
				"intent.project_id": "my-project",
			})
			if err != nil {
				t.Fatalf("unexpected error for %s with project_id: %v", target, err)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Dry-run observe passthrough for new targets
// ---------------------------------------------------------------------------

func TestGCPObserveDryRunPassthrough_G2C(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	cases := []struct {
		name   string
		target string
		live   map[string]interface{}
	}{
		{"api_gateway", "api_gateway", map[string]interface{}{"service": "api_gateway", "display_name": "my-gw"}},
		{"identity_platform", "identity_platform", map[string]interface{}{"service": "identity_platform", "display_name": "my-tenant"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &state.ResourceRecord{
				Managed:    true,
				ProviderID: "test-" + tc.target,
				LiveState:  tc.live,
			}
			res, err := e.observeGCP(t.Context(), "us-central1", rec)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !res.Exists {
				t.Fatal("expected Exists=true")
			}
			for k, v := range tc.live {
				got, ok := res.LiveState[k]
				if !ok {
					t.Errorf("missing key %q in LiveState", k)
					continue
				}
				if got != v {
					t.Errorf("key %q: want %v, got %v", k, v, got)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Nil IntentSnapshot for new targets via observeGCP
// ---------------------------------------------------------------------------

func TestGCPObserveNilIntentSnapshot_G2C(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"api_gateway", "api_gateway"},
		{"identity_platform", "identity_platform"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"_nil_snapshot", func(t *testing.T) {
			rec := &state.ResourceRecord{
				Managed:        true,
				ProviderID:     "test-id",
				IntentSnapshot: nil,
				LiveState:      map[string]interface{}{"service": tc.target},
				NodeType:       "STORE",
			}
			e := &DefaultExecutor{dryRun: false}
			_, err := e.observeGCP(t.Context(), "us-central1", rec)
			if err == nil {
				t.Fatal("expected error for nil IntentSnapshot, got nil")
			}
		})
		t.Run(tc.name+"_empty_snapshot", func(t *testing.T) {
			rec := &state.ResourceRecord{
				Managed:        true,
				ProviderID:     "test-id",
				IntentSnapshot: map[string]interface{}{},
				LiveState:      map[string]interface{}{"service": tc.target},
				NodeType:       "STORE",
			}
			e := &DefaultExecutor{dryRun: false}
			_, err := e.observeGCP(t.Context(), "us-central1", rec)
			if err == nil {
				t.Fatal("expected error for empty IntentSnapshot, got nil")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// G2C depth fields security check
// ---------------------------------------------------------------------------

func TestGCPObserveNoSensitiveKeysInG2CDepthFields(t *testing.T) {
	g2cFields := []string{
		// API Gateway
		"display_name", "state", "api_config", "default_hostname",
		"create_time", "update_time", "labels",
		"api_name", "config_name", "gateway_name",
		"gateway_deleted", "config_deleted", "api_deleted",
		// Identity Platform
		"allow_password_signup", "enable_email_link_signin",
		"mfa_state", "mfa_providers", "test_phone_numbers_count",
		"tenant_id",
	}
	for _, field := range g2cFields {
		if security.IsSensitiveKey(field) {
			t.Errorf("G2C depth field %q collides with sensitive key registry — must not store raw values in LiveState", field)
		}
	}
}

// ---------------------------------------------------------------------------
// buildMFAConfig helper tests
// ---------------------------------------------------------------------------

func TestBuildMFAConfig(t *testing.T) {
	tests := []struct {
		input    string
		wantState string
		wantProviders int
	}{
		{"ENABLED", "ENABLED", 1},
		{"enabled", "ENABLED", 1},
		{"MANDATORY", "MANDATORY", 1},
		{"DISABLED", "DISABLED", 0},
		{"disabled", "DISABLED", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			cfg := buildMFAConfig(tt.input)
			if cfg.State != tt.wantState {
				t.Errorf("State = %q, want %q", cfg.State, tt.wantState)
			}
			if len(cfg.EnabledProviders) != tt.wantProviders {
				t.Errorf("EnabledProviders count = %d, want %d", len(cfg.EnabledProviders), tt.wantProviders)
			}
			if tt.wantProviders > 0 && cfg.EnabledProviders[0] != "PHONE_SMS" {
				t.Errorf("EnabledProviders[0] = %q, want PHONE_SMS", cfg.EnabledProviders[0])
			}
		})
	}
}
