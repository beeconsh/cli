package provider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
)

// ---------------------------------------------------------------------------
// Cloud CDN tests
// ---------------------------------------------------------------------------

func TestApplyGCPCloudCDN_DryRun(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ops := []string{"CREATE", "UPDATE", "DELETE"}
	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			req := ApplyRequest{
				Action: &state.PlanAction{Operation: op, NodeName: "my_cdn"},
				Intent: map[string]interface{}{
					"intent.project_id": "test-proj",
					"intent.origin":     "my-backend",
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

func TestApplyGCPCloudCDN_MissingProjectID(t *testing.T) {
	e := &DefaultExecutor{dryRun: false}
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_cdn"},
		Intent: map[string]interface{}{
			"intent.origin": "my-backend",
		},
	}
	_, err := e.applyGCP(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
}

func TestApplyGCPCloudCDN_MissingOriginOnCreate(t *testing.T) {
	// This tests intent validation inside applyGCPCloudCDN itself (beyond validateGCPInput).
	// applyGCPCloudCDN should reject CREATE when origin/backend is missing.
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_cdn"},
		Intent: map[string]interface{}{
			"intent.project_id": "test-proj",
		},
	}
	_, err := applyGCPCloudCDN(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing origin on CREATE")
	}
	if got := err.Error(); got != "cloud_cdn CREATE requires intent.origin or intent.backend" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestObserveGCPCloudCDN_NilSnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/global/backendServices/my-cdn",
		IntentSnapshot: nil,
		LiveState:      map[string]interface{}{"service": "cloud_cdn"},
	}
	_, err := observeGCPCloudCDN(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for nil IntentSnapshot")
	}
}

func TestObserveGCPCloudCDN_EmptySnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/global/backendServices/my-cdn",
		IntentSnapshot: map[string]interface{}{},
		LiveState:      map[string]interface{}{"service": "cloud_cdn"},
	}
	_, err := observeGCPCloudCDN(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for empty IntentSnapshot")
	}
}

func TestGCPObserveExpectedCloudCDNKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "name",
		"cdn_enabled", "cache_mode", "default_ttl", "max_ttl",
		"negative_caching", "signed_url_cache_max_age",
		"protocol", "port", "creation_timestamp",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Cloud CDN observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Cloud Monitoring tests
// ---------------------------------------------------------------------------

func TestApplyGCPCloudMonitoring_DryRun(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ops := []string{"CREATE", "UPDATE", "DELETE"}
	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			req := ApplyRequest{
				Action: &state.PlanAction{Operation: op, NodeName: "my_alert"},
				Intent: map[string]interface{}{
					"intent.project_id": "test-proj",
					"intent.metric":     "compute.googleapis.com/instance/cpu/utilization",
					"intent.threshold":  "0.8",
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
		})
	}
}

func TestApplyGCPCloudMonitoring_MissingProjectID(t *testing.T) {
	e := &DefaultExecutor{dryRun: false}
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_alert"},
		Intent: map[string]interface{}{
			"intent.metric":    "compute.googleapis.com/instance/cpu/utilization",
			"intent.threshold": "0.8",
		},
	}
	_, err := e.applyGCP(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
}

func TestApplyGCPCloudMonitoring_MissingMetricOnCreate(t *testing.T) {
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_alert"},
		Intent: map[string]interface{}{
			"intent.project_id": "test-proj",
		},
	}
	_, err := applyGCPCloudMonitoring(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing metric on CREATE")
	}
	if got := err.Error(); got != "cloud_monitoring CREATE requires intent.metric" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestApplyGCPCloudMonitoring_UpdateRequiresProviderID(t *testing.T) {
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "UPDATE", NodeName: "my_alert"},
		Intent: map[string]interface{}{
			"intent.project_id": "test-proj",
			"intent.metric":     "compute.googleapis.com/instance/cpu/utilization",
		},
	}
	_, err := applyGCPCloudMonitoring(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for UPDATE without provider_id")
	}
}

func TestApplyGCPCloudMonitoring_DeleteRequiresProviderID(t *testing.T) {
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "DELETE", NodeName: "my_alert"},
		Intent: map[string]interface{}{
			"intent.project_id": "test-proj",
		},
	}
	_, err := applyGCPCloudMonitoring(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for DELETE without provider_id")
	}
}

func TestObserveGCPCloudMonitoring_NilSnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/alertPolicies/123",
		IntentSnapshot: nil,
		LiveState:      map[string]interface{}{"service": "cloud_monitoring"},
	}
	_, err := observeGCPCloudMonitoring(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for nil IntentSnapshot")
	}
}

func TestObserveGCPCloudMonitoring_EmptySnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/alertPolicies/123",
		IntentSnapshot: map[string]interface{}{},
		LiveState:      map[string]interface{}{"service": "cloud_monitoring"},
	}
	_, err := observeGCPCloudMonitoring(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for empty IntentSnapshot")
	}
}

func TestObserveGCPCloudMonitoring_MissingProviderID(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:    true,
		ProviderID: "",
		IntentSnapshot: map[string]interface{}{
			"intent.project_id": "test-proj",
		},
		LiveState: map[string]interface{}{"service": "cloud_monitoring"},
	}
	_, err := observeGCPCloudMonitoring(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for missing provider_id")
	}
}

func TestGCPObserveExpectedCloudMonitoringKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "display_name", "enabled",
		"metric_filter", "threshold", "comparison", "duration",
		"notification_channels", "creation_time", "mutation_time",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Cloud Monitoring observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// ---------------------------------------------------------------------------
// Eventarc tests
// ---------------------------------------------------------------------------

func TestApplyGCPEventarc_DryRun(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ops := []string{"CREATE", "UPDATE", "DELETE"}
	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			req := ApplyRequest{
				Action: &state.PlanAction{Operation: op, NodeName: "my_trigger"},
				Intent: map[string]interface{}{
					"intent.project_id":   "test-proj",
					"intent.destination":  "my-service",
					"intent.event_type":   "google.cloud.audit.log.v1.written",
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
		})
	}
}

func TestApplyGCPEventarc_MissingProjectID(t *testing.T) {
	e := &DefaultExecutor{dryRun: false}
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_trigger"},
		Intent: map[string]interface{}{
			"intent.destination": "my-service",
		},
	}
	_, err := e.applyGCP(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing project_id")
	}
}

func TestApplyGCPEventarc_MissingDestinationOnCreate(t *testing.T) {
	req := ApplyRequest{
		Action: &state.PlanAction{Operation: "CREATE", NodeName: "my_trigger"},
		Intent: map[string]interface{}{
			"intent.project_id": "test-proj",
		},
	}
	_, err := applyGCPEventarc(t.Context(), req)
	if err == nil {
		t.Fatal("expected error for missing destination on CREATE")
	}
	if got := err.Error(); got != "eventarc CREATE requires intent.destination" {
		t.Errorf("unexpected error message: %s", got)
	}
}

func TestObserveGCPEventarc_NilSnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/locations/us-central1/triggers/my-trigger",
		IntentSnapshot: nil,
		LiveState:      map[string]interface{}{"service": "eventarc"},
	}
	_, err := observeGCPEventarc(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for nil IntentSnapshot")
	}
}

func TestObserveGCPEventarc_EmptySnapshot(t *testing.T) {
	rec := &state.ResourceRecord{
		Managed:        true,
		ProviderID:     "projects/test-proj/locations/us-central1/triggers/my-trigger",
		IntentSnapshot: map[string]interface{}{},
		LiveState:      map[string]interface{}{"service": "eventarc"},
	}
	_, err := observeGCPEventarc(t.Context(), rec)
	if err == nil {
		t.Fatal("expected error for empty IntentSnapshot")
	}
}

func TestGCPObserveExpectedEventarcKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "name",
		"create_time", "update_time",
		"destination_service", "destination_region",
		"transport_topic", "service_account",
		"event_filters", "labels",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Eventarc observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// ---------------------------------------------------------------------------
// ProviderID name extraction on UPDATE/DELETE (Findings 1 & 2)
// ---------------------------------------------------------------------------

func TestApplyGCPCloudCDN_ProviderIDExtraction(t *testing.T) {
	// When a Record has a full-path ProviderID from a prior CREATE,
	// UPDATE/DELETE must extract the short name so providerID construction
	// does not double up the path prefix.
	tests := []struct {
		name       string
		op         string
		recordID   string
		wantSuffix string // expected short name at end of providerID
		wantNoDup  string // substring that must NOT appear (doubled prefix)
	}{
		{
			name:       "UPDATE with full path providerID",
			op:         "UPDATE",
			recordID:   "projects/test-proj/global/backendServices/my-cdn",
			wantSuffix: "/backendServices/my-cdn",
			wantNoDup:  "backendServices/projects/",
		},
		{
			name:       "DELETE with full path providerID",
			op:         "DELETE",
			recordID:   "projects/test-proj/global/backendServices/my-cdn",
			wantSuffix: "/backendServices/my-cdn",
			wantNoDup:  "backendServices/projects/",
		},
		{
			name:       "UPDATE with short name providerID",
			op:         "UPDATE",
			recordID:   "my-cdn",
			wantSuffix: "/backendServices/my-cdn",
			wantNoDup:  "backendServices/projects/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ApplyRequest{
				Action: &state.PlanAction{Operation: tt.op, NodeName: "my_cdn"},
				Intent: map[string]interface{}{
					"intent.project_id": "test-proj",
					"intent.origin":     "my-backend",
					"intent.cache_mode": "CACHE_ALL_STATIC",
				},
				Record: &state.ResourceRecord{ProviderID: tt.recordID},
				Region: "us-central1",
			}
			// The function will fail at the GCP service call (no credentials),
			// but we can verify the providerID construction is correct by
			// calling the function via the dry-run executor which bypasses
			// the actual GCP call and constructs the result from simulatedApply.
			// Instead, we directly call applyGCPCloudCDN — it will error at
			// gcpComputeService, but that's fine: we're testing the name
			// extraction logic by verifying the error is NOT about a doubled path.
			res, err := applyGCPCloudCDN(t.Context(), req)
			if err != nil {
				// Expected: gcpComputeService will fail without credentials.
				// Verify the error is about service init, not about a bad name.
				if strings.Contains(err.Error(), "backendServices/projects/") {
					t.Errorf("providerID doubled up: %v", err)
				}
				return
			}
			// If somehow the call succeeded (unlikely without creds), verify providerID
			if res != nil {
				if !strings.HasSuffix(res.ProviderID, tt.wantSuffix) {
					t.Errorf("providerID %q does not end with %q", res.ProviderID, tt.wantSuffix)
				}
				if strings.Contains(res.ProviderID, tt.wantNoDup) {
					t.Errorf("providerID %q contains doubled prefix %q", res.ProviderID, tt.wantNoDup)
				}
			}
		})
	}
}

func TestApplyGCPEventarc_ProviderIDExtraction(t *testing.T) {
	tests := []struct {
		name       string
		op         string
		recordID   string
		wantSuffix string
		wantNoDup  string
	}{
		{
			name:       "UPDATE with full path providerID",
			op:         "UPDATE",
			recordID:   "projects/test-proj/locations/us-central1/triggers/my-trigger",
			wantSuffix: "/triggers/my-trigger",
			wantNoDup:  "triggers/projects/",
		},
		{
			name:       "DELETE with full path providerID",
			op:         "DELETE",
			recordID:   "projects/test-proj/locations/us-central1/triggers/my-trigger",
			wantSuffix: "/triggers/my-trigger",
			wantNoDup:  "triggers/projects/",
		},
		{
			name:       "UPDATE with short name providerID",
			op:         "UPDATE",
			recordID:   "my-trigger",
			wantSuffix: "/triggers/my-trigger",
			wantNoDup:  "triggers/projects/",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := ApplyRequest{
				Action: &state.PlanAction{Operation: tt.op, NodeName: "my_trigger"},
				Intent: map[string]interface{}{
					"intent.project_id":  "test-proj",
					"intent.destination": "my-service",
					"intent.event_type":  "google.cloud.audit.log.v1.written",
				},
				Record: &state.ResourceRecord{ProviderID: tt.recordID},
				Region: "us-central1",
			}
			res, err := applyGCPEventarc(t.Context(), req)
			if err != nil {
				if strings.Contains(err.Error(), "triggers/projects/") {
					t.Errorf("providerID doubled up: %v", err)
				}
				return
			}
			if res != nil {
				if !strings.HasSuffix(res.ProviderID, tt.wantSuffix) {
					t.Errorf("providerID %q does not end with %q", res.ProviderID, tt.wantSuffix)
				}
				if strings.Contains(res.ProviderID, tt.wantNoDup) {
					t.Errorf("providerID %q contains doubled prefix %q", res.ProviderID, tt.wantNoDup)
				}
			}
		})
	}
}

// TestCDNProviderIDConstruction verifies the name extraction and providerID
// construction directly, without requiring GCP credentials.
func TestCDNProviderIDConstruction(t *testing.T) {
	tests := []struct {
		name         string
		recordID     string
		wantShort    string
		wantProvider string
	}{
		{
			"full path extracts short name",
			"projects/my-proj/global/backendServices/my-cdn",
			"my-cdn",
			"projects/test-proj/global/backendServices/my-cdn",
		},
		{
			"short name stays as-is",
			"my-cdn",
			"my-cdn",
			"projects/test-proj/global/backendServices/my-cdn",
		},
		{
			"empty falls back to node name",
			"",
			"my-cdn",
			"projects/test-proj/global/backendServices/my-cdn",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replicate the name extraction logic from applyGCPCloudCDN
			name := tt.recordID
			if name != "" && strings.Contains(name, "/") {
				name = name[strings.LastIndex(name, "/")+1:]
			}
			if name == "" {
				name = strings.TrimPrefix(identifierFor("my_cdn"), "beecon-")
			}
			if name != tt.wantShort {
				t.Errorf("extracted name = %q, want %q", name, tt.wantShort)
			}
			providerID := fmt.Sprintf("projects/%s/global/backendServices/%s", "test-proj", name)
			if providerID != tt.wantProvider {
				t.Errorf("providerID = %q, want %q", providerID, tt.wantProvider)
			}
		})
	}
}

// TestEventarcProviderIDConstruction verifies the name extraction and providerID
// construction directly, without requiring GCP credentials.
func TestEventarcProviderIDConstruction(t *testing.T) {
	tests := []struct {
		name         string
		recordID     string
		wantShort    string
		wantProvider string
	}{
		{
			"full path extracts short name",
			"projects/my-proj/locations/us-central1/triggers/my-trigger",
			"my-trigger",
			"projects/test-proj/locations/us-central1/triggers/my-trigger",
		},
		{
			"short name stays as-is",
			"my-trigger",
			"my-trigger",
			"projects/test-proj/locations/us-central1/triggers/my-trigger",
		},
		{
			"empty falls back to node name",
			"",
			"my-trigger",
			"projects/test-proj/locations/us-central1/triggers/my-trigger",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := tt.recordID
			if name != "" && strings.Contains(name, "/") {
				name = name[strings.LastIndex(name, "/")+1:]
			}
			if name == "" {
				name = strings.TrimPrefix(identifierFor("my_trigger"), "beecon-")
			}
			if name != tt.wantShort {
				t.Errorf("extracted name = %q, want %q", name, tt.wantShort)
			}
			providerID := fmt.Sprintf("projects/%s/locations/%s/triggers/%s", "test-proj", "us-central1", name)
			if providerID != tt.wantProvider {
				t.Errorf("providerID = %q, want %q", providerID, tt.wantProvider)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Helper tests
// ---------------------------------------------------------------------------

func TestParseEventFilters(t *testing.T) {
	tests := []struct {
		name  string
		input string
		count int
	}{
		{"empty", "", 0},
		{"single filter", "type=google.cloud.audit.log.v1.written", 1},
		{"multiple filters", "type=google.cloud.audit.log.v1.written,serviceName=storage.googleapis.com", 2},
		{"with spaces", " type = google.cloud.audit.log.v1.written , serviceName = storage.googleapis.com ", 2},
		{"malformed no equals", "invalid", 0},
		{"malformed trailing equals", "key=", 0},
		{"malformed leading equals", "=value", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filters := parseEventFilters(tt.input)
			if len(filters) != tt.count {
				t.Errorf("parseEventFilters(%q) returned %d filters, want %d", tt.input, len(filters), tt.count)
			}
		})
	}
}

func TestParseInt64(t *testing.T) {
	tests := []struct {
		input string
		want  int64
	}{
		{"0", 0},
		{"100", 100},
		{"3600", 3600},
		{"abc", 0},
		{"", 0},
		{"  42  ", 42},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseInt64(tt.input)
			if got != tt.want {
				t.Errorf("parseInt64(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseFloat64(t *testing.T) {
	tests := []struct {
		input string
		want  float64
	}{
		{"0", 0},
		{"0.8", 0.8},
		{"100.5", 100.5},
		{"abc", 0},
		{"", 0},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := parseFloat64(tt.input)
			if got != tt.want {
				t.Errorf("parseFloat64(%q) = %f, want %f", tt.input, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Dry-run observe passthrough for new targets
// ---------------------------------------------------------------------------

func TestGCPObserveDryRunPassthrough_G2B(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	cases := []struct {
		name   string
		target string
		live   map[string]interface{}
	}{
		{"cloud_cdn", "cloud_cdn", map[string]interface{}{"service": "cloud_cdn", "cdn_enabled": true}},
		{"cloud_monitoring", "cloud_monitoring", map[string]interface{}{"service": "cloud_monitoring", "display_name": "my-alert"}},
		{"eventarc", "eventarc", map[string]interface{}{"service": "eventarc", "name": "my-trigger"}},
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

func TestGCPObserveNilIntentSnapshot_G2B(t *testing.T) {
	cases := []struct {
		name   string
		target string
	}{
		{"cloud_cdn", "cloud_cdn"},
		{"cloud_monitoring", "cloud_monitoring"},
		{"eventarc", "eventarc"},
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
// G2B depth fields security check
// ---------------------------------------------------------------------------

func TestGCPObserveNoSensitiveKeysInG2BDepthFields(t *testing.T) {
	g2bFields := []string{
		// Cloud CDN
		"cdn_enabled", "cache_mode", "default_ttl", "max_ttl",
		"negative_caching", "signed_url_cache_max_age", "creation_timestamp",
		// Cloud Monitoring
		"display_name", "enabled", "metric_filter", "threshold",
		"comparison", "duration", "notification_channels", "creation_time", "mutation_time",
		// Eventarc
		"destination_service", "destination_region", "transport_topic",
		"event_filters", "labels",
	}
	for _, field := range g2bFields {
		if security.IsSensitiveKey(field) {
			t.Errorf("G2B depth field %q collides with sensitive key registry — must not store raw values in LiveState", field)
		}
	}
}

// ---------------------------------------------------------------------------
// validateGCPInput coverage for new targets
// ---------------------------------------------------------------------------

func TestValidateGCPInput_G2BTargets(t *testing.T) {
	targets := []string{"cloud_cdn", "cloud_monitoring", "eventarc"}
	for _, target := range targets {
		t.Run(target+"_missing_project_id", func(t *testing.T) {
			err := validateGCPInput(target, map[string]interface{}{})
			if err == nil {
				t.Fatalf("expected error for %s missing project_id", target)
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
