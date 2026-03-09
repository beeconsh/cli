package classify

import "testing"

// --- AWS Classification Tests ---

func TestClassifyNode(t *testing.T) {
	cases := []struct {
		name     string
		nodeType string
		intent   map[string]string
		want     string
	}{
		{"STORE postgres → rds", "STORE", map[string]string{"engine": "postgres"}, "rds"},
		{"STORE mysql → rds", "STORE", map[string]string{"engine": "mysql"}, "rds"},
		{"STORE aurora → rds_aurora_serverless", "STORE", map[string]string{"engine": "aurora"}, "rds_aurora_serverless"},
		{"STORE redis → elasticache", "STORE", map[string]string{"engine": "redis"}, "elasticache"},
		{"STORE sqs → sqs", "STORE", map[string]string{"engine": "sqs"}, "sqs"},
		{"STORE sns → sns", "STORE", map[string]string{"engine": "sns"}, "sns"},
		{"STORE s3 → s3", "STORE", map[string]string{"engine": "s3"}, "s3"},
		{"STORE bucket → s3", "STORE", map[string]string{"engine": "bucket"}, "s3"},
		{"STORE secret → secrets_manager", "STORE", map[string]string{"engine": "secret"}, "secrets_manager"},
		{"NETWORK vpc → vpc", "NETWORK", map[string]string{"engine": "vpc"}, "vpc"},
		{"NETWORK subnet → subnet", "NETWORK", map[string]string{"engine": "subnet"}, "subnet"},
		{"NETWORK security_group → security_group", "NETWORK", map[string]string{"engine": "security_group"}, "security_group"},
		{"NETWORK sg → security_group", "NETWORK", map[string]string{"engine": "sg"}, "security_group"},
		{"NETWORK alb → alb", "NETWORK", map[string]string{"engine": "alb"}, "alb"},
		{"NETWORK api_gateway → api_gateway", "NETWORK", map[string]string{"engine": "api_gateway"}, "api_gateway"},
		{"NETWORK apigateway → api_gateway", "NETWORK", map[string]string{"engine": "apigateway"}, "api_gateway"},
		{"NETWORK cloudfront → cloudfront", "NETWORK", map[string]string{"engine": "cloudfront"}, "cloudfront"},
		{"NETWORK route53 → route53", "NETWORK", map[string]string{"engine": "route53"}, "route53"},
		{"NETWORK dns → route53", "NETWORK", map[string]string{"engine": "dns"}, "route53"},
		{"NETWORK cloudwatch → cloudwatch", "NETWORK", map[string]string{"engine": "cloudwatch"}, "cloudwatch"},
		{"SERVICE lambda → lambda", "SERVICE", map[string]string{"engine": "lambda"}, "lambda"},
		{"SERVICE container → ecs", "SERVICE", map[string]string{"engine": "container"}, "ecs"},
		{"SERVICE container+api → api_gateway", "SERVICE", map[string]string{"engine": "container", "expose": "api"}, "api_gateway"},
		{"SERVICE eks → eks", "SERVICE", map[string]string{"engine": "eks"}, "eks"},
		{"SERVICE cognito → cognito", "SERVICE", map[string]string{"engine": "cognito"}, "cognito"},
		{"SERVICE ec2 → ec2", "SERVICE", map[string]string{"engine": "ec2"}, "ec2"},
		{"COMPUTE lambda → lambda", "COMPUTE", map[string]string{"engine": "lambda"}, "lambda"},
		{"COMPUTE eventbridge → eventbridge", "COMPUTE", map[string]string{"engine": "eventbridge"}, "eventbridge"},
		{"COMPUTE cloudwatch → cloudwatch", "COMPUTE", map[string]string{"engine": "cloudwatch"}, "cloudwatch"},
		{"COMPUTE cognito → cognito", "COMPUTE", map[string]string{"engine": "cognito"}, "cognito"},
		{"COMPUTE ec2 → ec2", "COMPUTE", map[string]string{"engine": "ec2"}, "ec2"},
		{"unknown → empty", "STORE", map[string]string{"engine": "unknown"}, ""},
		{"lowercase nodeType", "store", map[string]string{"engine": "postgres"}, "rds"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyNode(tc.nodeType, tc.intent)
			if got != tc.want {
				t.Errorf("ClassifyNode(%q, %v) = %q, want %q", tc.nodeType, tc.intent, got, tc.want)
			}
		})
	}
}

func TestIsVPCResident(t *testing.T) {
	residents := []string{"rds", "rds_aurora_serverless", "elasticache", "ecs", "eks", "ec2"}
	nonResidents := []string{"s3", "sqs", "sns", "lambda", "secrets_manager", "cloudfront", "route53"}
	for _, r := range residents {
		if !IsVPCResident(r) {
			t.Errorf("expected %q to be VPC-resident", r)
		}
	}
	for _, r := range nonResidents {
		if IsVPCResident(r) {
			t.Errorf("expected %q to NOT be VPC-resident", r)
		}
	}
}

func TestDefaultPort(t *testing.T) {
	if DefaultPort("rds") != 5432 {
		t.Error("expected rds default port 5432")
	}
	if DefaultPort("rds_aurora_serverless") != 5432 {
		t.Error("expected rds_aurora_serverless default port 5432")
	}
	if DefaultPort("elasticache") != 6379 {
		t.Error("expected elasticache default port 6379")
	}
	if DefaultPort("s3") != 0 {
		t.Error("expected s3 default port 0")
	}
}

func TestDefaultPortForEngine(t *testing.T) {
	if DefaultPortForEngine("mysql") != 3306 {
		t.Error("expected mysql port 3306")
	}
	if DefaultPortForEngine("postgres") != 5432 {
		t.Error("expected postgres port 5432")
	}
	if DefaultPortForEngine("redis") != 6379 {
		t.Error("expected redis port 6379")
	}
	if DefaultPortForEngine("unknown") != 0 {
		t.Error("expected unknown port 0")
	}
}

// --- GCP Classification Tests ---

func TestClassifyGCPNode(t *testing.T) {
	cases := []struct {
		name     string
		nodeType string
		intent   map[string]string
		want     string
	}{
		{"STORE postgres → cloud_sql", "STORE", map[string]string{"engine": "postgres"}, "cloud_sql"},
		{"STORE mysql → cloud_sql", "STORE", map[string]string{"engine": "mysql"}, "cloud_sql"},
		{"STORE cloudsql → cloud_sql", "STORE", map[string]string{"engine": "cloudsql"}, "cloud_sql"},
		{"STORE redis → memorystore_redis", "STORE", map[string]string{"engine": "redis"}, "memorystore_redis"},
		{"STORE gcs → gcs", "STORE", map[string]string{"engine": "gcs"}, "gcs"},
		{"STORE s3 → gcs", "STORE", map[string]string{"engine": "s3"}, "gcs"},
		{"STORE bucket → gcs", "STORE", map[string]string{"engine": "bucket"}, "gcs"},
		{"STORE pubsub → pubsub", "STORE", map[string]string{"engine": "pubsub"}, "pubsub"},
		{"STORE sqs → pubsub", "STORE", map[string]string{"engine": "sqs"}, "pubsub"},
		{"STORE secret → secret_manager", "STORE", map[string]string{"engine": "secret"}, "secret_manager"},
		{"NETWORK vpc → vpc", "NETWORK", map[string]string{"engine": "vpc"}, "vpc"},
		{"NETWORK subnet → subnet", "NETWORK", map[string]string{"engine": "subnet"}, "subnet"},
		{"NETWORK firewall → firewall", "NETWORK", map[string]string{"engine": "firewall"}, "firewall"},
		{"NETWORK security_group → firewall", "NETWORK", map[string]string{"engine": "security_group"}, "firewall"},
		{"NETWORK dns → cloud_dns", "NETWORK", map[string]string{"engine": "dns"}, "cloud_dns"},
		{"NETWORK cdn → cloud_cdn", "NETWORK", map[string]string{"engine": "cdn"}, "cloud_cdn"},
		{"SERVICE container → cloud_run", "SERVICE", map[string]string{"engine": "container"}, "cloud_run"},
		{"SERVICE lambda → cloud_functions", "SERVICE", map[string]string{"engine": "lambda"}, "cloud_functions"},
		{"SERVICE gke → gke", "SERVICE", map[string]string{"engine": "gke"}, "gke"},
		{"SERVICE compute → compute_engine", "SERVICE", map[string]string{"engine": "compute"}, "compute_engine"},
		{"SERVICE identity → identity_platform", "SERVICE", map[string]string{"engine": "identity"}, "identity_platform"},
		{"COMPUTE function → cloud_functions", "COMPUTE", map[string]string{"engine": "function"}, "cloud_functions"},
		{"COMPUTE eventarc → eventarc", "COMPUTE", map[string]string{"engine": "eventarc"}, "eventarc"},
		{"COMPUTE monitoring → cloud_monitoring", "COMPUTE", map[string]string{"engine": "monitoring"}, "cloud_monitoring"},
		{"unknown → empty", "STORE", map[string]string{"engine": "unknown"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyGCPNode(tc.nodeType, tc.intent)
			if got != tc.want {
				t.Errorf("ClassifyGCPNode(%q, %v) = %q, want %q", tc.nodeType, tc.intent, got, tc.want)
			}
		})
	}
}

func TestIsGCPVPCResident(t *testing.T) {
	residents := []string{"cloud_sql", "memorystore_redis", "gke", "compute_engine"}
	nonResidents := []string{"gcs", "pubsub", "secret_manager", "cloud_dns", "cloud_cdn", "cloud_run"}
	for _, r := range residents {
		if !IsGCPVPCResident(r) {
			t.Errorf("expected %q to be VPC-resident", r)
		}
	}
	for _, r := range nonResidents {
		if IsGCPVPCResident(r) {
			t.Errorf("expected %q to NOT be VPC-resident", r)
		}
	}
}

func TestGCPDefaultPort(t *testing.T) {
	if GCPDefaultPort("cloud_sql") != 5432 {
		t.Error("expected cloud_sql default port 5432")
	}
	if GCPDefaultPort("memorystore_redis") != 6379 {
		t.Error("expected memorystore_redis default port 6379")
	}
	if GCPDefaultPort("gcs") != 0 {
		t.Error("expected gcs default port 0")
	}
}

func TestGCPDefaultPortForEngine(t *testing.T) {
	if GCPDefaultPortForEngine("mysql") != 3306 {
		t.Error("expected mysql port 3306")
	}
	if GCPDefaultPortForEngine("postgres") != 5432 {
		t.Error("expected postgres port 5432")
	}
	if GCPDefaultPortForEngine("redis") != 6379 {
		t.Error("expected redis port 6379")
	}
	if GCPDefaultPortForEngine("unknown") != 0 {
		t.Error("expected unknown port 0")
	}
}

// --- Azure Classification Tests ---

func TestClassifyAzureNode(t *testing.T) {
	cases := []struct {
		name     string
		nodeType string
		intent   map[string]string
		want     string
	}{
		{"STORE postgres → postgres_flexible", "STORE", map[string]string{"engine": "postgres"}, "postgres_flexible"},
		{"STORE mysql → mysql_flexible", "STORE", map[string]string{"engine": "mysql"}, "mysql_flexible"},
		{"STORE postgres_flexible → postgres_flexible", "STORE", map[string]string{"engine": "postgres_flexible"}, "postgres_flexible"},
		{"STORE redis → azure_cache_redis", "STORE", map[string]string{"engine": "redis"}, "azure_cache_redis"},
		{"STORE blob → blob_storage", "STORE", map[string]string{"engine": "blob"}, "blob_storage"},
		{"STORE s3 → blob_storage", "STORE", map[string]string{"engine": "s3"}, "blob_storage"},
		{"STORE bucket → blob_storage", "STORE", map[string]string{"engine": "bucket"}, "blob_storage"},
		{"STORE secret → key_vault_secret", "STORE", map[string]string{"engine": "secret"}, "key_vault_secret"},
		{"STORE keyvault → key_vault_secret", "STORE", map[string]string{"engine": "keyvault"}, "key_vault_secret"},
		{"STORE key_vault → key_vault_secret", "STORE", map[string]string{"engine": "key_vault"}, "key_vault_secret"},
		{"STORE servicebus → service_bus", "STORE", map[string]string{"engine": "servicebus"}, "service_bus"},
		{"STORE service_bus → service_bus", "STORE", map[string]string{"engine": "service_bus"}, "service_bus"},
		{"STORE sqs → service_bus", "STORE", map[string]string{"engine": "sqs"}, "service_bus"},
		{"STORE sns → service_bus", "STORE", map[string]string{"engine": "sns"}, "service_bus"},
		{"NETWORK vpc → vnet", "NETWORK", map[string]string{"engine": "vpc"}, "vnet"},
		{"NETWORK vnet → vnet", "NETWORK", map[string]string{"engine": "vnet"}, "vnet"},
		{"NETWORK subnet → subnet", "NETWORK", map[string]string{"engine": "subnet"}, "subnet"},
		{"NETWORK nsg → nsg", "NETWORK", map[string]string{"engine": "nsg"}, "nsg"},
		{"NETWORK security_group → nsg", "NETWORK", map[string]string{"engine": "security_group"}, "nsg"},
		{"NETWORK sg → nsg", "NETWORK", map[string]string{"engine": "sg"}, "nsg"},
		{"NETWORK front_door → front_door", "NETWORK", map[string]string{"engine": "front_door"}, "front_door"},
		{"NETWORK frontdoor → front_door", "NETWORK", map[string]string{"engine": "frontdoor"}, "front_door"},
		{"NETWORK cdn → cdn", "NETWORK", map[string]string{"engine": "cdn"}, "cdn"},
		{"NETWORK dns → dns", "NETWORK", map[string]string{"engine": "dns"}, "dns"},
		{"SERVICE container → container_apps", "SERVICE", map[string]string{"engine": "container"}, "container_apps"},
		{"SERVICE lambda → functions", "SERVICE", map[string]string{"engine": "lambda"}, "functions"},
		{"SERVICE function → functions", "SERVICE", map[string]string{"engine": "function"}, "functions"},
		{"SERVICE aks → aks", "SERVICE", map[string]string{"engine": "aks"}, "aks"},
		{"SERVICE eks → aks", "SERVICE", map[string]string{"engine": "eks"}, "aks"},
		{"SERVICE kubernetes → aks", "SERVICE", map[string]string{"engine": "kubernetes"}, "aks"},
		{"SERVICE identity → entra_id", "SERVICE", map[string]string{"engine": "identity"}, "entra_id"},
		{"SERVICE entra → entra_id", "SERVICE", map[string]string{"engine": "entra"}, "entra_id"},
		{"SERVICE apim → api_management", "SERVICE", map[string]string{"engine": "apim"}, "api_management"},
		{"COMPUTE event_grid → event_grid", "COMPUTE", map[string]string{"engine": "event_grid"}, "event_grid"},
		{"COMPUTE eventgrid → event_grid", "COMPUTE", map[string]string{"engine": "eventgrid"}, "event_grid"},
		{"COMPUTE vm → vm", "COMPUTE", map[string]string{"engine": "vm"}, "vm"},
		{"COMPUTE compute → vm", "COMPUTE", map[string]string{"engine": "compute"}, "vm"},
		{"COMPUTE function → functions", "COMPUTE", map[string]string{"engine": "function"}, "functions"},
		{"COMPUTE lambda → functions", "COMPUTE", map[string]string{"engine": "lambda"}, "functions"},
		{"COMPUTE monitor → monitor", "COMPUTE", map[string]string{"engine": "monitor"}, "monitor"},
		{"COMPUTE monitoring → monitor", "COMPUTE", map[string]string{"engine": "monitoring"}, "monitor"},
		{"unknown → empty", "STORE", map[string]string{"engine": "unknown"}, ""},
		{"lowercase nodeType", "store", map[string]string{"engine": "postgres"}, "postgres_flexible"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyAzureNode(tc.nodeType, tc.intent)
			if got != tc.want {
				t.Errorf("ClassifyAzureNode(%q, %v) = %q, want %q", tc.nodeType, tc.intent, got, tc.want)
			}
		})
	}
}

func TestIsAzureVPCResident(t *testing.T) {
	residents := []string{"container_apps", "postgres_flexible", "mysql_flexible", "azure_cache_redis", "aks", "vm"}
	nonResidents := []string{"blob_storage", "key_vault_secret", "service_bus", "functions", "dns", "cdn", "front_door"}
	for _, r := range residents {
		if !IsAzureVPCResident(r) {
			t.Errorf("expected %q to be VNet-resident", r)
		}
	}
	for _, r := range nonResidents {
		if IsAzureVPCResident(r) {
			t.Errorf("expected %q to NOT be VNet-resident", r)
		}
	}
}

func TestAzureDefaultPort(t *testing.T) {
	if AzureDefaultPort("postgres_flexible") != 5432 {
		t.Error("expected postgres_flexible default port 5432")
	}
	if AzureDefaultPort("mysql_flexible") != 3306 {
		t.Error("expected mysql_flexible default port 3306")
	}
	if AzureDefaultPort("azure_cache_redis") != 6380 {
		t.Error("expected azure_cache_redis default port 6380")
	}
	if AzureDefaultPort("blob_storage") != 0 {
		t.Error("expected blob_storage default port 0")
	}
}

func TestAzureDefaultPortForEngine(t *testing.T) {
	if AzureDefaultPortForEngine("mysql") != 3306 {
		t.Error("expected mysql port 3306")
	}
	if AzureDefaultPortForEngine("postgres") != 5432 {
		t.Error("expected postgres port 5432")
	}
	if AzureDefaultPortForEngine("redis") != 6380 {
		t.Error("expected redis port 6380")
	}
	if AzureDefaultPortForEngine("unknown") != 0 {
		t.Error("expected unknown port 0")
	}
}
