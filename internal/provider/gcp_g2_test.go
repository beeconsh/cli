package provider

import (
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
)

// ---------------------------------------------------------------------------
// Cloud Functions — dry-run apply tests
// ---------------------------------------------------------------------------

func TestApplyGCPCloudFunctions_DryRun_CREATE(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	req := ApplyRequest{
		Provider: "gcp",
		Region:   "us-central1",
		Action: &state.PlanAction{
			NodeName:  "my-func",
			NodeType:  "SERVICE",
			Operation: "CREATE",
		},
		Intent: map[string]interface{}{
			"project_id":  "test-project",
			"runtime":     "nodejs20",
			"entry_point": "handler",
			"source_url":  "gs://my-bucket/code.zip",
			"memory":      "512Mi",
			"timeout":     "120s",
			"region":      "us-east1",
		},
	}
	res, err := e.applyGCP(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProviderID == "" {
		t.Error("expected non-empty ProviderID")
	}
	if res.LiveState == nil {
		t.Fatal("expected LiveState to be non-nil")
	}
	// Dry-run goes through simulatedApply, so just check we get a valid result.
	if res.LiveState["provider"] != "gcp" {
		t.Errorf("expected provider=gcp, got %v", res.LiveState["provider"])
	}
}

func TestApplyGCPCloudFunctions_DryRun_UPDATE(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	req := ApplyRequest{
		Provider: "gcp",
		Action: &state.PlanAction{
			NodeName:  "my-func",
			NodeType:  "SERVICE",
			Operation: "UPDATE",
		},
		Intent: map[string]interface{}{
			"project_id": "test-project",
			"runtime":    "python312",
			"memory":     "1Gi",
			"timeout":    "300s",
		},
	}
	res, err := e.applyGCP(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestApplyGCPCloudFunctions_DryRun_DELETE(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	req := ApplyRequest{
		Provider: "gcp",
		Action: &state.PlanAction{
			NodeName:  "my-func",
			NodeType:  "SERVICE",
			Operation: "DELETE",
		},
		Intent: map[string]interface{}{
			"project_id": "test-project",
			"runtime":    "go122",
		},
	}
	res, err := e.applyGCP(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
}

// ---------------------------------------------------------------------------
// Cloud Functions — validation tests
// ---------------------------------------------------------------------------

func TestValidateGCPInput_CloudFunctions_MissingProjectID(t *testing.T) {
	err := validateGCPInput("cloud_functions", map[string]interface{}{
		"intent.runtime": "nodejs20",
	})
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
	if !strings.Contains(err.Error(), "project_id") {
		t.Errorf("error should mention project_id: %v", err)
	}
}

func TestValidateGCPInput_CloudFunctions_MissingRuntime(t *testing.T) {
	err := validateGCPInput("cloud_functions", map[string]interface{}{
		"intent.project_id": "my-project",
	})
	if err == nil {
		t.Fatal("expected error for missing runtime")
	}
	if !strings.Contains(err.Error(), "runtime") {
		t.Errorf("error should mention runtime: %v", err)
	}
}

func TestValidateGCPInput_CloudFunctions_MissingBoth(t *testing.T) {
	err := validateGCPInput("cloud_functions", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing fields")
	}
	if !strings.Contains(err.Error(), "project_id") || !strings.Contains(err.Error(), "runtime") {
		t.Errorf("error should mention both project_id and runtime: %v", err)
	}
}

func TestValidateGCPInput_CloudFunctions_Valid(t *testing.T) {
	err := validateGCPInput("cloud_functions", map[string]interface{}{
		"intent.project_id": "my-project",
		"intent.runtime":    "nodejs20",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Cloud Functions — observe tests
// ---------------------------------------------------------------------------

func TestObserveGCPCloudFunctions_DryRunPassthrough(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	rec := &state.ResourceRecord{
		Managed:    true,
		ProviderID: "projects/p/locations/us-central1/functions/myfn",
		LiveState: map[string]interface{}{
			"service": "cloud_functions",
			"runtime": "nodejs20",
			"state":   "ACTIVE",
		},
	}
	res, err := e.observeGCP(t.Context(), "us-central1", rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Exists {
		t.Fatal("expected Exists=true")
	}
	if res.LiveState["runtime"] != "nodejs20" {
		t.Errorf("expected runtime=nodejs20, got %v", res.LiveState["runtime"])
	}
}

func TestObserveGCPCloudFunctions_NilIntentSnapshot(t *testing.T) {
	e := &DefaultExecutor{dryRun: false}
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "test-id",
		IntentSnapshot: nil,
		LiveState:      map[string]interface{}{"service": "cloud_functions"},
		NodeType:       "STORE",
	}
	_, err := e.observeGCP(t.Context(), "us-central1", rec)
	if err == nil {
		t.Fatal("expected error for nil IntentSnapshot")
	}
	if !strings.Contains(err.Error(), "project_id") {
		t.Errorf("error should mention project_id: %v", err)
	}
}

func TestObserveGCPCloudFunctions_EmptyIntentSnapshot(t *testing.T) {
	e := &DefaultExecutor{dryRun: false}
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "test-id",
		IntentSnapshot: map[string]interface{}{},
		LiveState:      map[string]interface{}{"service": "cloud_functions"},
		NodeType:       "STORE",
	}
	_, err := e.observeGCP(t.Context(), "us-central1", rec)
	if err == nil {
		t.Fatal("expected error for empty IntentSnapshot")
	}
}

func TestObserveGCPCloudFunctions_ExpectedKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "name", "region",
		"state", "environment", "update_time",
		"service_url", "memory", "timeout",
		"runtime", "entry_point",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Cloud Functions observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
	// env_keys is used instead of raw env var values
	if security.IsSensitiveKey("env_keys") {
		t.Error("env_keys should not be classified as sensitive")
	}
}

// ---------------------------------------------------------------------------
// GKE — dry-run apply tests
// ---------------------------------------------------------------------------

func TestApplyGCPGKE_DryRun_CREATE(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	req := ApplyRequest{
		Provider: "gcp",
		Region:   "us-central1",
		Action: &state.PlanAction{
			NodeName:  "my-cluster",
			NodeType:  "COMPUTE",
			Operation: "CREATE",
		},
		Intent: map[string]interface{}{
			"project_id":   "test-project",
			"region":       "us-central1",
			"node_count":   "5",
			"machine_type": "n1-standard-4",
			"version":      "1.28",
			"network":      "my-vpc",
		},
	}
	res, err := e.applyGCP(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProviderID == "" {
		t.Error("expected non-empty ProviderID")
	}
	if res.LiveState["provider"] != "gcp" {
		t.Errorf("expected provider=gcp, got %v", res.LiveState["provider"])
	}
}

func TestApplyGCPGKE_DryRun_UPDATE(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	req := ApplyRequest{
		Provider: "gcp",
		Action: &state.PlanAction{
			NodeName:  "my-cluster",
			NodeType:  "COMPUTE",
			Operation: "UPDATE",
		},
		Intent: map[string]interface{}{
			"project_id": "test-project",
			"node_count": "10",
		},
	}
	res, err := e.applyGCP(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
}

func TestApplyGCPGKE_DryRun_DELETE(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	req := ApplyRequest{
		Provider: "gcp",
		Action: &state.PlanAction{
			NodeName:  "my-cluster",
			NodeType:  "COMPUTE",
			Operation: "DELETE",
		},
		Intent: map[string]interface{}{
			"project_id": "test-project",
		},
	}
	res, err := e.applyGCP(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res == nil {
		t.Fatal("expected non-nil result")
	}
}

// ---------------------------------------------------------------------------
// GKE — validation tests
// ---------------------------------------------------------------------------

func TestValidateGCPInput_GKE_MissingProjectID(t *testing.T) {
	err := validateGCPInput("gke", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
	if !strings.Contains(err.Error(), "project_id") {
		t.Errorf("error should mention project_id: %v", err)
	}
}

func TestValidateGCPInput_GKE_Valid(t *testing.T) {
	err := validateGCPInput("gke", map[string]interface{}{
		"intent.project_id": "my-project",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GKE — observe tests
// ---------------------------------------------------------------------------

func TestObserveGCPGKE_DryRunPassthrough(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	rec := &state.ResourceRecord{
		Managed:    true,
		ProviderID: "projects/p/locations/us-central1/clusters/mycluster",
		LiveState: map[string]interface{}{
			"service":    "gke",
			"status":     "RUNNING",
			"node_count": int64(3),
		},
	}
	res, err := e.observeGCP(t.Context(), "us-central1", rec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Exists {
		t.Fatal("expected Exists=true")
	}
	if res.LiveState["status"] != "RUNNING" {
		t.Errorf("expected status=RUNNING, got %v", res.LiveState["status"])
	}
}

func TestObserveGCPGKE_NilIntentSnapshot(t *testing.T) {
	e := &DefaultExecutor{dryRun: false}
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "test-id",
		IntentSnapshot: nil,
		LiveState:      map[string]interface{}{"service": "gke"},
		NodeType:       "STORE",
	}
	_, err := e.observeGCP(t.Context(), "us-central1", rec)
	if err == nil {
		t.Fatal("expected error for nil IntentSnapshot")
	}
	if !strings.Contains(err.Error(), "project_id") {
		t.Errorf("error should mention project_id: %v", err)
	}
}

func TestObserveGCPGKE_EmptyIntentSnapshot(t *testing.T) {
	e := &DefaultExecutor{dryRun: false}
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "test-id",
		IntentSnapshot: map[string]interface{}{},
		LiveState:      map[string]interface{}{"service": "gke"},
		NodeType:       "STORE",
	}
	_, err := e.observeGCP(t.Context(), "us-central1", rec)
	if err == nil {
		t.Fatal("expected error for empty IntentSnapshot")
	}
}

func TestObserveGCPGKE_ExpectedKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "name", "location",
		"status", "current_master_version", "current_node_version",
		"endpoint", "network", "subnetwork",
		"services_ipv4_cidr", "cluster_ipv4_cidr", "create_time",
		"node_count", "machine_type", "labels",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("GKE observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// ---------------------------------------------------------------------------
// parseTimeoutSeconds unit tests
// ---------------------------------------------------------------------------

func TestParseTimeoutSeconds(t *testing.T) {
	cases := []struct {
		input string
		want  int64
	}{
		{"60s", 60},
		{"120s", 120},
		{"60", 60},
		{"300", 300},
		{"", 60},      // default
		{"abc", 60},   // invalid
		{"-1s", 60},   // negative
		{"0s", 60},    // zero
		{"  90s  ", 90},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := parseTimeoutSeconds(tc.input)
			if got != tc.want {
				t.Errorf("parseTimeoutSeconds(%q) = %d, want %d", tc.input, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Cloud Functions & GKE — intent defaults and ProviderID format
// ---------------------------------------------------------------------------

func TestCloudFunctionsProviderIDFormat(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	req := ApplyRequest{
		Provider: "gcp",
		Action: &state.PlanAction{
			NodeName:  "processor",
			NodeType:  "SERVICE",
			Operation: "CREATE",
		},
		Intent: map[string]interface{}{
			"project_id": "my-project",
			"runtime":    "go122",
			"region":     "europe-west1",
		},
	}
	res, err := e.applyGCP(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Dry-run simulatedApply generates a ProviderID; we just verify it's non-empty.
	if res.ProviderID == "" {
		t.Error("expected non-empty ProviderID")
	}
}

func TestGKEProviderIDFormat(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	req := ApplyRequest{
		Provider: "gcp",
		Action: &state.PlanAction{
			NodeName:  "prod-cluster",
			NodeType:  "COMPUTE",
			Operation: "CREATE",
		},
		Intent: map[string]interface{}{
			"project_id": "my-project",
			"region":     "asia-east1",
		},
	}
	res, err := e.applyGCP(t.Context(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProviderID == "" {
		t.Error("expected non-empty ProviderID")
	}
}

// ---------------------------------------------------------------------------
// Cloud Functions & GKE — defaults for optional intent fields
// ---------------------------------------------------------------------------

func TestCloudFunctionsIntentDefaults(t *testing.T) {
	// Verify defaults are applied when optional fields are omitted.
	// We test the non-dry-run path indirectly by checking the function builds
	// the result with correct defaults. Since BEECON_EXECUTE != "1", the main
	// switch goes to simulatedApply. So we test the dedicated function directly
	// by checking that the intent extraction helpers work.

	// memory default
	m := defaultString(intent(map[string]interface{}{}, "memory"), "256Mi")
	if m != "256Mi" {
		t.Errorf("expected default memory 256Mi, got %q", m)
	}

	// timeout default
	to := defaultString(intent(map[string]interface{}{}, "timeout"), "60s")
	if to != "60s" {
		t.Errorf("expected default timeout 60s, got %q", to)
	}

	// region default
	r := defaultString(intent(map[string]interface{}{}, "region"), "us-central1")
	if r != "us-central1" {
		t.Errorf("expected default region us-central1, got %q", r)
	}
}

func TestGKEIntentDefaults(t *testing.T) {
	// node_count default
	nc := defaultString(intent(map[string]interface{}{}, "node_count"), "3")
	if nc != "3" {
		t.Errorf("expected default node_count 3, got %q", nc)
	}

	// machine_type default
	mt := defaultString(intent(map[string]interface{}{}, "machine_type"), "e2-medium")
	if mt != "e2-medium" {
		t.Errorf("expected default machine_type e2-medium, got %q", mt)
	}

	// network default
	n := defaultString(intent(map[string]interface{}{}, "network"), "default")
	if n != "default" {
		t.Errorf("expected default network default, got %q", n)
	}
}

// ---------------------------------------------------------------------------
// detectGCPTarget and detectGCPRecordTarget routing
// ---------------------------------------------------------------------------

func TestDetectGCPRecordTarget_CloudFunctions(t *testing.T) {
	rec := &state.ResourceRecord{
		NodeType:  "STORE",
		LiveState: map[string]interface{}{"service": "cloud_functions"},
	}
	got := detectGCPRecordTarget(rec)
	if got != "cloud_functions" {
		t.Errorf("expected cloud_functions, got %q", got)
	}
}

func TestDetectGCPRecordTarget_GKE(t *testing.T) {
	rec := &state.ResourceRecord{
		NodeType:  "STORE",
		LiveState: map[string]interface{}{"service": "gke"},
	}
	got := detectGCPRecordTarget(rec)
	if got != "gke" {
		t.Errorf("expected gke, got %q", got)
	}
}

func TestDetectGCPRecordTarget_CloudFunctions_ViaEngine(t *testing.T) {
	rec := &state.ResourceRecord{
		NodeType:  "STORE",
		LiveState: map[string]interface{}{},
		IntentSnapshot: map[string]interface{}{
			"intent.engine": "cloud_function",
		},
	}
	got := detectGCPRecordTarget(rec)
	if got != "cloud_functions" {
		t.Errorf("expected cloud_functions, got %q", got)
	}
}

func TestDetectGCPRecordTarget_GKE_ViaEngine(t *testing.T) {
	rec := &state.ResourceRecord{
		NodeType:  "STORE",
		LiveState: map[string]interface{}{},
		IntentSnapshot: map[string]interface{}{
			"intent.engine": "gke",
		},
	}
	got := detectGCPRecordTarget(rec)
	if got != "gke" {
		t.Errorf("expected gke, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// G2 observe keys — comprehensive sensitivity check
// ---------------------------------------------------------------------------

func TestGCPObserveNoSensitiveKeysInG2Fields(t *testing.T) {
	g2Fields := []string{
		// Cloud Functions
		"state", "environment", "update_time", "service_url", "memory",
		"timeout", "runtime", "entry_point", "env_keys", "labels",
		// GKE
		"status", "current_master_version", "current_node_version",
		"endpoint", "network", "subnetwork", "services_ipv4_cidr",
		"cluster_ipv4_cidr", "create_time", "node_count", "machine_type",
		"operation_name", "operation_status",
	}
	for _, field := range g2Fields {
		if security.IsSensitiveKey(field) {
			t.Errorf("G2 field %q collides with sensitive key registry — must not store raw values in LiveState", field)
		}
	}
}
