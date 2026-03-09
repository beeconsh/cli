package provider

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/terracotta-ai/beecon/internal/logging"
	"github.com/terracotta-ai/beecon/internal/state"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/eventarc/v1"
	"google.golang.org/api/monitoring/v3"
	"google.golang.org/api/option"
)

// ---------------------------------------------------------------------------
// Cloud CDN — managed via backend services with CDN enabled
// ---------------------------------------------------------------------------

func applyGCPCloudCDN(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	if projectID == "" {
		return nil, fmt.Errorf("cloud_cdn requires intent.project_id")
	}
	name := req.RecordProviderID()
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}

	origin := defaultString(intent(req.Intent, "origin", "backend"), "")
	if origin == "" && req.Action.Operation == "CREATE" {
		return nil, fmt.Errorf("cloud_cdn CREATE requires intent.origin or intent.backend")
	}

	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}

	providerID := fmt.Sprintf("projects/%s/global/backendServices/%s", projectID, name)
	result := &ApplyResult{
		ProviderID: providerID,
		LiveState: map[string]interface{}{
			"provider":  "gcp",
			"service":   "cloud_cdn",
			"project":   projectID,
			"name":      name,
			"operation": req.Action.Operation,
		},
	}

	switch req.Action.Operation {
	case "CREATE":
		cacheMode := defaultString(intent(req.Intent, "cache_mode"), "CACHE_ALL_STATIC")
		bs := &compute.BackendService{
			Name:      name,
			EnableCDN: true,
			CdnPolicy: &compute.BackendServiceCdnPolicy{
				CacheMode: cacheMode,
			},
			Protocol: defaultString(intent(req.Intent, "protocol"), "HTTPS"),
		}

		// Parse optional CDN policy fields
		if maxTTL := intent(req.Intent, "max_ttl"); maxTTL != "" {
			bs.CdnPolicy.MaxTtl = parseInt64(maxTTL)
		}
		if defaultTTL := intent(req.Intent, "default_ttl"); defaultTTL != "" {
			bs.CdnPolicy.DefaultTtl = parseInt64(defaultTTL)
		}
		if signed := intent(req.Intent, "signed_url_cache_max_age"); signed != "" {
			bs.CdnPolicy.SignedUrlCacheMaxAgeSec = parseInt64(signed)
		}
		if neg := intent(req.Intent, "negative_caching"); neg == "true" {
			bs.CdnPolicy.NegativeCaching = true
		}

		if err := withGCPRetry(ctx, "cloud_cdn_create", func() error {
			_, err := svc.BackendServices.Insert(projectID, bs).Context(ctx).Do()
			if err != nil && isAlreadyExists(err) {
				return nil
			}
			return err
		}); err != nil {
			return nil, fmt.Errorf("cloud cdn create backend service: %w", err)
		}
		result.LiveState["cdn_enabled"] = true
		result.LiveState["cache_mode"] = cacheMode
		result.LiveState["origin"] = origin

	case "UPDATE":
		// Fetch existing backend service, update CDN policy
		existing, err := svc.BackendServices.Get(projectID, name).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("cloud cdn get backend service for update: %w", err)
		}
		existing.EnableCDN = true
		if existing.CdnPolicy == nil {
			existing.CdnPolicy = &compute.BackendServiceCdnPolicy{}
		}
		if cm := intent(req.Intent, "cache_mode"); cm != "" {
			existing.CdnPolicy.CacheMode = cm
		}
		if maxTTL := intent(req.Intent, "max_ttl"); maxTTL != "" {
			existing.CdnPolicy.MaxTtl = parseInt64(maxTTL)
		}
		if defaultTTL := intent(req.Intent, "default_ttl"); defaultTTL != "" {
			existing.CdnPolicy.DefaultTtl = parseInt64(defaultTTL)
		}
		if neg := intent(req.Intent, "negative_caching"); neg != "" {
			existing.CdnPolicy.NegativeCaching = neg == "true"
		}

		if _, err := svc.BackendServices.Update(projectID, name, existing).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("cloud cdn update backend service: %w", err)
		}

	case "DELETE":
		if _, err := svc.BackendServices.Delete(projectID, name).Context(ctx).Do(); err != nil && !isGCPNotFound(err) {
			return nil, fmt.Errorf("cloud cdn delete backend service: %w", err)
		}
	}

	return result, nil
}

func observeGCPCloudCDN(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := intentString(rec.IntentSnapshot, "intent.project_id")
	if projectID == "" {
		return nil, fmt.Errorf("cloud_cdn observe requires intent.project_id")
	}
	name := rec.ProviderID
	if name != "" && strings.Contains(name, "/backendServices/") {
		name = name[strings.LastIndex(name, "/")+1:]
	}
	if name == "" {
		name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}

	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}
	bs, err := svc.BackendServices.Get(projectID, name).Context(ctx).Do()
	if err != nil {
		if isGCPNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: fmt.Sprintf("projects/%s/global/backendServices/%s", projectID, name), LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("cloud cdn get backend service: %w", err)
	}
	live := map[string]interface{}{
		"provider":           "gcp",
		"service":            "cloud_cdn",
		"name":               name,
		"cdn_enabled":        bs.EnableCDN,
		"protocol":           bs.Protocol,
		"port":               bs.Port,
		"creation_timestamp": bs.CreationTimestamp,
	}
	if bs.CdnPolicy != nil {
		live["cache_mode"] = bs.CdnPolicy.CacheMode
		live["default_ttl"] = bs.CdnPolicy.DefaultTtl
		live["max_ttl"] = bs.CdnPolicy.MaxTtl
		live["negative_caching"] = bs.CdnPolicy.NegativeCaching
		live["signed_url_cache_max_age"] = bs.CdnPolicy.SignedUrlCacheMaxAgeSec
	}
	if len(bs.HealthChecks) > 0 {
		live["health_checks"] = strings.Join(bs.HealthChecks, ",")
	}

	return &ObserveResult{
		Exists:     true,
		ProviderID: fmt.Sprintf("projects/%s/global/backendServices/%s", projectID, name),
		LiveState:  live,
	}, nil
}

// ---------------------------------------------------------------------------
// Cloud Monitoring — AlertPolicy management
// ---------------------------------------------------------------------------

func gcpMonitoringService(ctx context.Context) (*monitoring.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := monitoring.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp monitoring service init: %w", err)
		}
		return svc, nil
	}
	svc, err := monitoring.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp monitoring service init: %w", err)
	}
	return svc, nil
}

func applyGCPCloudMonitoring(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	if projectID == "" {
		return nil, fmt.Errorf("cloud_monitoring requires intent.project_id")
	}
	metric := requiredIntent(req.Intent, "metric")
	if metric == "" && req.Action.Operation == "CREATE" {
		return nil, fmt.Errorf("cloud_monitoring CREATE requires intent.metric")
	}

	displayName := defaultString(intent(req.Intent, "display_name", "name"), identifierFor(req.Action.NodeName))
	threshold := defaultString(intent(req.Intent, "threshold"), "0")
	comparison := defaultString(intent(req.Intent, "comparison"), "COMPARISON_GT")
	duration := defaultString(intent(req.Intent, "duration"), "60s")

	parent := fmt.Sprintf("projects/%s", projectID)

	svc, err := gcpMonitoringService(ctx)
	if err != nil {
		return nil, err
	}

	result := &ApplyResult{
		LiveState: map[string]interface{}{
			"provider":     "gcp",
			"service":      "cloud_monitoring",
			"project":      projectID,
			"display_name": displayName,
			"operation":    req.Action.Operation,
		},
	}

	switch req.Action.Operation {
	case "CREATE":
		thresholdVal := parseFloat64(threshold)
		policy := &monitoring.AlertPolicy{
			DisplayName: displayName,
			Conditions: []*monitoring.Condition{
				{
					DisplayName: fmt.Sprintf("%s threshold", metric),
					ConditionThreshold: &monitoring.MetricThreshold{
						Filter:     fmt.Sprintf("metric.type = \"%s\"", metric),
						Comparison: comparison,
						ThresholdValue: thresholdVal,
						Duration:   duration,
					},
				},
			},
			Combiner: "OR",
		}

		// Optional notification channels
		if channels := stringListFromIntent(req.Intent, "notification_channels"); len(channels) > 0 {
			policy.NotificationChannels = channels
		}

		var created *monitoring.AlertPolicy
		if err := withGCPRetry(ctx, "cloud_monitoring_create", func() error {
			var createErr error
			created, createErr = svc.Projects.AlertPolicies.Create(parent, policy).Context(ctx).Do()
			if createErr != nil && isAlreadyExists(createErr) {
				return nil
			}
			return createErr
		}); err != nil {
			return nil, fmt.Errorf("cloud monitoring create alert policy: %w", err)
		}
		if created != nil {
			result.ProviderID = created.Name
			result.LiveState["alert_policy_name"] = created.Name
		} else {
			// Already-exists path — construct best-effort ID
			result.ProviderID = fmt.Sprintf("%s/alertPolicies/%s", parent, displayName)
		}
		result.LiveState["metric"] = metric
		result.LiveState["threshold"] = thresholdVal
		result.LiveState["comparison"] = comparison
		result.LiveState["duration"] = duration

	case "UPDATE":
		policyName := req.RecordProviderID()
		if policyName == "" {
			return nil, fmt.Errorf("cloud_monitoring UPDATE requires existing provider_id (alert policy name)")
		}
		existing, err := svc.Projects.AlertPolicies.Get(policyName).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("cloud monitoring get alert policy for update: %w", err)
		}
		if metric != "" && len(existing.Conditions) > 0 && existing.Conditions[0].ConditionThreshold != nil {
			existing.Conditions[0].ConditionThreshold.Filter = fmt.Sprintf("metric.type = \"%s\"", metric)
		}
		if t := intent(req.Intent, "threshold"); t != "" && len(existing.Conditions) > 0 && existing.Conditions[0].ConditionThreshold != nil {
			existing.Conditions[0].ConditionThreshold.ThresholdValue = parseFloat64(t)
		}
		if c := intent(req.Intent, "comparison"); c != "" && len(existing.Conditions) > 0 && existing.Conditions[0].ConditionThreshold != nil {
			existing.Conditions[0].ConditionThreshold.Comparison = c
		}
		if d := intent(req.Intent, "duration"); d != "" && len(existing.Conditions) > 0 && existing.Conditions[0].ConditionThreshold != nil {
			existing.Conditions[0].ConditionThreshold.Duration = d
		}
		if dn := intent(req.Intent, "display_name", "name"); dn != "" {
			existing.DisplayName = dn
		}
		if channels := stringListFromIntent(req.Intent, "notification_channels"); len(channels) > 0 {
			existing.NotificationChannels = channels
		}

		if _, err := svc.Projects.AlertPolicies.Patch(policyName, existing).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("cloud monitoring update alert policy: %w", err)
		}
		result.ProviderID = policyName

	case "DELETE":
		policyName := req.RecordProviderID()
		if policyName == "" {
			return nil, fmt.Errorf("cloud_monitoring DELETE requires existing provider_id (alert policy name)")
		}
		if _, err := svc.Projects.AlertPolicies.Delete(policyName).Context(ctx).Do(); err != nil && !isGCPNotFound(err) {
			return nil, fmt.Errorf("cloud monitoring delete alert policy: %w", err)
		}
		result.ProviderID = policyName
	}

	return result, nil
}

func observeGCPCloudMonitoring(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := intentString(rec.IntentSnapshot, "intent.project_id")
	if projectID == "" {
		return nil, fmt.Errorf("cloud_monitoring observe requires intent.project_id")
	}
	policyName := rec.ProviderID
	if policyName == "" {
		return nil, fmt.Errorf("cloud_monitoring observe requires provider_id (alert policy name)")
	}

	svc, err := gcpMonitoringService(ctx)
	if err != nil {
		return nil, err
	}
	policy, err := svc.Projects.AlertPolicies.Get(policyName).Context(ctx).Do()
	if err != nil {
		if isGCPNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: policyName, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("cloud monitoring get alert policy: %w", err)
	}
	live := map[string]interface{}{
		"provider":     "gcp",
		"service":      "cloud_monitoring",
		"display_name": policy.DisplayName,
		"enabled":      policy.Enabled,
	}
	if len(policy.Conditions) > 0 {
		cond := policy.Conditions[0]
		if cond.ConditionThreshold != nil {
			live["metric_filter"] = cond.ConditionThreshold.Filter
			live["threshold"] = cond.ConditionThreshold.ThresholdValue
			live["comparison"] = cond.ConditionThreshold.Comparison
			live["duration"] = cond.ConditionThreshold.Duration
		}
	}
	if len(policy.NotificationChannels) > 0 {
		live["notification_channels"] = strings.Join(policy.NotificationChannels, ",")
	}
	if policy.CreationRecord != nil {
		live["creation_time"] = policy.CreationRecord.MutateTime
	}
	if policy.MutationRecord != nil {
		live["mutation_time"] = policy.MutationRecord.MutateTime
	}

	return &ObserveResult{Exists: true, ProviderID: policyName, LiveState: live}, nil
}

// ---------------------------------------------------------------------------
// Eventarc — trigger management
// ---------------------------------------------------------------------------

func gcpEventarcService(ctx context.Context) (*eventarc.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := eventarc.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp eventarc service init: %w", err)
		}
		return svc, nil
	}
	svc, err := eventarc.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp eventarc service init: %w", err)
	}
	return svc, nil
}

func applyGCPEventarc(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	if projectID == "" {
		return nil, fmt.Errorf("eventarc requires intent.project_id")
	}
	name := req.RecordProviderID()
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	region := defaultString(intent(req.Intent, "region"), req.Region)
	if region == "" {
		region = "us-central1"
	}

	destination := intent(req.Intent, "destination")
	if destination == "" && req.Action.Operation == "CREATE" {
		return nil, fmt.Errorf("eventarc CREATE requires intent.destination")
	}

	svc, err := gcpEventarcService(ctx)
	if err != nil {
		return nil, err
	}

	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	fullName := fmt.Sprintf("%s/triggers/%s", parent, name)
	providerID := fullName

	result := &ApplyResult{
		ProviderID: providerID,
		LiveState: map[string]interface{}{
			"provider":  "gcp",
			"service":   "eventarc",
			"project":   projectID,
			"region":    region,
			"name":      name,
			"operation": req.Action.Operation,
		},
	}

	switch req.Action.Operation {
	case "CREATE":
		trigger := &eventarc.Trigger{
			Name: fullName,
			Destination: &eventarc.Destination{},
		}

		// Parse destination — could be a Cloud Run service or other
		if strings.Contains(destination, "/services/") {
			trigger.Destination.CloudRun = &eventarc.CloudRun{
				Service: destination,
				Region:  region,
			}
		} else {
			// Assume it's a Cloud Run service name in the same project
			trigger.Destination.CloudRun = &eventarc.CloudRun{
				Service: destination,
				Region:  region,
			}
		}

		// Parse event filters
		if filtersRaw := intent(req.Intent, "event_filters"); filtersRaw != "" {
			trigger.EventFilters = parseEventFilters(filtersRaw)
		} else if eventType := intent(req.Intent, "event_type"); eventType != "" {
			trigger.EventFilters = []*eventarc.EventFilter{
				{Attribute: "type", Value: eventType},
			}
		}

		if sa := intent(req.Intent, "service_account"); sa != "" {
			trigger.ServiceAccount = sa
		}

		if err := withGCPRetry(ctx, "eventarc_create", func() error {
			_, err := svc.Projects.Locations.Triggers.Create(parent, trigger).TriggerId(name).Context(ctx).Do()
			if err != nil && isAlreadyExists(err) {
				return nil
			}
			return err
		}); err != nil {
			return nil, fmt.Errorf("eventarc create trigger: %w", err)
		}
		result.LiveState["destination"] = destination
		if len(trigger.EventFilters) > 0 {
			result.LiveState["event_filters_count"] = len(trigger.EventFilters)
		}

	case "UPDATE":
		existing, err := svc.Projects.Locations.Triggers.Get(fullName).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("eventarc get trigger for update: %w", err)
		}
		if dest := intent(req.Intent, "destination"); dest != "" {
			if existing.Destination == nil {
				existing.Destination = &eventarc.Destination{}
			}
			existing.Destination.CloudRun = &eventarc.CloudRun{
				Service: dest,
				Region:  region,
			}
		}
		if sa := intent(req.Intent, "service_account"); sa != "" {
			existing.ServiceAccount = sa
		}
		if filtersRaw := intent(req.Intent, "event_filters"); filtersRaw != "" {
			existing.EventFilters = parseEventFilters(filtersRaw)
		}

		if _, err := svc.Projects.Locations.Triggers.Patch(fullName, existing).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("eventarc update trigger: %w", err)
		}

	case "DELETE":
		if _, err := svc.Projects.Locations.Triggers.Delete(fullName).Context(ctx).Do(); err != nil && !isGCPNotFound(err) {
			return nil, fmt.Errorf("eventarc delete trigger: %w", err)
		}
	}

	return result, nil
}

func observeGCPEventarc(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := intentString(rec.IntentSnapshot, "intent.project_id")
	if projectID == "" {
		return nil, fmt.Errorf("eventarc observe requires intent.project_id")
	}
	region := defaultString(intentString(rec.IntentSnapshot, "intent.region"), "us-central1")
	name := rec.ProviderID
	if name != "" && strings.Contains(name, "/triggers/") {
		// Already a full name — use as-is
	} else {
		if name == "" {
			name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
		}
		name = fmt.Sprintf("projects/%s/locations/%s/triggers/%s", projectID, region, name)
	}

	svc, err := gcpEventarcService(ctx)
	if err != nil {
		return nil, err
	}
	trigger, err := svc.Projects.Locations.Triggers.Get(name).Context(ctx).Do()
	if err != nil {
		if isGCPNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("eventarc get trigger: %w", err)
	}

	live := map[string]interface{}{
		"provider":    "gcp",
		"service":     "eventarc",
		"name":        trigger.Name,
		"create_time": trigger.CreateTime,
		"update_time": trigger.UpdateTime,
	}
	if trigger.Destination != nil {
		if trigger.Destination.CloudRun != nil {
			live["destination_service"] = trigger.Destination.CloudRun.Service
			live["destination_region"] = trigger.Destination.CloudRun.Region
		}
	}
	if trigger.Transport != nil && trigger.Transport.Pubsub != nil {
		live["transport_topic"] = trigger.Transport.Pubsub.Topic
	}
	if trigger.ServiceAccount != "" {
		live["service_account"] = trigger.ServiceAccount
	}
	if len(trigger.EventFilters) > 0 {
		filters := make([]string, 0, len(trigger.EventFilters))
		for _, f := range trigger.EventFilters {
			filters = append(filters, fmt.Sprintf("%s=%s", f.Attribute, f.Value))
		}
		live["event_filters"] = strings.Join(filters, ",")
	}
	if len(trigger.Labels) > 0 {
		labelParts := make([]string, 0, len(trigger.Labels))
		for k, v := range trigger.Labels {
			labelParts = append(labelParts, fmt.Sprintf("%s=%s", k, v))
		}
		live["labels"] = strings.Join(labelParts, ",")
	}

	return &ObserveResult{Exists: true, ProviderID: name, LiveState: live}, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// parseEventFilters parses a comma-separated list of "key=value" pairs into
// Eventarc EventFilter objects. Example: "type=google.cloud.audit.log.v1.written,serviceName=storage.googleapis.com"
func parseEventFilters(raw string) []*eventarc.EventFilter {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	filters := make([]*eventarc.EventFilter, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		eqIdx := strings.Index(p, "=")
		if eqIdx <= 0 || eqIdx == len(p)-1 {
			logging.Logger.Warn("eventarc:parse_filter:skip", "filter", p)
			continue
		}
		filters = append(filters, &eventarc.EventFilter{
			Attribute: strings.TrimSpace(p[:eqIdx]),
			Value:     strings.TrimSpace(p[eqIdx+1:]),
		})
	}
	return filters
}

// parseInt64 parses a string to int64, returning 0 on failure.
func parseInt64(s string) int64 {
	s = strings.TrimSpace(s)
	var n int64
	fmt.Sscanf(s, "%d", &n)
	return n
}

// parseFloat64 parses a string to float64, returning 0 on failure.
func parseFloat64(s string) float64 {
	s = strings.TrimSpace(s)
	var f float64
	fmt.Sscanf(s, "%f", &f)
	return f
}
