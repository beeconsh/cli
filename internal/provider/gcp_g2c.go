package provider

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/terracotta-ai/beecon/internal/logging"
	"github.com/terracotta-ai/beecon/internal/state"
	"google.golang.org/api/apigateway/v1"
	identitytoolkit "google.golang.org/api/identitytoolkit/v2"
	"google.golang.org/api/option"
)

// ---------------------------------------------------------------------------
// API Gateway — multi-step lifecycle (API + Config + Gateway)
// ---------------------------------------------------------------------------

func gcpAPIGatewayService(ctx context.Context) (*apigateway.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := apigateway.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp api_gateway service init: %w", err)
		}
		return svc, nil
	}
	svc, err := apigateway.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp api_gateway service init: %w", err)
	}
	return svc, nil
}

func applyGCPAPIGateway(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	if projectID == "" {
		return nil, fmt.Errorf("api_gateway requires intent.project_id")
	}
	location := defaultString(intent(req.Intent, "location", "region"), req.Region)
	if location == "" {
		location = "us-central1"
	}
	name := req.RecordProviderID()
	if name != "" && strings.Contains(name, "/") {
		name = name[strings.LastIndex(name, "/")+1:]
	}
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}

	displayName := defaultString(intent(req.Intent, "display_name"), name)
	configName := defaultString(intent(req.Intent, "config_name"), name+"-config")

	svc, err := gcpAPIGatewayService(ctx)
	if err != nil {
		return nil, err
	}

	// API resource path: projects/{project}/locations/global/apis/{api}
	apiParent := fmt.Sprintf("projects/%s/locations/global", projectID)
	apiFullName := fmt.Sprintf("%s/apis/%s", apiParent, name)
	// Config path: projects/{project}/locations/global/apis/{api}/configs/{config}
	configFullName := fmt.Sprintf("%s/configs/%s", apiFullName, configName)
	// Gateway path: projects/{project}/locations/{location}/gateways/{gateway}
	gwParent := fmt.Sprintf("projects/%s/locations/%s", projectID, location)
	gwFullName := fmt.Sprintf("%s/gateways/%s", gwParent, name)

	result := &ApplyResult{
		ProviderID: gwFullName,
		LiveState: map[string]interface{}{
			"provider":     "gcp",
			"service":      "api_gateway",
			"project":      projectID,
			"location":     location,
			"name":         name,
			"display_name": displayName,
			"operation":    req.Action.Operation,
		},
	}

	switch req.Action.Operation {
	case "CREATE":
		// Step 1: Create API resource
		apiRes := &apigateway.ApigatewayApi{
			DisplayName: displayName,
		}
		if err := withGCPRetry(ctx, "api_gateway_create_api", func() error {
			_, err := svc.Projects.Locations.Apis.Create(apiParent, apiRes).ApiId(name).Context(ctx).Do()
			if err != nil && isAlreadyExists(err) {
				return nil
			}
			return err
		}); err != nil {
			return nil, fmt.Errorf("api_gateway create api: %w", err)
		}
		result.LiveState["api_name"] = apiFullName

		// Step 2: Create API Config
		apiConfig := &apigateway.ApigatewayApiConfig{
			DisplayName: configName,
		}
		if sa := intent(req.Intent, "gateway_service_account"); sa != "" {
			apiConfig.GatewayServiceAccount = sa
		}
		if err := withGCPRetry(ctx, "api_gateway_create_config", func() error {
			_, err := svc.Projects.Locations.Apis.Configs.Create(apiFullName, apiConfig).ApiConfigId(configName).Context(ctx).Do()
			if err != nil && isAlreadyExists(err) {
				return nil
			}
			return err
		}); err != nil {
			// Return partial result: API was created but config failed
			return result, fmt.Errorf("api_gateway create config: %w", err)
		}
		result.LiveState["config_name"] = configFullName

		// Step 3: Create Gateway
		gw := &apigateway.ApigatewayGateway{
			ApiConfig:   configFullName,
			DisplayName: displayName,
		}
		if err := withGCPRetry(ctx, "api_gateway_create_gateway", func() error {
			_, err := svc.Projects.Locations.Gateways.Create(gwParent, gw).GatewayId(name).Context(ctx).Do()
			if err != nil && isAlreadyExists(err) {
				return nil
			}
			return err
		}); err != nil {
			// Return partial result: API + Config created but gateway failed
			return result, fmt.Errorf("api_gateway create gateway: %w", err)
		}
		result.LiveState["gateway_name"] = gwFullName

	case "UPDATE":
		// Step 1: Patch API Config
		patchConfig := &apigateway.ApigatewayApiConfig{
			DisplayName: configName,
		}
		if sa := intent(req.Intent, "gateway_service_account"); sa != "" {
			patchConfig.GatewayServiceAccount = sa
		}
		if err := withGCPRetry(ctx, "api_gateway_update_config", func() error {
			_, err := svc.Projects.Locations.Apis.Configs.Patch(configFullName, patchConfig).Context(ctx).Do()
			return err
		}); err != nil {
			return nil, fmt.Errorf("api_gateway update config: %w", err)
		}

		// Step 2: Patch Gateway
		patchGW := &apigateway.ApigatewayGateway{
			ApiConfig:   configFullName,
			DisplayName: displayName,
		}
		if err := withGCPRetry(ctx, "api_gateway_update_gateway", func() error {
			_, err := svc.Projects.Locations.Gateways.Patch(gwFullName, patchGW).Context(ctx).Do()
			return err
		}); err != nil {
			// Partial result: config updated but gateway patch failed
			return result, fmt.Errorf("api_gateway update gateway: %w", err)
		}

	case "DELETE":
		// Step 1: Delete Gateway
		if err := withGCPRetry(ctx, "api_gateway_delete_gateway", func() error {
			_, err := svc.Projects.Locations.Gateways.Delete(gwFullName).Context(ctx).Do()
			if err != nil && isGCPNotFound(err) {
				return nil
			}
			return err
		}); err != nil {
			return result, fmt.Errorf("api_gateway delete gateway: %w", err)
		}
		result.LiveState["gateway_deleted"] = true

		// Step 2: Delete Config
		if err := withGCPRetry(ctx, "api_gateway_delete_config", func() error {
			_, err := svc.Projects.Locations.Apis.Configs.Delete(configFullName).Context(ctx).Do()
			if err != nil && isGCPNotFound(err) {
				return nil
			}
			return err
		}); err != nil {
			return result, fmt.Errorf("api_gateway delete config: %w", err)
		}
		result.LiveState["config_deleted"] = true

		// Step 3: Delete API
		if err := withGCPRetry(ctx, "api_gateway_delete_api", func() error {
			_, err := svc.Projects.Locations.Apis.Delete(apiFullName).Context(ctx).Do()
			if err != nil && isGCPNotFound(err) {
				return nil
			}
			return err
		}); err != nil {
			return result, fmt.Errorf("api_gateway delete api: %w", err)
		}
		result.LiveState["api_deleted"] = true
	}

	return result, nil
}

func observeGCPAPIGateway(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := intentString(rec.IntentSnapshot, "intent.project_id")
	if projectID == "" {
		return nil, fmt.Errorf("api_gateway observe requires intent.project_id")
	}
	location := defaultString(intentString(rec.IntentSnapshot, "intent.location"), defaultString(intentString(rec.IntentSnapshot, "intent.region"), "us-central1"))

	name := rec.ProviderID
	if name != "" && strings.Contains(name, "/gateways/") {
		name = name[strings.LastIndex(name, "/")+1:]
	}
	if name == "" {
		name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}

	gwFullName := fmt.Sprintf("projects/%s/locations/%s/gateways/%s", projectID, location, name)

	svc, err := gcpAPIGatewayService(ctx)
	if err != nil {
		return nil, err
	}

	gw, err := svc.Projects.Locations.Gateways.Get(gwFullName).Context(ctx).Do()
	if err != nil {
		if isGCPNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: gwFullName, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("api_gateway get gateway: %w", err)
	}

	live := map[string]interface{}{
		"provider":         "gcp",
		"service":          "api_gateway",
		"display_name":     gw.DisplayName,
		"state":            gw.State,
		"api_config":       gw.ApiConfig,
		"default_hostname": gw.DefaultHostname,
		"create_time":      gw.CreateTime,
		"update_time":      gw.UpdateTime,
	}
	if len(gw.Labels) > 0 {
		labelParts := make([]string, 0, len(gw.Labels))
		for k, v := range gw.Labels {
			labelParts = append(labelParts, fmt.Sprintf("%s=%s", k, v))
		}
		live["labels"] = strings.Join(labelParts, ",")
	}

	return &ObserveResult{Exists: true, ProviderID: gwFullName, LiveState: live}, nil
}

// ---------------------------------------------------------------------------
// Identity Platform — tenant-based lifecycle
// ---------------------------------------------------------------------------

func gcpIdentityPlatformService(ctx context.Context) (*identitytoolkit.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := identitytoolkit.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp identity_platform service init: %w", err)
		}
		return svc, nil
	}
	svc, err := identitytoolkit.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp identity_platform service init: %w", err)
	}
	return svc, nil
}

func applyGCPIdentityPlatform(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	if projectID == "" {
		return nil, fmt.Errorf("identity_platform requires intent.project_id")
	}

	name := req.RecordProviderID()
	if name != "" && strings.Contains(name, "/tenants/") {
		name = name[strings.LastIndex(name, "/")+1:]
	}
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}

	displayName := defaultString(intent(req.Intent, "display_name"), name)
	parent := fmt.Sprintf("projects/%s", projectID)

	svc, err := gcpIdentityPlatformService(ctx)
	if err != nil {
		return nil, err
	}

	result := &ApplyResult{
		LiveState: map[string]interface{}{
			"provider":     "gcp",
			"service":      "identity_platform",
			"project":      projectID,
			"display_name": displayName,
			"operation":    req.Action.Operation,
		},
	}

	switch req.Action.Operation {
	case "CREATE":
		tenant := &identitytoolkit.GoogleCloudIdentitytoolkitAdminV2Tenant{
			DisplayName: displayName,
		}

		// Optional boolean fields
		if v := intent(req.Intent, "allow_password_signup"); v != "" {
			tenant.AllowPasswordSignup = v == "true"
		}
		if v := intent(req.Intent, "enable_email_link_signin"); v != "" {
			tenant.EnableEmailLinkSignin = v == "true"
		}

		// MFA config
		if mfaState := intent(req.Intent, "mfa_config"); mfaState != "" {
			tenant.MfaConfig = buildMFAConfig(mfaState)
		}

		var created *identitytoolkit.GoogleCloudIdentitytoolkitAdminV2Tenant
		if err := withGCPRetry(ctx, "identity_platform_create", func() error {
			var createErr error
			created, createErr = svc.Projects.Tenants.Create(parent, tenant).Context(ctx).Do()
			if createErr != nil && isAlreadyExists(createErr) {
				return nil
			}
			return createErr
		}); err != nil {
			return nil, fmt.Errorf("identity_platform create tenant: %w", err)
		}
		if created != nil {
			result.ProviderID = created.Name
			result.LiveState["tenant_id"] = created.Name
		} else {
			// Already-exists path — best-effort ID
			result.ProviderID = fmt.Sprintf("%s/tenants/%s", parent, name)
		}
		result.LiveState["allow_password_signup"] = tenant.AllowPasswordSignup
		result.LiveState["enable_email_link_signin"] = tenant.EnableEmailLinkSignin

	case "UPDATE":
		tenantName := req.RecordProviderID()
		if tenantName == "" {
			tenantName = fmt.Sprintf("%s/tenants/%s", parent, name)
		}
		// Ensure full path
		if !strings.Contains(tenantName, "/tenants/") {
			tenantName = fmt.Sprintf("%s/tenants/%s", parent, tenantName)
		}

		patch := &identitytoolkit.GoogleCloudIdentitytoolkitAdminV2Tenant{}
		if dn := intent(req.Intent, "display_name"); dn != "" {
			patch.DisplayName = dn
		}
		if v := intent(req.Intent, "allow_password_signup"); v != "" {
			patch.AllowPasswordSignup = v == "true"
		}
		if v := intent(req.Intent, "enable_email_link_signin"); v != "" {
			patch.EnableEmailLinkSignin = v == "true"
		}
		if mfaState := intent(req.Intent, "mfa_config"); mfaState != "" {
			patch.MfaConfig = buildMFAConfig(mfaState)
		}

		if err := withGCPRetry(ctx, "identity_platform_update", func() error {
			_, err := svc.Projects.Tenants.Patch(tenantName, patch).Context(ctx).Do()
			return err
		}); err != nil {
			return nil, fmt.Errorf("identity_platform update tenant: %w", err)
		}
		result.ProviderID = tenantName

	case "DELETE":
		tenantName := req.RecordProviderID()
		if tenantName == "" {
			tenantName = fmt.Sprintf("%s/tenants/%s", parent, name)
		}
		if !strings.Contains(tenantName, "/tenants/") {
			tenantName = fmt.Sprintf("%s/tenants/%s", parent, tenantName)
		}

		if err := withGCPRetry(ctx, "identity_platform_delete", func() error {
			_, err := svc.Projects.Tenants.Delete(tenantName).Context(ctx).Do()
			if err != nil && isGCPNotFound(err) {
				return nil
			}
			return err
		}); err != nil {
			return nil, fmt.Errorf("identity_platform delete tenant: %w", err)
		}
		result.ProviderID = tenantName
	}

	return result, nil
}

func observeGCPIdentityPlatform(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := intentString(rec.IntentSnapshot, "intent.project_id")
	if projectID == "" {
		return nil, fmt.Errorf("identity_platform observe requires intent.project_id")
	}

	tenantName := rec.ProviderID
	if tenantName == "" {
		name := strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
		tenantName = fmt.Sprintf("projects/%s/tenants/%s", projectID, name)
	}
	// Ensure full path
	if !strings.Contains(tenantName, "/tenants/") {
		tenantName = fmt.Sprintf("projects/%s/tenants/%s", projectID, tenantName)
	}

	svc, err := gcpIdentityPlatformService(ctx)
	if err != nil {
		return nil, err
	}
	tenant, err := svc.Projects.Tenants.Get(tenantName).Context(ctx).Do()
	if err != nil {
		if isGCPNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: tenantName, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("identity_platform get tenant: %w", err)
	}

	live := map[string]interface{}{
		"provider":                "gcp",
		"service":                 "identity_platform",
		"display_name":            tenant.DisplayName,
		"allow_password_signup":   tenant.AllowPasswordSignup,
		"enable_email_link_signin": tenant.EnableEmailLinkSignin,
	}
	if tenant.MfaConfig != nil {
		live["mfa_state"] = tenant.MfaConfig.State
		if len(tenant.MfaConfig.EnabledProviders) > 0 {
			live["mfa_providers"] = strings.Join(tenant.MfaConfig.EnabledProviders, ",")
		}
	}
	// Scrub test phone numbers: store count only, never actual numbers
	if tenant.TestPhoneNumbers != nil {
		live["test_phone_numbers_count"] = len(tenant.TestPhoneNumbers)
	}

	logging.Logger.Debug("identity_platform:observe", "tenant", tenantName, "display_name", tenant.DisplayName)

	return &ObserveResult{Exists: true, ProviderID: tenantName, LiveState: live}, nil
}

// buildMFAConfig parses an mfa_config intent value into the MFA config struct.
// Accepts values: "DISABLED", "ENABLED", "MANDATORY" (case-insensitive).
func buildMFAConfig(mfaState string) *identitytoolkit.GoogleCloudIdentitytoolkitAdminV2MultiFactorAuthConfig {
	mfaState = strings.ToUpper(strings.TrimSpace(mfaState))
	cfg := &identitytoolkit.GoogleCloudIdentitytoolkitAdminV2MultiFactorAuthConfig{
		State: mfaState,
	}
	// If MFA is enabled, default to PHONE_SMS provider
	if mfaState == "ENABLED" || mfaState == "MANDATORY" {
		cfg.EnabledProviders = []string{"PHONE_SMS"}
	}
	return cfg
}
