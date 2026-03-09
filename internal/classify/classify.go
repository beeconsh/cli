package classify

import (
	"strings"
)

// ClassifyNode determines the AWS target resource type from a node's type and
// intent fields. This is extracted from executor.detectAWSTarget for reuse by
// the wiring and cost packages.
func ClassifyNode(nodeType string, intent map[string]string) string {
	nodeType = strings.ToUpper(nodeType)
	engine := engineField(intent)
	expose := strings.ToLower(FieldVal(intent, "expose"))

	if nodeType == "STORE" {
		switch {
		case strings.Contains(engine, "aurora"):
			return "rds_aurora_serverless"
		case strings.Contains(engine, "postgres"), strings.Contains(engine, "mysql"):
			return "rds"
		case strings.Contains(engine, "redis"):
			return "elasticache"
		case strings.Contains(engine, "sqs"):
			return "sqs"
		case strings.Contains(engine, "sns"):
			return "sns"
		case strings.Contains(engine, "s3"), strings.Contains(engine, "bucket"):
			return "s3"
		case strings.Contains(engine, "secret"):
			return "secrets_manager"
		}
	}
	if nodeType == "NETWORK" {
		switch {
		case strings.Contains(engine, "vpc"):
			return "vpc"
		case strings.Contains(engine, "subnet"):
			return "subnet"
		case strings.Contains(engine, "security_group") || strings.Contains(engine, "sg"):
			return "security_group"
		case strings.Contains(engine, "alb"):
			return "alb"
		case strings.Contains(engine, "api_gateway"), strings.Contains(engine, "apigateway"):
			return "api_gateway"
		case strings.Contains(engine, "cloudfront"):
			return "cloudfront"
		case strings.Contains(engine, "route53") || strings.Contains(engine, "dns"):
			return "route53"
		case strings.Contains(engine, "cloudwatch"):
			return "cloudwatch"
		}
	}
	if nodeType == "SERVICE" {
		switch {
		case strings.Contains(engine, "lambda"):
			return "lambda"
		case strings.Contains(engine, "container"):
			if strings.Contains(expose, "api") {
				return "api_gateway"
			}
			return "ecs"
		case strings.Contains(engine, "eks"):
			return "eks"
		case strings.Contains(engine, "cognito"):
			return "cognito"
		case strings.Contains(engine, "ec2"):
			return "ec2"
		}
	}
	if nodeType == "COMPUTE" {
		switch {
		case strings.Contains(engine, "lambda"):
			return "lambda"
		case strings.Contains(engine, "eventbridge"):
			return "eventbridge"
		case strings.Contains(engine, "cloudwatch"):
			return "cloudwatch"
		case strings.Contains(engine, "cognito"):
			return "cognito"
		case strings.Contains(engine, "ec2"):
			return "ec2"
		}
	}
	return ""
}

// IsVPCResident returns true if the AWS target is typically deployed inside a VPC.
func IsVPCResident(target string) bool {
	switch target {
	case "rds", "rds_aurora_serverless", "elasticache", "ecs", "eks", "ec2":
		return true
	}
	return false
}

// DefaultPort returns the default port for a VPC-resident resource type.
func DefaultPort(target string) int {
	switch target {
	case "rds":
		return 5432 // default to postgres
	case "rds_aurora_serverless":
		return 5432
	case "elasticache":
		return 6379
	}
	return 0
}

// DefaultPortForEngine returns the port for a specific database engine.
func DefaultPortForEngine(engine string) int {
	engine = strings.ToLower(engine)
	switch {
	case strings.Contains(engine, "mysql"):
		return 3306
	case strings.Contains(engine, "postgres"):
		return 5432
	case strings.Contains(engine, "redis"):
		return 6379
	}
	return 0
}

func engineField(intent map[string]string) string {
	for _, k := range []string{"engine", "type", "runtime", "service", "topology", "resource"} {
		if v, ok := intent[k]; ok {
			return strings.ToLower(v)
		}
	}
	return ""
}

// FieldVal returns the value for a key in an intent map, or "" if absent.
func FieldVal(intent map[string]string, key string) string {
	if v, ok := intent[key]; ok {
		return v
	}
	return ""
}

// ClassifyGCPNode determines the GCP target resource type from a node's type
// and intent fields. This is the GCP counterpart of ClassifyNode.
func ClassifyGCPNode(nodeType string, intent map[string]string) string {
	nodeType = strings.ToUpper(nodeType)
	engine := engineField(intent)

	if nodeType == "STORE" {
		switch {
		case strings.Contains(engine, "postgres"),
			strings.Contains(engine, "mysql"),
			strings.Contains(engine, "cloudsql"):
			return "cloud_sql"
		case strings.Contains(engine, "redis"):
			return "memorystore_redis"
		case strings.Contains(engine, "s3"),
			strings.Contains(engine, "bucket"),
			strings.Contains(engine, "gcs"):
			return "gcs"
		case strings.Contains(engine, "sqs"),
			strings.Contains(engine, "pubsub"):
			return "pubsub"
		case strings.Contains(engine, "sns"):
			return "pubsub"
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
		case strings.Contains(engine, "firewall"),
			strings.Contains(engine, "security_group"),
			strings.Contains(engine, "sg"):
			return "firewall"
		case strings.Contains(engine, "dns"),
			strings.Contains(engine, "route53"),
			strings.Contains(engine, "cloud_dns"):
			return "cloud_dns"
		case strings.Contains(engine, "cdn"),
			strings.Contains(engine, "cloudfront"):
			return "cloud_cdn"
		}
	}
	if nodeType == "SERVICE" {
		switch {
		case strings.Contains(engine, "lambda"),
			strings.Contains(engine, "cloud_functions"),
			strings.Contains(engine, "function"):
			return "cloud_functions"
		case strings.Contains(engine, "container"):
			return "cloud_run"
		case strings.Contains(engine, "eks"),
			strings.Contains(engine, "gke"),
			strings.Contains(engine, "kubernetes"):
			return "gke"
		case strings.Contains(engine, "ec2"),
			strings.Contains(engine, "compute"):
			return "compute_engine"
		case strings.Contains(engine, "cognito"),
			strings.Contains(engine, "identity"):
			return "identity_platform"
		}
	}
	if nodeType == "COMPUTE" {
		switch {
		case strings.Contains(engine, "lambda"),
			strings.Contains(engine, "function"):
			return "cloud_functions"
		case strings.Contains(engine, "eventbridge"),
			strings.Contains(engine, "eventarc"):
			return "eventarc"
		case strings.Contains(engine, "cloudwatch"),
			strings.Contains(engine, "monitoring"):
			return "cloud_monitoring"
		case strings.Contains(engine, "ec2"),
			strings.Contains(engine, "compute"):
			return "compute_engine"
		}
	}
	return ""
}

// IsGCPVPCResident returns true if the GCP target is typically deployed inside a VPC.
// Note: Cloud Run uses VPC connectors rather than traditional VPC firewall rules,
// so it is NOT included here.
func IsGCPVPCResident(target string) bool {
	switch target {
	case "cloud_sql", "memorystore_redis", "gke", "compute_engine":
		return true
	}
	return false
}

// GCPDefaultPort returns the default port for a VPC-resident GCP resource type.
func GCPDefaultPort(target string) int {
	switch target {
	case "cloud_sql":
		return 5432 // default to postgres
	case "memorystore_redis":
		return 6379
	}
	return 0
}

// GCPDefaultPortForEngine returns the port for a specific database engine on GCP.
func GCPDefaultPortForEngine(engine string) int {
	engine = strings.ToLower(engine)
	switch {
	case strings.Contains(engine, "mysql"):
		return 3306
	case strings.Contains(engine, "postgres"):
		return 5432
	case strings.Contains(engine, "redis"):
		return 6379
	}
	return 0
}
