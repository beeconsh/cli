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
