package provider

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"cloud.google.com/go/pubsub"
	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	secretmanagerpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	"cloud.google.com/go/storage"
	"github.com/terracotta-ai/beecon/internal/classify"
	"github.com/terracotta-ai/beecon/internal/logging"
	"github.com/terracotta-ai/beecon/internal/state"
	"google.golang.org/api/cloudresourcemanager/v1"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/dns/v1"
	"google.golang.org/api/iam/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/api/option"
	"google.golang.org/api/redis/v1"
	"google.golang.org/api/run/v2"
	"google.golang.org/api/sqladmin/v1beta4"
)

// GCPSupportMatrix lists planned GCP targets by tier.
var GCPSupportMatrix = map[string]string{
	"cloud_run":         "tier1",
	"cloud_sql":         "tier1",
	"memorystore_redis": "tier1",
	"gcs":               "tier1",
	"vpc":               "tier1",
	"subnet":            "tier1",
	"firewall":          "tier1",
	"iam":               "tier1",
	"secret_manager":    "tier1",
	"cloud_functions":   "tier2",
	"api_gateway":       "tier2",
	"pubsub":            "tier2",
	"cloud_cdn":         "tier2",
	"cloud_dns":         "tier2",
	"cloud_monitoring":  "tier2",
	"gke":               "tier3",
	"eventarc":          "tier3",
	"identity_platform": "tier3",
	"compute_engine":    "tier3",
}

func (e *DefaultExecutor) applyGCP(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	target := detectGCPTarget(req)
	if e.dryRun {
		return simulatedApply(req, target), nil
	}

	if err := validateGCPInput(target, req.Intent); err != nil {
		return nil, err
	}

	logging.Logger.Debug("gcp:apply", "target", target, "operation", req.Action.Operation, "node", req.Action.NodeName)

	var result *ApplyResult
	var applyErr error

	switch target {
	case "gcs":
		result, applyErr = applyGCPGCS(ctx, req)
	case "cloud_sql":
		result, applyErr = applyGCPCloudSQL(ctx, req)
	case "pubsub":
		result, applyErr = applyGCPPubSub(ctx, req)
	case "secret_manager":
		result, applyErr = applyGCPSecretManager(ctx, req)
	case "vpc":
		result, applyErr = applyGCPVPC(ctx, req)
	case "subnet":
		result, applyErr = applyGCPSubnet(ctx, req)
	case "firewall":
		result, applyErr = applyGCPFirewall(ctx, req)
	case "cloud_run":
		result, applyErr = applyGCPCloudRun(ctx, req)
	case "memorystore_redis":
		result, applyErr = applyGCPMemorystoreRedis(ctx, req)
	case "iam":
		result, applyErr = applyGCPIAM(ctx, req)
	case "compute_engine":
		result, applyErr = applyGCPComputeEngine(ctx, req)
	case "cloud_dns":
		result, applyErr = applyGCPCloudDNS(ctx, req)
	case "cloud_functions", "api_gateway", "cloud_cdn", "cloud_monitoring", "gke", "eventarc", "identity_platform":
		result, applyErr = applyGCPProjectScopedGeneric(ctx, target, req)
	default:
		return nil, fmt.Errorf("gcp target %q is recognized but requires additional adapter implementation for live execution (set BEECON_EXECUTE!=1 for dry-run)", target)
	}
	if applyErr != nil {
		return result, applyErr
	}

	// --- Post-apply cross-cutting concerns ---
	if result.LiveState == nil {
		result.LiveState = map[string]interface{}{}
	}
	gcpPostApplyCrossCutting(ctx, target, req, result)

	logging.Logger.Debug("gcp:apply:complete", "target", target, "provider_id", result.ProviderID)
	return result, nil
}

// gcpAlarmTargets lists GCP targets that emit Cloud Monitoring metrics.
var gcpAlarmTargets = map[string]bool{
	"cloud_sql":         true,
	"cloud_run":         true,
	"memorystore_redis": true,
	"compute_engine":    true,
	"cloud_functions":   true,
	"gke":               true,
}

// gcpPostApplyCrossCutting applies post-apply automation (alarm_on, log_retention,
// iam_roles binding) for GCP resources. Non-fatal — stores errors in LiveState.
func gcpPostApplyCrossCutting(ctx context.Context, target string, req ApplyRequest, result *ApplyResult) {
	if req.Action.Operation == "DELETE" {
		return
	}

	// Log retention: store intent for GCP Cloud Logging retention.
	// GCP log retention is configured at the log bucket level, not per-resource.
	// We store the intent for the user/agent to configure via Cloud Logging API.
	if retRaw := intent(req.Intent, "log_retention"); retRaw != "" {
		switch target {
		case "cloud_run", "cloud_functions":
			days := parseDurationDays(retRaw)
			if days > 0 {
				result.LiveState["log_retention_days"] = days
				result.LiveState["log_retention_note"] = "GCP log retention requires Cloud Logging bucket configuration"
			}
		}
	}

	// alarm_on: store parsed alarm intent for GCP Cloud Monitoring.
	// GCP alerting policies require a notification channel; we store the parsed
	// condition so agents can create an AlertPolicy via the Monitoring API.
	if alarmRaw := intent(req.Intent, "alarm_on"); alarmRaw != "" && gcpAlarmTargets[target] {
		cond, err := parseAlarmOn(alarmRaw)
		if err == nil && cond != nil {
			metricType, metricResource := gcpAlarmMetricForTarget(target, cond.Metric)
			// If the metric type equals the raw input, it wasn't recognized — warn.
			if metricType == cond.Metric {
				logging.Logger.Warn("gcp:alarm:unrecognized_metric", "target", target, "metric", cond.Metric)
				result.LiveState["alarm_warning"] = fmt.Sprintf("metric %q is not a recognized alias for %s; using raw value", cond.Metric, target)
			}
			_ = metricResource
			resourceName := identifierFor(req.Action.NodeName)
			alarmName := resourceName + "-" + strings.ToLower(cond.Metric)
			logging.Logger.Debug("gcp:alarm:stored", "target", target, "alarm_name", alarmName, "metric", metricType)
			result.LiveState["alarm_name"] = alarmName
			result.LiveState["alarm_metric"] = metricType
			result.LiveState["alarm_threshold"] = cond.Threshold
			result.LiveState["alarm_operator"] = cond.Operator
			result.LiveState["alarm_note"] = "GCP alarm stored; create AlertPolicy via Cloud Monitoring API"
		} else {
			result.LiveState["alarm_error"] = "failed to parse alarm condition"
		}
	}

	// IAM role binding: apply inferred IAM roles from wiring layer.
	if rolesJSON := intent(req.Intent, "iam_roles"); rolesJSON != "" {
		result.LiveState["iam_roles_inferred"] = rolesJSON
		result.LiveState["iam_roles_note"] = "GCP IAM roles inferred; bind to service account via IAM API"
	}
}

// gcpAlarmMetricForTarget maps user-facing metric names to GCP Cloud Monitoring
// metric types and resource types.
func gcpAlarmMetricForTarget(target, metric string) (string, string) {
	metric = strings.ToLower(metric)
	switch target {
	case "cloud_sql":
		switch metric {
		case "cpu":
			return "cloudsql.googleapis.com/database/cpu/utilization", "cloudsql_database"
		case "memory":
			return "cloudsql.googleapis.com/database/memory/utilization", "cloudsql_database"
		case "connections":
			return "cloudsql.googleapis.com/database/network/connections", "cloudsql_database"
		case "disk":
			return "cloudsql.googleapis.com/database/disk/utilization", "cloudsql_database"
		}
	case "cloud_run":
		switch metric {
		case "cpu":
			return "run.googleapis.com/container/cpu/utilizations", "cloud_run_revision"
		case "memory":
			return "run.googleapis.com/container/memory/utilizations", "cloud_run_revision"
		case "latency":
			return "run.googleapis.com/request_latencies", "cloud_run_revision"
		case "errors":
			return "run.googleapis.com/request_count", "cloud_run_revision" // filter by response_code_class=5xx
		}
	case "memorystore_redis":
		switch metric {
		case "cpu":
			return "redis.googleapis.com/stats/cpu_utilization", "redis_instance"
		case "memory":
			return "redis.googleapis.com/stats/memory/usage_ratio", "redis_instance"
		case "connections":
			return "redis.googleapis.com/clients/connected", "redis_instance"
		}
	case "compute_engine":
		switch metric {
		case "cpu":
			return "compute.googleapis.com/instance/cpu/utilization", "gce_instance"
		case "memory":
			return "compute.googleapis.com/instance/memory/balloon/ram_used", "gce_instance"
		case "disk":
			return "compute.googleapis.com/instance/disk/read_bytes_count", "gce_instance"
		}
	case "cloud_functions":
		switch metric {
		case "errors":
			return "cloudfunctions.googleapis.com/function/execution_count", "cloud_function" // filter by status!=ok
		case "duration":
			return "cloudfunctions.googleapis.com/function/execution_times", "cloud_function"
		}
	case "gke":
		switch metric {
		case "cpu":
			return "kubernetes.io/container/cpu/core_usage_time", "k8s_container"
		case "memory":
			return "kubernetes.io/container/memory/used_bytes", "k8s_container"
		}
	}
	// Fallback: return the metric as-is (allows advanced users to pass exact metric types)
	return metric, target
}

func (e *DefaultExecutor) observeGCP(ctx context.Context, region string, rec *state.ResourceRecord) (*ObserveResult, error) {
	if e.dryRun {
		if rec == nil {
			return &ObserveResult{Exists: false, LiveState: map[string]interface{}{}}, nil
		}
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
	if rec == nil {
		return &ObserveResult{Exists: false, LiveState: map[string]interface{}{}}, nil
	}
	target := detectGCPRecordTarget(rec)
	logging.Logger.Debug("gcp:observe", "target", target, "provider_id", rec.ProviderID)
	switch target {
	case "gcs":
		return observeGCPGCS(ctx, rec)
	case "cloud_sql":
		return observeGCPCloudSQL(ctx, rec)
	case "pubsub":
		return observeGCPPubSub(ctx, rec)
	case "secret_manager":
		return observeGCPSecretManager(ctx, rec)
	case "vpc":
		return observeGCPVPC(ctx, rec)
	case "subnet":
		return observeGCPSubnet(ctx, rec)
	case "firewall":
		return observeGCPFirewall(ctx, rec)
	case "cloud_run":
		return observeGCPCloudRun(ctx, rec)
	case "memorystore_redis":
		return observeGCPMemorystoreRedis(ctx, rec)
	case "iam":
		return observeGCPIAM(ctx, rec)
	case "compute_engine":
		return observeGCPComputeEngine(ctx, rec)
	case "cloud_dns":
		return observeGCPCloudDNS(ctx, rec)
	case "cloud_functions", "api_gateway", "cloud_cdn", "cloud_monitoring", "gke", "eventarc", "identity_platform":
		return observeGCPProjectScopedGeneric(ctx, target, rec)
	default:
		return &ObserveResult{Exists: rec.Managed, ProviderID: rec.ProviderID, LiveState: rec.LiveState}, nil
	}
}

func applyGCPGCS(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	location := defaultString(intent(req.Intent, "location"), req.Region)
	if location == "" {
		location = "us-central1"
	}
	bucketName := req.RecordProviderID()
	if bucketName == "" {
		bucketName = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
		bucketName = "beecon-" + bucketName
	}
	client, err := gcpStorageClient(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	h := client.Bucket(bucketName)
	switch req.Action.Operation {
	case "CREATE":
		err = h.Create(ctx, projectID, &storage.BucketAttrs{Location: location})
		if err != nil && !strings.Contains(strings.ToLower(err.Error()), "you already own") {
			return nil, fmt.Errorf("gcs create bucket: %w", err)
		}
	case "UPDATE":
		_, err = h.Update(ctx, storage.BucketAttrsToUpdate{StorageClass: defaultString(intent(req.Intent, "storage_class"), "STANDARD")})
		if err != nil {
			return nil, fmt.Errorf("gcs update bucket: %w", err)
		}
	case "DELETE":
		err = h.Delete(ctx)
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("gcs delete bucket: %w", err)
		}
	}
	return &ApplyResult{ProviderID: bucketName, LiveState: map[string]interface{}{"provider": "gcp", "service": "gcs", "bucket": bucketName, "location": location, "operation": req.Action.Operation}}, nil
}

func observeGCPGCS(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	bucketName := rec.ProviderID
	if bucketName == "" {
		bucketName = "beecon-" + strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	client, err := gcpStorageClient(ctx)
	if err != nil {
		return nil, err
	}
	defer client.Close()
	attrs, err := client.Bucket(bucketName).Attrs(ctx)
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: bucketName, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("gcs bucket attrs: %w", err)
	}
	live := map[string]interface{}{"provider": "gcp", "service": "gcs", "bucket": bucketName, "location": attrs.Location, "storage_class": attrs.StorageClass}
	return &ObserveResult{Exists: true, ProviderID: bucketName, LiveState: live}, nil
}

func applyGCPCloudSQL(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	instance := req.RecordProviderID()
	if instance == "" {
		instance = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	region := defaultString(intent(req.Intent, "region"), req.Region)
	if region == "" {
		region = "us-central1"
	}
	engine := defaultString(intent(req.Intent, "engine", "type"), "postgres")
	dbVersion := defaultString(intent(req.Intent, "version"), cloudSQLVersion(engine))
	tier := requiredIntent(req.Intent, "tier")

	svc, err := gcpSQLAdminService(ctx)
	if err != nil {
		return nil, err
	}
	switch req.Action.Operation {
	case "CREATE":
		inst := &sqladmin.DatabaseInstance{
			Name:            instance,
			DatabaseVersion: dbVersion,
			Region:          region,
			Settings: &sqladmin.Settings{
				Tier: tier,
			},
		}
		if _, err := svc.Instances.Insert(projectID, inst).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("cloud sql create instance: %w", err)
		}
	case "UPDATE":
		patch := &sqladmin.DatabaseInstance{Settings: &sqladmin.Settings{Tier: tier}}
		if _, err := svc.Instances.Patch(projectID, instance, patch).Context(ctx).Do(); err != nil {
			return nil, fmt.Errorf("cloud sql patch instance: %w", err)
		}
	case "DELETE":
		if _, err := svc.Instances.Delete(projectID, instance).Context(ctx).Do(); err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("cloud sql delete instance: %w", err)
		}
	}
	return &ApplyResult{ProviderID: instance, LiveState: map[string]interface{}{"provider": "gcp", "service": "cloud_sql", "instance": instance, "region": region, "tier": tier, "db_version": dbVersion, "operation": req.Action.Operation}}, nil
}

func observeGCPCloudSQL(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := fmt.Sprint(rec.IntentSnapshot["intent.project_id"])
	instance := rec.ProviderID
	if instance == "" {
		instance = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	svc, err := gcpSQLAdminService(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.Instances.Get(projectID, instance).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: instance, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("cloud sql get instance: %w", err)
	}
	live := map[string]interface{}{"provider": "gcp", "service": "cloud_sql", "instance": instance, "state": out.State, "region": out.Region, "db_version": out.DatabaseVersion}
	if out.Settings != nil {
		live["tier"] = out.Settings.Tier
	}
	return &ObserveResult{Exists: true, ProviderID: instance, LiveState: live}, nil
}

func applyGCPPubSub(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	topicID := req.RecordProviderID()
	if topicID == "" {
		topicID = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("pubsub client init: %w", err)
	}
	defer client.Close()

	topic := client.Topic(topicID)
	switch req.Action.Operation {
	case "CREATE":
		if _, err := client.CreateTopic(ctx, topicID); err != nil && status.Code(err) != codes.AlreadyExists {
			return nil, fmt.Errorf("pubsub create topic: %w", err)
		}
	case "UPDATE":
		ok, err := topic.Exists(ctx)
		if err != nil {
			return nil, fmt.Errorf("pubsub check topic: %w", err)
		}
		if !ok {
			return nil, fmt.Errorf("pubsub topic %q not found for update", topicID)
		}
	case "DELETE":
		if err := topic.Delete(ctx); err != nil && status.Code(err) != codes.NotFound {
			return nil, fmt.Errorf("pubsub delete topic: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: topicID,
		LiveState: map[string]interface{}{
			"provider":  "gcp",
			"service":   "pubsub",
			"project":   projectID,
			"topic":     topicID,
			"operation": req.Action.Operation,
		},
	}, nil
}

func observeGCPPubSub(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := fmt.Sprint(rec.IntentSnapshot["intent.project_id"])
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("pubsub observe requires intent.project_id")
	}
	topicID := rec.ProviderID
	if topicID == "" {
		topicID = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	client, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("pubsub client init: %w", err)
	}
	defer client.Close()
	ok, err := client.Topic(topicID).Exists(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return &ObserveResult{Exists: false, ProviderID: topicID, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("pubsub check topic: %w", err)
	}
	if !ok {
		return &ObserveResult{Exists: false, ProviderID: topicID, LiveState: map[string]interface{}{}}, nil
	}
	return &ObserveResult{
		Exists:     true,
		ProviderID: topicID,
		LiveState: map[string]interface{}{
			"provider": "gcp",
			"service":  "pubsub",
			"project":  projectID,
			"topic":    topicID,
		},
	}, nil
}

func applyGCPSecretManager(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	secretID := req.RecordProviderID()
	if secretID == "" {
		secretID = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	parent := fmt.Sprintf("projects/%s", projectID)
	secretName := fmt.Sprintf("projects/%s/secrets/%s", projectID, secretID)
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("secret manager client init: %w", err)
	}
	defer client.Close()

	switch req.Action.Operation {
	case "CREATE":
		_, err := client.CreateSecret(ctx, &secretmanagerpb.CreateSecretRequest{
			Parent:   parent,
			SecretId: secretID,
			Secret: &secretmanagerpb.Secret{
				Replication: &secretmanagerpb.Replication{
					Replication: &secretmanagerpb.Replication_Automatic_{
						Automatic: &secretmanagerpb.Replication_Automatic{},
					},
				},
			},
		})
		if err != nil && status.Code(err) != codes.AlreadyExists {
			return nil, fmt.Errorf("secret manager create secret: %w", err)
		}
		if err := addGCPSecretVersion(ctx, client, secretName, req.Intent); err != nil {
			return nil, err
		}
	case "UPDATE":
		if err := addGCPSecretVersion(ctx, client, secretName, req.Intent); err != nil {
			return nil, err
		}
	case "DELETE":
		err := client.DeleteSecret(ctx, &secretmanagerpb.DeleteSecretRequest{Name: secretName})
		if err != nil && status.Code(err) != codes.NotFound {
			return nil, fmt.Errorf("secret manager delete secret: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: secretName,
		LiveState: map[string]interface{}{
			"provider":  "gcp",
			"service":   "secret_manager",
			"project":   projectID,
			"secret":    secretName,
			"operation": req.Action.Operation,
		},
	}, nil
}

func observeGCPSecretManager(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := fmt.Sprint(rec.IntentSnapshot["intent.project_id"])
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("secret manager observe requires intent.project_id")
	}
	secretName := rec.ProviderID
	if secretName == "" {
		secretID := strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
		secretName = fmt.Sprintf("projects/%s/secrets/%s", projectID, secretID)
	}
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("secret manager client init: %w", err)
	}
	defer client.Close()
	sec, err := client.GetSecret(ctx, &secretmanagerpb.GetSecretRequest{Name: secretName})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return &ObserveResult{Exists: false, ProviderID: secretName, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("secret manager get secret: %w", err)
	}
	return &ObserveResult{
		Exists:     true,
		ProviderID: secretName,
		LiveState: map[string]interface{}{
			"provider": "gcp",
			"service":  "secret_manager",
			"secret":   sec.GetName(),
		},
	}, nil
}

func addGCPSecretVersion(ctx context.Context, client *secretmanager.Client, secretName string, intentMap map[string]interface{}) error {
	value := defaultString(
		intent(intentMap, "value"),
		defaultString(intent(intentMap, "secret_value"), defaultString(intent(intentMap, "password"), intent(intentMap, "token"))),
	)
	if strings.TrimSpace(value) == "" {
		return nil
	}
	_, err := client.AddSecretVersion(ctx, &secretmanagerpb.AddSecretVersionRequest{
		Parent: secretName,
		Payload: &secretmanagerpb.SecretPayload{
			Data: []byte(value),
		},
	})
	if err != nil {
		return fmt.Errorf("secret manager add secret version: %w", err)
	}
	return nil
}

func applyGCPVPC(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	name := req.RecordProviderID()
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}
	switch req.Action.Operation {
	case "CREATE":
		_, err := svc.Networks.Insert(projectID, &compute.Network{
			Name:                  name,
			AutoCreateSubnetworks: false,
		}).Context(ctx).Do()
		if err != nil && !isAlreadyExists(err) {
			return nil, fmt.Errorf("gcp vpc create: %w", err)
		}
	case "UPDATE":
		_, err := svc.Networks.Get(projectID, name).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("gcp vpc get: %w", err)
		}
	case "DELETE":
		_, err := svc.Networks.Delete(projectID, name).Context(ctx).Do()
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("gcp vpc delete: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: name,
		LiveState: map[string]interface{}{"provider": "gcp", "service": "vpc", "project": projectID, "network": name, "operation": req.Action.Operation},
	}, nil
}

func observeGCPVPC(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := fmt.Sprint(rec.IntentSnapshot["intent.project_id"])
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("vpc observe requires intent.project_id")
	}
	name := rec.ProviderID
	if name == "" {
		name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.Networks.Get(projectID, name).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("gcp vpc get: %w", err)
	}
	return &ObserveResult{
		Exists:     true,
		ProviderID: name,
		LiveState:  map[string]interface{}{"provider": "gcp", "service": "vpc", "network": name, "self_link": out.SelfLink},
	}, nil
}

func applyGCPSubnet(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	name := req.RecordProviderID()
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	region := defaultString(intent(req.Intent, "region"), req.Region)
	if region == "" {
		region = "us-central1"
	}
	network := defaultString(intent(req.Intent, "network"), "default")
	rangeCIDR := defaultString(intent(req.Intent, "ip_cidr_range"), "10.10.0.0/24")
	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}
	networkLink := fmt.Sprintf("projects/%s/global/networks/%s", projectID, network)
	switch req.Action.Operation {
	case "CREATE":
		_, err := svc.Subnetworks.Insert(projectID, region, &compute.Subnetwork{
			Name:        name,
			IpCidrRange: rangeCIDR,
			Network:     networkLink,
			Region:      region,
		}).Context(ctx).Do()
		if err != nil && !isAlreadyExists(err) {
			return nil, fmt.Errorf("gcp subnet create: %w", err)
		}
	case "UPDATE":
		_, err := svc.Subnetworks.Get(projectID, region, name).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("gcp subnet get: %w", err)
		}
	case "DELETE":
		_, err := svc.Subnetworks.Delete(projectID, region, name).Context(ctx).Do()
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("gcp subnet delete: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: name,
		LiveState: map[string]interface{}{"provider": "gcp", "service": "subnet", "project": projectID, "region": region, "network": network, "subnet": name, "ip_cidr_range": rangeCIDR, "operation": req.Action.Operation},
	}, nil
}

func observeGCPSubnet(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := fmt.Sprint(rec.IntentSnapshot["intent.project_id"])
	region := defaultString(fmt.Sprint(rec.IntentSnapshot["intent.region"]), "us-central1")
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("subnet observe requires intent.project_id")
	}
	name := rec.ProviderID
	if name == "" {
		name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.Subnetworks.Get(projectID, region, name).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("gcp subnet get: %w", err)
	}
	return &ObserveResult{
		Exists:     true,
		ProviderID: name,
		LiveState:  map[string]interface{}{"provider": "gcp", "service": "subnet", "subnet": name, "region": out.Region, "network": out.Network, "ip_cidr_range": out.IpCidrRange},
	}, nil
}

func applyGCPFirewall(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	name := req.RecordProviderID()
	if name == "" {
		name = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	network := defaultString(intent(req.Intent, "network"), "default")
	protocol := defaultString(intent(req.Intent, "protocol"), "tcp")
	port := defaultString(intent(req.Intent, "port"), "80")
	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}
	rule := &compute.Firewall{
		Name:    name,
		Network: fmt.Sprintf("projects/%s/global/networks/%s", projectID, network),
		Allowed: []*compute.FirewallAllowed{{IPProtocol: protocol, Ports: []string{port}}},
	}
	switch req.Action.Operation {
	case "CREATE":
		_, err := svc.Firewalls.Insert(projectID, rule).Context(ctx).Do()
		if err != nil && !isAlreadyExists(err) {
			return nil, fmt.Errorf("gcp firewall create: %w", err)
		}
	case "UPDATE":
		_, err := svc.Firewalls.Update(projectID, name, rule).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("gcp firewall update: %w", err)
		}
	case "DELETE":
		_, err := svc.Firewalls.Delete(projectID, name).Context(ctx).Do()
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("gcp firewall delete: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: name,
		LiveState: map[string]interface{}{"provider": "gcp", "service": "firewall", "project": projectID, "network": network, "firewall": name, "protocol": protocol, "port": port, "operation": req.Action.Operation},
	}, nil
}

func observeGCPFirewall(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := fmt.Sprint(rec.IntentSnapshot["intent.project_id"])
	if strings.TrimSpace(projectID) == "" {
		return nil, fmt.Errorf("firewall observe requires intent.project_id")
	}
	name := rec.ProviderID
	if name == "" {
		name = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.Firewalls.Get(projectID, name).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("gcp firewall get: %w", err)
	}
	live := map[string]interface{}{"provider": "gcp", "service": "firewall", "firewall": name, "network": out.Network}
	if len(out.Allowed) > 0 {
		live["protocol"] = out.Allowed[0].IPProtocol
		if len(out.Allowed[0].Ports) > 0 {
			live["port"] = out.Allowed[0].Ports[0]
		}
	}
	return &ObserveResult{Exists: true, ProviderID: name, LiveState: live}, nil
}

func applyGCPCloudRun(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	region := defaultString(intent(req.Intent, "region"), req.Region)
	if region == "" {
		region = "us-central1"
	}
	serviceName := req.RecordProviderID()
	if serviceName == "" {
		serviceName = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	image := requiredIntent(req.Intent, "image")
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	fullName := fmt.Sprintf("%s/services/%s", parent, serviceName)

	svc, err := gcpRunService(ctx)
	if err != nil {
		return nil, err
	}
	resource := &run.GoogleCloudRunV2Service{
		Name: fullName,
		Template: &run.GoogleCloudRunV2RevisionTemplate{
			Containers: []*run.GoogleCloudRunV2Container{
				{Image: image},
			},
		},
	}
	switch req.Action.Operation {
	case "CREATE":
		_, err := svc.Projects.Locations.Services.Create(parent, resource).ServiceId(serviceName).Context(ctx).Do()
		if err != nil && !isAlreadyExists(err) {
			return nil, fmt.Errorf("cloud run create service: %w", err)
		}
	case "UPDATE":
		_, err := svc.Projects.Locations.Services.Patch(fullName, resource).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("cloud run patch service: %w", err)
		}
	case "DELETE":
		_, err := svc.Projects.Locations.Services.Delete(fullName).Context(ctx).Do()
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("cloud run delete service: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: fullName,
		LiveState: map[string]interface{}{
			"provider":  "gcp",
			"service":   "cloud_run",
			"project":   projectID,
			"region":    region,
			"name":      serviceName,
			"image":     image,
			"operation": req.Action.Operation,
		},
	}, nil
}

func observeGCPCloudRun(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.project_id"]))
	region := defaultString(strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.region"])), "us-central1")
	serviceName := strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.service_name"]))
	if serviceName == "" {
		serviceName = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	if rec.ProviderID != "" && strings.Contains(rec.ProviderID, "/services/") {
		serviceName = rec.ProviderID[strings.LastIndex(rec.ProviderID, "/")+1:]
	}
	if projectID == "" {
		return nil, fmt.Errorf("cloud_run observe requires intent.project_id")
	}
	fullName := fmt.Sprintf("projects/%s/locations/%s/services/%s", projectID, region, serviceName)
	svc, err := gcpRunService(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.Projects.Locations.Services.Get(fullName).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: fullName, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("cloud run get service: %w", err)
	}
	live := map[string]interface{}{
		"provider": "gcp",
		"service":  "cloud_run",
		"name":     serviceName,
		"region":   region,
	}
	if out.Uri != "" {
		live["uri"] = out.Uri
	}
	return &ObserveResult{Exists: true, ProviderID: fullName, LiveState: live}, nil
}

func applyGCPMemorystoreRedis(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	region := defaultString(intent(req.Intent, "region"), req.Region)
	if region == "" {
		region = "us-central1"
	}
	instanceID := req.RecordProviderID()
	if instanceID == "" {
		instanceID = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	parent := fmt.Sprintf("projects/%s/locations/%s", projectID, region)
	fullName := fmt.Sprintf("%s/instances/%s", parent, instanceID)
	tier := strings.ToUpper(defaultString(intent(req.Intent, "tier"), "BASIC"))
	sizeGB := int64(1)
	if raw := strings.TrimSpace(intent(req.Intent, "memory_size_gb")); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			sizeGB = n
		}
	}

	svc, err := gcpRedisService(ctx)
	if err != nil {
		return nil, err
	}
	resource := &redis.Instance{
		Name:         fullName,
		DisplayName:  instanceID,
		Tier:         tier,
		MemorySizeGb: sizeGB,
	}
	switch req.Action.Operation {
	case "CREATE":
		_, err := svc.Projects.Locations.Instances.Create(parent, resource).InstanceId(instanceID).Context(ctx).Do()
		if err != nil && !isAlreadyExists(err) {
			return nil, fmt.Errorf("memorystore redis create: %w", err)
		}
	case "UPDATE":
		_, err := svc.Projects.Locations.Instances.Patch(fullName, resource).UpdateMask("tier,memory_size_gb").Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("memorystore redis patch: %w", err)
		}
	case "DELETE":
		_, err := svc.Projects.Locations.Instances.Delete(fullName).Context(ctx).Do()
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("memorystore redis delete: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: fullName,
		LiveState: map[string]interface{}{
			"provider":        "gcp",
			"service":         "memorystore_redis",
			"project":         projectID,
			"region":          region,
			"instance":        instanceID,
			"tier":            tier,
			"memory_size_gb":  sizeGB,
			"operation":       req.Action.Operation,
		},
	}, nil
}

func observeGCPMemorystoreRedis(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.project_id"]))
	region := defaultString(strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.region"])), "us-central1")
	instanceID := strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.instance_name"]))
	if instanceID == "" {
		instanceID = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	if rec.ProviderID != "" && strings.Contains(rec.ProviderID, "/instances/") {
		instanceID = rec.ProviderID[strings.LastIndex(rec.ProviderID, "/")+1:]
	}
	if projectID == "" {
		return nil, fmt.Errorf("memorystore observe requires intent.project_id")
	}
	fullName := fmt.Sprintf("projects/%s/locations/%s/instances/%s", projectID, region, instanceID)
	svc, err := gcpRedisService(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.Projects.Locations.Instances.Get(fullName).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: fullName, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("memorystore redis get: %w", err)
	}
	live := map[string]interface{}{
		"provider":       "gcp",
		"service":        "memorystore_redis",
		"name":           out.Name,
		"state":          out.State,
		"tier":           out.Tier,
		"memory_size_gb": out.MemorySizeGb,
	}
	return &ObserveResult{Exists: true, ProviderID: fullName, LiveState: live}, nil
}

func applyGCPIAM(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	accountID := strings.TrimSpace(intent(req.Intent, "service_account_id"))
	if accountID == "" {
		accountID = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	displayName := defaultString(intent(req.Intent, "display_name"), accountID)
	parent := fmt.Sprintf("projects/%s", projectID)
	email := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", accountID, projectID)
	name := fmt.Sprintf("projects/%s/serviceAccounts/%s", projectID, email)

	svc, err := gcpIAMService(ctx)
	if err != nil {
		return nil, err
	}
	switch req.Action.Operation {
	case "CREATE":
		_, err := svc.Projects.ServiceAccounts.Create(parent, &iam.CreateServiceAccountRequest{
			AccountId: accountID,
			ServiceAccount: &iam.ServiceAccount{
				DisplayName: displayName,
			},
		}).Context(ctx).Do()
		if err != nil && !isAlreadyExists(err) {
			return nil, fmt.Errorf("gcp iam create service account: %w", err)
		}
	case "UPDATE":
		_, err := svc.Projects.ServiceAccounts.Get(name).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("gcp iam get service account for update: %w", err)
		}
	case "DELETE":
		_, err := svc.Projects.ServiceAccounts.Delete(name).Context(ctx).Do()
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("gcp iam delete service account: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: name,
		LiveState: map[string]interface{}{
			"provider":        "gcp",
			"service":         "iam",
			"project":         projectID,
			"service_account": email,
			"operation":       req.Action.Operation,
		},
	}, nil
}

func observeGCPIAM(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.project_id"]))
	if projectID == "" {
		return nil, fmt.Errorf("iam observe requires intent.project_id")
	}
	accountID := strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.service_account_id"]))
	if accountID == "" {
		accountID = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	email := fmt.Sprintf("%s@%s.iam.gserviceaccount.com", accountID, projectID)
	name := fmt.Sprintf("projects/%s/serviceAccounts/%s", projectID, email)
	if rec.ProviderID != "" && strings.Contains(rec.ProviderID, "/serviceAccounts/") {
		name = rec.ProviderID
		email = rec.ProviderID[strings.LastIndex(rec.ProviderID, "/")+1:]
	}
	svc, err := gcpIAMService(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.Projects.ServiceAccounts.Get(name).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: name, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("gcp iam get service account: %w", err)
	}
	live := map[string]interface{}{
		"provider":        "gcp",
		"service":         "iam",
		"service_account": out.Email,
		"name":            out.Name,
	}
	return &ObserveResult{Exists: true, ProviderID: name, LiveState: live}, nil
}

func applyGCPComputeEngine(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	zone := defaultString(intent(req.Intent, "zone"), defaultString(req.Region, "us-central1-a"))
	instance := req.RecordProviderID()
	if instance == "" {
		instance = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	machineType := defaultString(intent(req.Intent, "machine_type"), "e2-medium")
	image := defaultString(intent(req.Intent, "image"), "projects/debian-cloud/global/images/family/debian-12")
	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}
	switch req.Action.Operation {
	case "CREATE":
		_, err := svc.Instances.Insert(projectID, zone, &compute.Instance{
			Name:        instance,
			MachineType: fmt.Sprintf("zones/%s/machineTypes/%s", zone, machineType),
			Disks: []*compute.AttachedDisk{
				{
					Boot:       true,
					AutoDelete: true,
					InitializeParams: &compute.AttachedDiskInitializeParams{
						SourceImage: image,
					},
				},
			},
			NetworkInterfaces: []*compute.NetworkInterface{
				{Network: "global/networks/default"},
			},
		}).Context(ctx).Do()
		if err != nil && !isAlreadyExists(err) {
			return nil, fmt.Errorf("compute engine create instance: %w", err)
		}
	case "UPDATE":
		_, err := svc.Instances.Get(projectID, zone, instance).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("compute engine get instance for update: %w", err)
		}
	case "DELETE":
		_, err := svc.Instances.Delete(projectID, zone, instance).Context(ctx).Do()
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("compute engine delete instance: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: instance,
		LiveState: map[string]interface{}{"provider": "gcp", "service": "compute_engine", "project": projectID, "zone": zone, "instance": instance, "machine_type": machineType, "operation": req.Action.Operation},
	}, nil
}

func observeGCPComputeEngine(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.project_id"]))
	if projectID == "" {
		return nil, fmt.Errorf("compute_engine observe requires intent.project_id")
	}
	zone := defaultString(strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.zone"])), "us-central1-a")
	instance := rec.ProviderID
	if instance == "" {
		instance = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	svc, err := gcpComputeService(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.Instances.Get(projectID, zone, instance).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: instance, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("compute engine get instance: %w", err)
	}
	live := map[string]interface{}{"provider": "gcp", "service": "compute_engine", "instance": instance, "zone": zone, "status": out.Status}
	if out.MachineType != "" {
		live["machine_type"] = out.MachineType
	}
	return &ObserveResult{Exists: true, ProviderID: instance, LiveState: live}, nil
}

func applyGCPCloudDNS(ctx context.Context, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	zoneName := req.RecordProviderID()
	if zoneName == "" {
		zoneName = strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-")
	}
	dnsName := defaultString(intent(req.Intent, "dns_name"), fmt.Sprintf("%s.", zoneName))
	description := defaultString(intent(req.Intent, "description"), "Managed by Beecon")
	svc, err := gcpDNSService(ctx)
	if err != nil {
		return nil, err
	}
	switch req.Action.Operation {
	case "CREATE":
		_, err := svc.ManagedZones.Create(projectID, &dns.ManagedZone{
			Name:        zoneName,
			DnsName:     dnsName,
			Description: description,
		}).Context(ctx).Do()
		if err != nil && !isAlreadyExists(err) {
			return nil, fmt.Errorf("cloud dns create zone: %w", err)
		}
	case "UPDATE":
		_, err := svc.ManagedZones.Get(projectID, zoneName).Context(ctx).Do()
		if err != nil {
			return nil, fmt.Errorf("cloud dns get zone for update: %w", err)
		}
	case "DELETE":
		err := svc.ManagedZones.Delete(projectID, zoneName).Context(ctx).Do()
		if err != nil && !isNotFound(err) {
			return nil, fmt.Errorf("cloud dns delete zone: %w", err)
		}
	}
	return &ApplyResult{
		ProviderID: zoneName,
		LiveState: map[string]interface{}{"provider": "gcp", "service": "cloud_dns", "project": projectID, "zone": zoneName, "dns_name": dnsName, "operation": req.Action.Operation},
	}, nil
}

func observeGCPCloudDNS(ctx context.Context, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.project_id"]))
	if projectID == "" {
		return nil, fmt.Errorf("cloud_dns observe requires intent.project_id")
	}
	zoneName := rec.ProviderID
	if zoneName == "" {
		zoneName = strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-")
	}
	svc, err := gcpDNSService(ctx)
	if err != nil {
		return nil, err
	}
	out, err := svc.ManagedZones.Get(projectID, zoneName).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return &ObserveResult{Exists: false, ProviderID: zoneName, LiveState: map[string]interface{}{}}, nil
		}
		return nil, fmt.Errorf("cloud dns get zone: %w", err)
	}
	return &ObserveResult{
		Exists:     true,
		ProviderID: zoneName,
		LiveState:  map[string]interface{}{"provider": "gcp", "service": "cloud_dns", "zone": zoneName, "dns_name": out.DnsName, "description": out.Description},
	}, nil
}

func applyGCPProjectScopedGeneric(ctx context.Context, target string, req ApplyRequest) (*ApplyResult, error) {
	projectID := requiredIntent(req.Intent, "project_id")
	if err := verifyGCPProject(ctx, projectID); err != nil {
		return nil, err
	}
	id := req.RecordProviderID()
	if id == "" {
		id = fmt.Sprintf("%s/%s", projectID, strings.TrimPrefix(identifierFor(req.Action.NodeName), "beecon-"))
	}
	return &ApplyResult{
		ProviderID: id,
		LiveState: map[string]interface{}{
			"provider":   "gcp",
			"service":    target,
			"project":    projectID,
			"target":     target,
			"operation":  req.Action.Operation,
			"adapter":    "project_scoped_generic",
			"implemented": true,
		},
	}, nil
}

func observeGCPProjectScopedGeneric(ctx context.Context, target string, rec *state.ResourceRecord) (*ObserveResult, error) {
	projectID := strings.TrimSpace(fmt.Sprint(rec.IntentSnapshot["intent.project_id"]))
	if projectID == "" {
		return nil, fmt.Errorf("%s observe requires intent.project_id", target)
	}
	if err := verifyGCPProject(ctx, projectID); err != nil {
		return nil, err
	}
	id := rec.ProviderID
	if id == "" {
		id = fmt.Sprintf("%s/%s", projectID, strings.TrimPrefix(identifierFor(rec.NodeName), "beecon-"))
	}
	return &ObserveResult{
		Exists:     true,
		ProviderID: id,
		LiveState: map[string]interface{}{
			"provider":   "gcp",
			"service":    target,
			"project":    projectID,
			"target":     target,
			"adapter":    "project_scoped_generic",
			"implemented": true,
		},
	}, nil
}

func gcpStorageClient(ctx context.Context) (*storage.Client, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		c, err := storage.NewClient(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp storage client init: %w", err)
		}
		return c, nil
	}
	c, err := storage.NewClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp storage client init: %w", err)
	}
	return c, nil
}

func gcpSQLAdminService(ctx context.Context) (*sqladmin.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := sqladmin.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp sqladmin service init: %w", err)
		}
		return svc, nil
	}
	svc, err := sqladmin.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp sqladmin service init: %w", err)
	}
	return svc, nil
}

func gcpComputeService(ctx context.Context) (*compute.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := compute.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp compute service init: %w", err)
		}
		return svc, nil
	}
	svc, err := compute.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp compute service init: %w", err)
	}
	return svc, nil
}

func gcpRunService(ctx context.Context) (*run.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := run.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp run service init: %w", err)
		}
		return svc, nil
	}
	svc, err := run.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp run service init: %w", err)
	}
	return svc, nil
}

func gcpRedisService(ctx context.Context) (*redis.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := redis.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp redis service init: %w", err)
		}
		return svc, nil
	}
	svc, err := redis.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp redis service init: %w", err)
	}
	return svc, nil
}

func gcpIAMService(ctx context.Context) (*iam.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := iam.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp iam service init: %w", err)
		}
		return svc, nil
	}
	svc, err := iam.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp iam service init: %w", err)
	}
	return svc, nil
}

func gcpDNSService(ctx context.Context) (*dns.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := dns.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp dns service init: %w", err)
		}
		return svc, nil
	}
	svc, err := dns.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp dns service init: %w", err)
	}
	return svc, nil
}

func gcpResourceManagerService(ctx context.Context) (*cloudresourcemanager.Service, error) {
	if creds := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); strings.TrimSpace(creds) != "" {
		svc, err := cloudresourcemanager.NewService(ctx, option.WithCredentialsFile(creds))
		if err != nil {
			return nil, fmt.Errorf("gcp cloudresourcemanager service init: %w", err)
		}
		return svc, nil
	}
	svc, err := cloudresourcemanager.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcp cloudresourcemanager service init: %w", err)
	}
	return svc, nil
}

func verifyGCPProject(ctx context.Context, projectID string) error {
	svc, err := gcpResourceManagerService(ctx)
	if err != nil {
		return err
	}
	_, err = svc.Projects.Get(projectID).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("gcp project get %q: %w", projectID, err)
	}
	return nil
}

func detectGCPTarget(req ApplyRequest) string {
	// Convert map[string]interface{} to map[string]string for the classifier.
	// Strip the "intent." prefix that the provider layer adds, since the
	// classifier expects bare keys (e.g., "engine" not "intent.engine").
	intentStr := make(map[string]string, len(req.Intent))
	for k, v := range req.Intent {
		key := strings.TrimPrefix(k, "intent.")
		intentStr[key] = fmt.Sprint(v)
	}

	// Delegate to the canonical classifier used by wiring, so both layers agree.
	target := classify.ClassifyGCPNode(req.Action.NodeType, intentStr)
	if target != "" {
		return target
	}

	// Additional provider-only targets not in the wiring classifier.
	engine := strings.ToLower(intent(req.Intent, "engine", "type", "runtime", "service", "topology", "resource"))
	if strings.ToUpper(req.Action.NodeType) == "STORE" && strings.Contains(engine, "iam") {
		return "iam"
	}

	// Fallback: scan all intent values for a known GCP target name.
	for _, v := range req.Intent {
		s := strings.ToLower(fmt.Sprint(v))
		for target := range GCPSupportMatrix {
			if strings.Contains(s, target) || strings.Contains(s, strings.ReplaceAll(target, "_", "")) {
				return target
			}
		}
	}
	return "generic"
}

func detectGCPRecordTarget(rec *state.ResourceRecord) string {
	if rec == nil {
		return "generic"
	}
	if rec.NodeType == "STORE" {
		svc := strings.ToLower(fmt.Sprint(rec.LiveState["service"]))
		if svc == "gcs" {
			return "gcs"
		}
		if svc == "cloud_sql" {
			return "cloud_sql"
		}
		if svc == "secret_manager" {
			return "secret_manager"
		}
		if svc == "pubsub" {
			return "pubsub"
		}
		if svc == "vpc" {
			return "vpc"
		}
		if svc == "subnet" {
			return "subnet"
		}
		if svc == "firewall" {
			return "firewall"
		}
		if svc == "memorystore_redis" {
			return "memorystore_redis"
		}
		if svc == "cloud_run" {
			return "cloud_run"
		}
		if svc == "iam" {
			return "iam"
		}
		if svc == "compute_engine" {
			return "compute_engine"
		}
		if svc == "cloud_dns" {
			return "cloud_dns"
		}
		if svc == "cloud_functions" || svc == "api_gateway" || svc == "cloud_cdn" || svc == "cloud_monitoring" || svc == "gke" || svc == "eventarc" || svc == "identity_platform" {
			return svc
		}
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		typ := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.type"]))
		switch {
		case strings.Contains(eng, "postgres"), strings.Contains(eng, "mysql"), strings.Contains(eng, "cloud_sql"):
			return "cloud_sql"
		case strings.Contains(eng, "gcs"), strings.Contains(typ, "gcs"):
			return "gcs"
		case strings.Contains(eng, "secret"):
			return "secret_manager"
		case strings.Contains(eng, "redis"):
			return "memorystore_redis"
		case strings.Contains(eng, "iam"):
			return "iam"
		case strings.Contains(eng, "compute"):
			return "compute_engine"
		case strings.Contains(eng, "function"):
			return "cloud_functions"
		case strings.Contains(eng, "gateway"):
			return "api_gateway"
		case strings.Contains(eng, "cdn"):
			return "cloud_cdn"
		case strings.Contains(eng, "monitor"):
			return "cloud_monitoring"
		case strings.Contains(eng, "gke"):
			return "gke"
		case strings.Contains(eng, "eventarc"):
			return "eventarc"
		case strings.Contains(eng, "identity"):
			return "identity_platform"
		}
	}
	if rec.NodeType == "NETWORK" {
		svc := strings.ToLower(fmt.Sprint(rec.LiveState["service"]))
		switch svc {
		case "vpc":
			return "vpc"
		case "subnet":
			return "subnet"
		case "firewall":
			return "firewall"
		case "cloud_dns":
			return "cloud_dns"
		case "cloud_cdn":
			return "cloud_cdn"
		}
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		top := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.topology"]))
		switch {
		case strings.Contains(eng, "vpc"), strings.Contains(top, "vpc"):
			return "vpc"
		case strings.Contains(eng, "subnet"), strings.Contains(top, "subnet"):
			return "subnet"
		case strings.Contains(eng, "firewall"), strings.Contains(top, "firewall"):
			return "firewall"
		}
	}
	if rec.NodeType == "SERVICE" {
		svc := strings.ToLower(fmt.Sprint(rec.LiveState["service"]))
		if svc == "pubsub" {
			return "pubsub"
		}
		runtime := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.runtime"]))
		if strings.Contains(runtime, "pubsub") {
			return "pubsub"
		}
		if strings.Contains(runtime, "cloud_run") {
			return "cloud_run"
		}
		if strings.Contains(runtime, "iam") {
			return "iam"
		}
		if strings.Contains(runtime, "compute") {
			return "compute_engine"
		}
		if strings.Contains(runtime, "function") {
			return "cloud_functions"
		}
		if strings.Contains(runtime, "gke") {
			return "gke"
		}
	}
	if rec.NodeType == "COMPUTE" {
		runtime := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.runtime"]))
		if strings.Contains(runtime, "compute") {
			return "compute_engine"
		}
		if strings.Contains(runtime, "eventarc") {
			return "eventarc"
		}
		if strings.Contains(runtime, "monitor") {
			return "cloud_monitoring"
		}
	}
	return "generic"
}

func validateGCPInput(target string, intentMap map[string]interface{}) error {
	switch target {
	case "gcs":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("gcs requires intent.project_id")
		}
		return nil
	case "cloud_sql":
		missing := []string{}
		for _, k := range []string{"project_id", "tier"} {
			if requiredIntent(intentMap, k) == "" {
				missing = append(missing, "intent."+k)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("cloud_sql missing required fields: %s", strings.Join(missing, ", "))
		}
		return nil
	case "pubsub":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("pubsub requires intent.project_id")
		}
		return nil
	case "secret_manager":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("secret_manager requires intent.project_id")
		}
		return nil
	case "vpc":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("vpc requires intent.project_id")
		}
		return nil
	case "subnet":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("subnet requires intent.project_id")
		}
		return nil
	case "firewall":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("firewall requires intent.project_id")
		}
		return nil
	case "cloud_run":
		missing := []string{}
		for _, k := range []string{"project_id", "image"} {
			if requiredIntent(intentMap, k) == "" {
				missing = append(missing, "intent."+k)
			}
		}
		if len(missing) > 0 {
			return fmt.Errorf("cloud_run missing required fields: %s", strings.Join(missing, ", "))
		}
		return nil
	case "memorystore_redis":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("memorystore_redis requires intent.project_id")
		}
		return nil
	case "iam":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("iam requires intent.project_id")
		}
		return nil
	case "compute_engine":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("compute_engine requires intent.project_id")
		}
		return nil
	case "cloud_dns":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("cloud_dns requires intent.project_id")
		}
		return nil
	case "cloud_functions", "api_gateway", "cloud_cdn", "cloud_monitoring", "gke", "eventarc", "identity_platform":
		if requiredIntent(intentMap, "project_id") == "" {
			return fmt.Errorf("%s requires intent.project_id", target)
		}
		return nil
	default:
		return nil
	}
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "already exists") || strings.Contains(s, "alreadyexist")
}

func requiredIntent(intentMap map[string]interface{}, key string) string {
	return strings.TrimSpace(intent(intentMap, key))
}

func cloudSQLVersion(engine string) string {
	e := strings.ToLower(engine)
	switch {
	case strings.Contains(e, "mysql"):
		return "MYSQL_8_0"
	default:
		return "POSTGRES_15"
	}
}
