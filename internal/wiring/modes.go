package wiring

import "fmt"

// Mode represents a dependency access mode.
type Mode string

const (
	ModeRead      Mode = "read"
	ModeWrite     Mode = "write"
	ModeReadWrite Mode = "read_write"
	ModeInvoke    Mode = "invoke"
	ModePublish   Mode = "publish"
	ModeSubscribe Mode = "subscribe"
	ModeAdmin     Mode = "admin"
)

// NormalizeMode converts user-specified dependency modes to canonical form.
// Supports legacy modes like "read_only" and "read_write".
func NormalizeMode(raw string) (Mode, error) {
	switch raw {
	case "read", "read_only", "ro":
		return ModeRead, nil
	case "write", "wo":
		return ModeWrite, nil
	case "read_write", "rw":
		return ModeReadWrite, nil
	case "invoke":
		return ModeInvoke, nil
	case "publish", "pub":
		return ModePublish, nil
	case "subscribe", "sub":
		return ModeSubscribe, nil
	case "admin":
		return ModeAdmin, nil
	default:
		return "", fmt.Errorf("unknown dependency mode %q", raw)
	}
}

// ValidModes defines which modes are valid for each AWS target type.
var ValidModes = map[string][]Mode{
	"rds":                   {ModeRead, ModeWrite, ModeReadWrite, ModeAdmin},
	"rds_aurora_serverless":  {ModeRead, ModeWrite, ModeReadWrite, ModeAdmin},
	"elasticache":           {ModeRead, ModeWrite, ModeReadWrite},
	"s3":                    {ModeRead, ModeWrite, ModeReadWrite, ModeAdmin},
	"sqs":                   {ModeRead, ModeWrite, ModeReadWrite, ModePublish, ModeSubscribe},
	"sns":                   {ModePublish, ModeSubscribe},
	"lambda":                {ModeInvoke, ModeAdmin},
	"ecs":                   {ModeInvoke},
	"secrets_manager":       {ModeRead, ModeWrite, ModeReadWrite},
}

// IsValidMode checks whether a mode is valid for a given AWS target.
// Unknown target types are allowed (returns true) to support dependency
// declarations on targets not yet in the ValidModes registry.
func IsValidMode(target string, mode Mode) bool {
	if target == "" {
		return true // unclassified nodes skip mode validation
	}
	valid, ok := ValidModes[target]
	if !ok {
		return true // unknown targets: allow to avoid blocking valid needs declarations
	}
	for _, v := range valid {
		if v == mode {
			return true
		}
	}
	return false
}

// IAMActionsFor returns the IAM actions required for a given target+mode combination.
func IAMActionsFor(target string, mode Mode) ([]string, error) {
	key := target + ":" + string(mode)
	actions, ok := iamActionMatrix[key]
	if !ok {
		return nil, fmt.Errorf("no IAM actions defined for %s with mode %s", target, mode)
	}
	return actions, nil
}

var iamActionMatrix = map[string][]string{
	// RDS
	"rds:read":       {"rds-data:ExecuteStatement", "rds-data:BatchExecuteStatement", "rds:DescribeDBInstances"},
	"rds:write":      {"rds-data:ExecuteStatement", "rds-data:BatchExecuteStatement"},
	"rds:read_write":  {"rds-data:*"},
	"rds:admin":      {"rds:*"},
	// RDS Aurora Serverless
	"rds_aurora_serverless:read":       {"rds-data:ExecuteStatement", "rds-data:BatchExecuteStatement", "rds:DescribeDBClusters"},
	"rds_aurora_serverless:write":      {"rds-data:ExecuteStatement", "rds-data:BatchExecuteStatement"},
	"rds_aurora_serverless:read_write":  {"rds-data:*"},
	"rds_aurora_serverless:admin":      {"rds:*"},
	// S3
	"s3:read":       {"s3:GetObject", "s3:ListBucket"},
	"s3:write":      {"s3:PutObject", "s3:DeleteObject"},
	"s3:read_write":  {"s3:GetObject", "s3:PutObject", "s3:DeleteObject", "s3:ListBucket"},
	"s3:admin":      {"s3:*"},
	// SQS
	"sqs:read":      {"sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"},
	"sqs:write":     {"sqs:SendMessage"},
	"sqs:read_write": {"sqs:SendMessage", "sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"},
	"sqs:publish":   {"sqs:SendMessage"},
	"sqs:subscribe": {"sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:GetQueueAttributes"},
	// SNS
	"sns:publish":   {"sns:Publish"},
	"sns:subscribe": {"sns:Subscribe", "sns:Unsubscribe"},
	// Lambda
	"lambda:invoke": {"lambda:InvokeFunction"},
	"lambda:admin":  {"lambda:*"},
	// ECS
	"ecs:invoke": {"ecs:RunTask", "ecs:DescribeTasks"},
	// Secrets Manager
	"secrets_manager:read":       {"secretsmanager:GetSecretValue"},
	"secrets_manager:write":      {"secretsmanager:PutSecretValue", "secretsmanager:UpdateSecret"},
	"secrets_manager:read_write":  {"secretsmanager:GetSecretValue", "secretsmanager:PutSecretValue", "secretsmanager:UpdateSecret"},
	// ElastiCache — IAM auth is limited; typically uses SG access
	"elasticache:read":       {"elasticache:DescribeCacheClusters"},
	"elasticache:write":      {"elasticache:DescribeCacheClusters"},
	"elasticache:read_write":  {"elasticache:DescribeCacheClusters"},
}

// GCPValidModes defines which modes are valid for each GCP target type.
var GCPValidModes = map[string][]Mode{
	"cloud_sql":         {ModeRead, ModeWrite, ModeReadWrite, ModeAdmin},
	"memorystore_redis": {ModeRead, ModeWrite, ModeReadWrite},
	"gcs":               {ModeRead, ModeWrite, ModeReadWrite, ModeAdmin},
	"pubsub":            {ModeRead, ModeWrite, ModeReadWrite, ModePublish, ModeSubscribe},
	"secret_manager":    {ModeRead, ModeWrite, ModeReadWrite},
	"cloud_functions":   {ModeInvoke, ModeAdmin},
	"cloud_run":         {ModeInvoke},
	"gke":               {ModeInvoke},
}

// IsValidGCPMode checks whether a mode is valid for a given GCP target.
// Unknown target types are allowed (returns true) to support dependency
// declarations on targets not yet in the GCPValidModes registry.
func IsValidGCPMode(target string, mode Mode) bool {
	if target == "" {
		return true // unclassified nodes skip mode validation
	}
	valid, ok := GCPValidModes[target]
	if !ok {
		return true // unknown targets: allow to avoid blocking valid needs declarations
	}
	for _, v := range valid {
		if v == mode {
			return true
		}
	}
	return false
}

// GCPIAMRolesFor returns the GCP IAM roles required for a given target+mode combination.
func GCPIAMRolesFor(target string, mode Mode) ([]string, error) {
	key := target + ":" + string(mode)
	roles, ok := gcpIAMRoleMatrix[key]
	if !ok {
		return nil, fmt.Errorf("no GCP IAM roles defined for %s with mode %s", target, mode)
	}
	return roles, nil
}

var gcpIAMRoleMatrix = map[string][]string{
	// Cloud SQL
	"cloud_sql:read":       {"roles/cloudsql.viewer"},
	"cloud_sql:write":      {"roles/cloudsql.client"},
	"cloud_sql:read_write": {"roles/cloudsql.client"},
	"cloud_sql:admin":      {"roles/cloudsql.admin"},
	// GCS
	"gcs:read":       {"roles/storage.objectViewer"},
	"gcs:write":      {"roles/storage.objectCreator"},
	"gcs:read_write": {"roles/storage.objectUser"},
	"gcs:admin":      {"roles/storage.admin"},
	// Pub/Sub
	"pubsub:read":       {"roles/pubsub.subscriber"},
	"pubsub:write":      {"roles/pubsub.publisher"},
	"pubsub:read_write": {"roles/pubsub.editor"},
	"pubsub:publish":    {"roles/pubsub.publisher"},
	"pubsub:subscribe":  {"roles/pubsub.subscriber"},
	// Secret Manager
	"secret_manager:read":       {"roles/secretmanager.secretAccessor"},
	"secret_manager:write":      {"roles/secretmanager.secretVersionManager"},
	"secret_manager:read_write": {"roles/secretmanager.secretAccessor", "roles/secretmanager.secretVersionManager"},
	// Memorystore Redis
	"memorystore_redis:read":       {"roles/redis.viewer"},
	"memorystore_redis:write":      {"roles/redis.editor"},
	"memorystore_redis:read_write": {"roles/redis.editor"},
	// Cloud Functions
	"cloud_functions:invoke": {"roles/cloudfunctions.invoker"},
	"cloud_functions:admin":  {"roles/cloudfunctions.admin"},
	// Cloud Run
	"cloud_run:invoke": {"roles/run.invoker"},
	// GKE
	"gke:invoke": {"roles/container.developer"},
}

// AzureValidModes defines which modes are valid for each Azure target type.
var AzureValidModes = map[string][]Mode{
	"postgres_flexible":  {ModeRead, ModeWrite, ModeReadWrite, ModeAdmin},
	"mysql_flexible":     {ModeRead, ModeWrite, ModeReadWrite, ModeAdmin},
	"azure_cache_redis":  {ModeRead, ModeWrite, ModeReadWrite},
	"blob_storage":       {ModeRead, ModeWrite, ModeReadWrite, ModeAdmin},
	"key_vault_secret":   {ModeRead, ModeWrite, ModeReadWrite},
	"service_bus":        {ModeRead, ModeWrite, ModeReadWrite, ModePublish, ModeSubscribe},
	"container_apps":     {ModeInvoke},
	"functions":          {ModeInvoke, ModeAdmin},
	"aks":                {ModeInvoke},
}

// IsValidAzureMode checks whether a mode is valid for a given Azure target.
// Unknown target types are allowed (returns true) to support dependency
// declarations on targets not yet in the AzureValidModes registry.
func IsValidAzureMode(target string, mode Mode) bool {
	if target == "" {
		return true // unclassified nodes skip mode validation
	}
	valid, ok := AzureValidModes[target]
	if !ok {
		return true // unknown targets: allow to avoid blocking valid needs declarations
	}
	for _, v := range valid {
		if v == mode {
			return true
		}
	}
	return false
}

// AzureIAMRolesFor returns the Azure RBAC built-in role names required for a
// given target+mode combination.
func AzureIAMRolesFor(target string, mode Mode) ([]string, error) {
	key := target + ":" + string(mode)
	roles, ok := azureRBACRoleMatrix[key]
	if !ok {
		return nil, fmt.Errorf("no Azure RBAC roles defined for %s with mode %s", target, mode)
	}
	return roles, nil
}

var azureRBACRoleMatrix = map[string][]string{
	// Postgres Flexible Server
	"postgres_flexible:read":       {"Reader"},
	"postgres_flexible:write":      {"Contributor"},
	"postgres_flexible:read_write": {"Contributor"},
	"postgres_flexible:admin":      {"Owner"},
	// MySQL Flexible Server
	"mysql_flexible:read":       {"Reader"},
	"mysql_flexible:write":      {"Contributor"},
	"mysql_flexible:read_write": {"Contributor"},
	"mysql_flexible:admin":      {"Owner"},
	// Azure Cache for Redis
	"azure_cache_redis:read":       {"Reader"},
	"azure_cache_redis:write":      {"Contributor"},
	"azure_cache_redis:read_write": {"Contributor"},
	// Blob Storage
	"blob_storage:read":       {"Storage Blob Data Reader"},
	"blob_storage:write":      {"Storage Blob Data Contributor"},
	"blob_storage:read_write": {"Storage Blob Data Contributor"},
	"blob_storage:admin":      {"Storage Blob Data Owner"},
	// Key Vault Secret
	"key_vault_secret:read":       {"Key Vault Secrets User"},
	"key_vault_secret:write":      {"Key Vault Secrets Officer"},
	"key_vault_secret:read_write": {"Key Vault Secrets Officer"},
	// Service Bus
	"service_bus:read":       {"Azure Service Bus Data Receiver"},
	"service_bus:write":      {"Azure Service Bus Data Sender"},
	"service_bus:read_write": {"Azure Service Bus Data Sender", "Azure Service Bus Data Receiver"},
	"service_bus:publish":    {"Azure Service Bus Data Sender"},
	"service_bus:subscribe":  {"Azure Service Bus Data Receiver"},
	// Container Apps
	"container_apps:invoke": {"Contributor"},
	// Functions
	"functions:invoke": {"Contributor"},
	"functions:admin":  {"Owner"},
	// AKS
	"aks:invoke": {"Azure Kubernetes Service Cluster User Role"},
}
