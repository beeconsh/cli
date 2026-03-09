package wiring

import (
	"strings"

	"github.com/terracotta-ai/beecon/internal/classify"
)

// EnvVarSet holds inferred environment variables for a dependency.
type EnvVarSet struct {
	Vars map[string]string // e.g. {"DATABASE_URL": "${postgres.url}", "DATABASE_HOST": "${postgres.host}"}
}

// InferEnvVars generates environment variable names for a dependency based on
// the dependency name and the target's engine type.
//
// Rules:
//   - Single-word matching engine name → short form (DATABASE_URL, REDIS_URL)
//   - Multi-word or non-matching → prefix with uppercase dep name (POSTGRES_DATABASE_URL)
//   - Explicit env{} always wins (caller checks before calling this)
func InferEnvVars(depName, targetType string, targetIntent map[string]string) EnvVarSet {
	engine := strings.ToLower(classify.FieldVal(targetIntent, "engine"))
	prefix := envPrefix(depName, engine, targetType)

	vars := make(map[string]string)

	switch targetType {
	case "rds", "rds_aurora_serverless":
		vars[prefix+"DATABASE_URL"] = "${" + depName + ".url}"
		vars[prefix+"HOST"] = "${" + depName + ".host}"
		vars[prefix+"PORT"] = "${" + depName + ".port}"
	case "elasticache":
		vars[prefix+"REDIS_URL"] = "${" + depName + ".url}"
		vars[prefix+"HOST"] = "${" + depName + ".host}"
		vars[prefix+"PORT"] = "${" + depName + ".port}"
	case "s3":
		vars[prefix+"BUCKET_NAME"] = "${" + depName + ".bucket_name}"
		vars[prefix+"BUCKET_ARN"] = "${" + depName + ".arn}"
	case "sqs":
		vars[prefix+"QUEUE_URL"] = "${" + depName + ".queue_url}"
		vars[prefix+"QUEUE_ARN"] = "${" + depName + ".arn}"
	case "sns":
		vars[prefix+"TOPIC_ARN"] = "${" + depName + ".arn}"
	case "lambda":
		vars[prefix+"FUNCTION_NAME"] = "${" + depName + ".function_name}"
		vars[prefix+"FUNCTION_ARN"] = "${" + depName + ".arn}"
	case "secrets_manager":
		vars[prefix+"SECRET_ARN"] = "${" + depName + ".arn}"
	}

	return EnvVarSet{Vars: vars}
}

// envPrefix determines the env var prefix. If the dep name matches the engine
// type (e.g., dep "postgres" for engine "postgres"), use no prefix for a
// cleaner developer experience. Otherwise, uppercase the dep name as prefix.
func envPrefix(depName, engine, targetType string) string {
	depLower := strings.ToLower(depName)

	// If dep name matches engine or is a known short name, use no prefix
	if depLower == engine {
		return ""
	}
	if depLower == targetType {
		return ""
	}
	// Standard short names
	switch depLower {
	case "postgres", "mysql", "redis", "s3", "sqs", "sns":
		if strings.Contains(engine, depLower) || targetType == depLower || targetType == "rds" || targetType == "elasticache" {
			return ""
		}
	}

	return strings.ToUpper(depName) + "_"
}

// InferGCPEnvVars generates environment variable names for a GCP dependency
// based on the dependency name and the GCP target type. Follows the same prefix
// conventions as InferEnvVars but uses GCP-appropriate variable names.
func InferGCPEnvVars(depName, targetType string, targetIntent map[string]string) EnvVarSet {
	engine := strings.ToLower(classify.FieldVal(targetIntent, "engine"))
	prefix := gcpEnvPrefix(depName, engine, targetType)

	vars := make(map[string]string)

	switch targetType {
	case "cloud_sql":
		vars[prefix+"DATABASE_URL"] = "${" + depName + ".url}"
		vars[prefix+"DB_HOST"] = "${" + depName + ".host}"
		vars[prefix+"DB_PORT"] = "${" + depName + ".port}"
		vars[prefix+"INSTANCE_CONNECTION_NAME"] = "${" + depName + ".connection_name}"
	case "memorystore_redis":
		vars[prefix+"REDIS_URL"] = "${" + depName + ".url}"
		vars[prefix+"REDIS_HOST"] = "${" + depName + ".host}"
		vars[prefix+"REDIS_PORT"] = "${" + depName + ".port}"
	case "gcs":
		vars[prefix+"BUCKET_NAME"] = "${" + depName + ".bucket_name}"
	case "pubsub":
		vars[prefix+"TOPIC_NAME"] = "${" + depName + ".topic_name}"
		vars[prefix+"PROJECT_ID"] = "${" + depName + ".project_id}"
	case "secret_manager":
		vars[prefix+"SECRET_NAME"] = "${" + depName + ".secret_name}"
	case "cloud_functions":
		vars[prefix+"FUNCTION_NAME"] = "${" + depName + ".function_name}"
		vars[prefix+"FUNCTION_URL"] = "${" + depName + ".url}"
	case "cloud_run":
		vars[prefix+"SERVICE_URL"] = "${" + depName + ".url}"
	}

	return EnvVarSet{Vars: vars}
}

func gcpEnvPrefix(depName, engine, targetType string) string {
	depLower := strings.ToLower(depName)

	if depLower == engine {
		return ""
	}
	if depLower == targetType {
		return ""
	}
	switch depLower {
	case "postgres", "mysql", "redis", "gcs", "pubsub":
		if strings.Contains(engine, depLower) || targetType == depLower || targetType == "cloud_sql" || targetType == "memorystore_redis" {
			return ""
		}
	}

	return strings.ToUpper(depName) + "_"
}

// InferAzureEnvVars generates environment variable names for an Azure dependency
// based on the dependency name and the Azure target type. Follows the same prefix
// conventions as InferEnvVars but uses Azure-appropriate variable names.
func InferAzureEnvVars(depName, targetType string, targetIntent map[string]string) EnvVarSet {
	engine := strings.ToLower(classify.FieldVal(targetIntent, "engine"))
	prefix := azureEnvPrefix(depName, engine, targetType)

	vars := make(map[string]string)

	switch targetType {
	case "postgres_flexible":
		vars[prefix+"DATABASE_URL"] = "${" + depName + ".url}"
		vars[prefix+"DB_HOST"] = "${" + depName + ".host}"
		vars[prefix+"DB_PORT"] = "${" + depName + ".port}"
	case "mysql_flexible":
		vars[prefix+"DATABASE_URL"] = "${" + depName + ".url}"
		vars[prefix+"DB_HOST"] = "${" + depName + ".host}"
		vars[prefix+"DB_PORT"] = "${" + depName + ".port}"
	case "azure_cache_redis":
		vars[prefix+"REDIS_URL"] = "${" + depName + ".url}"
		vars[prefix+"REDIS_HOST"] = "${" + depName + ".host}"
		vars[prefix+"REDIS_PORT"] = "${" + depName + ".port}"
	case "blob_storage":
		vars[prefix+"STORAGE_ACCOUNT_URL"] = "${" + depName + ".storage_account_url}"
		vars[prefix+"CONTAINER_NAME"] = "${" + depName + ".container_name}"
	case "key_vault_secret":
		vars[prefix+"KEY_VAULT_URL"] = "${" + depName + ".key_vault_url}"
		vars[prefix+"SECRET_NAME"] = "${" + depName + ".secret_name}"
	case "service_bus":
		vars[prefix+"SERVICE_BUS_CONNECTION_STRING"] = "${" + depName + ".connection_string}"
	case "container_apps":
		vars[prefix+"SERVICE_URL"] = "${" + depName + ".url}"
	case "functions":
		vars[prefix+"FUNCTION_NAME"] = "${" + depName + ".function_name}"
		vars[prefix+"FUNCTION_URL"] = "${" + depName + ".url}"
	}

	return EnvVarSet{Vars: vars}
}

// azureEnvPrefix determines the env var prefix for Azure dependencies. If the dep
// name matches the engine type, use no prefix for a cleaner developer experience.
func azureEnvPrefix(depName, engine, targetType string) string {
	depLower := strings.ToLower(depName)

	if depLower == engine {
		return ""
	}
	if depLower == targetType {
		return ""
	}
	switch depLower {
	case "postgres", "mysql", "redis", "blob", "servicebus":
		if strings.Contains(engine, depLower) || targetType == depLower ||
			targetType == "postgres_flexible" || targetType == "mysql_flexible" ||
			targetType == "azure_cache_redis" {
			return ""
		}
	}

	return strings.ToUpper(depName) + "_"
}
