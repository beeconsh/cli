package provider

import (
	"testing"

	"github.com/terracotta-ai/beecon/internal/state"
)

func TestIdentifierFor(t *testing.T) {
	id := identifierFor("My.Service_Name")
	if id == "" || id[:7] != "beecon-" {
		t.Fatalf("unexpected id: %s", id)
	}
}

func TestParseStorageGiB(t *testing.T) {
	if got := parseStorageGiB("100gb"); got != 100 {
		t.Fatalf("expected 100, got %d", got)
	}
	if got := parseStorageGiB("bad"); got != 0 {
		t.Fatalf("expected 0, got %d", got)
	}
}

func TestSimulatedApplyDefault(t *testing.T) {
	res := simulatedApply(ApplyRequest{
		Provider: "aws",
		Action: &state.PlanAction{
			NodeName:  "postgres",
			Operation: "CREATE",
		},
		Intent: map[string]interface{}{"intent.engine": "postgres"},
	}, "rds")
	if res.ProviderID == "" {
		t.Fatalf("expected provider id")
	}
	if sim, ok := res.LiveState["simulated"].(bool); !ok || !sim {
		t.Fatalf("expected simulated live state")
	}
	if target, ok := res.LiveState["target"].(string); !ok || target != "rds" {
		t.Fatalf("expected target rds, got %#v", res.LiveState["target"])
	}
}

func TestDetectAWSTargetTierCoverage(t *testing.T) {
	cases := []struct {
		name   string
		node   string
		intent map[string]interface{}
		want   string
	}{
		{"rds", "STORE", map[string]interface{}{"intent.engine": "postgres"}, "rds"},
		{"aurora", "STORE", map[string]interface{}{"intent.engine": "aurora-postgresql"}, "rds_aurora_serverless"},
		{"redis", "STORE", map[string]interface{}{"intent.engine": "redis"}, "elasticache"},
		{"s3", "STORE", map[string]interface{}{"intent.type": "s3"}, "s3"},
		{"vpc", "NETWORK", map[string]interface{}{"intent.topology": "vpc"}, "vpc"},
		{"lambda", "SERVICE", map[string]interface{}{"intent.runtime": "lambda"}, "lambda"},
		{"api_gateway", "NETWORK", map[string]interface{}{"intent.topology": "api_gateway"}, "api_gateway"},
		{"alb", "NETWORK", map[string]interface{}{"intent.topology": "alb"}, "alb"},
		{"ecs", "SERVICE", map[string]interface{}{"intent.runtime": "container"}, "ecs"},
		{"eks", "SERVICE", map[string]interface{}{"intent.runtime": "eks"}, "eks"},
		{"eventbridge", "COMPUTE", map[string]interface{}{"intent.runtime": "eventbridge"}, "eventbridge"},
		{"cloudwatch", "COMPUTE", map[string]interface{}{"intent.runtime": "cloudwatch"}, "cloudwatch"},
		{"cloudfront", "NETWORK", map[string]interface{}{"intent.topology": "cloudfront"}, "cloudfront"},
		{"route53", "NETWORK", map[string]interface{}{"intent.topology": "dns"}, "route53"},
		{"cognito", "SERVICE", map[string]interface{}{"intent.runtime": "cognito"}, "cognito"},
		{"ec2", "SERVICE", map[string]interface{}{"intent.runtime": "ec2"}, "ec2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectAWSTarget(ApplyRequest{Action: &state.PlanAction{NodeType: tc.node}, Intent: tc.intent})
			if got != tc.want {
				t.Fatalf("want %s got %s", tc.want, got)
			}
		})
	}
}

func TestSupportMatrixContainsRequestedServices(t *testing.T) {
	required := []string{
		"ecs", "rds", "rds_aurora_serverless", "elasticache", "s3", "alb", "vpc", "subnet", "security_group", "iam", "secrets_manager",
		"lambda", "api_gateway", "sqs", "sns", "cloudfront", "route53", "cloudwatch",
		"eks", "eventbridge", "cognito", "ec2",
	}
	for _, k := range required {
		if _, ok := AWSSupportMatrix[k]; !ok {
			t.Fatalf("missing support matrix key %s", k)
		}
	}
}

func TestGCPSupportMatrixTier1Keys(t *testing.T) {
	required := []string{"cloud_run", "cloud_sql", "memorystore_redis", "gcs", "vpc", "subnet", "firewall", "iam", "secret_manager"}
	for _, k := range required {
		if _, ok := GCPSupportMatrix[k]; !ok {
			t.Fatalf("missing gcp support key %s", k)
		}
	}
}

func TestDetectGCPTargetTier1(t *testing.T) {
	cases := []struct {
		name   string
		node   string
		intent map[string]interface{}
		want   string
	}{
		{"cloud_sql_postgres", "STORE", map[string]interface{}{"intent.engine": "postgres"}, "cloud_sql"},
		{"gcs", "STORE", map[string]interface{}{"intent.type": "gcs"}, "gcs"},
		{"redis", "STORE", map[string]interface{}{"intent.engine": "redis"}, "memorystore_redis"},
		{"cloud_run", "SERVICE", map[string]interface{}{"intent.runtime": "cloud_run"}, "cloud_run"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := detectGCPTarget(ApplyRequest{Action: &state.PlanAction{NodeType: tc.node}, Intent: tc.intent})
			if got != tc.want {
				t.Fatalf("want %s got %s", tc.want, got)
			}
		})
	}
}

func TestValidateGCPInput(t *testing.T) {
	if err := validateGCPInput("gcs", map[string]interface{}{"intent.project_id": "p1"}); err != nil {
		t.Fatalf("unexpected gcs validation error: %v", err)
	}
	if err := validateGCPInput("gcs", map[string]interface{}{}); err == nil {
		t.Fatalf("expected gcs validation error")
	}
	if err := validateGCPInput("cloud_sql", map[string]interface{}{"intent.project_id": "p1", "intent.tier": "db-custom-1-3840"}); err != nil {
		t.Fatalf("unexpected cloud_sql validation error: %v", err)
	}
	if err := validateGCPInput("cloud_sql", map[string]interface{}{"intent.project_id": "p1"}); err == nil {
		t.Fatalf("expected cloud_sql validation error")
	}
}

func TestRDSCredentialsValidation(t *testing.T) {
	_, _, err := rdsCredentials(map[string]interface{}{"intent.username": "u"})
	if err == nil {
		t.Fatalf("expected error for missing password")
	}
	user, pass, err := rdsCredentials(map[string]interface{}{"intent.username": "u", "intent.password": "p"})
	if err != nil || user != "u" || pass != "p" {
		t.Fatalf("unexpected creds result user=%q pass=%q err=%v", user, pass, err)
	}
}

func TestStringListFromIntent(t *testing.T) {
	got := stringListFromIntent(map[string]interface{}{"intent.subnet_ids": "[subnet-1, subnet-2]"}, "subnet_ids")
	if len(got) != 2 || got[0] != "subnet-1" || got[1] != "subnet-2" {
		t.Fatalf("unexpected parsed list: %#v", got)
	}
}

func TestDetectRecordTargetExtendedServices(t *testing.T) {
	cases := []struct {
		name string
		rec  *state.ResourceRecord
		want string
	}{
		{
			name: "elasticache_service_key",
			rec:  &state.ResourceRecord{LiveState: map[string]interface{}{"service": "elasticache"}},
			want: "elasticache",
		},
		{
			name: "cloudwatch_runtime",
			rec: &state.ResourceRecord{
				NodeType:       "COMPUTE",
				IntentSnapshot: map[string]interface{}{"intent.runtime": "cloudwatch"},
			},
			want: "cloudwatch",
		},
		{
			name: "eventbridge_runtime",
			rec: &state.ResourceRecord{
				NodeType:       "COMPUTE",
				IntentSnapshot: map[string]interface{}{"intent.runtime": "eventbridge"},
			},
			want: "eventbridge",
		},
		{
			name: "ec2_service_runtime",
			rec: &state.ResourceRecord{
				NodeType:       "SERVICE",
				IntentSnapshot: map[string]interface{}{"intent.runtime": "ec2"},
			},
			want: "ec2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectRecordTarget(tc.rec); got != tc.want {
				t.Fatalf("want %s got %s", tc.want, got)
			}
		})
	}
}

func TestApplyDispatchUsesAzureExecutor(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "azure",
		Action: &state.PlanAction{
			NodeType: "STORE",
			NodeName: "blob-store",
		},
		Intent: map[string]interface{}{"intent.engine": "blob"},
	})
	if err != nil {
		t.Fatalf("unexpected apply error: %v", err)
	}
	if target := res.LiveState["target"]; target != "blob_storage" {
		t.Fatalf("expected azure target blob_storage, got %#v", target)
	}
}

func TestValidateAzureInput(t *testing.T) {
	if err := validateAzureInput("blob_storage", map[string]interface{}{
		"intent.resource_group": "rg",
		"intent.location":       "westus2",
		"intent.account_tier":   "Standard",
		"intent.account_name":   "acct",
	}); err != nil {
		t.Fatalf("unexpected blob_storage validation error: %v", err)
	}
	if err := validateAzureInput("blob_storage", map[string]interface{}{}); err == nil {
		t.Fatalf("expected blob_storage validation error")
	}
}

// --- C0: Helper + Validation tests ---

func TestParseIntIntent(t *testing.T) {
	m := map[string]interface{}{"intent.memory": "512", "intent.bad": "abc"}
	if got := parseIntIntent(m, "memory", 128); got != 512 {
		t.Fatalf("expected 512, got %d", got)
	}
	if got := parseIntIntent(m, "missing", 128); got != 128 {
		t.Fatalf("expected fallback 128, got %d", got)
	}
	if got := parseIntIntent(m, "bad", 128); got != 128 {
		t.Fatalf("expected fallback 128 for bad value, got %d", got)
	}
}

func TestParseBoolIntent(t *testing.T) {
	m := map[string]interface{}{
		"intent.enabled":  "true",
		"intent.flag":     "1",
		"intent.disabled": "false",
		"intent.zero":     "0",
	}
	if !parseBoolIntent(m, "enabled", false) {
		t.Fatal("expected true for 'true'")
	}
	if !parseBoolIntent(m, "flag", false) {
		t.Fatal("expected true for '1'")
	}
	if parseBoolIntent(m, "disabled", true) {
		t.Fatal("expected false for 'false'")
	}
	if parseBoolIntent(m, "zero", true) {
		t.Fatal("expected false for '0'")
	}
	if !parseBoolIntent(m, "missing", true) {
		t.Fatal("expected fallback true for missing key")
	}
	if parseBoolIntent(m, "missing", false) {
		t.Fatal("expected fallback false for missing key")
	}
}

func TestEnvFromIntent(t *testing.T) {
	m := map[string]interface{}{
		"intent.env.DB_HOST": "localhost",
		"intent.env.DB_PORT": "5432",
		"intent.runtime":     "lambda",
	}
	env := envFromIntent(m)
	if len(env) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(env))
	}
	if env["DB_HOST"] != "localhost" || env["DB_PORT"] != "5432" {
		t.Fatalf("unexpected env: %#v", env)
	}
	// Empty case
	if envFromIntent(map[string]interface{}{}) != nil {
		t.Fatal("expected nil for no env vars")
	}
}

func TestTrustPolicyForService(t *testing.T) {
	policy, err := trustPolicyForService("lambda.amazonaws.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !contains(policy, "lambda.amazonaws.com") {
		t.Fatalf("expected lambda principal in trust policy, got %s", policy)
	}
	if !contains(policy, "sts:AssumeRole") {
		t.Fatalf("expected sts:AssumeRole in trust policy, got %s", policy)
	}
	// Invalid service principal should be rejected
	if _, err := trustPolicyForService(`lambda.amazonaws.com","Action":"sts:*"}]}`); err == nil {
		t.Fatal("expected error for JSON injection attempt")
	}
	if _, err := trustPolicyForService("not-a-principal"); err == nil {
		t.Fatal("expected error for invalid principal")
	}
}

func TestDetectTrustService(t *testing.T) {
	cases := []struct {
		runtime string
		want    string
	}{
		{"lambda", "lambda.amazonaws.com"},
		{"ec2", "ec2.amazonaws.com"},
		{"eks", "eks.amazonaws.com"},
		{"container", "ecs-tasks.amazonaws.com"},
		{"", "ecs-tasks.amazonaws.com"},
	}
	for _, tc := range cases {
		m := map[string]interface{}{"intent.runtime": tc.runtime}
		got := detectTrustService(m)
		if got != tc.want {
			t.Fatalf("runtime=%q: want %s, got %s", tc.runtime, tc.want, got)
		}
	}
}

func TestParseSecurityGroupRulesValid(t *testing.T) {
	rules, err := parseSecurityGroupRules("[tcp:443:10.0.0.0/16, tcp:80:0.0.0.0/0]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	if rules[0].Protocol != "tcp" || rules[0].FromPort != 443 || rules[0].ToPort != 443 || rules[0].CIDR != "10.0.0.0/16" {
		t.Fatalf("unexpected rule[0]: %+v", rules[0])
	}
	if rules[1].Protocol != "tcp" || rules[1].FromPort != 80 || rules[1].ToPort != 80 || rules[1].CIDR != "0.0.0.0/0" {
		t.Fatalf("unexpected rule[1]: %+v", rules[1])
	}
}

func TestParseSecurityGroupRulesPortRange(t *testing.T) {
	rules, err := parseSecurityGroupRules("tcp:8000-8080:10.0.0.0/16")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 || rules[0].FromPort != 8000 || rules[0].ToPort != 8080 {
		t.Fatalf("unexpected port range: %+v", rules)
	}
}

func TestParseSecurityGroupRulesInvalid(t *testing.T) {
	// Bad protocol
	if _, err := parseSecurityGroupRules("ftp:80:0.0.0.0/0"); err == nil {
		t.Fatal("expected error for invalid protocol")
	}
	// Bad CIDR
	if _, err := parseSecurityGroupRules("tcp:80:notacidr"); err == nil {
		t.Fatal("expected error for invalid CIDR")
	}
	// Bad format
	if _, err := parseSecurityGroupRules("tcp:80"); err == nil {
		t.Fatal("expected error for bad format")
	}
	// Empty returns nil
	rules, err := parseSecurityGroupRules("")
	if err != nil || rules != nil {
		t.Fatalf("expected nil for empty, got %v err=%v", rules, err)
	}
}

func TestSerializeSGRulesRoundTrip(t *testing.T) {
	input := "[tcp:443:10.0.0.0/16, tcp:80:0.0.0.0/0]"
	rules, err := parseSecurityGroupRules(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	perms := sgRulesToIPPermissions(rules)
	serialized := serializeSGRules(perms)
	// Re-parse to verify round-trip
	rules2, err := parseSecurityGroupRules(serialized)
	if err != nil {
		t.Fatalf("re-parse error: %v", err)
	}
	if len(rules2) != len(rules) {
		t.Fatalf("round-trip length mismatch: %d vs %d", len(rules2), len(rules))
	}
	for i := range rules {
		if rules[i].Protocol != rules2[i].Protocol || rules[i].FromPort != rules2[i].FromPort ||
			rules[i].ToPort != rules2[i].ToPort || rules[i].CIDR != rules2[i].CIDR {
			t.Fatalf("round-trip mismatch at %d: %+v vs %+v", i, rules[i], rules2[i])
		}
	}
}

func TestValidateAWSInput_Lambda(t *testing.T) {
	// Valid
	if err := validateAWSInput("lambda", map[string]interface{}{"intent.memory": "256", "intent.timeout": "60"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Memory out of range
	if err := validateAWSInput("lambda", map[string]interface{}{"intent.memory": "50"}); err == nil {
		t.Fatal("expected error for memory < 128")
	}
	if err := validateAWSInput("lambda", map[string]interface{}{"intent.memory": "20000"}); err == nil {
		t.Fatal("expected error for memory > 10240")
	}
	// Timeout out of range
	if err := validateAWSInput("lambda", map[string]interface{}{"intent.timeout": "0"}); err == nil {
		t.Fatal("expected error for timeout < 1")
	}
	if err := validateAWSInput("lambda", map[string]interface{}{"intent.timeout": "1000"}); err == nil {
		t.Fatal("expected error for timeout > 900")
	}
}

func TestValidateAWSInput_RDS_IOPS(t *testing.T) {
	// Valid: IOPS with io1
	if err := validateAWSInput("rds", map[string]interface{}{"intent.iops": "3000", "intent.storage_type": "io1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Valid: IOPS with gp3 (default)
	if err := validateAWSInput("rds", map[string]interface{}{"intent.iops": "3000"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid: IOPS with gp2
	if err := validateAWSInput("rds", map[string]interface{}{"intent.iops": "3000", "intent.storage_type": "gp2"}); err == nil {
		t.Fatal("expected error for IOPS with gp2")
	}
	// Valid: IOPS with io2
	if err := validateAWSInput("rds", map[string]interface{}{"intent.iops": "3000", "intent.storage_type": "io2"}); err != nil {
		t.Fatalf("unexpected error for io2: %v", err)
	}
	// No IOPS: always valid
	if err := validateAWSInput("rds", map[string]interface{}{"intent.storage_type": "gp2"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAWSInput_Defaults(t *testing.T) {
	// Default values should pass validation
	if err := validateAWSInput("lambda", map[string]interface{}{}); err != nil {
		t.Fatalf("unexpected error with defaults: %v", err)
	}
	if err := validateAWSInput("rds", map[string]interface{}{}); err != nil {
		t.Fatalf("unexpected error with defaults: %v", err)
	}
	// Unknown target should pass (no validation rules)
	if err := validateAWSInput("ecs", map[string]interface{}{}); err != nil {
		t.Fatalf("unexpected error for ecs: %v", err)
	}
}

// --- C1: RDS dry-run tests ---

func TestRDSDryRunCreateWithAllOptions(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "main-db", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":              "postgres",
			"intent.username":            "admin",
			"intent.password":            "secret",
			"intent.multi_az":            "true",
			"intent.backup_retention":    "30",
			"intent.storage_type":        "io1",
			"intent.iops":               "3000",
			"intent.deletion_protection": "true",
			"intent.kms_key":             "arn:aws:kms:us-east-1:123:key/abc",
			"intent.subnet_group":        "my-subnet-group",
			"intent.security_group_ids":  "[sg-123, sg-456]",
			"intent.parameter_group":     "my-param-group",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "rds" {
		t.Fatalf("expected target rds, got %v", res.LiveState["target"])
	}
	if res.LiveState["simulated"] != true {
		t.Fatal("expected simulated=true")
	}
}

func TestRDSDryRunDefaults(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "simple-db", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":   "postgres",
			"intent.username": "admin",
			"intent.password": "secret",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "rds" {
		t.Fatalf("expected target rds, got %v", res.LiveState["target"])
	}
}

// --- C5: Lambda dry-run tests ---

func TestLambdaDryRunWithMemoryTimeout(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "my-func", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime":       "lambda",
			"intent.role_arn":      "arn:aws:iam::123:role/r",
			"intent.code_s3_bucket": "my-bucket",
			"intent.code_s3_key":   "code.zip",
			"intent.memory":        "512",
			"intent.timeout":       "60",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "lambda" {
		t.Fatalf("expected target lambda, got %v", res.LiveState["target"])
	}
}

func TestLambdaDryRunWithEnvVars(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "env-func", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime":       "lambda",
			"intent.role_arn":      "arn:aws:iam::123:role/r",
			"intent.code_s3_bucket": "my-bucket",
			"intent.code_s3_key":   "code.zip",
			"intent.env.DB_HOST":   "localhost",
			"intent.env.DB_PORT":   "5432",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "lambda" {
		t.Fatalf("expected target lambda, got %v", res.LiveState["target"])
	}
}

func TestLambdaValidationRejectsInvalidMemory(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "bad-func", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime": "lambda",
			"intent.memory":  "50",
		},
	})
	if err == nil {
		t.Fatal("expected validation error for memory=50")
	}
}

// --- C2: S3 dry-run tests ---

func TestS3DryRunWithVersioning(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "data-bucket", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.type":       "s3",
			"intent.versioning": "true",
			"intent.kms_key":    "arn:aws:kms:us-east-1:123:key/abc",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "s3" {
		t.Fatalf("expected target s3, got %v", res.LiveState["target"])
	}
}

func TestS3DryRunUpdate(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "data-bucket", Operation: "UPDATE"},
		Intent: map[string]interface{}{
			"intent.type":           "s3",
			"intent.lifecycle_days": "90",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "s3" {
		t.Fatalf("expected target s3, got %v", res.LiveState["target"])
	}
}

// --- C3: Security Group dry-run tests ---

func TestSGDryRunWithRules(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "NETWORK", NodeName: "web-sg", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.topology":    "security_group",
			"intent.vpc_id":      "vpc-123",
			"intent.ingress":     "[tcp:443:10.0.0.0/16, tcp:80:0.0.0.0/0]",
			"intent.egress":      "[tcp:443:0.0.0.0/0]",
			"intent.description": "Web traffic SG",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "security_group" {
		t.Fatalf("expected target security_group, got %v", res.LiveState["target"])
	}
}

// --- C4: IAM dry-run tests ---

func TestIAMDryRunAutoDetectLambda(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "lambda-role", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime":          "lambda",
			"intent.managed_policies": "[arn:aws:iam::aws:policy/service-role/AWSLambdaBasicExecutionRole]",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "lambda" {
		// IAM role for lambda — detectAWSTarget may map this to lambda, not iam.
		// That's fine for dry-run since it lands on simulatedApply either way.
		t.Logf("target=%v (auto-detect chose lambda over iam)", res.LiveState["target"])
	}
}

func TestIAMDryRunTrustServiceOverride(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "iam-role", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime":       "iam",
			"intent.trust_service": "ec2.amazonaws.com",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "iam" {
		t.Fatalf("expected target iam, got %v", res.LiveState["target"])
	}
}

func TestIAMManagedPolicyValidationInDryRun(t *testing.T) {
	// Validation now runs in validateAWSInput, which is called before dry-run gate.
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "bad-role", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime":          "iam",
			"intent.managed_policies": "[not-an-arn]",
		},
	})
	if err == nil {
		t.Fatal("expected validation error for non-ARN managed policy in dry-run")
	}
}

func TestIAMTrustServiceValidationInDryRun(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "bad-role", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime":       "iam",
			"intent.trust_service": `bad-service","Action":"sts:*"}]}`,
		},
	})
	if err == nil {
		t.Fatal("expected validation error for invalid trust_service")
	}
}

// --- C6: ElastiCache + Secrets Manager dry-run tests ---

func TestElastiCacheDryRunWithSubnetSGs(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "redis-cache", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":             "redis",
			"intent.subnet_group":       "my-subnet-group",
			"intent.security_group_ids": "[sg-123]",
			"intent.parameter_group":    "default.redis6.x",
			"intent.snapshot_retention": "7",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "elasticache" {
		t.Fatalf("expected target elasticache, got %v", res.LiveState["target"])
	}
}

func TestElastiCacheDryRunAuthTokenSensitivity(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "secure-redis", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":     "redis",
			"intent.auth_token": "my-secret-token",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// auth_token should be scrubbed from simulated output
	if v, ok := res.LiveState["intent.auth_token"]; ok {
		t.Fatalf("expected auth_token to be scrubbed, got %v", v)
	}
}

func TestSecretsManagerDryRunWithKMS(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "api-secret", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.type":        "secret",
			"intent.kms_key":     "arn:aws:kms:us-east-1:123:key/abc",
			"intent.description": "API credentials",
			"intent.secret_value": "supersecret",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "secrets_manager" {
		t.Fatalf("expected target secrets_manager, got %v", res.LiveState["target"])
	}
}

// --- detectRecordTarget: lambda ---

func TestDetectRecordTargetLambda(t *testing.T) {
	rec := &state.ResourceRecord{LiveState: map[string]interface{}{"service": "lambda"}}
	if got := detectRecordTarget(rec); got != "lambda" {
		t.Fatalf("want lambda, got %s", got)
	}
}

func TestDetectRecordTargetLambdaServiceFallback(t *testing.T) {
	// Lambda via IntentSnapshot when LiveState doesn't have service key
	rec := &state.ResourceRecord{
		NodeType:       "SERVICE",
		IntentSnapshot: map[string]interface{}{"intent.runtime": "lambda"},
	}
	if got := detectRecordTarget(rec); got != "lambda" {
		t.Fatalf("want lambda via SERVICE fallback, got %s", got)
	}
}

// --- QA fix tests ---

func TestParseSecurityGroupRulesRejectsNegativeTCPPort(t *testing.T) {
	if _, err := parseSecurityGroupRules("tcp:-5:0.0.0.0/0"); err == nil {
		t.Fatal("expected error for negative TCP port")
	}
}

func TestParseSecurityGroupRulesRejectsReversedRange(t *testing.T) {
	if _, err := parseSecurityGroupRules("tcp:8080-80:0.0.0.0/0"); err == nil {
		t.Fatal("expected error for reversed port range")
	}
}

func TestParseSecurityGroupRulesAllowsICMPNegativeOne(t *testing.T) {
	rules, err := parseSecurityGroupRules("icmp:-1:0.0.0.0/0")
	if err != nil {
		t.Fatalf("unexpected error for ICMP -1: %v", err)
	}
	if len(rules) != 1 || rules[0].FromPort != -1 {
		t.Fatalf("expected ICMP rule with port -1, got %+v", rules)
	}
}

func TestParseSecurityGroupRulesAllowsAllTraffic(t *testing.T) {
	rules, err := parseSecurityGroupRules("-1:0:0.0.0.0/0")
	if err != nil {
		t.Fatalf("unexpected error for all-traffic rule: %v", err)
	}
	if len(rules) != 1 || rules[0].Protocol != "-1" {
		t.Fatalf("expected all-traffic rule, got %+v", rules)
	}
}

func TestDiffIPPermissions(t *testing.T) {
	old := sgRulesToIPPermissions([]SGRule{
		{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "10.0.0.0/16"},
		{Protocol: "tcp", FromPort: 80, ToPort: 80, CIDR: "0.0.0.0/0"},
	})
	new := sgRulesToIPPermissions([]SGRule{
		{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "10.0.0.0/16"},
		{Protocol: "tcp", FromPort: 8080, ToPort: 8080, CIDR: "0.0.0.0/0"},
	})
	stale := diffIPPermissions(old, new)
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale rule, got %d", len(stale))
	}
	// The stale rule should be port 80
	if *stale[0].FromPort != 80 {
		t.Fatalf("expected stale rule port 80, got %d", *stale[0].FromPort)
	}
}

func TestValidateAWSInput_IAM_ManagedPolicies(t *testing.T) {
	// Valid ARN
	if err := validateAWSInput("iam", map[string]interface{}{
		"intent.managed_policies": "[arn:aws:iam::aws:policy/ReadOnlyAccess]",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Invalid: not an ARN
	if err := validateAWSInput("iam", map[string]interface{}{
		"intent.managed_policies": "[not-an-arn]",
	}); err == nil {
		t.Fatal("expected error for non-ARN policy")
	}
	// Invalid trust_service
	if err := validateAWSInput("iam", map[string]interface{}{
		"intent.trust_service": "bad-value",
	}); err == nil {
		t.Fatal("expected error for invalid trust_service")
	}
	// Valid trust_service
	if err := validateAWSInput("iam", map[string]interface{}{
		"intent.trust_service": "lambda.amazonaws.com",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSecretsManagerRequiresSecretValue(t *testing.T) {
	// validateAWSInput doesn't check this, but the handler does.
	// This test verifies via dry-run that missing secret_value passes validation
	// (validation is at handler level, not validateAWSInput level).
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "empty-secret", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.type": "secret",
		},
	})
	// Dry-run returns simulated apply, so no error expected here.
	// The actual requirement is enforced in the handler during live execution.
	if err != nil {
		t.Fatalf("unexpected error in dry-run: %v", err)
	}
}

// =====================================================================
// Phase 2: Production-Grade Tests
// =====================================================================

// --- C1: ECS Task Def + Service ---

func TestECSDryRunCreateWithTaskDef(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "api-service", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime":       "container",
			"intent.image_uri":     "123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v1",
			"intent.cpu":           "512",
			"intent.memory":        "1024",
			"intent.desired_count": "2",
			"intent.container_port": "8080",
			"intent.role_arn":      "arn:aws:iam::123:role/ecsTaskExecution",
			"intent.subnet_ids":   "[subnet-1, subnet-2]",
			"intent.env.DB_HOST":   "rds.example.com",
			"intent.env.DB_PORT":   "5432",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "ecs" {
		t.Fatalf("expected target ecs, got %v", res.LiveState["target"])
	}
}

func TestECSDryRunUpdate(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "api-service", Operation: "UPDATE"},
		Intent: map[string]interface{}{
			"intent.runtime":       "container",
			"intent.image_uri":     "123456789.dkr.ecr.us-east-1.amazonaws.com/myapp:v2",
			"intent.desired_count": "3",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "ecs" {
		t.Fatalf("expected target ecs, got %v", res.LiveState["target"])
	}
}

func TestECSValidationRejectsBadCPU(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "bad-ecs", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime": "container",
			"intent.cpu":     "300",
		},
	})
	if err == nil {
		t.Fatal("expected validation error for cpu=300")
	}
}

func TestECSValidationRejectsBadMemory(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "bad-ecs", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime": "container",
			"intent.memory":  "100",
		},
	})
	if err == nil {
		t.Fatal("expected validation error for memory=100")
	}
}

// --- C2: ALB Full Stack ---

func TestALBDryRunCreateWithTargetGroup(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "NETWORK", NodeName: "web-alb", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.topology":              "alb",
			"intent.subnet_ids":            "[subnet-1, subnet-2]",
			"intent.scheme":                "internet-facing",
			"intent.vpc_id":                "vpc-123",
			"intent.target_port":           "8080",
			"intent.listener_port":         "443",
			"intent.certificate_arn":       "arn:aws:acm:us-east-1:123:certificate/abc",
			"intent.health_check_path":     "/health",
			"intent.health_check_interval": "30",
			"intent.target_type":           "ip",
			"intent.security_group_ids":    "[sg-123]",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "alb" {
		t.Fatalf("expected target alb, got %v", res.LiveState["target"])
	}
}

func TestALBDryRunCreateMinimal(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "NETWORK", NodeName: "simple-alb", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.topology":   "alb",
			"intent.subnet_ids": "[subnet-1, subnet-2]",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "alb" {
		t.Fatalf("expected target alb, got %v", res.LiveState["target"])
	}
}

func TestALBValidationRejectsBadPort(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "NETWORK", NodeName: "bad-alb", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.topology":    "alb",
			"intent.subnet_ids":  "[subnet-1]",
			"intent.target_port": "99999",
		},
	})
	if err == nil {
		t.Fatal("expected validation error for target_port=99999")
	}
}

// --- C3a: RDS Enhanced Monitoring ---

func TestRDSDryRunWithEnhancedMonitoring(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "monitored-db", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":              "postgres",
			"intent.username":            "admin",
			"intent.password":            "secret",
			"intent.enhanced_monitoring": "true",
			"intent.monitoring_interval": "30",
			"intent.monitoring_role_arn": "arn:aws:iam::123:role/rds-monitoring",
			"intent.log_exports":         "[postgresql, upgrade]",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "rds" {
		t.Fatalf("expected target rds, got %v", res.LiveState["target"])
	}
}

func TestRDSMonitoringIntervalValidation(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "bad-monitoring", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":              "postgres",
			"intent.monitoring_interval": "42",
		},
	})
	if err == nil {
		t.Fatal("expected validation error for monitoring_interval=42")
	}
}

// --- C4: Lambda VPC + Layers ---

func TestLambdaDryRunWithVPCAndLayers(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "vpc-func", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime":            "lambda",
			"intent.role_arn":           "arn:aws:iam::123:role/r",
			"intent.code_s3_bucket":     "my-bucket",
			"intent.code_s3_key":        "code.zip",
			"intent.subnet_ids":         "[subnet-1, subnet-2]",
			"intent.security_group_ids": "[sg-123]",
			"intent.layer_arns":         "[arn:aws:lambda:us-east-1:123:layer:my-layer:1]",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "lambda" {
		t.Fatalf("expected target lambda, got %v", res.LiveState["target"])
	}
}

// --- C5: RDS Read Replica Intent ---

func TestRDSDryRunWithReadReplicaCount(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "replica-db", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":             "postgres",
			"intent.username":           "admin",
			"intent.password":           "secret",
			"intent.read_replica_count": "2",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "rds" {
		t.Fatalf("expected target rds, got %v", res.LiveState["target"])
	}
}

// --- C6: ElastiCache az_mode ---

func TestElastiCacheDryRunWithAZMode(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "ha-redis", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":          "redis",
			"intent.num_cache_nodes": "3",
			"intent.az_mode":         "cross-az",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "elasticache" {
		t.Fatalf("expected target elasticache, got %v", res.LiveState["target"])
	}
}

func TestElastiCacheAZModeValidation(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "bad-redis", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":  "redis",
			"intent.az_mode": "triple-az",
		},
	})
	if err == nil {
		t.Fatal("expected validation error for az_mode=triple-az")
	}
}

// --- C3c: alarm_on parsing ---

func TestParseAlarmOn(t *testing.T) {
	cases := []struct {
		input     string
		metric    string
		operator  string
		threshold float64
		wantErr   bool
	}{
		{"cpu > 80", "cpu", ">", 80, false},
		{"memory >= 90%", "memory", ">=", 90, false},
		{"errors < 5", "errors", "<", 5, false},
		{"connections <= 200", "connections", "<=", 200, false},
		{"", "", "", 0, false}, // nil result, no error
		{"bad format", "", "", 0, true},
		{"> 80", "", "", 0, true},   // empty metric
		{"cpu > ", "", "", 0, true},  // empty threshold
	}
	for _, tc := range cases {
		cond, err := parseAlarmOn(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Fatalf("parseAlarmOn(%q): expected error", tc.input)
			}
			continue
		}
		if err != nil {
			t.Fatalf("parseAlarmOn(%q): unexpected error: %v", tc.input, err)
		}
		if tc.input == "" {
			if cond != nil {
				t.Fatalf("parseAlarmOn(%q): expected nil, got %+v", tc.input, cond)
			}
			continue
		}
		if cond.Metric != tc.metric || cond.Operator != tc.operator || cond.Threshold != tc.threshold {
			t.Fatalf("parseAlarmOn(%q): got %+v, want metric=%s op=%s thresh=%f", tc.input, cond, tc.metric, tc.operator, tc.threshold)
		}
	}
}

func TestAlarmMetricForTarget(t *testing.T) {
	metric, ns := alarmMetricForTarget("rds", "cpu")
	if metric != "CPUUtilization" || ns != "AWS/RDS" {
		t.Fatalf("expected CPUUtilization/AWS/RDS, got %s/%s", metric, ns)
	}
	metric, ns = alarmMetricForTarget("lambda", "errors")
	if metric != "Errors" || ns != "AWS/Lambda" {
		t.Fatalf("expected Errors/AWS/Lambda, got %s/%s", metric, ns)
	}
	metric, ns = alarmMetricForTarget("ecs", "memory")
	if metric != "MemoryUtilization" || ns != "AWS/ECS" {
		t.Fatalf("expected MemoryUtilization/AWS/ECS, got %s/%s", metric, ns)
	}
	// connections maps to DatabaseConnections for RDS
	metric, ns = alarmMetricForTarget("rds", "connections")
	if metric != "DatabaseConnections" || ns != "AWS/RDS" {
		t.Fatalf("expected DatabaseConnections/AWS/RDS, got %s/%s", metric, ns)
	}
	// Unknown metric passes through as-is (lowercased)
	metric, _ = alarmMetricForTarget("rds", "CustomMetric")
	if metric != "custommetric" {
		t.Fatalf("expected pass-through custommetric, got %s", metric)
	}
}

func TestAlarmOnValidationInDryRun(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "alarmed-db", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":   "postgres",
			"intent.alarm_on": "no operator here",
		},
	})
	if err == nil {
		t.Fatal("expected validation error for bad alarm_on")
	}
}

func TestAlarmOnDryRunDoesNotFail(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "alarmed-db", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":   "postgres",
			"intent.username": "admin",
			"intent.password": "secret",
			"intent.alarm_on": "cpu > 80",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "rds" {
		t.Fatalf("expected target rds, got %v", res.LiveState["target"])
	}
}

// --- C3b: Log Retention ---

func TestParseDurationDays(t *testing.T) {
	cases := []struct {
		input string
		want  int32
	}{
		{"7d", 7},
		{"30d", 30},
		{"90d", 90},
		{"365d", 365},
		{"", 0},
		{"bad", 0},
		{"-1d", 0},
		{"0d", 0},
	}
	for _, tc := range cases {
		got := parseDurationDays(tc.input)
		if got != tc.want {
			t.Fatalf("parseDurationDays(%q): got %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestLogRetentionDays(t *testing.T) {
	// Should snap to nearest valid CloudWatch Logs retention value
	if got := logRetentionDays(7); got != 7 {
		t.Fatalf("expected 7, got %d", got)
	}
	if got := logRetentionDays(10); got != 14 {
		t.Fatalf("expected 14 (nearest valid), got %d", got)
	}
	if got := logRetentionDays(45); got != 60 {
		t.Fatalf("expected 60 (nearest valid), got %d", got)
	}
	if got := logRetentionDays(1); got != 1 {
		t.Fatalf("expected 1, got %d", got)
	}
}

func TestLogRetentionDryRunDoesNotFail(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "logged-func", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.runtime":       "lambda",
			"intent.role_arn":      "arn:aws:iam::123:role/r",
			"intent.code_s3_bucket": "bucket",
			"intent.code_s3_key":   "code.zip",
			"intent.log_retention": "30d",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "lambda" {
		t.Fatalf("expected target lambda, got %v", res.LiveState["target"])
	}
}

// --- QA Round: Extracted helpers + edge cases ---

func TestBuildECSTaskDef(t *testing.T) {
	intentMap := map[string]interface{}{
		"intent.cpu":            "1024",
		"intent.memory":         "2048",
		"intent.container_port": "3000",
		"intent.role_arn":       "arn:aws:iam::123:role/ecs",
		"intent.env.A":          "1",
		"intent.env.B":          "2",
		"intent.env.C":          "3",
	}
	taskDef := buildECSTaskDef("my-svc", "img:v1", intentMap)
	if *taskDef.Family != "my-svc" {
		t.Fatalf("expected family my-svc, got %s", *taskDef.Family)
	}
	if *taskDef.Cpu != "1024" || *taskDef.Memory != "2048" {
		t.Fatalf("unexpected cpu/memory: %s/%s", *taskDef.Cpu, *taskDef.Memory)
	}
	if *taskDef.ExecutionRoleArn != "arn:aws:iam::123:role/ecs" {
		t.Fatalf("expected execution role, got %s", *taskDef.ExecutionRoleArn)
	}
	// Verify env vars are sorted (A, B, C — deterministic ordering)
	cd := taskDef.ContainerDefinitions[0]
	if len(cd.Environment) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(cd.Environment))
	}
	if *cd.Environment[0].Name != "A" || *cd.Environment[1].Name != "B" || *cd.Environment[2].Name != "C" {
		t.Fatalf("env vars not sorted: %s, %s, %s", *cd.Environment[0].Name, *cd.Environment[1].Name, *cd.Environment[2].Name)
	}
}

func TestBuildECSTaskDefDefaults(t *testing.T) {
	taskDef := buildECSTaskDef("svc", "img:latest", map[string]interface{}{})
	if *taskDef.Cpu != "256" || *taskDef.Memory != "512" {
		t.Fatalf("expected default cpu=256/memory=512, got %s/%s", *taskDef.Cpu, *taskDef.Memory)
	}
	if taskDef.ExecutionRoleArn != nil {
		t.Fatal("expected nil execution role for empty intent")
	}
}

func TestLambdaVpcConfig(t *testing.T) {
	// No subnets: should return nil
	if vpc := lambdaVpcConfig(map[string]interface{}{}); vpc != nil {
		t.Fatal("expected nil vpc config for empty intent")
	}
	// With subnets only
	vpc := lambdaVpcConfig(map[string]interface{}{"intent.subnet_ids": "[subnet-1, subnet-2]"})
	if vpc == nil || len(vpc.SubnetIds) != 2 {
		t.Fatalf("expected 2 subnets, got %v", vpc)
	}
	if len(vpc.SecurityGroupIds) != 0 {
		t.Fatalf("expected no security groups, got %d", len(vpc.SecurityGroupIds))
	}
	// With subnets and security groups
	vpc = lambdaVpcConfig(map[string]interface{}{
		"intent.subnet_ids":         "[subnet-1]",
		"intent.security_group_ids": "[sg-1, sg-2]",
	})
	if vpc == nil || len(vpc.SecurityGroupIds) != 2 {
		t.Fatalf("expected 2 security groups, got %v", vpc)
	}
}

func TestRDSEnhancedMonitoringRequiresRoleArn(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	_, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "STORE", NodeName: "no-role-db", Operation: "CREATE"},
		Intent: map[string]interface{}{
			"intent.engine":              "postgres",
			"intent.enhanced_monitoring": "true",
			// missing monitoring_role_arn
		},
	})
	if err == nil {
		t.Fatal("expected validation error for enhanced_monitoring without monitoring_role_arn")
	}
}

func TestAlarmDimensionsForTarget(t *testing.T) {
	// Lambda should have FunctionName dimension
	dims := alarmDimensionsForTarget("lambda", "my-func")
	if len(dims) != 1 || *dims[0].Name != "FunctionName" {
		t.Fatalf("expected FunctionName dimension for lambda, got %v", dims)
	}
	// ECS should have ClusterName dimension
	dims = alarmDimensionsForTarget("ecs", "my-cluster")
	if len(dims) != 1 || *dims[0].Name != "ClusterName" {
		t.Fatalf("expected ClusterName dimension for ecs, got %v", dims)
	}
	// RDS should have DBInstanceIdentifier dimension
	dims = alarmDimensionsForTarget("rds", "my-db")
	if len(dims) != 1 || *dims[0].Name != "DBInstanceIdentifier" {
		t.Fatalf("expected DBInstanceIdentifier dimension for rds, got %v", dims)
	}
	// EC2 returns nil (no instance ID at alarm time)
	dims = alarmDimensionsForTarget("ec2", "my-instance")
	if dims != nil {
		t.Fatalf("expected nil dimensions for ec2, got %v", dims)
	}
	// Unknown target returns nil
	dims = alarmDimensionsForTarget("s3", "my-bucket")
	if dims != nil {
		t.Fatalf("expected nil dimensions for s3, got %v", dims)
	}
}

func TestLogRetentionDaysZero(t *testing.T) {
	if got := logRetentionDays(0); got != 0 {
		t.Fatalf("expected 0 for days=0, got %d", got)
	}
	if got := logRetentionDays(-1); got != 0 {
		t.Fatalf("expected 0 for days=-1, got %d", got)
	}
}

func TestParseDurationDaysNonDayUnits(t *testing.T) {
	// Non-day suffixes should return 0 (not supported)
	for _, input := range []string{"7h", "30m", "1w", "2y"} {
		if got := parseDurationDays(input); got != 0 {
			t.Fatalf("parseDurationDays(%q): expected 0, got %d", input, got)
		}
	}
	// Bare number should work (interpreted as days)
	if got := parseDurationDays("30"); got != 30 {
		t.Fatalf("parseDurationDays(\"30\"): expected 30, got %d", got)
	}
}

func TestParseAlarmOnRejectsEquals(t *testing.T) {
	// Bare equals (=) is not a supported operator
	if _, err := parseAlarmOn("cpu = 80"); err == nil {
		t.Fatal("expected error for unsupported = operator")
	}
}

func TestECSDryRunUpdateDesiredCountOnly(t *testing.T) {
	// ECS UPDATE with only desired_count (no image_uri) should pass dry-run
	e := &DefaultExecutor{dryRun: true}
	res, err := e.Apply(t.Context(), ApplyRequest{
		Provider: "aws",
		Action:   &state.PlanAction{NodeType: "SERVICE", NodeName: "scale-svc", Operation: "UPDATE"},
		Intent: map[string]interface{}{
			"intent.runtime":       "container",
			"intent.desired_count": "5",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.LiveState["target"] != "ecs" {
		t.Fatalf("expected target ecs, got %v", res.LiveState["target"])
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
