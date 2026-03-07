package provider

import (
	"context"
	"fmt"
	"os"
	"strings"

	"cloud.google.com/go/storage"
	"github.com/terracotta-ai/beecon/internal/state"
	"google.golang.org/api/option"
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
	switch target {
	case "gcs":
		if err := validateGCPInput(target, req.Intent); err != nil {
			return nil, err
		}
		return applyGCPGCS(ctx, req)
	case "cloud_sql":
		if err := validateGCPInput(target, req.Intent); err != nil {
			return nil, err
		}
		return applyGCPCloudSQL(ctx, req)
	default:
		return nil, fmt.Errorf("gcp target %q is recognized but requires additional adapter implementation for live execution (set BEECON_EXECUTE!=1 for dry-run)", target)
	}
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
	switch target {
	case "gcs":
		return observeGCPGCS(ctx, rec)
	case "cloud_sql":
		return observeGCPCloudSQL(ctx, rec)
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

func detectGCPTarget(req ApplyRequest) string {
	nodeType := strings.ToUpper(req.Action.NodeType)
	engine := strings.ToLower(intent(req.Intent, "engine", "type", "runtime", "service", "topology", "resource"))
	expose := strings.ToLower(intent(req.Intent, "expose"))
	if nodeType == "STORE" {
		switch {
		case strings.Contains(engine, "postgres"), strings.Contains(engine, "mysql"), strings.Contains(engine, "cloud_sql"):
			return "cloud_sql"
		case strings.Contains(engine, "redis"):
			return "memorystore_redis"
		case strings.Contains(engine, "gcs"), strings.Contains(engine, "storage"), strings.Contains(engine, "bucket"):
			return "gcs"
		case strings.Contains(engine, "secret"):
			return "secret_manager"
		}
	}
	if nodeType == "NETWORK" {
		switch {
		case strings.Contains(engine, "vpc"):
			return "vpc"
		case strings.Contains(engine, "subnet"):
			return "subnet"
		case strings.Contains(engine, "firewall"):
			return "firewall"
		case strings.Contains(engine, "api_gateway"):
			return "api_gateway"
		case strings.Contains(engine, "cdn"):
			return "cloud_cdn"
		case strings.Contains(engine, "dns"):
			return "cloud_dns"
		}
	}
	if nodeType == "SERVICE" {
		switch {
		case strings.Contains(engine, "cloud_run"):
			return "cloud_run"
		case strings.Contains(engine, "gke"):
			return "gke"
		case strings.Contains(engine, "function"):
			return "cloud_functions"
		case strings.Contains(expose, "api"):
			return "api_gateway"
		}
	}
	if nodeType == "COMPUTE" {
		switch {
		case strings.Contains(engine, "eventarc"):
			return "eventarc"
		case strings.Contains(engine, "compute"):
			return "compute_engine"
		}
	}
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
		eng := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.engine"]))
		typ := strings.ToLower(fmt.Sprint(rec.IntentSnapshot["intent.type"]))
		switch {
		case strings.Contains(eng, "postgres"), strings.Contains(eng, "mysql"), strings.Contains(eng, "cloud_sql"):
			return "cloud_sql"
		case strings.Contains(eng, "gcs"), strings.Contains(typ, "gcs"):
			return "gcs"
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
	default:
		return nil
	}
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
