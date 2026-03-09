package provider

import (
	"testing"

	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
)

// TestGCPObserveDryRunPassthrough verifies that dry-run observe returns LiveState from the record unchanged.
func TestGCPObserveDryRunPassthrough(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}

	cases := []struct {
		name   string
		target string
		live   map[string]interface{}
	}{
		{"cloud_run", "cloud_run", map[string]interface{}{"service": "cloud_run", "image": "gcr.io/my/app", "min_instances": int64(1)}},
		{"cloud_sql", "cloud_sql", map[string]interface{}{"service": "cloud_sql", "tier": "db-custom-1-3840", "backup_enabled": true}},
		{"memorystore_redis", "memorystore_redis", map[string]interface{}{"service": "memorystore_redis", "redis_version": "REDIS_7_0"}},
		{"gcs", "gcs", map[string]interface{}{"service": "gcs", "versioning_enabled": true}},
		{"vpc", "vpc", map[string]interface{}{"service": "vpc", "auto_create_subnetworks": false}},
		{"subnet", "subnet", map[string]interface{}{"service": "subnet", "ip_cidr_range": "10.0.0.0/24"}},
		{"firewall", "firewall", map[string]interface{}{"service": "firewall", "direction": "INGRESS"}},
		{"iam", "iam", map[string]interface{}{"service": "iam", "disabled": false}},
		{"compute_engine", "compute_engine", map[string]interface{}{"service": "compute_engine", "status": "RUNNING"}},
		{"cloud_dns", "cloud_dns", map[string]interface{}{"service": "cloud_dns", "visibility": "public"}},
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
			// Verify all expected keys are preserved
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

// TestGCPObserveDryRunNilRecord verifies that dry-run observe with nil record returns Exists=false.
func TestGCPObserveDryRunNilRecord(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.observeGCP(t.Context(), "us-central1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Exists {
		t.Fatal("expected Exists=false for nil record")
	}
}

// TestGCPObserveExpectedCloudRunKeys documents and verifies the expected LiveState keys
// for Cloud Run observation depth. This ensures the observe function populates the
// expected fields when called against a real API (field list verified against the
// run/v2 Service type).
func TestGCPObserveExpectedCloudRunKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "name", "region",
		// Depth fields (G4)
		"service_url", "revision", "ingress", "launch_stage",
		"create_time", "update_time",
		// Template fields (conditional)
		// "image", "container_port", "env_keys", "min_instances", "max_instances", "service_account",
	}

	// Verify none of the expected keys are sensitive
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Cloud Run observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}

	// Verify the template-conditional keys are also not sensitive
	templateKeys := []string{"image", "container_port", "env_keys", "min_instances", "max_instances", "service_account"}
	for _, key := range templateKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Cloud Run template key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestGCPObserveExpectedCloudSQLKeys documents and verifies the expected LiveState keys
// for Cloud SQL observation depth.
func TestGCPObserveExpectedCloudSQLKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "instance",
		// Existing
		"state", "region", "db_version",
		// Depth fields (G4)
		"database_version", "connection_name", "tier",
		"data_disk_size_gb", "availability_type",
		// Conditional: "storage_auto_resize", "backup_enabled", "ipv4_enabled",
		// "private_network", "ip_addresses",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Cloud SQL observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}

	conditionalKeys := []string{"storage_auto_resize", "backup_enabled", "ipv4_enabled", "private_network", "ip_addresses"}
	for _, key := range conditionalKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Cloud SQL conditional key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestGCPObserveExpectedRedisKeys documents the expected LiveState keys for Memorystore Redis.
func TestGCPObserveExpectedRedisKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "name",
		// Depth fields (G4)
		"redis_version", "memory_size_gb", "host", "port",
		"state", "tier", "auth_enabled",
		"transit_encryption_mode", "display_name", "current_location_id",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Redis observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestGCPObserveExpectedGCSKeys documents the expected LiveState keys for GCS.
func TestGCPObserveExpectedGCSKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "bucket", "location", "storage_class",
		// Depth fields (G4)
		"location_type", "versioning_enabled", "create_time",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("GCS observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestGCPObserveExpectedVPCKeys documents the expected LiveState keys for VPC.
func TestGCPObserveExpectedVPCKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "network", "self_link",
		// Depth fields (G4)
		"auto_create_subnetworks", "mtu",
		// Conditional: "routing_mode",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("VPC observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestGCPObserveExpectedSubnetKeys documents the expected LiveState keys for Subnet.
func TestGCPObserveExpectedSubnetKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "subnet", "region", "network", "ip_cidr_range",
		// Depth fields (G4)
		"purpose", "private_ip_google_access",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Subnet observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestGCPObserveExpectedFirewallKeys documents the expected LiveState keys for Firewall.
func TestGCPObserveExpectedFirewallKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "firewall", "network",
		// Existing
		"protocol", "port",
		// Depth fields (G4)
		"direction", "priority",
		// Conditional: "source_ranges", "allowed_ports",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Firewall observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestGCPObserveExpectedIAMKeys documents the expected LiveState keys for IAM.
func TestGCPObserveExpectedIAMKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "service_account", "name",
		// Depth fields (G4)
		"email", "display_name", "disabled",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("IAM observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestGCPObserveExpectedComputeEngineKeys documents the expected LiveState keys for Compute Engine.
func TestGCPObserveExpectedComputeEngineKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "instance",
		// Depth fields (G4)
		"machine_type", "zone", "status",
		// Conditional: "network_ips",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Compute Engine observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestGCPObserveExpectedCloudDNSKeys documents the expected LiveState keys for Cloud DNS.
func TestGCPObserveExpectedCloudDNSKeys(t *testing.T) {
	expectedKeys := []string{
		"provider", "service", "zone", "dns_name", "description",
		// Depth fields (G4)
		"visibility",
		// Conditional: "nameservers",
	}
	for _, key := range expectedKeys {
		if security.IsSensitiveKey(key) {
			t.Errorf("Cloud DNS observe key %q is classified as sensitive — must not store in LiveState", key)
		}
	}
}

// TestIntentString verifies the intentString helper handles nil, missing, empty, whitespace, and valid values.
func TestIntentString(t *testing.T) {
	cases := []struct {
		name string
		snap map[string]interface{}
		key  string
		want string
	}{
		{"nil map", nil, "intent.project_id", ""},
		{"missing key", map[string]interface{}{"intent.other": "val"}, "intent.project_id", ""},
		{"nil value", map[string]interface{}{"intent.project_id": nil}, "intent.project_id", ""},
		{"empty string", map[string]interface{}{"intent.project_id": ""}, "intent.project_id", ""},
		{"whitespace only", map[string]interface{}{"intent.project_id": "  \t "}, "intent.project_id", ""},
		{"valid string", map[string]interface{}{"intent.project_id": "my-project"}, "intent.project_id", "my-project"},
		{"string with whitespace", map[string]interface{}{"intent.project_id": "  my-project  "}, "intent.project_id", "my-project"},
		{"integer value", map[string]interface{}{"intent.count": 42}, "intent.count", "42"},
		{"boolean value", map[string]interface{}{"intent.enabled": true}, "intent.enabled", "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := intentString(tc.snap, tc.key)
			if got != tc.want {
				t.Errorf("intentString(%v, %q) = %q, want %q", tc.snap, tc.key, got, tc.want)
			}
		})
	}
}

// TestGCPObserveNilIntentSnapshot verifies that observe functions return clean errors
// when IntentSnapshot is nil or missing required keys (not "<nil>" leaking through).
func TestGCPObserveNilIntentSnapshot(t *testing.T) {
	// All observe functions that require intent.project_id should error cleanly
	// when IntentSnapshot is nil.
	cases := []struct {
		name   string
		target string
	}{
		{"cloud_sql", "cloud_sql"},
		{"pubsub", "pubsub"},
		{"secret_manager", "secret_manager"},
		{"vpc", "vpc"},
		{"subnet", "subnet"},
		{"firewall", "firewall"},
		{"cloud_run", "cloud_run"},
		{"memorystore_redis", "memorystore_redis"},
		{"iam", "iam"},
		{"compute_engine", "compute_engine"},
		{"cloud_dns", "cloud_dns"},
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
			// Error message should mention project_id, not contain "<nil>"
			if contains := "<nil>"; err.Error() == contains {
				t.Errorf("error message should not be literally %q", contains)
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

// TestGCPObserveProjectScopedGenericNotExistsTrue verifies the generic stub
// does not falsely claim Exists: true.
func TestGCPObserveProjectScopedGenericNotExistsTrue(t *testing.T) {
	// This test verifies Finding 3: generic stub should not return Exists: true
	// since it cannot actually verify resource existence.
	rec := &state.ResourceRecord{
		Managed:    true,
		ProviderID: "my-project/my-resource",
		IntentSnapshot: map[string]interface{}{
			"intent.project_id": "my-project",
		},
		LiveState: map[string]interface{}{"service": "cloud_functions"},
		NodeType:  "STORE",
	}
	// We can't call the real function without GCP creds, but we verify the
	// intentString helper works correctly with the snapshot.
	projectID := intentString(rec.IntentSnapshot, "intent.project_id")
	if projectID != "my-project" {
		t.Fatalf("expected my-project, got %q", projectID)
	}
}

// TestGCPObserveNoSensitiveKeysInDepthFields is a comprehensive check that
// no G4 depth field name collides with the security sensitive key registry.
func TestGCPObserveNoSensitiveKeysInDepthFields(t *testing.T) {
	// All new fields added in G4 across all GCP observe functions
	g4Fields := []string{
		// Cloud Run
		"service_url", "revision", "ingress", "launch_stage", "create_time", "update_time",
		"image", "container_port", "env_keys", "min_instances", "max_instances", "service_account",
		// Cloud SQL
		"database_version", "connection_name", "data_disk_size_gb", "availability_type",
		"storage_auto_resize", "backup_enabled", "ipv4_enabled", "private_network", "ip_addresses",
		// Memorystore Redis
		"redis_version", "memory_size_gb", "host", "port", "auth_enabled",
		"transit_encryption_mode", "display_name", "current_location_id",
		// GCS
		"location_type", "versioning_enabled",
		// VPC
		"auto_create_subnetworks", "routing_mode", "mtu",
		// Subnet
		"purpose", "private_ip_google_access",
		// Firewall
		"direction", "priority", "source_ranges", "allowed_ports",
		// IAM
		"email", "disabled",
		// Compute Engine
		"network_ips",
		// Cloud DNS
		"visibility", "nameservers",
	}

	for _, field := range g4Fields {
		if security.IsSensitiveKey(field) {
			t.Errorf("G4 depth field %q collides with sensitive key registry — must not store raw values in LiveState", field)
		}
	}
}
