package wiring

import (
	"strings"
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
	engine := strings.ToLower(fieldVal(targetIntent, "engine"))
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

func fieldVal(intent map[string]string, key string) string {
	if v, ok := intent[key]; ok {
		return v
	}
	return ""
}
