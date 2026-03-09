package provider

import (
	"encoding/json"
	"testing"
)

func TestGetProviderCapabilities_AWS(t *testing.T) {
	cap := GetProviderCapabilities("aws")
	if cap == nil {
		t.Fatal("expected non-nil capabilities for aws")
	}
	if cap.Provider != "aws" {
		t.Fatalf("expected provider aws, got %s", cap.Provider)
	}
	if len(cap.Targets) != len(AWSSupportMatrix) {
		t.Fatalf("expected %d targets, got %d", len(AWSSupportMatrix), len(cap.Targets))
	}
	if cap.TotalReal+cap.TotalGeneric != len(cap.Targets) {
		t.Fatalf("total_real (%d) + total_generic (%d) != len(targets) (%d)",
			cap.TotalReal, cap.TotalGeneric, len(cap.Targets))
	}
	// All AWS targets should have real adapters.
	for _, tc := range cap.Targets {
		if tc.Adapter != "real" {
			t.Errorf("expected aws target %s to have real adapter, got %s", tc.Target, tc.Adapter)
		}
	}
}

func TestGetProviderCapabilities_GCP(t *testing.T) {
	cap := GetProviderCapabilities("gcp")
	if cap == nil {
		t.Fatal("expected non-nil capabilities for gcp")
	}
	if cap.Provider != "gcp" {
		t.Fatalf("expected provider gcp, got %s", cap.Provider)
	}
	if len(cap.Targets) != len(GCPSupportMatrix) {
		t.Fatalf("expected %d targets, got %d", len(GCPSupportMatrix), len(cap.Targets))
	}
	if cap.TotalReal+cap.TotalGeneric != len(cap.Targets) {
		t.Fatalf("total_real (%d) + total_generic (%d) != len(targets) (%d)",
			cap.TotalReal, cap.TotalGeneric, len(cap.Targets))
	}
}

func TestGetProviderCapabilities_Azure(t *testing.T) {
	cap := GetProviderCapabilities("azure")
	if cap == nil {
		t.Fatal("expected non-nil capabilities for azure")
	}
	if cap.Provider != "azure" {
		t.Fatalf("expected provider azure, got %s", cap.Provider)
	}
	if len(cap.Targets) != len(AzureSupportMatrix) {
		t.Fatalf("expected %d targets, got %d", len(AzureSupportMatrix), len(cap.Targets))
	}
	if cap.TotalReal+cap.TotalGeneric != len(cap.Targets) {
		t.Fatalf("total_real (%d) + total_generic (%d) != len(targets) (%d)",
			cap.TotalReal, cap.TotalGeneric, len(cap.Targets))
	}

	// Verify real vs generic classification for known targets.
	targetMap := make(map[string]TargetCapability, len(cap.Targets))
	for _, tc := range cap.Targets {
		targetMap[tc.Target] = tc
	}
	realExpected := []string{"blob_storage", "key_vault_secret", "vnet", "subnet", "nsg", "managed_identity", "rbac", "entra_id"}
	for _, name := range realExpected {
		tc, ok := targetMap[name]
		if !ok {
			t.Errorf("expected azure target %s to exist", name)
			continue
		}
		if tc.Adapter != "real" {
			t.Errorf("expected azure target %s to have real adapter, got %s", name, tc.Adapter)
		}
	}
	genericExpected := []string{"container_apps", "postgres_flexible", "functions", "aks"}
	for _, name := range genericExpected {
		tc, ok := targetMap[name]
		if !ok {
			t.Errorf("expected azure target %s to exist", name)
			continue
		}
		if tc.Adapter != "generic" {
			t.Errorf("expected azure target %s to have generic adapter, got %s", name, tc.Adapter)
		}
	}
}

func TestGetProviderCapabilities_Unknown(t *testing.T) {
	cap := GetProviderCapabilities("unknown")
	if cap != nil {
		t.Fatal("expected nil capabilities for unknown provider")
	}
}

func TestGetAllProviderCapabilities(t *testing.T) {
	all := GetAllProviderCapabilities()
	if len(all) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(all))
	}
	providers := map[string]bool{}
	for _, cap := range all {
		providers[cap.Provider] = true
	}
	for _, p := range []string{"aws", "gcp", "azure"} {
		if !providers[p] {
			t.Errorf("expected provider %s in results", p)
		}
	}
}

func TestProviderCapability_JSONRoundTrip(t *testing.T) {
	cap := GetProviderCapabilities("aws")
	data, err := json.Marshal(cap)
	if err != nil {
		t.Fatalf("json marshal: %v", err)
	}

	var decoded ProviderCapability
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("json unmarshal: %v", err)
	}
	if decoded.Provider != "aws" {
		t.Fatalf("expected provider aws after roundtrip, got %s", decoded.Provider)
	}
	if len(decoded.Targets) != len(cap.Targets) {
		t.Fatalf("expected %d targets after roundtrip, got %d", len(cap.Targets), len(decoded.Targets))
	}
	if decoded.TotalReal != cap.TotalReal {
		t.Fatalf("expected total_real %d after roundtrip, got %d", cap.TotalReal, decoded.TotalReal)
	}

	// Verify JSON has expected fields.
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("json unmarshal raw: %v", err)
	}
	for _, field := range []string{"provider", "targets", "total_real", "total_generic"} {
		if _, ok := raw[field]; !ok {
			t.Errorf("expected field %q in JSON output", field)
		}
	}
}

func TestTargetCapability_TierValues(t *testing.T) {
	validTiers := map[string]bool{"tier1": true, "tier2": true, "tier3": true}
	for _, provider := range []string{"aws", "gcp", "azure"} {
		cap := GetProviderCapabilities(provider)
		for _, tc := range cap.Targets {
			if !validTiers[tc.Tier] {
				t.Errorf("%s target %s has invalid tier %q", provider, tc.Target, tc.Tier)
			}
		}
	}
}

func TestTargetCapability_SortedOutput(t *testing.T) {
	for _, provider := range []string{"aws", "gcp", "azure"} {
		cap := GetProviderCapabilities(provider)
		for i := 1; i < len(cap.Targets); i++ {
			if cap.Targets[i].Target < cap.Targets[i-1].Target {
				t.Errorf("%s targets not sorted: %s before %s",
					provider, cap.Targets[i-1].Target, cap.Targets[i].Target)
			}
		}
	}
}
