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
