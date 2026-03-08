package compliance

// hipaaConstraints defines HIPAA compliance rules per AWS resource type.
var hipaaConstraints = []Constraint{
	{
		Framework: "hipaa",
		Targets:   []string{"rds", "rds_aurora_serverless"},
		Rules: []Rule{
			{Field: "backup_retention", MinValue: "7", DefaultValue: "7", Description: "HIPAA requires ≥7 day backup retention"},
			{Field: "multi_az", DefaultValue: "true", Description: "HIPAA requires multi-AZ for data stores"},
			{Field: "deletion_protection", DefaultValue: "true", Description: "HIPAA requires deletion protection"},
			{Field: "encryption", DefaultValue: "cmk", Description: "HIPAA requires CMK encryption"},
			{Field: "kms_key", Required: true, Description: "HIPAA requires customer-managed KMS key"},
			{Field: "audit_logging", DefaultValue: "true", Description: "HIPAA requires audit logging"},
		},
	},
	{
		Framework: "hipaa",
		Targets:   []string{"elasticache"},
		Rules: []Rule{
			{Field: "encryption", DefaultValue: "cmk", Description: "HIPAA requires CMK encryption for data at rest"},
			{Field: "transit_encryption", DefaultValue: "true", Description: "HIPAA requires encryption in transit"},
			{Field: "kms_key", Required: true, Description: "HIPAA requires customer-managed KMS key"},
			{Field: "backup_retention", MinValue: "7", DefaultValue: "7", Description: "HIPAA requires ≥7 day backup retention"},
		},
	},
	{
		Framework: "hipaa",
		Targets:   []string{"s3"},
		Rules: []Rule{
			{Field: "encryption", DefaultValue: "cmk", Description: "HIPAA requires CMK encryption"},
			{Field: "kms_key", Required: true, Description: "HIPAA requires customer-managed KMS key"},
			{Field: "versioning", DefaultValue: "true", Description: "HIPAA requires S3 versioning"},
			{Field: "public_access_block", DefaultValue: "true", Description: "HIPAA prohibits public access on data-tier S3"},
		},
	},
	{
		Framework: "hipaa",
		Targets:   []string{"ecs", "lambda"},
		Rules: []Rule{
			{Field: "audit_logging", DefaultValue: "true", Description: "HIPAA requires audit logging for compute"},
		},
	},
	{
		Framework: "hipaa",
		Targets:   []string{"vpc"},
		Rules: []Rule{
			{Field: "flow_logs", DefaultValue: "true", Description: "HIPAA requires VPC flow logs"},
		},
	},
}

// soc2Constraints defines SOC2 compliance rules per AWS resource type.
var soc2Constraints = []Constraint{
	{
		Framework: "soc2",
		Targets:   []string{"rds", "rds_aurora_serverless"},
		Rules: []Rule{
			{Field: "backup_retention", MinValue: "1", DefaultValue: "1", Description: "SOC2 requires ≥1 day backup retention"},
			{Field: "encryption", DefaultValue: "true", Description: "SOC2 requires encryption at rest"},
			{Field: "audit_logging", DefaultValue: "true", Description: "SOC2 requires audit logging"},
		},
	},
	{
		Framework: "soc2",
		Targets:   []string{"s3"},
		Rules: []Rule{
			{Field: "encryption", DefaultValue: "true", Description: "SOC2 requires encryption at rest"},
			{Field: "public_access_block", DefaultValue: "true", Description: "SOC2 requires S3 public access block"},
		},
	},
	{
		Framework: "soc2",
		Targets:   []string{"ecs", "lambda"},
		Rules: []Rule{
			{Field: "audit_logging", DefaultValue: "true", Description: "SOC2 requires audit logging for compute"},
		},
	},
	{
		Framework: "soc2",
		Targets:   []string{"vpc"},
		Rules: []Rule{
			{Field: "flow_logs", DefaultValue: "true", Description: "SOC2 requires VPC flow logs"},
		},
	},
}

// FrameworkConstraints maps framework name to its constraint set.
var FrameworkConstraints = map[string][]Constraint{
	"hipaa": hipaaConstraints,
	"soc2":  soc2Constraints,
}
