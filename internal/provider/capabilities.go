package provider

import "sort"

// ProviderCapability describes the capability summary for a single cloud provider.
type ProviderCapability struct {
	Provider     string             `json:"provider"`
	Targets      []TargetCapability `json:"targets"`
	TotalReal    int                `json:"total_real"`
	TotalGeneric int                `json:"total_generic"`
}

// TargetCapability describes the implementation status of a single target type.
type TargetCapability struct {
	Target     string `json:"target"`
	Tier       string `json:"tier"`        // tier1, tier2, tier3
	Adapter    string `json:"adapter"`     // "real", "generic", "simulated"
	HasObserve bool   `json:"has_observe"` // true if has deep observation
	HasWiring  bool   `json:"has_wiring"`  // true if wiring layer covers this target
}

// AWS targets with real SDK adapters (not generic/simulated).
var awsRealAdapters = map[string]bool{
	"ecs": true, "rds": true, "rds_aurora_serverless": true, "elasticache": true,
	"s3": true, "alb": true, "vpc": true, "subnet": true, "security_group": true,
	"iam": true, "secrets_manager": true, "lambda": true, "api_gateway": true,
	"sqs": true, "sns": true, "cloudfront": true, "route53": true, "cloudwatch": true,
	"eks": true, "eventbridge": true, "cognito": true, "ec2": true,
}

// Azure targets with real SDK adapters (not ARM generic).
var azureRealAdapters = map[string]bool{
	"blob_storage": true, "key_vault_secret": true, "vnet": true, "subnet": true,
	"nsg": true, "managed_identity": true, "rbac": true, "entra_id": true,
}

// GCP targets with real SDK adapters (not project-scoped generic).
var gcpRealAdapters = map[string]bool{
	"cloud_run": true, "cloud_sql": true, "memorystore_redis": true, "gcs": true,
	"pubsub": true, "secret_manager": true, "cloud_functions": true, "gke": true,
	"cloud_cdn": true, "cloud_monitoring": true, "eventarc": true, "api_gateway": true,
	"identity_platform": true, "vpc": true, "subnet": true, "firewall": true,
	"iam": true, "compute_engine": true, "cloud_dns": true,
}

// AWS targets with deep observe implementations.
// Only targets with explicit observe switch cases (not default passthrough).
var awsObserveTargets = map[string]bool{
	"rds": true, "s3": true, "sqs": true, "sns": true, "secrets_manager": true,
	"iam": true, "lambda": true, "elasticache": true, "cloudfront": true,
	"route53": true, "cloudwatch": true, "eks": true, "eventbridge": true,
	"cognito": true, "vpc": true, "subnet": true, "security_group": true, "ec2": true,
}

// Azure targets with deep observe implementations.
var azureObserveTargets = map[string]bool{
	"blob_storage": true, "key_vault_secret": true, "vnet": true, "subnet": true,
	"nsg": true, "managed_identity": true, "rbac": true, "entra_id": true,
}

// GCP targets with deep observe implementations.
var gcpObserveTargets = map[string]bool{
	"cloud_run": true, "cloud_sql": true, "memorystore_redis": true, "gcs": true,
	"pubsub": true, "secret_manager": true, "cloud_functions": true, "gke": true,
	"cloud_cdn": true, "cloud_monitoring": true, "eventarc": true, "api_gateway": true,
	"identity_platform": true, "vpc": true, "subnet": true, "firewall": true,
	"iam": true, "compute_engine": true, "cloud_dns": true,
}

// Wiring-covered targets by provider.
var wiringTargets = map[string]map[string]bool{
	"aws": {
		"ecs": true, "rds": true, "rds_aurora_serverless": true, "elasticache": true,
		"s3": true, "lambda": true, "sqs": true, "sns": true, "secrets_manager": true,
		"security_group": true,
	},
	"gcp": {
		"cloud_run": true, "cloud_sql": true, "memorystore_redis": true, "gcs": true,
		"pubsub": true, "secret_manager": true, "cloud_functions": true,
	},
	"azure": {
		"container_apps": true, "postgres_flexible": true, "mysql_flexible": true,
		"azure_cache_redis": true, "blob_storage": true, "key_vault_secret": true,
		"service_bus": true, "functions": true,
	},
}

// GetProviderCapabilities returns the capability summary for a given provider.
// Returns nil if the provider is not recognized.
func GetProviderCapabilities(provider string) *ProviderCapability {
	var matrix map[string]string
	var realAdapters map[string]bool
	var observeTargets map[string]bool

	switch provider {
	case "aws":
		matrix = AWSSupportMatrix
		realAdapters = awsRealAdapters
		observeTargets = awsObserveTargets
	case "gcp":
		matrix = GCPSupportMatrix
		realAdapters = gcpRealAdapters
		observeTargets = gcpObserveTargets
	case "azure":
		matrix = AzureSupportMatrix
		realAdapters = azureRealAdapters
		observeTargets = azureObserveTargets
	default:
		return nil
	}

	wiring := wiringTargets[provider]
	targets := make([]TargetCapability, 0, len(matrix))
	totalReal := 0
	totalGeneric := 0

	// Sort target names for deterministic output.
	names := make([]string, 0, len(matrix))
	for name := range matrix {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		tier := matrix[name]
		adapter := "generic"
		if realAdapters[name] {
			adapter = "real"
			totalReal++
		} else {
			totalGeneric++
		}
		targets = append(targets, TargetCapability{
			Target:     name,
			Tier:       tier,
			Adapter:    adapter,
			HasObserve: observeTargets[name],
			HasWiring:  wiring[name],
		})
	}

	return &ProviderCapability{
		Provider:     provider,
		Targets:      targets,
		TotalReal:    totalReal,
		TotalGeneric: totalGeneric,
	}
}

// GetAllProviderCapabilities returns capabilities for all supported providers.
func GetAllProviderCapabilities() []*ProviderCapability {
	providers := []string{"aws", "gcp", "azure"}
	result := make([]*ProviderCapability, 0, len(providers))
	for _, p := range providers {
		if cap := GetProviderCapabilities(p); cap != nil {
			result = append(result, cap)
		}
	}
	return result
}
