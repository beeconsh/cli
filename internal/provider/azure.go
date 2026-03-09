package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/msi/armmsi"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork/v5"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/service"
	"github.com/terracotta-ai/beecon/internal/logging"
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
	switch target {
	case "blob_storage":
		return applyAzureBlobStorage(ctx, req)
	case "key_vault_secret":
		return applyAzureKeyVaultSecret(ctx, req)
	case "vnet":
		return applyAzureVNet(ctx, req)
	case "subnet":
		return applyAzureSubnet(ctx, req)
	case "nsg":
		return applyAzureNSG(ctx, req)
	case "managed_identity":
		return applyAzureManagedIdentity(ctx, req)
	case "container_apps", "postgres_flexible", "mysql_flexible", "azure_cache_redis", "functions", "api_management", "service_bus", "event_grid", "front_door", "cdn", "dns", "monitor", "aks", "vm":
		return applyAzureGenericResource(ctx, target, req)
	case "rbac":
		return applyAzureRBAC(ctx, req)
	case "entra_id":
		return applyAzureEntraID(ctx, req)
	default:
		return nil, fmt.Errorf("azure target %q is recognized but requires additional adapter implementation for live execution (set BEECON_EXECUTE!=1 for dry-run)", target)
	}
}

func (e *DefaultExecutor) observeAzure(ctx context.Context, region string, rec *state.ResourceRecord) (*ObserveResult, error) {
	_ = ctx
	_ = region
	if e.dryRun {
		if rec == nil {
			return &ObserveResult{Exists: false, LiveState: map[string]interface{}{}}, nil
		}
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
	if rec == nil {
		return &ObserveResult{Exists: false, LiveState: map[string]interface{}{}}, nil
	}
	switch detectAzureRecordTarget(rec) {
	case "blob_storage":
		return observeAzureBlobStorage(ctx, rec)
	case "key_vault_secret":
		return observeAzureKeyVaultSecret(ctx, rec)
	case "vnet":
		return observeAzureVNet(ctx, rec)
	case "subnet":
		return observeAzureSubnet(ctx, rec)
	case "nsg":
		return observeAzureNSG(ctx, rec)
	case "managed_identity":
		return observeAzureManagedIdentity(ctx, rec)
	case "container_apps", "postgres_flexible", "mysql_flexible", "azure_cache_redis", "functions", "api_management", "service_bus", "event_grid", "front_door", "cdn", "dns", "monitor", "aks", "vm":
		return observeAzureGenericResource(ctx, detectAzureRecordTarget(rec), rec)
	case "rbac":
		return observeAzureRBAC(ctx, rec)
	case "entra_id":
		return observeAzureEntraID(ctx, rec)
	default:
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
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
		case strings.Contains(engine, "entra"), strings.Contains(engine, "aad"), strings.Contains(engine, "azuread"):
			return "entra_id"
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
		if err := required("resource_group", "location", "account_tier"); err != nil {
			return err
		}
		if strings.TrimSpace(intent(intentMap, "account_name")) == "" && strings.TrimSpace(intent(intentMap, "account_url")) == "" {
			return fmt.Errorf("blob_storage missing required fields: intent.account_name or intent.account_url")
		}
		return nil
	case "postgres_flexible", "mysql_flexible":
		return required("resource_group", "location", "sku", "version", "admin_username", "admin_password")
	case "container_apps":
		return required("resource_group", "location", "image", "environment_id")
	case "vnet", "nsg":
		return required("subscription_id", "resource_group", "location")
	case "subnet":
		return required("subscription_id", "resource_group", "location", "vnet_name")
	case "key_vault_secret":
		return required("vault_url")
	case "managed_identity":
		return required("subscription_id", "resource_group", "location")
	case "azure_cache_redis", "functions", "api_management", "service_bus", "event_grid", "front_door", "cdn", "dns", "monitor", "aks", "vm":
		return required("subscription_id", "resource_group", "location")
	case "rbac":
		if strings.TrimSpace(intent(intentMap, "scope")) == "" || strings.TrimSpace(intent(intentMap, "role_definition_id")) == "" || strings.TrimSpace(intent(intentMap, "principal_id")) == "" {
			return fmt.Errorf("rbac missing required fields: intent.scope, intent.role_definition_id, intent.principal_id")
		}
		return nil
	case "entra_id":
		if strings.TrimSpace(intent(intentMap, "tenant_id")) == "" {
			return fmt.Errorf("entra_id requires intent.tenant_id")
		}
		return nil
	default:
		return nil
	}
}

func applyAzureBlobStorage(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential init: %w", err)
	}
	accountURL := strings.TrimRight(intent(req.Intent, "account_url"), "/")
	if accountURL == "" {
		accountName := strings.TrimSpace(intent(req.Intent, "account_name"))
		if accountName == "" {
			return nil, fmt.Errorf("blob_storage requires intent.account_name or intent.account_url")
		}
		accountURL = fmt.Sprintf("https://%s.blob.core.windows.net", accountName)
	}
	containerName := strings.TrimSpace(intent(req.Intent, "container_name"))
	if containerName == "" {
		containerName = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	svc, err := service.NewClient(accountURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("blob service client init: %w", err)
	}
	container := svc.NewContainerClient(containerName)
	switch req.Action.Operation {
	case "CREATE":
		_, err = container.Create(ctx, nil)
		if err != nil && azureStatusCode(err) != 409 {
			return nil, fmt.Errorf("blob container create: %w", err)
		}
	case "UPDATE":
		_, err = container.GetProperties(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("blob container get properties: %w", err)
		}
	case "DELETE":
		_, err = container.Delete(ctx, nil)
		if err != nil && !isAzureNotFound(err) {
			return nil, fmt.Errorf("blob container delete: %w", err)
		}
	}
	providerID := fmt.Sprintf("%s/%s", accountURL, containerName)
	return &ApplyResult{
		ProviderID: providerID,
		LiveState: map[string]interface{}{
			"provider":    "azure",
			"service":     "blob_storage",
			"account_url": accountURL,
			"container":   containerName,
			"operation":   req.Action.Operation,
		},
	}, nil
}

func observeAzureBlobStorage(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	accountURL, containerName := azureBlobRecordIdentity(rec)
	if accountURL == "" || containerName == "" {
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential init: %w", err)
	}
	svc, err := service.NewClient(accountURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("blob service client init: %w", err)
	}
	containerClient := svc.NewContainerClient(containerName)
	props, err := containerClient.GetProperties(ctx, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: fmt.Sprintf("%s/%s", accountURL, containerName), LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("blob container get properties: %w", err)
	}
	// Extract account_name from the URL (https://<account>.blob.core.windows.net)
	accountName := ""
	if parts := strings.Split(strings.TrimPrefix(strings.TrimPrefix(accountURL, "https://"), "http://"), "."); len(parts) > 0 {
		accountName = parts[0]
	}
	providerID := fmt.Sprintf("%s/%s", accountURL, containerName)
	live := map[string]interface{}{
		"provider":    "azure",
		"service":     "blob_storage",
		"account_url": accountURL,
		"container":   containerName,
	}
	if accountName != "" {
		live["account_name"] = accountName
	}
	if props.LeaseStatus != nil {
		live["lease_status"] = string(*props.LeaseStatus)
	}
	if props.LastModified != nil {
		live["last_modified"] = props.LastModified.UTC().Format(time.RFC3339)
	}
	return &ObserveResult{
		Exists:     true,
		ProviderID: providerID,
		LiveState:  live,
	}, nil
}

func applyAzureKeyVaultSecret(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential init: %w", err)
	}
	vaultURL := strings.TrimRight(strings.TrimSpace(intent(req.Intent, "vault_url")), "/")
	if vaultURL == "" {
		return nil, fmt.Errorf("key_vault_secret requires intent.vault_url")
	}
	secretName := strings.TrimSpace(intent(req.Intent, "secret_name"))
	if secretName == "" {
		secretName = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	client, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("key vault client init: %w", err)
	}
	switch req.Action.Operation {
	case "CREATE", "UPDATE":
		value := defaultString(
			intent(req.Intent, "value"),
			defaultString(intent(req.Intent, "secret_value"), defaultString(intent(req.Intent, "password"), intent(req.Intent, "token"))),
		)
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("key_vault_secret requires secret value in intent.value (or intent.secret_value/password/token)")
		}
		_, err = client.SetSecret(ctx, secretName, azsecrets.SetSecretParameters{Value: &value}, nil)
		if err != nil {
			return nil, fmt.Errorf("key vault set secret: %w", err)
		}
	case "DELETE":
		_, err = client.DeleteSecret(ctx, secretName, nil)
		if err != nil && !isAzureNotFound(err) {
			return nil, fmt.Errorf("key vault delete secret: %w", err)
		}
	}
	providerID := fmt.Sprintf("%s/secrets/%s", vaultURL, secretName)
	return &ApplyResult{
		ProviderID: providerID,
		LiveState: map[string]interface{}{
			"provider":  "azure",
			"service":   "key_vault_secret",
			"vault_url": vaultURL,
			"name":      secretName,
			"operation": req.Action.Operation,
		},
	}, nil
}

func observeAzureKeyVaultSecret(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	vaultURL := strings.TrimRight(strings.TrimSpace(intent(rec.IntentSnapshot, "vault_url")), "/")
	secretName := strings.TrimSpace(intent(rec.IntentSnapshot, "secret_name"))
	if secretName == "" {
		if _, parsedName, ok := parseAzureKeyVaultProviderID(rec.ProviderID); ok {
			secretName = parsedName
		}
	}
	if secretName == "" {
		secretName = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	if vaultURL == "" {
		if parsedURL, _, ok := parseAzureKeyVaultProviderID(rec.ProviderID); ok {
			vaultURL = parsedURL
		}
	}
	if vaultURL == "" {
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential init: %w", err)
	}
	client, err := azsecrets.NewClient(vaultURL, cred, nil)
	if err != nil {
		return nil, fmt.Errorf("key vault client init: %w", err)
	}
	resp, err := client.GetSecret(ctx, secretName, "", nil)
	if err != nil {
		if isAzureNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: fmt.Sprintf("%s/secrets/%s", vaultURL, secretName), LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("key vault get secret: %w", err)
	}
	providerID := fmt.Sprintf("%s/secrets/%s", vaultURL, secretName)
	live := map[string]interface{}{
		"provider":  "azure",
		"service":   "key_vault_secret",
		"vault_url": vaultURL,
		"name":      secretName,
	}
	if resp.ID != nil {
		live["id"] = *resp.ID
	}
	if resp.ContentType != nil {
		live["content_type"] = *resp.ContentType
	}
	// Scrub secret value — never include in LiveState
	if resp.Attributes != nil {
		if resp.Attributes.Enabled != nil {
			live["enabled"] = *resp.Attributes.Enabled
		}
		if resp.Attributes.Expires != nil {
			live["expires"] = resp.Attributes.Expires.UTC().Format(time.RFC3339)
		}
		if resp.Attributes.Created != nil {
			live["created"] = resp.Attributes.Created.UTC().Format(time.RFC3339)
		}
		if resp.Attributes.Updated != nil {
			live["updated"] = resp.Attributes.Updated.UTC().Format(time.RFC3339)
		}
	}
	return &ObserveResult{Exists: true, ProviderID: providerID, LiveState: live}, nil
}

func applyAzureVNet(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	factory, subID, rg, location, err := azureNetworkFactory(ctx, req.Intent)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(intent(req.Intent, "vnet_name"))
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	addressPrefix := defaultString(intent(req.Intent, "address_prefix"), "10.0.0.0/16")
	client := factory.NewVirtualNetworksClient()
	switch req.Action.Operation {
	case "CREATE", "UPDATE":
		poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armnetwork.VirtualNetwork{
			Location: strPtr(location),
			Properties: &armnetwork.VirtualNetworkPropertiesFormat{
				AddressSpace: &armnetwork.AddressSpace{
					AddressPrefixes: []*string{strPtr(addressPrefix)},
				},
			},
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("azure vnet create/update: %w", err)
		}
		resp, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("azure vnet create/update poll: %w", err)
		}
		return &ApplyResult{
			ProviderID: defaultString(valueOrEmpty(resp.ID), name),
			LiveState: map[string]interface{}{
				"provider":      "azure",
				"service":       "vnet",
				"subscription":  subID,
				"resource_group": rg,
				"name":          name,
				"location":      location,
				"address_prefix": addressPrefix,
				"operation":     req.Action.Operation,
			},
		}, nil
	case "DELETE":
		poller, err := client.BeginDelete(ctx, rg, name, nil)
		if err != nil {
			if isAzureNotFound(err) {
				return &ApplyResult{ProviderID: name, LiveState: map[string]interface{}{"provider": "azure", "service": "vnet", "name": name, "operation": req.Action.Operation}}, nil
			}
			return nil, fmt.Errorf("azure vnet delete: %w", err)
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil && !isAzureNotFound(err) {
			return nil, fmt.Errorf("azure vnet delete poll: %w", err)
		}
		return &ApplyResult{ProviderID: name, LiveState: map[string]interface{}{"provider": "azure", "service": "vnet", "name": name, "operation": req.Action.Operation}}, nil
	default:
		return nil, fmt.Errorf("unsupported operation %q", req.Action.Operation)
	}
}

func observeAzureVNet(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	factory, _, rg, _, err := azureNetworkFactory(ctx, rec.IntentSnapshot)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(intent(rec.IntentSnapshot, "vnet_name"))
	if name == "" {
		name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	out, err := factory.NewVirtualNetworksClient().Get(ctx, rg, name, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("azure vnet get: %w", err)
	}
	live := map[string]interface{}{"provider": "azure", "service": "vnet", "name": name}
	if out.Location != nil {
		live["location"] = *out.Location
	}
	if out.Properties != nil {
		if out.Properties.ProvisioningState != nil {
			live["provisioning_state"] = string(*out.Properties.ProvisioningState)
		}
		if out.Properties.AddressSpace != nil && out.Properties.AddressSpace.AddressPrefixes != nil {
			prefixes := make([]string, 0, len(out.Properties.AddressSpace.AddressPrefixes))
			for _, p := range out.Properties.AddressSpace.AddressPrefixes {
				if p != nil {
					prefixes = append(prefixes, *p)
				}
			}
			live["address_space"] = prefixes
		}
		if out.Properties.Subnets != nil {
			live["subnets_count"] = len(out.Properties.Subnets)
		}
	}
	return &ObserveResult{Exists: true, ProviderID: defaultString(valueOrEmpty(out.ID), name), LiveState: live}, nil
}

func applyAzureSubnet(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	factory, _, rg, _, err := azureNetworkFactory(ctx, req.Intent)
	if err != nil {
		return nil, err
	}
	vnetName := requiredAzureVNetName(req.Intent)
	subnetName := strings.TrimSpace(intent(req.Intent, "subnet_name"))
	if subnetName == "" {
		subnetName = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	addressPrefix := defaultString(intent(req.Intent, "subnet_prefix"), defaultString(intent(req.Intent, "address_prefix"), "10.0.1.0/24"))
	client := factory.NewSubnetsClient()
	switch req.Action.Operation {
	case "CREATE", "UPDATE":
		poller, err := client.BeginCreateOrUpdate(ctx, rg, vnetName, subnetName, armnetwork.Subnet{
			Properties: &armnetwork.SubnetPropertiesFormat{
				AddressPrefix: strPtr(addressPrefix),
			},
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("azure subnet create/update: %w", err)
		}
		resp, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("azure subnet create/update poll: %w", err)
		}
		return &ApplyResult{
			ProviderID: defaultString(valueOrEmpty(resp.ID), subnetName),
			LiveState: map[string]interface{}{
				"provider":      "azure",
				"service":       "subnet",
				"resource_group": rg,
				"vnet":          vnetName,
				"name":          subnetName,
				"address_prefix": addressPrefix,
				"operation":     req.Action.Operation,
			},
		}, nil
	case "DELETE":
		poller, err := client.BeginDelete(ctx, rg, vnetName, subnetName, nil)
		if err != nil {
			if isAzureNotFound(err) {
				return &ApplyResult{ProviderID: subnetName, LiveState: map[string]interface{}{"provider": "azure", "service": "subnet", "name": subnetName, "operation": req.Action.Operation}}, nil
			}
			return nil, fmt.Errorf("azure subnet delete: %w", err)
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil && !isAzureNotFound(err) {
			return nil, fmt.Errorf("azure subnet delete poll: %w", err)
		}
		return &ApplyResult{ProviderID: subnetName, LiveState: map[string]interface{}{"provider": "azure", "service": "subnet", "name": subnetName, "operation": req.Action.Operation}}, nil
	default:
		return nil, fmt.Errorf("unsupported operation %q", req.Action.Operation)
	}
}

func observeAzureSubnet(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	factory, _, rg, _, err := azureNetworkFactory(ctx, rec.IntentSnapshot)
	if err != nil {
		return nil, err
	}
	vnetName := requiredAzureVNetName(rec.IntentSnapshot)
	subnetName := strings.TrimSpace(intent(rec.IntentSnapshot, "subnet_name"))
	if subnetName == "" {
		subnetName = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	out, err := factory.NewSubnetsClient().Get(ctx, rg, vnetName, subnetName, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: subnetName, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("azure subnet get: %w", err)
	}
	live := map[string]interface{}{"provider": "azure", "service": "subnet", "name": subnetName, "vnet": vnetName}
	if out.Properties != nil {
		if out.Properties.AddressPrefix != nil {
			live["address_prefix"] = *out.Properties.AddressPrefix
		}
		if out.Properties.ProvisioningState != nil {
			live["provisioning_state"] = string(*out.Properties.ProvisioningState)
		}
		if out.Properties.NetworkSecurityGroup != nil && out.Properties.NetworkSecurityGroup.ID != nil {
			live["nsg_id"] = *out.Properties.NetworkSecurityGroup.ID
		}
	}
	return &ObserveResult{Exists: true, ProviderID: defaultString(valueOrEmpty(out.ID), subnetName), LiveState: live}, nil
}

func applyAzureNSG(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	factory, _, rg, location, err := azureNetworkFactory(ctx, req.Intent)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(intent(req.Intent, "nsg_name"))
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	client := factory.NewSecurityGroupsClient()
	switch req.Action.Operation {
	case "CREATE", "UPDATE":
		poller, err := client.BeginCreateOrUpdate(ctx, rg, name, armnetwork.SecurityGroup{Location: strPtr(location)}, nil)
		if err != nil {
			return nil, fmt.Errorf("azure nsg create/update: %w", err)
		}
		resp, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("azure nsg create/update poll: %w", err)
		}
		return &ApplyResult{
			ProviderID: defaultString(valueOrEmpty(resp.ID), name),
			LiveState: map[string]interface{}{
				"provider":      "azure",
				"service":       "nsg",
				"resource_group": rg,
				"name":          name,
				"location":      location,
				"operation":     req.Action.Operation,
			},
		}, nil
	case "DELETE":
		poller, err := client.BeginDelete(ctx, rg, name, nil)
		if err != nil {
			if isAzureNotFound(err) {
				return &ApplyResult{ProviderID: name, LiveState: map[string]interface{}{"provider": "azure", "service": "nsg", "name": name, "operation": req.Action.Operation}}, nil
			}
			return nil, fmt.Errorf("azure nsg delete: %w", err)
		}
		if _, err := poller.PollUntilDone(ctx, nil); err != nil && !isAzureNotFound(err) {
			return nil, fmt.Errorf("azure nsg delete poll: %w", err)
		}
		return &ApplyResult{ProviderID: name, LiveState: map[string]interface{}{"provider": "azure", "service": "nsg", "name": name, "operation": req.Action.Operation}}, nil
	default:
		return nil, fmt.Errorf("unsupported operation %q", req.Action.Operation)
	}
}

func observeAzureNSG(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	factory, _, rg, _, err := azureNetworkFactory(ctx, rec.IntentSnapshot)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(intent(rec.IntentSnapshot, "nsg_name"))
	if name == "" {
		name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	out, err := factory.NewSecurityGroupsClient().Get(ctx, rg, name, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("azure nsg get: %w", err)
	}
	live := map[string]interface{}{"provider": "azure", "service": "nsg", "name": name}
	if out.Location != nil {
		live["location"] = *out.Location
	}
	if out.Properties != nil {
		if out.Properties.ProvisioningState != nil {
			live["provisioning_state"] = string(*out.Properties.ProvisioningState)
		}
		if out.Properties.SecurityRules != nil {
			live["security_rules_count"] = len(out.Properties.SecurityRules)
		}
	}
	return &ObserveResult{Exists: true, ProviderID: defaultString(valueOrEmpty(out.ID), name), LiveState: live}, nil
}

func applyAzureManagedIdentity(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	factory, _, rg, location, err := azureMSIFactory(ctx, req.Intent)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(intent(req.Intent, "identity_name"))
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	client := factory.NewUserAssignedIdentitiesClient()
	switch req.Action.Operation {
	case "CREATE", "UPDATE":
		resp, err := client.CreateOrUpdate(ctx, rg, name, armmsi.Identity{
			Location: strPtr(location),
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("azure managed identity create/update: %w", err)
		}
		return &ApplyResult{
			ProviderID: defaultString(valueOrEmpty(resp.ID), name),
			LiveState: map[string]interface{}{
				"provider":      "azure",
				"service":       "managed_identity",
				"resource_group": rg,
				"name":          name,
				"location":      location,
				"operation":     req.Action.Operation,
			},
		}, nil
	case "DELETE":
		_, err := client.Delete(ctx, rg, name, nil)
		if err != nil && !isAzureNotFound(err) {
			return nil, fmt.Errorf("azure managed identity delete: %w", err)
		}
		return &ApplyResult{
			ProviderID: name,
			LiveState:  map[string]interface{}{"provider": "azure", "service": "managed_identity", "name": name, "operation": req.Action.Operation},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported operation %q", req.Action.Operation)
	}
}

func observeAzureManagedIdentity(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	factory, _, rg, _, err := azureMSIFactory(ctx, rec.IntentSnapshot)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(intent(rec.IntentSnapshot, "identity_name"))
	if name == "" {
		name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	resp, err := factory.NewUserAssignedIdentitiesClient().Get(ctx, rg, name, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("azure managed identity get: %w", err)
	}
	live := map[string]interface{}{
		"provider": "azure",
		"service":  "managed_identity",
		"name":     name,
	}
	if resp.Properties != nil {
		if resp.Properties.PrincipalID != nil {
			live["principal_id"] = *resp.Properties.PrincipalID
		}
		if resp.Properties.ClientID != nil {
			live["client_id"] = *resp.Properties.ClientID
		}
		if resp.Properties.TenantID != nil {
			live["tenant_id"] = *resp.Properties.TenantID
		}
	}
	return &ObserveResult{Exists: true, ProviderID: defaultString(valueOrEmpty(resp.ID), name), LiveState: live}, nil
}

func detectAzureRecordTarget(rec *state.ResourceRecord) string {
	if rec == nil {
		return "generic"
	}
	if svc := strings.ToLower(fmt.Sprint(rec.LiveState["service"])); svc != "" {
		switch svc {
		case "blob_storage":
			return "blob_storage"
		case "key_vault_secret":
			return "key_vault_secret"
		case "vnet":
			return "vnet"
		case "subnet":
			return "subnet"
		case "nsg":
			return "nsg"
		case "managed_identity":
			return "managed_identity"
		case "entra_id":
			return "entra_id"
		}
	}
	if rec.NodeType == "STORE" {
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		if strings.Contains(eng, "blob") || strings.Contains(eng, "storage") {
			return "blob_storage"
		}
		if strings.Contains(eng, "keyvault") || strings.Contains(eng, "secret") {
			return "key_vault_secret"
		}
		if strings.Contains(eng, "identity") {
			return "managed_identity"
		}
	}
	if rec.NodeType == "NETWORK" {
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		top := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.topology"]))
		switch {
		case strings.Contains(eng, "vnet"), strings.Contains(top, "vnet"):
			return "vnet"
		case strings.Contains(eng, "subnet"), strings.Contains(top, "subnet"):
			return "subnet"
		case strings.Contains(eng, "nsg"), strings.Contains(top, "nsg"), strings.Contains(top, "security_group"):
			return "nsg"
		}
	}
	if rec.NodeType == "SERVICE" {
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		if strings.Contains(eng, "identity") {
			return "managed_identity"
		}
		if strings.Contains(eng, "entra") || strings.Contains(eng, "aad") || strings.Contains(eng, "azuread") {
			return "entra_id"
		}
		if strings.Contains(eng, "container") {
			return "container_apps"
		}
		if strings.Contains(eng, "function") {
			return "functions"
		}
		if strings.Contains(eng, "aks") {
			return "aks"
		}
		if strings.Contains(eng, "api_management") || strings.Contains(eng, "apim") {
			return "api_management"
		}
		if strings.Contains(eng, "service_bus") {
			return "service_bus"
		}
	}
	if rec.NodeType == "COMPUTE" {
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		if strings.Contains(eng, "vm") {
			return "vm"
		}
		if strings.Contains(eng, "eventgrid") {
			return "event_grid"
		}
	}
	if rec.NodeType == "STORE" {
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		switch {
		case strings.Contains(eng, "postgres"):
			return "postgres_flexible"
		case strings.Contains(eng, "mysql"):
			return "mysql_flexible"
		case strings.Contains(eng, "redis"):
			return "azure_cache_redis"
		}
	}
	if rec.NodeType == "NETWORK" {
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		switch {
		case strings.Contains(eng, "dns"):
			return "dns"
		case strings.Contains(eng, "frontdoor"):
			return "front_door"
		case strings.Contains(eng, "cdn"):
			return "cdn"
		}
	}
	return "generic"
}

func azureBlobRecordIdentity(rec *state.ResourceRecord) (accountURL, containerName string) {
	accountURL = strings.TrimRight(strings.TrimSpace(intent(rec.IntentSnapshot, "account_url")), "/")
	if accountURL == "" {
		accountName := strings.TrimSpace(intent(rec.IntentSnapshot, "account_name"))
		if accountName != "" {
			accountURL = fmt.Sprintf("https://%s.blob.core.windows.net", accountName)
		}
	}
	containerName = strings.TrimSpace(intent(rec.IntentSnapshot, "container_name"))
	if containerName == "" {
		containerName = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	if rec.ProviderID != "" && strings.Contains(rec.ProviderID, "/") {
		i := strings.LastIndex(rec.ProviderID, "/")
		idAccount := strings.TrimSpace(rec.ProviderID[:i])
		idContainer := strings.TrimSpace(rec.ProviderID[i+1:])
		if accountURL == "" {
			accountURL = idAccount
		}
		if containerName == "" {
			containerName = idContainer
		}
	}
	return accountURL, containerName
}

func parseAzureKeyVaultProviderID(id string) (vaultURL, secretName string, ok bool) {
	trim := strings.TrimRight(strings.TrimSpace(id), "/")
	marker := "/secrets/"
	i := strings.LastIndex(trim, marker)
	if i == -1 {
		return "", "", false
	}
	vaultURL = trim[:i]
	secretName = trim[i+len(marker):]
	if vaultURL == "" || secretName == "" {
		return "", "", false
	}
	return vaultURL, secretName, true
}

// Deprecated: azureStatusCode is retained for backward compatibility. Use
// isAzureNotFound or isAzureTransient instead.
func azureStatusCode(err error) int {
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		return respErr.StatusCode
	}
	return 0
}

// isAzureNotFound checks whether an Azure error indicates that the target
// resource does not exist.
func isAzureNotFound(err error) bool {
	if err == nil {
		return false
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		if respErr.StatusCode == 404 {
			return true
		}
		switch respErr.ErrorCode {
		case "ResourceNotFound", "ResourceGroupNotFound":
			return true
		}
	}
	// String matching fallback (covers unknown wrappers)
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "not found") || strings.Contains(s, "notfound") || strings.Contains(s, "does not exist")
}

// isAzureTransient checks whether an Azure error is transient and safe to retry.
func isAzureTransient(err error) bool {
	if err == nil {
		return false
	}
	// Context-level timeouts and cancellations are transient
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var respErr *azcore.ResponseError
	if errors.As(err, &respErr) {
		switch respErr.StatusCode {
		case 429, 500, 502, 503, 504:
			return true
		}
		switch respErr.ErrorCode {
		case "ServerBusy", "TooManyRequests":
			return true
		}
	}
	// String matching fallback
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "temporarily unavailable") || strings.Contains(s, "rate limit")
}

// withAzureRetry retries an Azure operation up to 3 times with exponential
// backoff and jitter when isAzureTransient returns true. Non-transient errors
// are returned immediately.
func withAzureRetry(ctx context.Context, op string, fn func() error) error {
	const maxRetries = 3
	baseDelay := 500 * time.Millisecond

	var lastErr error
	for attempt := 0; attempt <= maxRetries; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !isAzureTransient(lastErr) {
			return lastErr
		}
		if attempt == maxRetries {
			break
		}
		// Exponential backoff: 500ms, 1s, 2s + jitter up to 25%
		delay := baseDelay << uint(attempt)
		jitter := time.Duration(rand.Int63n(int64(delay / 4)))
		delay += jitter

		logging.Logger.Warn("azure:retry", "op", op, "attempt", attempt+1, "delay", delay.String(), "error", lastErr.Error())

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}
	return lastErr
}

func azureNetworkFactory(ctx context.Context, intentMap map[string]interface{}) (*armnetwork.ClientFactory, string, string, string, error) {
	_ = ctx
	subID := strings.TrimSpace(intent(intentMap, "subscription_id"))
	if subID == "" {
		subID = strings.TrimSpace(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	}
	rg := strings.TrimSpace(intent(intentMap, "resource_group"))
	location := strings.TrimSpace(intent(intentMap, "location"))
	if subID == "" || rg == "" {
		return nil, "", "", "", fmt.Errorf("azure network adapters require intent.subscription_id and intent.resource_group")
	}
	if location == "" {
		location = "eastus"
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("azure credential init: %w", err)
	}
	factory, err := armnetwork.NewClientFactory(subID, cred, nil)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("azure network client factory init: %w", err)
	}
	return factory, subID, rg, location, nil
}

func azureMSIFactory(ctx context.Context, intentMap map[string]interface{}) (*armmsi.ClientFactory, string, string, string, error) {
	subID := strings.TrimSpace(intent(intentMap, "subscription_id"))
	if subID == "" {
		subID = strings.TrimSpace(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	}
	rg := strings.TrimSpace(intent(intentMap, "resource_group"))
	location := strings.TrimSpace(intent(intentMap, "location"))
	if subID == "" || rg == "" {
		return nil, "", "", "", fmt.Errorf("azure managed identity adapters require intent.subscription_id and intent.resource_group")
	}
	if location == "" {
		location = "eastus"
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("azure credential init: %w", err)
	}
	factory, err := armmsi.NewClientFactory(subID, cred, nil)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("azure msi client factory init: %w", err)
	}
	return factory, subID, rg, location, nil
}

type azureGenericDef struct {
	Type       string
	APIVersion string
	NameKey    string
}

var azureGenericDefs = map[string]azureGenericDef{
	"container_apps":    {Type: "Microsoft.App/containerApps", APIVersion: "2023-05-01", NameKey: "container_app_name"},
	"postgres_flexible": {Type: "Microsoft.DBforPostgreSQL/flexibleServers", APIVersion: "2023-12-01-preview", NameKey: "server_name"},
	"mysql_flexible":    {Type: "Microsoft.DBforMySQL/flexibleServers", APIVersion: "2023-12-30", NameKey: "server_name"},
	"azure_cache_redis": {Type: "Microsoft.Cache/Redis", APIVersion: "2024-03-01", NameKey: "cache_name"},
	"functions":         {Type: "Microsoft.Web/sites", APIVersion: "2023-12-01", NameKey: "function_app_name"},
	"api_management":    {Type: "Microsoft.ApiManagement/service", APIVersion: "2023-05-01-preview", NameKey: "apim_name"},
	"service_bus":       {Type: "Microsoft.ServiceBus/namespaces", APIVersion: "2022-10-01-preview", NameKey: "namespace_name"},
	"event_grid":        {Type: "Microsoft.EventGrid/topics", APIVersion: "2023-12-15-preview", NameKey: "topic_name"},
	"front_door":        {Type: "Microsoft.Cdn/profiles", APIVersion: "2024-02-01", NameKey: "profile_name"},
	"cdn":               {Type: "Microsoft.Cdn/profiles", APIVersion: "2024-02-01", NameKey: "profile_name"},
	"dns":               {Type: "Microsoft.Network/dnsZones", APIVersion: "2023-07-01-preview", NameKey: "zone_name"},
	"monitor":           {Type: "Microsoft.Insights/actionGroups", APIVersion: "2023-01-01", NameKey: "action_group_name"},
	"aks":               {Type: "Microsoft.ContainerService/managedClusters", APIVersion: "2024-01-01", NameKey: "cluster_name"},
	"vm":                {Type: "Microsoft.Compute/virtualMachines", APIVersion: "2024-03-01", NameKey: "vm_name"},
}

func applyAzureGenericResource(ctx context.Context, target string, req ApplyRequest) (*ApplyResult, error) {
	def, ok := azureGenericDefs[target]
	if !ok {
		return nil, fmt.Errorf("azure generic target not mapped: %s", target)
	}
	factory, subID, rg, location, err := azureResourcesFactory(ctx, req.Intent)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(intent(req.Intent, def.NameKey))
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	resourceID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s", subID, rg, def.Type, name)
	client := factory.NewClient()
	switch req.Action.Operation {
	case "CREATE", "UPDATE":
		payload := map[string]interface{}{
			"location": location,
			"tags": map[string]string{
				"managed-by": "beecon",
				"target":     target,
			},
			"properties": map[string]interface{}{},
		}
		raw, _ := json.Marshal(payload)
		poller, err := client.BeginCreateOrUpdateByID(ctx, resourceID, def.APIVersion, armresources.GenericResource{
			Location:   strPtr(location),
			Properties: raw,
			Tags: map[string]*string{
				"managed-by": strPtr("beecon"),
				"target":     strPtr(target),
			},
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("azure %s create/update: %w", target, err)
		}
		resp, err := poller.PollUntilDone(ctx, nil)
		if err != nil {
			return nil, fmt.Errorf("azure %s create/update poll: %w", target, err)
		}
		return &ApplyResult{
			ProviderID: defaultString(valueOrEmpty(resp.ID), resourceID),
			LiveState: map[string]interface{}{
				"provider":      "azure",
				"service":       target,
				"subscription":  subID,
				"resource_group": rg,
				"name":          name,
				"type":          def.Type,
				"operation":     req.Action.Operation,
			},
		}, nil
	case "DELETE":
		poller, err := client.BeginDeleteByID(ctx, resourceID, def.APIVersion, nil)
		if err != nil && !isAzureNotFound(err) {
			return nil, fmt.Errorf("azure %s delete: %w", target, err)
		}
		if err == nil {
			if _, err := poller.PollUntilDone(ctx, nil); err != nil && !isAzureNotFound(err) {
				return nil, fmt.Errorf("azure %s delete poll: %w", target, err)
			}
		}
		return &ApplyResult{ProviderID: resourceID, LiveState: map[string]interface{}{"provider": "azure", "service": target, "name": name, "operation": req.Action.Operation}}, nil
	default:
		return nil, fmt.Errorf("unsupported operation %q", req.Action.Operation)
	}
}

func observeAzureGenericResource(ctx context.Context, target string, rec *state.ResourceRecord) (*ObserveResult, error) {
	def, ok := azureGenericDefs[target]
	if !ok {
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
	factory, subID, rg, _, err := azureResourcesFactory(ctx, rec.IntentSnapshot)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(intent(rec.IntentSnapshot, def.NameKey))
	if name == "" {
		name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	resourceID := rec.ProviderID
	if resourceID == "" || !strings.HasPrefix(resourceID, "/subscriptions/") {
		resourceID = fmt.Sprintf("/subscriptions/%s/resourceGroups/%s/providers/%s/%s", subID, rg, def.Type, name)
	}
	out, err := factory.NewClient().GetByID(ctx, resourceID, def.APIVersion, nil)
	if err != nil {
		if isAzureNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: resourceID, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("azure %s get: %w", target, err)
	}
	live := map[string]interface{}{"provider": "azure", "service": target, "name": name, "type": def.Type}
	if out.Location != nil {
		live["location"] = *out.Location
	}
	return &ObserveResult{Exists: true, ProviderID: defaultString(valueOrEmpty(out.ID), resourceID), LiveState: live}, nil
}

func applyAzureRBAC(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	scope := strings.TrimSpace(intent(req.Intent, "scope"))
	roleDefinitionID := strings.TrimSpace(intent(req.Intent, "role_definition_id"))
	principalID := strings.TrimSpace(intent(req.Intent, "principal_id"))
	assignmentName := strings.TrimSpace(intent(req.Intent, "assignment_name"))
	if assignmentName == "" {
		assignmentName = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	providerID := fmt.Sprintf("%s/providers/Microsoft.Authorization/roleAssignments/%s", scope, assignmentName)
	if strings.EqualFold(req.Action.Operation, "DELETE") {
		return &ApplyResult{ProviderID: providerID, LiveState: map[string]interface{}{"provider": "azure", "service": "rbac", "scope": scope, "operation": req.Action.Operation}}, nil
	}
	return &ApplyResult{
		ProviderID: providerID,
		LiveState: map[string]interface{}{
			"provider":           "azure",
			"service":            "rbac",
			"scope":              scope,
			"role_definition_id": roleDefinitionID,
			"principal_id":       principalID,
			"operation":          req.Action.Operation,
		},
	}, nil
}

func observeAzureRBAC(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	_ = ctx
	if rec.ProviderID == "" {
		return &ObserveResult{Exists: rec.Managed, ProviderID: "", LiveState: rec.LiveState}, nil
	}
	return &ObserveResult{
		Exists:     true,
		ProviderID: rec.ProviderID,
		LiveState: map[string]interface{}{
			"provider": "azure",
			"service":  "rbac",
			"id":       rec.ProviderID,
		},
	}, nil
}

func applyAzureEntraID(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	_ = ctx
	tenantID := strings.TrimSpace(intent(req.Intent, "tenant_id"))
	domain := strings.TrimSpace(intent(req.Intent, "domain"))
	providerID := defaultString(req.RecordProviderID(), tenantID)
	if providerID == "" {
		providerID = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	return &ApplyResult{
		ProviderID: providerID,
		LiveState: map[string]interface{}{
			"provider":  "azure",
			"service":   "entra_id",
			"tenant_id": tenantID,
			"domain":    domain,
			"operation": req.Action.Operation,
		},
	}, nil
}

func observeAzureEntraID(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	_ = ctx
	tenantID := strings.TrimSpace(intent(rec.IntentSnapshot, "tenant_id"))
	domain := strings.TrimSpace(intent(rec.IntentSnapshot, "domain"))
	providerID := defaultString(rec.ProviderID, tenantID)
	if providerID == "" {
		providerID = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	return &ObserveResult{
		Exists:     true,
		ProviderID: providerID,
		LiveState: map[string]interface{}{
			"provider":  "azure",
			"service":   "entra_id",
			"tenant_id": tenantID,
			"domain":    domain,
		},
	}, nil
}

func azureResourcesFactory(ctx context.Context, intentMap map[string]interface{}) (*armresources.ClientFactory, string, string, string, error) {
	_ = ctx
	subID := strings.TrimSpace(intent(intentMap, "subscription_id"))
	if subID == "" {
		subID = strings.TrimSpace(os.Getenv("AZURE_SUBSCRIPTION_ID"))
	}
	rg := strings.TrimSpace(intent(intentMap, "resource_group"))
	location := strings.TrimSpace(intent(intentMap, "location"))
	if subID == "" || rg == "" {
		return nil, "", "", "", fmt.Errorf("azure generic adapters require intent.subscription_id and intent.resource_group")
	}
	if location == "" {
		location = "eastus"
	}
	cred, err := azidentity.NewDefaultAzureCredential(nil)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("azure credential init: %w", err)
	}
	factory, err := armresources.NewClientFactory(subID, cred, nil)
	if err != nil {
		return nil, "", "", "", fmt.Errorf("azure resources client factory init: %w", err)
	}
	return factory, subID, rg, location, nil
}

func requiredAzureVNetName(intentMap map[string]interface{}) string {
	return defaultString(strings.TrimSpace(intent(intentMap, "vnet_name")), strings.TrimSpace(intent(intentMap, "network")))
}

func strPtr(v string) *string {
	return &v
}

func valueOrEmpty(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
