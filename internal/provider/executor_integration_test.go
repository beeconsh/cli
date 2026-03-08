package provider

import (
	"context"
	"testing"

	"github.com/terracotta-ai/beecon/internal/state"
)

// --- Full Apply dispatch tests (dryRun=true, exercises target detection + validation + simulated result) ---

func TestApplyDispatchAWS_AllTier1Targets(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	cases := []struct {
		name       string
		nodeType   string
		intent     map[string]interface{}
		wantTarget string
	}{
		{
			name:     "rds_create",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.engine":   "postgres",
				"intent.username": "admin",
				"intent.password": "secret",
			},
			wantTarget: "rds",
		},
		{
			name:     "elasticache_create",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.engine":    "redis",
				"intent.node_type": "cache.t3.micro",
			},
			wantTarget: "elasticache",
		},
		{
			name:     "s3_create",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.type": "s3",
			},
			wantTarget: "s3",
		},
		{
			name:     "secrets_manager_create",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.type":         "secret",
				"intent.secret_value": "my-secret",
			},
			wantTarget: "secrets_manager",
		},
		{
			name:     "ecs_create",
			nodeType: "SERVICE",
			intent: map[string]interface{}{
				"intent.runtime":   "container",
				"intent.image_uri": "nginx:latest",
			},
			wantTarget: "ecs",
		},
		{
			name:     "lambda_create",
			nodeType: "SERVICE",
			intent: map[string]interface{}{
				"intent.runtime":       "lambda",
				"intent.code_s3_bucket": "my-bucket",
				"intent.code_s3_key":    "code.zip",
			},
			wantTarget: "lambda",
		},
		{
			name:     "alb_create",
			nodeType: "NETWORK",
			intent: map[string]interface{}{
				"intent.topology":   "alb",
				"intent.vpc_id":     "vpc-123",
				"intent.subnet_ids": "[subnet-1, subnet-2]",
			},
			wantTarget: "alb",
		},
		{
			name:     "vpc_create",
			nodeType: "NETWORK",
			intent: map[string]interface{}{
				"intent.topology": "vpc",
				"intent.cidr":     "10.0.0.0/16",
			},
			wantTarget: "vpc",
		},
		{
			name:     "security_group_create",
			nodeType: "NETWORK",
			intent: map[string]interface{}{
				"intent.topology": "security_group",
				"intent.vpc_id":   "vpc-123",
			},
			wantTarget: "security_group",
		},
		{
			name:     "iam_create",
			nodeType: "COMPUTE",
			intent: map[string]interface{}{
				"intent.type": "iam_role",
			},
			wantTarget: "iam",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.Apply(ctx, ApplyRequest{
				Provider: "aws",
				Region:   "us-east-1",
				Action: &state.PlanAction{
					NodeType:  tc.nodeType,
					NodeName:  "test-" + tc.name,
					Operation: "CREATE",
				},
				Intent: tc.intent,
			})
			if err != nil {
				t.Fatalf("Apply failed: %v", err)
			}
			if res.ProviderID == "" {
				t.Fatal("expected non-empty ProviderID")
			}
			target, ok := res.LiveState["target"].(string)
			if !ok {
				t.Fatal("expected target in LiveState")
			}
			if target != tc.wantTarget {
				t.Fatalf("want target %q, got %q", tc.wantTarget, target)
			}
			sim, _ := res.LiveState["simulated"].(bool)
			if !sim {
				t.Fatal("expected simulated=true in dryRun mode")
			}
		})
	}
}

func TestApplyDispatchAWS_DeleteOperations(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	cases := []struct {
		name     string
		nodeType string
		intent   map[string]interface{}
		record   *state.ResourceRecord
	}{
		{
			name:     "rds_delete",
			nodeType: "STORE",
			intent:   map[string]interface{}{"intent.engine": "postgres"},
			record: &state.ResourceRecord{
				ProviderID: "beecon-postgres",
				LiveState:  map[string]interface{}{"service": "rds"},
			},
		},
		{
			name:     "s3_delete",
			nodeType: "STORE",
			intent:   map[string]interface{}{"intent.type": "s3"},
			record: &state.ResourceRecord{
				ProviderID: "beecon-uploads",
				LiveState:  map[string]interface{}{"service": "s3"},
			},
		},
		{
			name:     "lambda_delete",
			nodeType: "SERVICE",
			intent:   map[string]interface{}{"intent.runtime": "lambda"},
			record: &state.ResourceRecord{
				ProviderID: "beecon-handler",
				LiveState:  map[string]interface{}{"service": "lambda"},
			},
		},
		{
			name:     "ecs_delete",
			nodeType: "SERVICE",
			intent:   map[string]interface{}{"intent.runtime": "container"},
			record: &state.ResourceRecord{
				ProviderID: "beecon-api",
				LiveState:  map[string]interface{}{"service": "ecs"},
			},
		},
		{
			name:     "security_group_delete",
			nodeType: "NETWORK",
			intent:   map[string]interface{}{"intent.topology": "security_group"},
			record: &state.ResourceRecord{
				ProviderID: "sg-123",
				LiveState:  map[string]interface{}{"service": "ec2"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.Apply(ctx, ApplyRequest{
				Provider: "aws",
				Region:   "us-east-1",
				Action: &state.PlanAction{
					NodeType:  tc.nodeType,
					NodeName:  "test-" + tc.name,
					Operation: "DELETE",
				},
				Intent: tc.intent,
				Record: tc.record,
			})
			if err != nil {
				t.Fatalf("Apply DELETE failed: %v", err)
			}
			if res.ProviderID == "" {
				t.Fatal("expected ProviderID on delete result")
			}
			if op, ok := res.LiveState["operation"].(string); !ok || op != "DELETE" {
				t.Fatalf("expected operation=DELETE in LiveState, got %v", res.LiveState["operation"])
			}
		})
	}
}

func TestApplyDispatchAWS_UpdateOperations(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	cases := []struct {
		name     string
		nodeType string
		intent   map[string]interface{}
		record   *state.ResourceRecord
	}{
		{
			name:     "rds_update",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.engine":        "postgres",
				"intent.instance_type": "db.r6g.xlarge",
				"intent.username":      "admin",
				"intent.password":      "secret",
			},
			record: &state.ResourceRecord{
				ProviderID: "beecon-postgres",
				LiveState:  map[string]interface{}{"service": "rds"},
			},
		},
		{
			name:     "ecs_update",
			nodeType: "SERVICE",
			intent: map[string]interface{}{
				"intent.runtime":       "container",
				"intent.image_uri":     "nginx:latest",
				"intent.desired_count": "3",
			},
			record: &state.ResourceRecord{
				ProviderID: "beecon-api",
				LiveState:  map[string]interface{}{"service": "ecs"},
			},
		},
		{
			name:     "lambda_update",
			nodeType: "SERVICE",
			intent: map[string]interface{}{
				"intent.runtime":       "lambda",
				"intent.code_s3_bucket": "my-bucket",
				"intent.code_s3_key":    "code-v2.zip",
			},
			record: &state.ResourceRecord{
				ProviderID: "arn:aws:lambda:us-east-1:123:function:handler",
				LiveState:  map[string]interface{}{"service": "lambda"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.Apply(ctx, ApplyRequest{
				Provider: "aws",
				Region:   "us-east-1",
				Action: &state.PlanAction{
					NodeType:  tc.nodeType,
					NodeName:  "test-" + tc.name,
					Operation: "UPDATE",
				},
				Intent: tc.intent,
				Record: tc.record,
			})
			if err != nil {
				t.Fatalf("Apply UPDATE failed: %v", err)
			}
			if res.ProviderID == "" {
				t.Fatal("expected ProviderID on update result")
			}
		})
	}
}

// --- GCP Full Apply Dispatch ---

func TestApplyDispatchGCP_Tier1Targets(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	cases := []struct {
		name       string
		nodeType   string
		intent     map[string]interface{}
		wantTarget string
	}{
		{
			name:     "cloud_sql",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.engine":     "postgres",
				"intent.project_id": "my-project",
				"intent.tier":       "db-custom-1-3840",
			},
			wantTarget: "cloud_sql",
		},
		{
			name:     "gcs",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.type":       "gcs",
				"intent.project_id": "my-project",
			},
			wantTarget: "gcs",
		},
		{
			name:     "memorystore_redis",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.engine":     "redis",
				"intent.project_id": "my-project",
			},
			wantTarget: "memorystore_redis",
		},
		{
			name:     "cloud_run",
			nodeType: "SERVICE",
			intent: map[string]interface{}{
				"intent.runtime":    "cloud_run",
				"intent.project_id": "my-project",
				"intent.image_uri":  "gcr.io/my-project/app:latest",
				"intent.region":     "us-central1",
			},
			wantTarget: "cloud_run",
		},
		{
			name:     "pubsub",
			nodeType: "COMPUTE",
			intent: map[string]interface{}{
				"intent.runtime":    "pubsub",
				"intent.project_id": "my-project",
			},
			wantTarget: "pubsub",
		},
		{
			name:     "secret_manager",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.type":         "secret",
				"intent.project_id":   "my-project",
				"intent.secret_value": "my-secret",
			},
			wantTarget: "secret_manager",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.Apply(ctx, ApplyRequest{
				Provider: "gcp",
				Region:   "us-central1",
				Action: &state.PlanAction{
					NodeType:  tc.nodeType,
					NodeName:  "test-" + tc.name,
					Operation: "CREATE",
				},
				Intent: tc.intent,
			})
			if err != nil {
				t.Fatalf("GCP Apply failed: %v", err)
			}
			if res.ProviderID == "" {
				t.Fatal("expected non-empty ProviderID")
			}
			target, ok := res.LiveState["target"].(string)
			if !ok {
				t.Fatal("expected target in LiveState")
			}
			if target != tc.wantTarget {
				t.Fatalf("want target %q, got %q", tc.wantTarget, target)
			}
		})
	}
}

// --- Azure Full Apply Dispatch ---

func TestApplyDispatchAzure_Tier1Targets(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	cases := []struct {
		name       string
		nodeType   string
		intent     map[string]interface{}
		wantTarget string
	}{
		{
			name:     "blob_storage",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.engine":         "blob",
				"intent.resource_group":  "rg",
				"intent.location":        "westus2",
				"intent.account_tier":    "Standard",
				"intent.account_name":    "acct",
			},
			wantTarget: "blob_storage",
		},
		{
			name:     "key_vault",
			nodeType: "STORE",
			intent: map[string]interface{}{
				"intent.type":            "secret",
				"intent.resource_group":   "rg",
				"intent.location":         "westus2",
				"intent.vault_url":        "https://myvault.vault.azure.net/",
				"intent.secret_value":     "my-secret",
			},
			wantTarget: "key_vault_secret",
		},
		{
			name:     "vnet",
			nodeType: "NETWORK",
			intent: map[string]interface{}{
				"intent.topology":        "vnet",
				"intent.resource_group":   "rg",
				"intent.location":         "westus2",
				"intent.cidr":             "10.0.0.0/16",
			},
			wantTarget: "vnet",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := e.Apply(ctx, ApplyRequest{
				Provider: "azure",
				Region:   "westus2",
				Action: &state.PlanAction{
					NodeType:  tc.nodeType,
					NodeName:  "test-" + tc.name,
					Operation: "CREATE",
				},
				Intent: tc.intent,
			})
			if err != nil {
				t.Fatalf("Azure Apply failed: %v", err)
			}
			if res.ProviderID == "" {
				t.Fatal("expected non-empty ProviderID")
			}
			target, ok := res.LiveState["target"].(string)
			if !ok {
				t.Fatal("expected target in LiveState")
			}
			if target != tc.wantTarget {
				t.Fatalf("want target %q, got %q", tc.wantTarget, target)
			}
		})
	}
}

// --- Observe dispatch tests ---

func TestObserveAWS_DryRunReturnsCachedState(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	rec := &state.ResourceRecord{
		Managed:    true,
		ProviderID: "beecon-postgres",
		NodeType:   "STORE",
		LiveState: map[string]interface{}{
			"service": "rds",
			"status":  "available",
			"engine":  "postgres",
		},
		IntentSnapshot: map[string]interface{}{"intent.engine": "postgres"},
	}

	result, err := e.Observe(ctx, "aws", "us-east-1", rec)
	if err != nil {
		t.Fatalf("Observe failed: %v", err)
	}
	if !result.Exists {
		t.Fatal("expected Exists=true for managed resource in dryRun")
	}
	if result.ProviderID != "beecon-postgres" {
		t.Fatalf("expected cached ProviderID, got %q", result.ProviderID)
	}
}

func TestObserveGCP_DryRunReturnsCachedState(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	rec := &state.ResourceRecord{
		Managed:    true,
		ProviderID: "projects/my-project/instances/db",
		NodeType:   "STORE",
		LiveState: map[string]interface{}{
			"service": "cloud_sql",
			"status":  "RUNNABLE",
		},
		IntentSnapshot: map[string]interface{}{"intent.engine": "postgres"},
	}

	result, err := e.Observe(ctx, "gcp", "us-central1", rec)
	if err != nil {
		t.Fatalf("Observe failed: %v", err)
	}
	if !result.Exists {
		t.Fatal("expected Exists=true for managed GCP resource in dryRun")
	}
}

func TestObserveAzure_DryRunReturnsCachedState(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	rec := &state.ResourceRecord{
		Managed:    true,
		ProviderID: "/subscriptions/xxx/resourceGroups/rg/providers/Microsoft.Storage/storageAccounts/acct",
		NodeType:   "STORE",
		LiveState: map[string]interface{}{
			"service": "blob_storage",
		},
		IntentSnapshot: map[string]interface{}{"intent.engine": "blob"},
	}

	result, err := e.Observe(ctx, "azure", "westus2", rec)
	if err != nil {
		t.Fatalf("Observe failed: %v", err)
	}
	if !result.Exists {
		t.Fatal("expected Exists=true for managed Azure resource in dryRun")
	}
}

func TestObserveUnknownProvider_ReturnsCachedState(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	rec := &state.ResourceRecord{
		Managed:    true,
		ProviderID: "some-id",
		LiveState:  map[string]interface{}{"key": "value"},
	}

	result, err := e.Observe(ctx, "unknown", "region", rec)
	if err != nil {
		t.Fatalf("Observe unknown provider failed: %v", err)
	}
	if !result.Exists {
		t.Fatal("expected Exists=true")
	}
	if result.LiveState["key"] != "value" {
		t.Fatal("expected cached LiveState preserved")
	}
}

func TestObserveNilRecord_ReturnsFalse(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	result, err := e.Observe(ctx, "unknown", "region", nil)
	if err != nil {
		t.Fatalf("Observe nil rec failed: %v", err)
	}
	if result.Exists {
		t.Fatal("expected Exists=false for nil record")
	}
}

// --- Sensitive key redaction in simulated apply ---

func TestSimulatedApplyRedactsSensitiveKeys(t *testing.T) {
	res := simulatedApply(ApplyRequest{
		Provider: "aws",
		Action: &state.PlanAction{
			NodeName:  "db",
			Operation: "CREATE",
		},
		Intent: map[string]interface{}{
			"intent.engine":   "postgres",
			"intent.password": "super-secret-password",
			"intent.api_key":  "key-123",
			"intent.username": "admin",
		},
	}, "rds")

	// Sensitive keys must not appear in LiveState
	for _, key := range []string{"intent.password", "intent.api_key"} {
		if _, exists := res.LiveState[key]; exists {
			t.Fatalf("sensitive key %q was not redacted from LiveState", key)
		}
	}
	// Non-sensitive keys should be present
	if res.LiveState["intent.username"] != "admin" {
		t.Fatal("expected non-sensitive key intent.username in LiveState")
	}
	if res.LiveState["intent.engine"] != "postgres" {
		t.Fatal("expected non-sensitive key intent.engine in LiveState")
	}
}

// --- Validation edge cases ---

func TestValidateAWSInput_RDS_MissingCredentials(t *testing.T) {
	err := validateAWSInput("rds", map[string]interface{}{
		"intent.engine": "postgres",
		// missing username and password
	})
	if err != nil {
		t.Fatalf("RDS should not require credentials at validation time (they're checked at apply), got: %v", err)
	}
}

func TestValidateAWSInput_ECS_InvalidCPU(t *testing.T) {
	err := validateAWSInput("ecs", map[string]interface{}{
		"intent.runtime": "container",
		"intent.cpu":     "999",
	})
	if err == nil {
		t.Fatal("expected validation error for ECS with invalid CPU value")
	}
}

func TestValidateAWSInput_ECS_ValidMinimal(t *testing.T) {
	err := validateAWSInput("ecs", map[string]interface{}{
		"intent.runtime": "container",
	})
	if err != nil {
		t.Fatalf("unexpected validation error for minimal ECS: %v", err)
	}
}

func TestValidateAWSInput_ALB_InvalidPort(t *testing.T) {
	err := validateAWSInput("alb", map[string]interface{}{
		"intent.topology":    "alb",
		"intent.target_port": "99999",
	})
	if err == nil {
		t.Fatal("expected validation error for ALB with invalid port")
	}
}

func TestValidateAWSInput_Lambda_InvalidMemory(t *testing.T) {
	err := validateAWSInput("lambda", map[string]interface{}{
		"intent.runtime": "lambda",
		"intent.memory":  "50", // below 128 minimum
	})
	if err == nil {
		t.Fatal("expected validation error for Lambda with memory below 128")
	}
}

func TestValidateAWSInput_Lambda_InvalidTimeout(t *testing.T) {
	err := validateAWSInput("lambda", map[string]interface{}{
		"intent.runtime": "lambda",
		"intent.timeout": "1000", // above 900 max
	})
	if err == nil {
		t.Fatal("expected validation error for Lambda with timeout above 900")
	}
}

// --- Cross-provider dispatch ---

func TestApplyDispatch_UnknownProvider_ReturnsSimulated(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	res, err := e.Apply(ctx, ApplyRequest{
		Provider: "digitalocean",
		Action: &state.PlanAction{
			NodeType:  "SERVICE",
			NodeName:  "app",
			Operation: "CREATE",
		},
		Intent: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Apply unknown provider should succeed in dryRun: %v", err)
	}
	if res.LiveState["target"] != "generic" {
		t.Fatalf("expected target=generic for unknown provider, got %v", res.LiveState["target"])
	}
}

func TestApplyDispatch_EmptyProvider_ReturnsSimulated(t *testing.T) {
	e := &DefaultExecutor{dryRun: true}
	ctx := context.Background()

	res, err := e.Apply(ctx, ApplyRequest{
		Provider: "",
		Action: &state.PlanAction{
			NodeType:  "SERVICE",
			NodeName:  "local-app",
			Operation: "CREATE",
		},
		Intent: map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Apply empty provider should succeed: %v", err)
	}
	if res.ProviderID == "" {
		t.Fatal("expected ProviderID for empty provider")
	}
}

// --- Tier classification ---

func TestAWSSupportMatrixTierValues(t *testing.T) {
	tier1 := []string{"rds", "rds_aurora_serverless", "elasticache", "s3", "alb", "vpc", "subnet", "security_group", "iam", "secrets_manager"}
	tier2 := []string{"lambda", "api_gateway", "sqs", "sns", "cloudfront", "route53", "cloudwatch"}
	tier3 := []string{"eks", "eventbridge", "cognito", "ec2"}

	for _, k := range tier1 {
		if AWSSupportMatrix[k] != "tier1" {
			t.Errorf("%s should be tier1, got %s", k, AWSSupportMatrix[k])
		}
	}
	for _, k := range tier2 {
		if AWSSupportMatrix[k] != "tier2" {
			t.Errorf("%s should be tier2, got %s", k, AWSSupportMatrix[k])
		}
	}
	for _, k := range tier3 {
		if AWSSupportMatrix[k] != "tier3" {
			t.Errorf("%s should be tier3, got %s", k, AWSSupportMatrix[k])
		}
	}
}

// --- IsDryRun ---

func TestNewExecutor_DefaultDryRun(t *testing.T) {
	// Default executor should be dryRun unless BEECON_EXECUTE=1
	e := &DefaultExecutor{dryRun: true}
	if !e.IsDryRun() {
		t.Fatal("expected IsDryRun=true")
	}

	e2 := &DefaultExecutor{dryRun: false}
	if e2.IsDryRun() {
		t.Fatal("expected IsDryRun=false when dryRun=false")
	}
}

// --- Helper function edge cases ---

func TestIdentifierForSpecialCharacters(t *testing.T) {
	cases := []struct {
		input string
	}{
		{"simple"},
		{"with.dots"},
		{"with_underscores"},
		{"MixedCase.Name_Value"},
		{""},
	}
	for _, tc := range cases {
		id := identifierFor(tc.input)
		if len(id) < 7 || id[:7] != "beecon-" {
			t.Errorf("identifierFor(%q) = %q, want beecon- prefix", tc.input, id)
		}
	}
}

func TestParseStorageGiB_EdgeCases(t *testing.T) {
	cases := []struct {
		input string
		want  int32
	}{
		{"20gb", 20},
		{"100GB", 100},
		{"50Gb", 50},
		{"1000gb", 1000},
		{"0gb", 0},
		{"", 0},
		{"notanumber", 0},
		{"50", 50}, // numeric without unit is accepted
	}
	for _, tc := range cases {
		got := parseStorageGiB(tc.input)
		if got != tc.want {
			t.Errorf("parseStorageGiB(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestDefaultString(t *testing.T) {
	if got := defaultString("value", "fallback"); got != "value" {
		t.Fatalf("expected 'value', got %q", got)
	}
	if got := defaultString("", "fallback"); got != "fallback" {
		t.Fatalf("expected 'fallback', got %q", got)
	}
}

func TestTrimResourceName(t *testing.T) {
	short := "abc"
	if got := trimResourceName(short, 10); got != "abc" {
		t.Fatalf("expected %q, got %q", short, got)
	}
	long := "abcdefghijklmnop"
	if got := trimResourceName(long, 10); len(got) != 10 {
		t.Fatalf("expected length 10, got %d (%q)", len(got), got)
	}
}

func TestIntentHelper(t *testing.T) {
	m := map[string]interface{}{
		"intent.engine":   "postgres",
		"intent.runtime":  "container",
	}
	if got := intent(m, "engine"); got != "postgres" {
		t.Fatalf("expected 'postgres', got %q", got)
	}
	if got := intent(m, "missing"); got != "" {
		t.Fatalf("expected empty for missing key, got %q", got)
	}
	// Multi-key fallback
	if got := intent(m, "nonexistent", "engine"); got != "postgres" {
		t.Fatalf("expected fallback to 'engine', got %q", got)
	}
}
