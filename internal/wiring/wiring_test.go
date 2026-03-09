package wiring

import (
	"strings"
	"testing"

	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/parser"
)

func buildGraph(t *testing.T, src string) *ir.Graph {
	t.Helper()
	f, err := parser.Parse(strings.NewReader(src))
	if err != nil {
		t.Fatal(err)
	}
	g, err := ir.Build(f, "test")
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestWireGraphInfersEnvVars(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    postgres = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	// Check inferred env vars on the service node
	var apiNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}
	if apiNode.Env["DATABASE_URL"] == "" {
		t.Error("expected DATABASE_URL env var to be inferred")
	}
	if apiNode.Env["HOST"] == "" {
		t.Error("expected HOST env var to be inferred")
	}

	// Check inferred env vars in result
	if len(result.InferredEnvVars["service.api"]) == 0 {
		t.Error("expected inferred env vars in result")
	}
}

func TestWireGraphInfersIAMPolicy(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store mydb {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    mydb = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	// Check inline policy was set
	var apiNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}
	if apiNode.Intent["inline_policy"] == "" {
		t.Error("expected inline_policy to be set")
	}
	if !strings.Contains(apiNode.Intent["inline_policy"], "rds-data") {
		t.Error("expected inline_policy to contain rds-data actions")
	}

	if len(result.InferredPolicies) == 0 {
		t.Error("expected inferred policies in result")
	}
}

func TestWireGraphSGRules(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store db {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    db = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	if len(result.InferredSGRules) == 0 {
		t.Error("expected SG rules for VPC-resident resources")
	}
	for _, rule := range result.InferredSGRules {
		if rule.Port != 5432 {
			t.Errorf("expected port 5432, got %d", rule.Port)
		}
	}
}

func TestWireGraphExplicitEnvWins(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    postgres = read_write
  }
  env {
    DATABASE_URL = my-custom-url
  }
}
`)
	_, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	var apiNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}
	if apiNode.Env["DATABASE_URL"] != "my-custom-url" {
		t.Errorf("explicit env should win, got %q", apiNode.Env["DATABASE_URL"])
	}
}

func TestWireGraphInvalidMode(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store db {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    db = publish
  }
}
`)
	_, err := WireGraph(g)
	if err == nil {
		t.Fatal("expected error for invalid mode 'publish' on rds target")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWireGraphS3Dependency(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store uploads {
  engine = s3
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    uploads = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	var apiNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}

	// Should infer S3 env vars (prefixed with UPLOADS_ since dep name != target type)
	if apiNode.Env["UPLOADS_BUCKET_NAME"] == "" {
		t.Error("expected UPLOADS_BUCKET_NAME env var")
	}

	// Inline policy should contain s3 actions
	if !strings.Contains(apiNode.Intent["inline_policy"], "s3:GetObject") {
		t.Error("expected s3:GetObject in inline policy")
	}

	// No SG rules for S3 (not VPC-resident)
	if len(result.InferredSGRules) != 0 {
		t.Error("expected no SG rules for S3")
	}
}

func TestNormalizeModeVariants(t *testing.T) {
	tests := []struct {
		input    string
		expected Mode
	}{
		{"read", ModeRead},
		{"read_only", ModeRead},
		{"ro", ModeRead},
		{"write", ModeWrite},
		{"read_write", ModeReadWrite},
		{"rw", ModeReadWrite},
		{"invoke", ModeInvoke},
		{"publish", ModePublish},
		{"pub", ModePublish},
		{"subscribe", ModeSubscribe},
		{"sub", ModeSubscribe},
		{"admin", ModeAdmin},
	}
	for _, tt := range tests {
		m, err := NormalizeMode(tt.input)
		if err != nil {
			t.Errorf("NormalizeMode(%q) error: %v", tt.input, err)
			continue
		}
		if m != tt.expected {
			t.Errorf("NormalizeMode(%q) = %q, want %q", tt.input, m, tt.expected)
		}
	}
}

func TestNormalizeModeInvalid(t *testing.T) {
	_, err := NormalizeMode("destroy")
	if err == nil {
		t.Error("expected error for unknown mode")
	}
}

func TestIsValidModeAllowsUnknownTargets(t *testing.T) {
	// Unknown targets are allowed to support needs declarations on targets
	// not yet in the ValidModes registry (e.g., alb, vpc, eks).
	if !IsValidMode("unknown_type", ModeRead) {
		t.Error("expected unknown targets to be allowed")
	}
	// Empty target (unclassified) should be allowed
	if !IsValidMode("", ModeRead) {
		t.Error("expected empty target to be allowed")
	}
	// Known target with invalid mode should still be denied
	if IsValidMode("rds", ModePublish) {
		t.Error("expected invalid mode on known target to be denied")
	}
}

func TestWireGraphDanglingDependencyError(t *testing.T) {
	// Manually create a graph with a dangling dependency (target doesn't exist as a node)
	g := &ir.Graph{
		Nodes: []ir.IntentNode{
			{
				ID:   "service.api",
				Name: "api",
				Type: ir.NodeService,
				Intent: map[string]string{"runtime": "container(from: ./Dockerfile)"},
				Env:  map[string]string{},
				Needs: []ir.Dependency{{Target: "nonexistent", Mode: "read"}},
			},
		},
	}
	_, err := WireGraph(g)
	if err == nil {
		t.Fatal("expected error for dangling dependency reference")
	}
	if !strings.Contains(err.Error(), "no such node exists") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWireGraphAdminModeWarning(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store mydb {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    mydb = admin
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}
	foundAdmin := false
	foundResource := false
	for _, w := range result.Warnings {
		if strings.Contains(w, "admin mode") {
			foundAdmin = true
		}
		if strings.Contains(w, "Resource \"*\"") {
			foundResource = true
		}
	}
	if !foundAdmin {
		t.Error("expected admin mode warning")
	}
	if !foundResource {
		t.Error("expected Resource \"*\" warning")
	}
}

func TestWireGraphInjectsSGRulesIntoIntent(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
network sg {
  topology = security_group
  vpc_id = vpc-123
}
store db {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    db = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	// SG rules should be inferred
	if len(result.InferredSGRules) == 0 {
		t.Fatal("expected inferred SG rules")
	}

	// The security_group NETWORK node should have ingress populated
	var sgNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "sg" {
			sgNode = &g.Nodes[i]
			break
		}
	}
	if sgNode == nil {
		t.Fatal("sg node not found")
	}
	if sgNode.Intent["ingress"] == "" {
		t.Fatal("expected ingress to be injected into security_group node")
	}
	if !strings.Contains(sgNode.Intent["ingress"], "5432") {
		t.Fatalf("expected port 5432 in ingress, got %q", sgNode.Intent["ingress"])
	}
}

func TestWireGraphSGRulesPreservesExplicit(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
network sg {
  topology = security_group
  vpc_id = vpc-123
  ingress = tcp:443:10.0.0.0/16
}
store db {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    db = read_write
  }
}
`)
	_, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	var sgNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "sg" {
			sgNode = &g.Nodes[i]
			break
		}
	}
	if sgNode == nil {
		t.Fatal("sg node not found")
	}

	// Existing explicit rule should be preserved
	if !strings.Contains(sgNode.Intent["ingress"], "tcp:443:10.0.0.0/16") {
		t.Fatal("expected explicit ingress rule to be preserved")
	}
	// Inferred rule should be appended
	if !strings.Contains(sgNode.Intent["ingress"], "5432") {
		t.Fatal("expected inferred port 5432 to be appended")
	}
}

func TestWireGraphSGRulesNoSGNode(t *testing.T) {
	// When there's no security_group node, inferred rules should still be in result
	// but no intent injection should occur
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store db {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    db = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	// Rules should still be inferred
	if len(result.InferredSGRules) == 0 {
		t.Fatal("expected inferred SG rules even without SG node")
	}

	// No node should have ingress set (no SG node to inject into)
	for i := range g.Nodes {
		if g.Nodes[i].Intent["ingress"] != "" {
			t.Fatalf("unexpected ingress on node %s without SG node in graph", g.Nodes[i].Name)
		}
	}
}

func TestWireGraphSGRulesScopedByEdge(t *testing.T) {
	// Manually construct a graph with two SG nodes and edges connecting
	// only one SG to the api service. SG rules should be scoped to sg1 only.
	g := &ir.Graph{
		Domain: &ir.DomainNode{Name: "acme", Cloud: "aws(region: us-east-1)", Owner: "team(platform)"},
		Nodes: []ir.IntentNode{
			{
				ID:     "network.sg1",
				Name:   "sg1",
				Type:   ir.NodeNetwork,
				Intent: map[string]string{"topology": "security_group", "vpc_id": "vpc-111"},
				Env:    map[string]string{},
			},
			{
				ID:     "network.sg2",
				Name:   "sg2",
				Type:   ir.NodeNetwork,
				Intent: map[string]string{"topology": "security_group", "vpc_id": "vpc-222"},
				Env:    map[string]string{},
			},
			{
				ID:     "store.db",
				Name:   "db",
				Type:   ir.NodeStore,
				Intent: map[string]string{"engine": "postgres"},
				Env:    map[string]string{},
				Needs:  []ir.Dependency{},
			},
			{
				ID:   "service.api",
				Name: "api",
				Type: ir.NodeService,
				Intent: map[string]string{"runtime": "container(from: ./Dockerfile)"},
				Env:    map[string]string{},
				Needs:  []ir.Dependency{{Target: "db", Mode: "read_write"}},
			},
		},
		Edges: []ir.Edge{
			// api depends on sg1 (edge from sg1 to api)
			{From: "network.sg1", To: "service.api"},
			// api depends on db
			{From: "store.db", To: "service.api"},
		},
		Profiles: map[string]ir.Profile{},
	}

	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	if len(result.InferredSGRules) == 0 {
		t.Fatal("expected inferred SG rules")
	}

	// sg1 is connected to api via edge, so rules should be injected into sg1
	var sg1, sg2 *ir.IntentNode
	for i := range g.Nodes {
		switch g.Nodes[i].Name {
		case "sg1":
			sg1 = &g.Nodes[i]
		case "sg2":
			sg2 = &g.Nodes[i]
		}
	}
	if sg1 == nil || sg2 == nil {
		t.Fatal("missing sg nodes")
	}
	if sg1.Intent["ingress"] == "" {
		t.Error("expected ingress on sg1 (connected via edge to api)")
	}
	if sg2.Intent["ingress"] != "" {
		t.Errorf("expected no ingress on sg2 (not connected), got %q", sg2.Intent["ingress"])
	}
}

func TestWireGraphNoDependencies(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
service api {
  runtime = container(from: ./Dockerfile)
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}
	if len(result.InferredEnvVars) != 0 {
		t.Error("expected no inferred env vars for node without dependencies")
	}
	if len(result.InferredPolicies) != 0 {
		t.Error("expected no inferred policies")
	}
}

// --- GCP Wiring Tests ---

func TestWireGraphGCPInfersEnvVars(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    postgres = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	var apiNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}
	// GCP should infer DATABASE_URL, DB_HOST, DB_PORT, INSTANCE_CONNECTION_NAME
	if apiNode.Env["DATABASE_URL"] == "" {
		t.Error("expected DATABASE_URL env var for GCP cloud_sql dependency")
	}
	if apiNode.Env["INSTANCE_CONNECTION_NAME"] == "" {
		t.Error("expected INSTANCE_CONNECTION_NAME env var for GCP cloud_sql dependency")
	}
	if len(result.InferredEnvVars["service.api"]) == 0 {
		t.Error("expected inferred env vars in result")
	}
}

func TestWireGraphGCPInfersIAMRoles(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store mydb {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    mydb = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	var apiNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}
	// GCP should set iam_roles (not inline_policy)
	if apiNode.Intent["iam_roles"] == "" {
		t.Error("expected iam_roles to be set for GCP")
	}
	if apiNode.Intent["inline_policy"] != "" {
		t.Error("GCP should NOT set inline_policy (that's AWS)")
	}
	if !strings.Contains(apiNode.Intent["iam_roles"], "roles/cloudsql.client") {
		t.Errorf("expected roles/cloudsql.client in iam_roles, got %q", apiNode.Intent["iam_roles"])
	}
	if len(result.InferredPolicies) == 0 {
		t.Error("expected inferred policies in result")
	}
}

func TestWireGraphGCPFirewallRules(t *testing.T) {
	// Use compute_engine (VPC-resident) → cloud_sql (VPC-resident).
	// Cloud Run is NOT VPC-resident (uses VPC connectors), so it shouldn't
	// generate firewall rules.
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store db {
  engine = postgres
}
service api {
  engine = compute
  needs {
    db = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}
	if len(result.InferredSGRules) == 0 {
		t.Error("expected firewall rules for GCP VPC-resident resources")
	}
	for _, rule := range result.InferredSGRules {
		if rule.Port != 5432 {
			t.Errorf("expected port 5432, got %d", rule.Port)
		}
	}
}

func TestWireGraphGCPCloudRunNoFirewall(t *testing.T) {
	// Cloud Run uses VPC connectors, not traditional VPC firewall rules.
	// Verify that cloud_run → cloud_sql does NOT generate firewall rules.
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store db {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    db = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}
	if len(result.InferredSGRules) != 0 {
		t.Errorf("expected no firewall rules for cloud_run (uses VPC connectors), got %d", len(result.InferredSGRules))
	}
}

func TestWireGraphGCPExplicitEnvWins(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    postgres = read_write
  }
  env {
    DATABASE_URL = my-custom-url
  }
}
`)
	_, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	var apiNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}
	if apiNode.Env["DATABASE_URL"] != "my-custom-url" {
		t.Errorf("explicit env should win on GCP, got %q", apiNode.Env["DATABASE_URL"])
	}
}

func TestWireGraphGCPGCSDependency(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store uploads {
  engine = gcs
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    uploads = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	var apiNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}
	if apiNode.Env["UPLOADS_BUCKET_NAME"] == "" {
		t.Error("expected UPLOADS_BUCKET_NAME env var for GCS dependency")
	}
	if !strings.Contains(apiNode.Intent["iam_roles"], "roles/storage.objectUser") {
		t.Errorf("expected roles/storage.objectUser in iam_roles, got %q", apiNode.Intent["iam_roles"])
	}
	// No firewall rules for GCS (not VPC-resident)
	if len(result.InferredSGRules) != 0 {
		t.Error("expected no firewall rules for GCS")
	}
}

func TestWireGraphGCPInvalidMode(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store db {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    db = publish
  }
}
`)
	_, err := WireGraph(g)
	if err == nil {
		t.Fatal("expected error for invalid mode 'publish' on cloud_sql target")
	}
	if !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestWireGraphGCPSecretManagerDependency(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store credentials {
  engine = secret
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    credentials = read
  }
}
`)
	_, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	var apiNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "api" {
			apiNode = &g.Nodes[i]
			break
		}
	}
	if apiNode == nil {
		t.Fatal("api node not found")
	}
	if apiNode.Env["CREDENTIALS_SECRET_NAME"] == "" {
		t.Error("expected CREDENTIALS_SECRET_NAME env var for secret_manager dependency")
	}
	if !strings.Contains(apiNode.Intent["iam_roles"], "roles/secretmanager.secretAccessor") {
		t.Errorf("expected roles/secretmanager.secretAccessor in iam_roles, got %q", apiNode.Intent["iam_roles"])
	}
}

func TestWireGraphGCPPubSubDependency(t *testing.T) {
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store events {
  engine = pubsub
}
service worker {
  runtime = container(from: ./Dockerfile)
  needs {
    events = subscribe
  }
}
`)
	_, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}

	var workerNode *ir.IntentNode
	for i := range g.Nodes {
		if g.Nodes[i].Name == "worker" {
			workerNode = &g.Nodes[i]
			break
		}
	}
	if workerNode == nil {
		t.Fatal("worker node not found")
	}
	if workerNode.Env["EVENTS_TOPIC_NAME"] == "" {
		t.Error("expected EVENTS_TOPIC_NAME env var for pubsub dependency")
	}
	if !strings.Contains(workerNode.Intent["iam_roles"], "roles/pubsub.subscriber") {
		t.Errorf("expected roles/pubsub.subscriber in iam_roles, got %q", workerNode.Intent["iam_roles"])
	}
}

func TestIsValidGCPMode(t *testing.T) {
	// Known target with valid mode
	if !IsValidGCPMode("cloud_sql", ModeRead) {
		t.Error("expected cloud_sql:read to be valid")
	}
	// Known target with invalid mode
	if IsValidGCPMode("cloud_sql", ModePublish) {
		t.Error("expected cloud_sql:publish to be invalid")
	}
	// Unknown target should be allowed
	if !IsValidGCPMode("unknown_target", ModeRead) {
		t.Error("expected unknown targets to be allowed")
	}
	// Empty target should be allowed
	if !IsValidGCPMode("", ModeRead) {
		t.Error("expected empty target to be allowed")
	}
}

func TestGCPIAMRolesFor(t *testing.T) {
	cases := []struct {
		target string
		mode   Mode
		want   string // substring of first role
	}{
		{"cloud_sql", ModeRead, "roles/cloudsql.viewer"},
		{"cloud_sql", ModeReadWrite, "roles/cloudsql.client"},
		{"gcs", ModeRead, "roles/storage.objectViewer"},
		{"gcs", ModeWrite, "roles/storage.objectCreator"},
		{"pubsub", ModePublish, "roles/pubsub.publisher"},
		{"pubsub", ModeSubscribe, "roles/pubsub.subscriber"},
		{"secret_manager", ModeRead, "roles/secretmanager.secretAccessor"},
		{"cloud_run", ModeInvoke, "roles/run.invoker"},
	}
	for _, tc := range cases {
		t.Run(tc.target+":"+string(tc.mode), func(t *testing.T) {
			roles, err := GCPIAMRolesFor(tc.target, tc.mode)
			if err != nil {
				t.Fatalf("GCPIAMRolesFor(%q, %q) error: %v", tc.target, tc.mode, err)
			}
			found := false
			for _, r := range roles {
				if r == tc.want {
					found = true
				}
			}
			if !found {
				t.Errorf("GCPIAMRolesFor(%q, %q) = %v, expected to contain %q", tc.target, tc.mode, roles, tc.want)
			}
		})
	}
}

func TestGCPIAMRolesForUnknown(t *testing.T) {
	_, err := GCPIAMRolesFor("unknown_target", ModeRead)
	if err == nil {
		t.Error("expected error for unknown target")
	}
}

func TestDetectCloudProvider(t *testing.T) {
	cases := []struct {
		cloud string
		want  string
	}{
		{"aws(region: us-east-1)", "aws"},
		{"gcp(project: my-project, region: us-central1)", "gcp"},
		{"azure(subscription: sub-123)", "azure"},
		{"", "aws"},
	}
	for _, tc := range cases {
		g := &ir.Graph{Domain: &ir.DomainNode{Cloud: tc.cloud}}
		got := detectCloudProvider(g)
		if got != tc.want {
			t.Errorf("detectCloudProvider(cloud=%q) = %q, want %q", tc.cloud, got, tc.want)
		}
	}
	// Nil domain should default to aws
	g := &ir.Graph{}
	if got := detectCloudProvider(g); got != "aws" {
		t.Errorf("detectCloudProvider(nil domain) = %q, want aws", got)
	}
}

func TestWireGraphGCPContainerEngine(t *testing.T) {
	// Verify that "container" engine on GCP classifies as cloud_run and
	// infers Cloud Run env vars + IAM roles correctly.
	g := buildGraph(t, `domain acme {
  cloud = gcp(project: my-project, region: us-central1)
  owner = team(platform)
}
store db {
  engine = postgres
}
service api {
  runtime = container(from: ./Dockerfile)
  needs {
    db = read_write
  }
}
`)
	result, err := WireGraph(g)
	if err != nil {
		t.Fatalf("WireGraph failed: %v", err)
	}
	// Should infer cloud_sql env vars (with DB_ prefix since dep name is "db")
	for _, node := range g.Nodes {
		if node.Name == "api" {
			if _, ok := node.Env["DB_DATABASE_URL"]; !ok {
				t.Errorf("expected DB_DATABASE_URL env var for container → cloud_sql dependency, got: %v", node.Env)
			}
			if _, ok := node.Env["DB_INSTANCE_CONNECTION_NAME"]; !ok {
				t.Errorf("expected DB_INSTANCE_CONNECTION_NAME env var for container → cloud_sql dependency, got: %v", node.Env)
			}
		}
	}
	// Should infer IAM roles
	if len(result.InferredPolicies) == 0 {
		t.Error("expected GCP IAM role binding for cloud_run → cloud_sql")
	}
}

func TestParsePortBoundsCheck(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"80", 80},
		{"443", 443},
		{"65535", 65535},
		{"65536", 0},   // exceeds max port
		{"99999", 0},   // exceeds max port
		{"0", 0},       // zero is technically valid but not useful
		{"abc", 0},     // non-numeric
		{"", 0},        // empty
		{"8080", 8080},
	}
	for _, tc := range cases {
		got := parsePort(tc.input)
		if got != tc.want {
			t.Errorf("parsePort(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}
