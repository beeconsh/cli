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
