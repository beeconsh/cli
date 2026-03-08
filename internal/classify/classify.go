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
	expose := strings.ToLower(fieldVal(intent, "expose"))

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

func fieldVal(intent map[string]string, key string) string {
	if v, ok := intent[key]; ok {
		return v
	}
	return ""
}
