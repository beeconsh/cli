package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/terracotta-ai/beecon/internal/state"
)

// AzureSupportMatrix lists planned Azure targets by tier.
var AzureSupportMatrix = map[string]string{
	"container_apps":    "tier1",
	"postgres_flexible": "tier1",
	"mysql_flexible":    "tier1",
	"azure_cache_redis": "tier1",
	"blob_storage":      "tier1",
	"vnet":              "tier1",
	"subnet":            "tier1",
	"nsg":               "tier1",
	"rbac":              "tier1",
	"managed_identity":  "tier1",
	"key_vault_secret":  "tier1",
	"functions":         "tier2",
	"api_management":    "tier2",
	"service_bus":       "tier2",
	"event_grid":        "tier2",
	"front_door":        "tier2",
	"cdn":               "tier2",
	"dns":               "tier2",
	"monitor":           "tier2",
	"aks":               "tier3",
	"event_grid_adv":    "tier3",
	"entra_id":          "tier3",
	"vm":                "tier3",
}

func (e *DefaultExecutor) applyAzure(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	target := detectAzureTarget(req)
	if e.dryRun {
		return simulatedApply(req, target), nil
	}
	if err := validateAzureInput(target, req.Intent); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("azure target %q is recognized but requires additional adapter implementation for live execution (set BEECON_EXECUTE!=1 for dry-run)", target)
}

func (e *DefaultExecutor) observeAzure(ctx context.Context, region string, rec *state.ResourceRecord) (*ObserveResult, error) {
	_ = ctx
	_ = region
	if rec == nil {
		return &ObserveResult{Exists: false, LiveState: map[string]interface{}{}}, nil
	}
	return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
}

func detectAzureTarget(req ApplyRequest) string {
	nodeType := strings.ToUpper(req.Action.NodeType)
	engine := strings.ToLower(intent(req.Intent, "engine", "type", "runtime", "service", "topology", "resource"))
	expose := strings.ToLower(intent(req.Intent, "expose"))
	if nodeType == "STORE" {
		switch {
		case strings.Contains(engine, "postgres"):
			return "postgres_flexible"
		case strings.Contains(engine, "mysql"):
			return "mysql_flexible"
		case strings.Contains(engine, "redis"):
			return "azure_cache_redis"
		case strings.Contains(engine, "blob"), strings.Contains(engine, "storage"):
			return "blob_storage"
		case strings.Contains(engine, "keyvault"), strings.Contains(engine, "secret"):
			return "key_vault_secret"
		}
	}
	if nodeType == "NETWORK" {
		switch {
		case strings.Contains(engine, "vnet"):
			return "vnet"
		case strings.Contains(engine, "subnet"):
			return "subnet"
		case strings.Contains(engine, "nsg"):
			return "nsg"
		case strings.Contains(engine, "frontdoor"):
			return "front_door"
		case strings.Contains(engine, "cdn"):
			return "cdn"
		case strings.Contains(engine, "dns"):
			return "dns"
		}
	}
	if nodeType == "SERVICE" {
		switch {
		case strings.Contains(engine, "container"):
			return "container_apps"
		case strings.Contains(engine, "function"):
			return "functions"
		case strings.Contains(engine, "aks"):
			return "aks"
		case strings.Contains(expose, "api"):
			return "api_management"
		}
	}
	if nodeType == "COMPUTE" {
		switch {
		case strings.Contains(engine, "eventgrid"):
			return "event_grid"
		case strings.Contains(engine, "vm"):
			return "vm"
		}
	}
	for _, v := range req.Intent {
		s := strings.ToLower(fmt.Sprint(v))
		for target := range AzureSupportMatrix {
			if strings.Contains(s, target) || strings.Contains(s, strings.ReplaceAll(target, "_", "")) {
				return target
			}
		}
	}
	return "generic"
}

func validateAzureInput(target string, intentMap map[string]interface{}) error {
	required := func(fields ...string) error {
		missing := []string{}
		for _, k := range fields {
			if strings.TrimSpace(intent(intentMap, k)) == "" {
				missing = append(missing, "intent."+k)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("%s missing required fields: %s", target, strings.Join(missing, ", "))
		}
		return nil
	}

	switch target {
	case "blob_storage":
		return required("resource_group", "location", "account_tier")
	case "postgres_flexible", "mysql_flexible":
		return required("resource_group", "location", "sku", "version", "admin_username", "admin_password")
	case "container_apps":
		return required("resource_group", "location", "image", "environment_id")
	case "subnet", "nsg", "vnet":
		return required("resource_group", "location")
	default:
		return nil
	}
}
