package provider

import (
	"encoding/json"
	"strings"
	"testing"

	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	lambdatypes "github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/terracotta-ai/beecon/internal/state"
)

// ============================================================
// Security Group Rule Parsing
// ============================================================

func TestParseSecurityGroupRules_ValidRules(t *testing.T) {
	rules, err := parseSecurityGroupRules("tcp:443:10.0.0.0/16,udp:53:0.0.0.0/0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	// First rule: tcp:443
	if rules[0].Protocol != "tcp" {
		t.Errorf("rule[0].Protocol = %q, want tcp", rules[0].Protocol)
	}
	if rules[0].FromPort != 443 || rules[0].ToPort != 443 {
		t.Errorf("rule[0] ports = %d-%d, want 443-443", rules[0].FromPort, rules[0].ToPort)
	}
	if rules[0].CIDR != "10.0.0.0/16" {
		t.Errorf("rule[0].CIDR = %q, want 10.0.0.0/16", rules[0].CIDR)
	}
	// Second rule: udp:53
	if rules[1].Protocol != "udp" {
		t.Errorf("rule[1].Protocol = %q, want udp", rules[1].Protocol)
	}
	if rules[1].FromPort != 53 || rules[1].ToPort != 53 {
		t.Errorf("rule[1] ports = %d-%d, want 53-53", rules[1].FromPort, rules[1].ToPort)
	}
	if rules[1].CIDR != "0.0.0.0/0" {
		t.Errorf("rule[1].CIDR = %q, want 0.0.0.0/0", rules[1].CIDR)
	}
}

func TestParseSecurityGroupRules_EmptyString(t *testing.T) {
	rules, err := parseSecurityGroupRules("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rules != nil {
		t.Fatalf("expected nil rules for empty string, got %v", rules)
	}
}

func TestParseSecurityGroupRules_InvalidFormat(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"missing_cidr", "tcp:443"},
		{"bad_protocol", "http:443:10.0.0.0/16"},
		{"bad_cidr", "tcp:443:not-a-cidr"},
		{"bad_port", "tcp:abc:10.0.0.0/16"},
		{"empty_segments", "::"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseSecurityGroupRules(tc.input)
			if err == nil {
				t.Fatalf("expected error for input %q", tc.input)
			}
		})
	}
}

func TestParseSecurityGroupRules_PortRanges(t *testing.T) {
	rules, err := parseSecurityGroupRules("tcp:8000-8100:10.0.0.0/16")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].FromPort != 8000 || rules[0].ToPort != 8100 {
		t.Errorf("ports = %d-%d, want 8000-8100", rules[0].FromPort, rules[0].ToPort)
	}
}

func TestParseSecurityGroupRules_ICMP(t *testing.T) {
	rules, err := parseSecurityGroupRules("icmp:-1:0.0.0.0/0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Protocol != "icmp" {
		t.Errorf("Protocol = %q, want icmp", rules[0].Protocol)
	}
	if rules[0].FromPort != -1 || rules[0].ToPort != -1 {
		t.Errorf("ports = %d-%d, want -1:-1", rules[0].FromPort, rules[0].ToPort)
	}
}

func TestParseSecurityGroupRules_AllTraffic(t *testing.T) {
	rules, err := parseSecurityGroupRules("-1:0:0.0.0.0/0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Protocol != "-1" {
		t.Errorf("Protocol = %q, want -1", rules[0].Protocol)
	}
}

func TestParseSecurityGroupRules_BracketWrapped(t *testing.T) {
	rules, err := parseSecurityGroupRules("[tcp:443:10.0.0.0/16]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Protocol != "tcp" || rules[0].FromPort != 443 {
		t.Errorf("unexpected rule: %+v", rules[0])
	}
}

func TestSGRulesToIPPermissions(t *testing.T) {
	rules := []SGRule{
		{Protocol: "tcp", FromPort: 443, ToPort: 443, CIDR: "10.0.0.0/16"},
		{Protocol: "udp", FromPort: 53, ToPort: 53, CIDR: "0.0.0.0/0"},
	}
	perms := sgRulesToIPPermissions(rules)
	if len(perms) != 2 {
		t.Fatalf("expected 2 permissions, got %d", len(perms))
	}
	if *perms[0].IpProtocol != "tcp" {
		t.Errorf("perm[0].IpProtocol = %q, want tcp", *perms[0].IpProtocol)
	}
	if *perms[0].FromPort != 443 || *perms[0].ToPort != 443 {
		t.Errorf("perm[0] ports = %d-%d, want 443-443", *perms[0].FromPort, *perms[0].ToPort)
	}
	if len(perms[0].IpRanges) != 1 || *perms[0].IpRanges[0].CidrIp != "10.0.0.0/16" {
		t.Errorf("perm[0] CIDR unexpected")
	}
	if *perms[1].IpProtocol != "udp" {
		t.Errorf("perm[1].IpProtocol = %q, want udp", *perms[1].IpProtocol)
	}
}

func TestDiffIPPermissions_OldVsNew(t *testing.T) {
	old := []ec2types.IpPermission{
		{IpProtocol: awsString("tcp"), FromPort: awsInt32(80), ToPort: awsInt32(80), IpRanges: []ec2types.IpRange{{CidrIp: awsString("0.0.0.0/0")}}},
		{IpProtocol: awsString("tcp"), FromPort: awsInt32(443), ToPort: awsInt32(443), IpRanges: []ec2types.IpRange{{CidrIp: awsString("0.0.0.0/0")}}},
	}
	new := []ec2types.IpPermission{
		{IpProtocol: awsString("tcp"), FromPort: awsInt32(443), ToPort: awsInt32(443), IpRanges: []ec2types.IpRange{{CidrIp: awsString("0.0.0.0/0")}}},
	}
	stale := diffIPPermissions(old, new)
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale permission, got %d", len(stale))
	}
	if *stale[0].FromPort != 80 {
		t.Errorf("stale port = %d, want 80", *stale[0].FromPort)
	}
}

func TestDiffIPPermissions_EmptyOld(t *testing.T) {
	stale := diffIPPermissions(nil, []ec2types.IpPermission{
		{IpProtocol: awsString("tcp"), FromPort: awsInt32(80), ToPort: awsInt32(80)},
	})
	if stale != nil {
		t.Fatalf("expected nil for empty old, got %v", stale)
	}
}

func TestDiffIPPermissions_AllStale(t *testing.T) {
	old := []ec2types.IpPermission{
		{IpProtocol: awsString("tcp"), FromPort: awsInt32(80), ToPort: awsInt32(80), IpRanges: []ec2types.IpRange{{CidrIp: awsString("0.0.0.0/0")}}},
	}
	stale := diffIPPermissions(old, nil)
	if len(stale) != 1 {
		t.Fatalf("expected 1 stale, got %d", len(stale))
	}
}

func TestIPPermissionKey(t *testing.T) {
	p := ec2types.IpPermission{
		IpProtocol: awsString("tcp"),
		FromPort:   awsInt32(443),
		ToPort:     awsInt32(443),
		IpRanges:   []ec2types.IpRange{{CidrIp: awsString("10.0.0.0/16")}},
	}
	key := ipPermissionKey(p)
	if key != "tcp:443:443:10.0.0.0/16" {
		t.Errorf("key = %q, want tcp:443:443:10.0.0.0/16", key)
	}
}

func TestIPPermissionKey_MultipleCIDRs(t *testing.T) {
	p := ec2types.IpPermission{
		IpProtocol: awsString("tcp"),
		FromPort:   awsInt32(80),
		ToPort:     awsInt32(80),
		IpRanges: []ec2types.IpRange{
			{CidrIp: awsString("10.0.0.0/16")},
			{CidrIp: awsString("172.16.0.0/12")},
		},
	}
	key := ipPermissionKey(p)
	// CIDRs should be sorted
	if key != "tcp:80:80:10.0.0.0/16,172.16.0.0/12" {
		t.Errorf("key = %q, want sorted CIDRs", key)
	}
}

func TestIPPermissionKey_Consistency(t *testing.T) {
	// Same permission constructed differently should produce the same key
	p1 := ec2types.IpPermission{
		IpProtocol: awsString("tcp"),
		FromPort:   awsInt32(80),
		ToPort:     awsInt32(80),
		IpRanges:   []ec2types.IpRange{{CidrIp: awsString("0.0.0.0/0")}},
	}
	p2 := ec2types.IpPermission{
		IpProtocol: awsString("tcp"),
		FromPort:   awsInt32(80),
		ToPort:     awsInt32(80),
		IpRanges:   []ec2types.IpRange{{CidrIp: awsString("0.0.0.0/0")}},
	}
	if ipPermissionKey(p1) != ipPermissionKey(p2) {
		t.Error("identical permissions should have identical keys")
	}
}

func TestSerializeSGRules_RoundTrip(t *testing.T) {
	input := "tcp:443:10.0.0.0/16,udp:53:0.0.0.0/0"
	rules, err := parseSecurityGroupRules(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	perms := sgRulesToIPPermissions(rules)
	serialized := serializeSGRules(perms)

	// Re-parse the serialized output (strip brackets)
	rules2, err := parseSecurityGroupRules(serialized)
	if err != nil {
		t.Fatalf("re-parse error on %q: %v", serialized, err)
	}
	if len(rules2) != len(rules) {
		t.Fatalf("round-trip rule count mismatch: %d vs %d", len(rules2), len(rules))
	}
	for i := range rules {
		if rules[i].Protocol != rules2[i].Protocol ||
			rules[i].FromPort != rules2[i].FromPort ||
			rules[i].ToPort != rules2[i].ToPort ||
			rules[i].CIDR != rules2[i].CIDR {
			t.Errorf("rule[%d] mismatch: %+v vs %+v", i, rules[i], rules2[i])
		}
	}
}

func TestSerializeSGRules_Empty(t *testing.T) {
	s := serializeSGRules(nil)
	if s != "[]" {
		t.Errorf("expected [], got %q", s)
	}
}

func TestSerializeSGRules_PortRange(t *testing.T) {
	perms := []ec2types.IpPermission{
		{
			IpProtocol: awsString("tcp"),
			FromPort:   awsInt32(8000),
			ToPort:     awsInt32(8100),
			IpRanges:   []ec2types.IpRange{{CidrIp: awsString("10.0.0.0/8")}},
		},
	}
	s := serializeSGRules(perms)
	if !strings.Contains(s, "8000-8100") {
		t.Errorf("expected port range in serialized output, got %q", s)
	}
}

func TestBuildNewPermsFromIntent(t *testing.T) {
	intentMap := map[string]interface{}{
		"intent.ingress": "tcp:80:0.0.0.0/0,tcp:443:0.0.0.0/0",
	}
	perms, err := buildNewPermsFromIntent(intentMap, "ingress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(perms) != 2 {
		t.Fatalf("expected 2 permissions, got %d", len(perms))
	}
	if *perms[0].FromPort != 80 {
		t.Errorf("first perm port = %d, want 80", *perms[0].FromPort)
	}
	if *perms[1].FromPort != 443 {
		t.Errorf("second perm port = %d, want 443", *perms[1].FromPort)
	}
}

func TestBuildNewPermsFromIntent_EmptyKey(t *testing.T) {
	intentMap := map[string]interface{}{}
	perms, err := buildNewPermsFromIntent(intentMap, "ingress")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if perms != nil {
		t.Fatalf("expected nil for missing key, got %v", perms)
	}
}

func TestBuildNewPermsFromIntent_InvalidRules(t *testing.T) {
	intentMap := map[string]interface{}{
		"intent.egress": "bad-format",
	}
	_, err := buildNewPermsFromIntent(intentMap, "egress")
	if err == nil {
		t.Fatal("expected error for invalid rules")
	}
}

// ============================================================
// ECS Task Definition Builder
// ============================================================

func TestBuildECSTaskDef_Minimal(t *testing.T) {
	td := buildECSTaskDef("my-service", "nginx:latest", map[string]interface{}{})
	if *td.Family != "my-service" {
		t.Errorf("Family = %q, want my-service", *td.Family)
	}
	if *td.Cpu != "256" {
		t.Errorf("Cpu = %q, want 256", *td.Cpu)
	}
	if *td.Memory != "512" {
		t.Errorf("Memory = %q, want 512", *td.Memory)
	}
	if len(td.ContainerDefinitions) != 1 {
		t.Fatalf("expected 1 container def, got %d", len(td.ContainerDefinitions))
	}
	cd := td.ContainerDefinitions[0]
	if *cd.Image != "nginx:latest" {
		t.Errorf("Image = %q, want nginx:latest", *cd.Image)
	}
	if *cd.Name != "my-service" {
		t.Errorf("container Name = %q, want my-service", *cd.Name)
	}
	if !*cd.Essential {
		t.Error("expected Essential=true")
	}
	if td.NetworkMode != ecstypes.NetworkModeAwsvpc {
		t.Errorf("NetworkMode = %v, want awsvpc", td.NetworkMode)
	}
	if len(td.RequiresCompatibilities) != 1 || td.RequiresCompatibilities[0] != ecstypes.CompatibilityFargate {
		t.Error("expected Fargate compatibility")
	}
}

func TestBuildECSTaskDef_CustomCPUMemory(t *testing.T) {
	td := buildECSTaskDef("svc", "app:v1", map[string]interface{}{
		"intent.cpu":    "1024",
		"intent.memory": "2048",
	})
	if *td.Cpu != "1024" {
		t.Errorf("Cpu = %q, want 1024", *td.Cpu)
	}
	if *td.Memory != "2048" {
		t.Errorf("Memory = %q, want 2048", *td.Memory)
	}
}

func TestBuildECSTaskDef_WithEnvVars(t *testing.T) {
	td := buildECSTaskDef("svc", "app:v1", map[string]interface{}{
		"intent.env.DB_HOST": "localhost",
		"intent.env.DB_PORT": "5432",
	})
	cd := td.ContainerDefinitions[0]
	if len(cd.Environment) != 2 {
		t.Fatalf("expected 2 env vars, got %d", len(cd.Environment))
	}
	// env vars are sorted by key
	if *cd.Environment[0].Name != "DB_HOST" || *cd.Environment[0].Value != "localhost" {
		t.Errorf("env[0] = %s=%s, want DB_HOST=localhost", *cd.Environment[0].Name, *cd.Environment[0].Value)
	}
	if *cd.Environment[1].Name != "DB_PORT" || *cd.Environment[1].Value != "5432" {
		t.Errorf("env[1] = %s=%s, want DB_PORT=5432", *cd.Environment[1].Name, *cd.Environment[1].Value)
	}
}

func TestBuildECSTaskDef_WithRoleARN(t *testing.T) {
	td := buildECSTaskDef("svc", "app:v1", map[string]interface{}{
		"intent.role_arn": "arn:aws:iam::123456789:role/ecsTaskRole",
	})
	if td.ExecutionRoleArn == nil || *td.ExecutionRoleArn != "arn:aws:iam::123456789:role/ecsTaskRole" {
		t.Errorf("ExecutionRoleArn unexpected: %v", td.ExecutionRoleArn)
	}
	if td.TaskRoleArn == nil || *td.TaskRoleArn != "arn:aws:iam::123456789:role/ecsTaskRole" {
		t.Errorf("TaskRoleArn unexpected: %v", td.TaskRoleArn)
	}
}

func TestBuildECSTaskDef_DesiredCountNotInTaskDef(t *testing.T) {
	// desired_count is a service-level field, not a task def field
	td := buildECSTaskDef("svc", "app:v1", map[string]interface{}{
		"intent.desired_count": "5",
	})
	// Task def should not have desired_count — it shouldn't break anything
	if *td.Cpu != "256" {
		t.Errorf("unexpected defaults affected by desired_count")
	}
}

func TestBuildECSTaskDef_CustomContainerPort(t *testing.T) {
	td := buildECSTaskDef("svc", "app:v1", map[string]interface{}{
		"intent.container_port": "3000",
	})
	cd := td.ContainerDefinitions[0]
	if len(cd.PortMappings) != 1 || *cd.PortMappings[0].ContainerPort != 3000 {
		t.Errorf("expected container port 3000, got %v", cd.PortMappings)
	}
}

// ============================================================
// Lambda VPC Config
// ============================================================

func TestLambdaVpcConfig_WithSubnetsAndSGs(t *testing.T) {
	cfg := lambdaVpcConfig(map[string]interface{}{
		"intent.subnet_ids":         "[subnet-1, subnet-2]",
		"intent.security_group_ids": "[sg-1, sg-2]",
	})
	if cfg == nil {
		t.Fatal("expected non-nil VPC config")
	}
	if len(cfg.SubnetIds) != 2 {
		t.Fatalf("expected 2 subnets, got %d", len(cfg.SubnetIds))
	}
	if cfg.SubnetIds[0] != "subnet-1" || cfg.SubnetIds[1] != "subnet-2" {
		t.Errorf("subnets = %v", cfg.SubnetIds)
	}
	if len(cfg.SecurityGroupIds) != 2 {
		t.Fatalf("expected 2 security groups, got %d", len(cfg.SecurityGroupIds))
	}
	if cfg.SecurityGroupIds[0] != "sg-1" || cfg.SecurityGroupIds[1] != "sg-2" {
		t.Errorf("security groups = %v", cfg.SecurityGroupIds)
	}
}

func TestLambdaVpcConfig_NoVPCConfig(t *testing.T) {
	cfg := lambdaVpcConfig(map[string]interface{}{})
	if cfg != nil {
		t.Fatalf("expected nil for no VPC config, got %v", cfg)
	}
}

func TestLambdaVpcConfig_OnlySubnets(t *testing.T) {
	cfg := lambdaVpcConfig(map[string]interface{}{
		"intent.subnet_ids": "[subnet-a]",
	})
	if cfg == nil {
		t.Fatal("expected non-nil VPC config with subnets only")
	}
	if len(cfg.SubnetIds) != 1 || cfg.SubnetIds[0] != "subnet-a" {
		t.Errorf("subnets = %v", cfg.SubnetIds)
	}
	if len(cfg.SecurityGroupIds) != 0 {
		t.Errorf("expected empty security groups, got %v", cfg.SecurityGroupIds)
	}
}

// ============================================================
// Alarm Parsing
// ============================================================

func TestParseAlarmOn_Valid(t *testing.T) {
	cond, err := parseAlarmOn("cpu > 80")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cond.Metric != "cpu" {
		t.Errorf("Metric = %q, want cpu", cond.Metric)
	}
	if cond.Operator != ">" {
		t.Errorf("Operator = %q, want >", cond.Operator)
	}
	if cond.Threshold != 80.0 {
		t.Errorf("Threshold = %f, want 80.0", cond.Threshold)
	}
}

func TestParseAlarmOn_LessThan(t *testing.T) {
	cond, err := parseAlarmOn("memory < 20")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cond.Metric != "memory" {
		t.Errorf("Metric = %q, want memory", cond.Metric)
	}
	if cond.Operator != "<" {
		t.Errorf("Operator = %q, want <", cond.Operator)
	}
	if cond.Threshold != 20.0 {
		t.Errorf("Threshold = %f, want 20.0", cond.Threshold)
	}
}

func TestParseAlarmOn_GreaterThanOrEqual(t *testing.T) {
	cond, err := parseAlarmOn("errors >= 10")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cond.Operator != ">=" {
		t.Errorf("Operator = %q, want >=", cond.Operator)
	}
}

func TestParseAlarmOn_LessThanOrEqual(t *testing.T) {
	cond, err := parseAlarmOn("freeable_memory <= 256")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cond.Operator != "<=" {
		t.Errorf("Operator = %q, want <=", cond.Operator)
	}
}

func TestParseAlarmOn_WithPercentSign(t *testing.T) {
	cond, err := parseAlarmOn("cpu > 80%")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cond.Threshold != 80.0 {
		t.Errorf("Threshold = %f, want 80.0 (percent stripped)", cond.Threshold)
	}
}

func TestParseAlarmOn_InvalidFormat(t *testing.T) {
	_, err := parseAlarmOn("bad")
	if err == nil {
		t.Fatal("expected error for invalid alarm_on format")
	}
}

func TestParseAlarmOn_Empty(t *testing.T) {
	cond, err := parseAlarmOn("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cond != nil {
		t.Fatalf("expected nil for empty alarm_on, got %+v", cond)
	}
}

func TestParseAlarmOn_EmptyMetric(t *testing.T) {
	_, err := parseAlarmOn("> 80")
	if err == nil {
		t.Fatal("expected error for empty metric")
	}
}

func TestParseAlarmOn_EmptyThreshold(t *testing.T) {
	_, err := parseAlarmOn("cpu >")
	if err == nil {
		t.Fatal("expected error for empty threshold")
	}
}

func TestParseAlarmOn_InvalidThreshold(t *testing.T) {
	_, err := parseAlarmOn("cpu > abc")
	if err == nil {
		t.Fatal("expected error for non-numeric threshold")
	}
}

func TestAlarmComparisonOperator_AllOps(t *testing.T) {
	cases := []struct {
		op   string
		want cloudwatchtypes.ComparisonOperator
	}{
		{">", cloudwatchtypes.ComparisonOperatorGreaterThanThreshold},
		{"<", cloudwatchtypes.ComparisonOperatorLessThanThreshold},
		{">=", cloudwatchtypes.ComparisonOperatorGreaterThanOrEqualToThreshold},
		{"<=", cloudwatchtypes.ComparisonOperatorLessThanOrEqualToThreshold},
		{"==", cloudwatchtypes.ComparisonOperatorGreaterThanOrEqualToThreshold}, // default
		{"", cloudwatchtypes.ComparisonOperatorGreaterThanOrEqualToThreshold},   // default
	}
	for _, tc := range cases {
		got := alarmComparisonOperator(tc.op)
		if got != tc.want {
			t.Errorf("alarmComparisonOperator(%q) = %v, want %v", tc.op, got, tc.want)
		}
	}
}

func TestAlarmMetricForTarget_RDS(t *testing.T) {
	cases := []struct {
		metric    string
		wantName  string
		wantNS    string
	}{
		{"cpu", "CPUUtilization", "AWS/RDS"},
		{"connections", "DatabaseConnections", "AWS/RDS"},
		{"freeable_memory", "FreeableMemory", "AWS/RDS"},
		{"read_latency", "ReadLatency", "AWS/RDS"},
		{"write_latency", "WriteLatency", "AWS/RDS"},
	}
	for _, tc := range cases {
		name, ns := alarmMetricForTarget("rds", tc.metric)
		if name != tc.wantName || ns != tc.wantNS {
			t.Errorf("alarmMetricForTarget(rds, %q) = (%q, %q), want (%q, %q)", tc.metric, name, ns, tc.wantName, tc.wantNS)
		}
	}
}

func TestAlarmMetricForTarget_ECS(t *testing.T) {
	name, ns := alarmMetricForTarget("ecs", "cpu")
	if name != "CPUUtilization" || ns != "AWS/ECS" {
		t.Errorf("got (%q, %q), want (CPUUtilization, AWS/ECS)", name, ns)
	}
	name, ns = alarmMetricForTarget("ecs", "memory")
	if name != "MemoryUtilization" || ns != "AWS/ECS" {
		t.Errorf("got (%q, %q), want (MemoryUtilization, AWS/ECS)", name, ns)
	}
}

func TestAlarmMetricForTarget_Lambda(t *testing.T) {
	name, ns := alarmMetricForTarget("lambda", "errors")
	if name != "Errors" || ns != "AWS/Lambda" {
		t.Errorf("got (%q, %q), want (Errors, AWS/Lambda)", name, ns)
	}
	name, ns = alarmMetricForTarget("lambda", "duration")
	if name != "Duration" || ns != "AWS/Lambda" {
		t.Errorf("got (%q, %q), want (Duration, AWS/Lambda)", name, ns)
	}
}

func TestAlarmMetricForTarget_EC2(t *testing.T) {
	name, ns := alarmMetricForTarget("ec2", "cpu")
	if name != "CPUUtilization" || ns != "AWS/EC2" {
		t.Errorf("got (%q, %q), want (CPUUtilization, AWS/EC2)", name, ns)
	}
}

func TestAlarmMetricForTarget_CustomMetric(t *testing.T) {
	name, ns := alarmMetricForTarget("rds", "CustomMetricName")
	// Custom metrics pass through as-is with default EC2 namespace
	if name != "custommetricname" || ns != "AWS/EC2" {
		t.Errorf("got (%q, %q), want (custommetricname, AWS/EC2)", name, ns)
	}
}

func TestAlarmDimensionsForTarget_AllTargets(t *testing.T) {
	cases := []struct {
		target   string
		resource string
		wantDim  string // expected dimension name, or "" for nil
	}{
		{"lambda", "my-func", "FunctionName"},
		{"ecs", "my-cluster", "ClusterName"},
		{"rds", "my-db", "DBInstanceIdentifier"},
		{"ec2", "i-123", ""},
		{"elasticache", "my-cache", "CacheClusterId"},
		{"eks", "my-eks", "ClusterName"},
		{"unknown", "anything", ""},
	}
	for _, tc := range cases {
		dims := alarmDimensionsForTarget(tc.target, tc.resource)
		if tc.wantDim == "" {
			if dims != nil {
				t.Errorf("alarmDimensionsForTarget(%q, %q): expected nil, got %v", tc.target, tc.resource, dims)
			}
			continue
		}
		if len(dims) != 1 {
			t.Errorf("alarmDimensionsForTarget(%q, %q): expected 1 dimension, got %d", tc.target, tc.resource, len(dims))
			continue
		}
		if *dims[0].Name != tc.wantDim {
			t.Errorf("alarmDimensionsForTarget(%q, %q): dim name = %q, want %q", tc.target, tc.resource, *dims[0].Name, tc.wantDim)
		}
	}
}

func TestAlarmDimensionsForTarget_TrimResourceName(t *testing.T) {
	// Lambda FunctionName is trimmed to 64 chars
	longName := strings.Repeat("a", 100)
	dims := alarmDimensionsForTarget("lambda", longName)
	if len(dims) != 1 {
		t.Fatal("expected 1 dimension")
	}
	if len(*dims[0].Value) != 64 {
		t.Errorf("expected value trimmed to 64, got %d", len(*dims[0].Value))
	}
}

// ============================================================
// IAM Trust Policy
// ============================================================

func TestTrustPolicyForService_Lambda(t *testing.T) {
	policy, err := trustPolicyForService("lambda.amazonaws.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(policy), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if doc["Version"] != "2012-10-17" {
		t.Errorf("Version = %v, want 2012-10-17", doc["Version"])
	}
	if !strings.Contains(policy, "lambda.amazonaws.com") {
		t.Error("expected lambda.amazonaws.com in policy")
	}
	if !strings.Contains(policy, "sts:AssumeRole") {
		t.Error("expected sts:AssumeRole in policy")
	}
}

func TestTrustPolicyForService_EC2(t *testing.T) {
	policy, err := trustPolicyForService("ec2.amazonaws.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(policy, "ec2.amazonaws.com") {
		t.Error("expected ec2.amazonaws.com in policy")
	}
}

func TestTrustPolicyForService_ECS(t *testing.T) {
	policy, err := trustPolicyForService("ecs-tasks.amazonaws.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(policy, "ecs-tasks.amazonaws.com") {
		t.Error("expected ecs-tasks.amazonaws.com in policy")
	}
}

func TestTrustPolicyForService_Empty(t *testing.T) {
	_, err := trustPolicyForService("")
	if err == nil {
		t.Fatal("expected error for empty service")
	}
}

func TestTrustPolicyForService_Invalid(t *testing.T) {
	cases := []string{
		"evil.com",
		"lambda",
		"<script>alert(1)</script>.amazonaws.com",
		"LAMBDA.amazonaws.com", // uppercase not allowed
	}
	for _, svc := range cases {
		_, err := trustPolicyForService(svc)
		if err == nil {
			t.Errorf("expected error for service %q", svc)
		}
	}
}

func TestDetectTrustService_AllRuntimes(t *testing.T) {
	cases := []struct {
		intent map[string]interface{}
		want   string
	}{
		{map[string]interface{}{"intent.runtime": "lambda"}, "lambda.amazonaws.com"},
		{map[string]interface{}{"intent.runtime": "ec2"}, "ec2.amazonaws.com"},
		{map[string]interface{}{"intent.runtime": "eks"}, "eks.amazonaws.com"},
		{map[string]interface{}{"intent.runtime": "container"}, "ecs-tasks.amazonaws.com"},
		{map[string]interface{}{}, "ecs-tasks.amazonaws.com"}, // default
	}
	for _, tc := range cases {
		got := detectTrustService(tc.intent)
		if got != tc.want {
			t.Errorf("detectTrustService(%v) = %q, want %q", tc.intent, got, tc.want)
		}
	}
}

// ============================================================
// Validation Per Target
// ============================================================

func TestValidateAWSInput_ElastiCache_InvalidAZMode(t *testing.T) {
	err := validateAWSInput("elasticache", map[string]interface{}{
		"intent.az_mode": "multi-az",
	})
	if err == nil {
		t.Fatal("expected validation error for invalid az_mode")
	}
	if !strings.Contains(err.Error(), "az_mode") {
		t.Errorf("error should mention az_mode: %v", err)
	}
}

func TestValidateAWSInput_ElastiCache_ValidAZMode(t *testing.T) {
	for _, mode := range []string{"single-az", "cross-az", "single_az", "cross_az"} {
		err := validateAWSInput("elasticache", map[string]interface{}{
			"intent.az_mode": mode,
		})
		if err != nil {
			t.Errorf("unexpected error for az_mode=%q: %v", mode, err)
		}
	}
}

func TestValidateAWSInput_ElastiCache_NoAZMode(t *testing.T) {
	err := validateAWSInput("elasticache", map[string]interface{}{})
	if err != nil {
		t.Fatalf("unexpected error for elasticache without az_mode: %v", err)
	}
}

func TestValidateAWSInput_RDS_InvalidMonitoringInterval(t *testing.T) {
	err := validateAWSInput("rds", map[string]interface{}{
		"intent.monitoring_interval": "45",
	})
	if err == nil {
		t.Fatal("expected validation error for non-standard monitoring interval")
	}
	if !strings.Contains(err.Error(), "monitoring_interval") {
		t.Errorf("error should mention monitoring_interval: %v", err)
	}
}

func TestValidateAWSInput_RDS_ValidMonitoringIntervals(t *testing.T) {
	for _, interval := range []string{"0", "1", "5", "10", "15", "30", "60"} {
		err := validateAWSInput("rds", map[string]interface{}{
			"intent.monitoring_interval": interval,
		})
		if err != nil {
			t.Errorf("unexpected error for monitoring_interval=%s: %v", interval, err)
		}
	}
}

func TestValidateAWSInput_RDS_MonitoringRequiresRoleARN(t *testing.T) {
	err := validateAWSInput("rds", map[string]interface{}{
		"intent.enhanced_monitoring": "true",
	})
	if err == nil {
		t.Fatal("expected validation error: enhanced_monitoring without monitoring_role_arn")
	}
	if !strings.Contains(err.Error(), "monitoring_role_arn") {
		t.Errorf("error should mention monitoring_role_arn: %v", err)
	}
}

func TestValidateAWSInput_RDS_MonitoringWithRoleARN(t *testing.T) {
	err := validateAWSInput("rds", map[string]interface{}{
		"intent.enhanced_monitoring":  "true",
		"intent.monitoring_role_arn": "arn:aws:iam::123456789:role/rds-monitoring",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateAWSInput_RDS_IOPSStorageType(t *testing.T) {
	// Valid: io1, io2, gp3
	for _, st := range []string{"io1", "io2", "gp3"} {
		err := validateAWSInput("rds", map[string]interface{}{
			"intent.iops":         "3000",
			"intent.storage_type": st,
		})
		if err != nil {
			t.Errorf("unexpected error for storage_type=%s with iops: %v", st, err)
		}
	}
	// Invalid: gp2
	err := validateAWSInput("rds", map[string]interface{}{
		"intent.iops":         "3000",
		"intent.storage_type": "gp2",
	})
	if err == nil {
		t.Fatal("expected validation error for iops with gp2 storage")
	}
}

func TestValidateAWSInput_UnknownTarget(t *testing.T) {
	err := validateAWSInput("unknown_thing", map[string]interface{}{
		"intent.anything": "value",
	})
	if err != nil {
		t.Fatalf("expected nil error for unknown target, got: %v", err)
	}
}

func TestValidateAWSInput_CrossCutting_AlarmOn(t *testing.T) {
	// Valid alarm_on should pass for any target
	err := validateAWSInput("rds", map[string]interface{}{
		"intent.alarm_on": "cpu > 80",
	})
	if err != nil {
		t.Fatalf("unexpected error for valid alarm_on: %v", err)
	}
	// Invalid alarm_on should fail for any target
	err = validateAWSInput("rds", map[string]interface{}{
		"intent.alarm_on": "bad",
	})
	if err == nil {
		t.Fatal("expected validation error for invalid alarm_on")
	}
}

// ============================================================
// Intent Helpers
// ============================================================

func TestStringListFromIntent_CommaSeparated(t *testing.T) {
	m := map[string]interface{}{
		"intent.items": "[a, b, c]",
	}
	got := stringListFromIntent(m, "items")
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(got), got)
	}
	if got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("items = %v, want [a b c]", got)
	}
}

func TestStringListFromIntent_NoBrackets(t *testing.T) {
	m := map[string]interface{}{
		"intent.items": "a, b, c",
	}
	got := stringListFromIntent(m, "items")
	if len(got) != 3 {
		t.Fatalf("expected 3 items, got %d: %v", len(got), got)
	}
}

func TestStringListFromIntent_Empty(t *testing.T) {
	m := map[string]interface{}{}
	got := stringListFromIntent(m, "items")
	if got != nil {
		t.Fatalf("expected nil for missing key, got %v", got)
	}
}

func TestStringListFromIntent_EmptyBrackets(t *testing.T) {
	m := map[string]interface{}{
		"intent.items": "[]",
	}
	got := stringListFromIntent(m, "items")
	if got != nil {
		t.Fatalf("expected nil for empty brackets, got %v", got)
	}
}

func TestStringListFromIntent_SingleItem(t *testing.T) {
	m := map[string]interface{}{
		"intent.items": "[foo]",
	}
	got := stringListFromIntent(m, "items")
	if len(got) != 1 || got[0] != "foo" {
		t.Errorf("got %v, want [foo]", got)
	}
}

func TestStringListFromIntent_QuotedItems(t *testing.T) {
	// The parser does a simple Trim of `"` chars — it strips trailing quotes
	// but can't strip leading quotes when preceded by whitespace after comma split.
	// This test documents the actual behavior: only first item gets fully unquoted.
	m := map[string]interface{}{
		"intent.items": `"hello","world"`,
	}
	got := stringListFromIntent(m, "items")
	if len(got) != 2 {
		t.Fatalf("expected 2 items, got %d: %v", len(got), got)
	}
	if got[0] != "hello" || got[1] != "world" {
		t.Errorf("items = %v, want [hello world]", got)
	}
}

func TestEnvFromIntent_WithEnvKeys(t *testing.T) {
	m := map[string]interface{}{
		"intent.env.DB_HOST":   "localhost",
		"intent.env.DB_PORT":   5432,
		"intent.engine":        "postgres",
		"intent.env.":          "empty-key-ignored",
	}
	env := envFromIntent(m)
	if len(env) != 2 {
		t.Fatalf("expected 2 env vars, got %d: %v", len(env), env)
	}
	if env["DB_HOST"] != "localhost" {
		t.Errorf("DB_HOST = %q, want localhost", env["DB_HOST"])
	}
	if env["DB_PORT"] != "5432" {
		t.Errorf("DB_PORT = %q, want 5432", env["DB_PORT"])
	}
}

func TestEnvFromIntent_Empty(t *testing.T) {
	m := map[string]interface{}{
		"intent.engine": "postgres",
	}
	env := envFromIntent(m)
	if env != nil {
		t.Fatalf("expected nil for no env keys, got %v", env)
	}
}

func TestParseBoolIntent_AllCases(t *testing.T) {
	m := map[string]interface{}{
		"intent.flag_true":  "true",
		"intent.flag_one":   "1",
		"intent.flag_false": "false",
		"intent.flag_zero":  "0",
		"intent.flag_other": "yes",
	}
	if !parseBoolIntent(m, "flag_true", false) {
		t.Error("expected true for 'true'")
	}
	if !parseBoolIntent(m, "flag_one", false) {
		t.Error("expected true for '1'")
	}
	if parseBoolIntent(m, "flag_false", true) {
		t.Error("expected false for 'false'")
	}
	if parseBoolIntent(m, "flag_zero", true) {
		t.Error("expected false for '0'")
	}
	if parseBoolIntent(m, "flag_other", false) {
		t.Error("expected false for 'yes'")
	}
	// Missing key → fallback
	if !parseBoolIntent(m, "nonexistent", true) {
		t.Error("expected fallback=true for missing key")
	}
	if parseBoolIntent(m, "nonexistent", false) {
		t.Error("expected fallback=false for missing key")
	}
}

func TestParseIntIntent_AllCases(t *testing.T) {
	m := map[string]interface{}{
		"intent.port":    "8080",
		"intent.bad":     "abc",
		"intent.empty":   "",
		"intent.float":   "3.14",
		"intent.zero":    "0",
	}
	if got := parseIntIntent(m, "port", 80); got != 8080 {
		t.Errorf("port: got %d, want 8080", got)
	}
	if got := parseIntIntent(m, "bad", 99); got != 99 {
		t.Errorf("bad: got %d, want fallback 99", got)
	}
	if got := parseIntIntent(m, "empty", 42); got != 42 {
		t.Errorf("empty: got %d, want fallback 42", got)
	}
	if got := parseIntIntent(m, "missing", 100); got != 100 {
		t.Errorf("missing: got %d, want fallback 100", got)
	}
	if got := parseIntIntent(m, "float", 0); got != 0 {
		t.Errorf("float: got %d, want fallback 0 (strconv.Atoi fails on floats)", got)
	}
	if got := parseIntIntent(m, "zero", 99); got != 0 {
		t.Errorf("zero: got %d, want 0", got)
	}
}

// ============================================================
// Duration / Log Retention
// ============================================================

func TestParseDurationDays_Valid(t *testing.T) {
	cases := []struct {
		input string
		want  int32
	}{
		{"30d", 30},
		{"365d", 365},
		{"7d", 7},
		{"1d", 1},
	}
	for _, tc := range cases {
		got := parseDurationDays(tc.input)
		if got != tc.want {
			t.Errorf("parseDurationDays(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestParseDurationDays_Invalid(t *testing.T) {
	cases := []struct {
		input string
		want  int32
	}{
		{"bad", 0},
		{"", 0},
		{"-5d", 0},
		{"0d", 0},
		{"abc123", 0},
	}
	for _, tc := range cases {
		got := parseDurationDays(tc.input)
		if got != tc.want {
			t.Errorf("parseDurationDays(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestLogRetentionDays_SnapsToValidValues(t *testing.T) {
	cases := []struct {
		input int32
		want  int32
	}{
		{0, 0},
		{-1, 0},
		{1, 1},
		{2, 3},
		{3, 3},
		{4, 5},
		{6, 7},
		{10, 14},
		{15, 30},
		{31, 60},
		{61, 90},
		{100, 120},
		{140, 150},
		{160, 180},
		{200, 365},
		{366, 400},
		{401, 545},
		{600, 731},
		{1000, 1096},
		{1500, 1827},
		{2000, 2192},
		{2500, 2557},
		{2900, 2922},
		{3000, 3288},
		{3300, 3653},
		{5000, 3653}, // capped at max
	}
	for _, tc := range cases {
		got := logRetentionDays(tc.input)
		if got != tc.want {
			t.Errorf("logRetentionDays(%d) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

func TestLogRetentionDays_ExactValidValues(t *testing.T) {
	// Every exact valid value should map to itself
	validValues := []int32{1, 3, 5, 7, 14, 30, 60, 90, 120, 150, 180, 365, 400, 545, 731, 1096, 1827, 2192, 2557, 2922, 3288, 3653}
	for _, v := range validValues {
		got := logRetentionDays(v)
		if got != v {
			t.Errorf("logRetentionDays(%d) = %d, want %d (exact valid value)", v, got, v)
		}
	}
}

// ============================================================
// RDS Credentials
// ============================================================

func TestRDSCredentials_Valid(t *testing.T) {
	m := map[string]interface{}{
		"intent.username": "admin",
		"intent.password": "secret123",
	}
	user, pass, err := rdsCredentials(m)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if user != "admin" {
		t.Errorf("user = %q, want admin", user)
	}
	if pass != "secret123" {
		t.Errorf("pass = %q, want secret123", pass)
	}
}

func TestRDSCredentials_MissingUsername(t *testing.T) {
	m := map[string]interface{}{
		"intent.password": "secret123",
	}
	_, _, err := rdsCredentials(m)
	if err == nil {
		t.Fatal("expected error for missing username")
	}
	if !strings.Contains(err.Error(), "username") {
		t.Errorf("error should mention username: %v", err)
	}
}

func TestRDSCredentials_MissingPassword(t *testing.T) {
	m := map[string]interface{}{
		"intent.username": "admin",
	}
	_, _, err := rdsCredentials(m)
	if err == nil {
		t.Fatal("expected error for missing password")
	}
	if !strings.Contains(err.Error(), "password") {
		t.Errorf("error should mention password: %v", err)
	}
}

func TestRDSCredentials_BothMissing(t *testing.T) {
	_, _, err := rdsCredentials(map[string]interface{}{})
	if err == nil {
		t.Fatal("expected error when both username and password are missing")
	}
}

// ============================================================
// CloudFront Config
// ============================================================

func TestCloudFrontConfigFromIntent_Valid(t *testing.T) {
	cfg := `{"DefaultCacheBehavior":{"ViewerProtocolPolicy":"redirect-to-https","TargetOriginId":"myOrigin","ForwardedValues":{"QueryString":false}},"Origins":{"Quantity":1,"Items":[{"Id":"myOrigin","DomainName":"example.com","S3OriginConfig":{"OriginAccessIdentity":""}}]},"Enabled":true,"Comment":"test"}`
	m := map[string]interface{}{
		"intent.distribution_config_json": cfg,
	}
	result, err := cloudFrontDistributionConfigFromIntent(m, "beecon-test", "suffix")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil distribution config")
	}
}

func TestCloudFrontConfigFromIntent_InvalidJSON(t *testing.T) {
	m := map[string]interface{}{
		"intent.distribution_config_json": "not-json",
	}
	_, err := cloudFrontDistributionConfigFromIntent(m, "base", "suffix")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestCloudFrontConfigFromIntent_MissingKey(t *testing.T) {
	m := map[string]interface{}{}
	_, err := cloudFrontDistributionConfigFromIntent(m, "base", "suffix")
	if err == nil {
		t.Fatal("expected error for missing distribution_config_json")
	}
}

// ============================================================
// Detect Record Target
// ============================================================

func TestDetectRecordTarget_FromLiveState(t *testing.T) {
	cases := []struct {
		service string
		want    string
	}{
		{"rds", "rds"},
		{"s3", "s3"},
		{"sqs", "sqs"},
		{"sns", "sns"},
		{"secretsmanager", "secrets_manager"},
		{"iam", "iam"},
		{"lambda", "lambda"},
		{"elasticache", "elasticache"},
		{"cloudfront", "cloudfront"},
		{"route53", "route53"},
		{"cloudwatch", "cloudwatch"},
		{"eks", "eks"},
		{"eventbridge", "eventbridge"},
		{"cognito", "cognito"},
	}
	for _, tc := range cases {
		rec := &state.ResourceRecord{
			LiveState: map[string]interface{}{"service": tc.service},
		}
		got := detectRecordTarget(rec)
		if got != tc.want {
			t.Errorf("detectRecordTarget(service=%q) = %q, want %q", tc.service, got, tc.want)
		}
	}
}

func TestDetectRecordTarget_EC2Subtypes(t *testing.T) {
	cases := []struct {
		resource string
		want     string
	}{
		{"vpc", "vpc"},
		{"subnet", "subnet"},
		{"security_group", "security_group"},
		{"ec2", "ec2"},
	}
	for _, tc := range cases {
		rec := &state.ResourceRecord{
			LiveState: map[string]interface{}{"service": "ec2", "resource": tc.resource},
		}
		got := detectRecordTarget(rec)
		if got != tc.want {
			t.Errorf("detectRecordTarget(ec2/%q) = %q, want %q", tc.resource, got, tc.want)
		}
	}
}

func TestDetectRecordTarget_FromIntentSnapshot(t *testing.T) {
	cases := []struct {
		name     string
		nodeType string
		intent   map[string]interface{}
		want     string
	}{
		{"rds_from_engine", "STORE", map[string]interface{}{"intent.engine": "postgres"}, "rds"},
		// aurora-postgresql contains "postgres" which matches rds before aurora in the switch
		{"aurora_from_engine", "STORE", map[string]interface{}{"intent.engine": "aurora"}, "rds_aurora_serverless"},
		{"redis_from_engine", "STORE", map[string]interface{}{"intent.engine": "redis"}, "elasticache"},
		{"s3_from_type", "STORE", map[string]interface{}{"intent.type": "s3"}, "s3"},
		{"vpc_from_topology", "NETWORK", map[string]interface{}{"intent.topology": "vpc"}, "vpc"},
		{"subnet_from_topology", "NETWORK", map[string]interface{}{"intent.topology": "subnet"}, "subnet"},
		{"sg_from_topology", "NETWORK", map[string]interface{}{"intent.topology": "security_group"}, "security_group"},
		{"lambda_from_runtime", "SERVICE", map[string]interface{}{"intent.runtime": "lambda"}, "lambda"},
		{"eks_from_runtime", "SERVICE", map[string]interface{}{"intent.runtime": "eks"}, "eks"},
		{"eventbridge_from_runtime", "COMPUTE", map[string]interface{}{"intent.runtime": "eventbridge"}, "eventbridge"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &state.ResourceRecord{
				NodeType:       tc.nodeType,
				LiveState:      map[string]interface{}{},
				IntentSnapshot: tc.intent,
			}
			got := detectRecordTarget(rec)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDetectRecordTarget_NilRecord(t *testing.T) {
	got := detectRecordTarget(nil)
	if got != "generic" {
		t.Errorf("expected 'generic' for nil record, got %q", got)
	}
}

func TestDetectRecordTarget_EmptyRecord(t *testing.T) {
	rec := &state.ResourceRecord{
		LiveState:      map[string]interface{}{},
		IntentSnapshot: map[string]interface{}{},
	}
	got := detectRecordTarget(rec)
	if got != "generic" {
		t.Errorf("expected 'generic' for empty record, got %q", got)
	}
}

// ============================================================
// CloudWatch Statistic
// ============================================================

func TestCloudWatchStatistic_AllValues(t *testing.T) {
	cases := []struct {
		input string
		want  cloudwatchtypes.Statistic
	}{
		{"Average", cloudwatchtypes.StatisticAverage},
		{"average", cloudwatchtypes.StatisticAverage},
		{"Sum", cloudwatchtypes.StatisticSum},
		{"sum", cloudwatchtypes.StatisticSum},
		{"Maximum", cloudwatchtypes.StatisticMaximum},
		{"max", cloudwatchtypes.StatisticMaximum},
		{"Minimum", cloudwatchtypes.StatisticMinimum},
		{"min", cloudwatchtypes.StatisticMinimum},
		{"SampleCount", cloudwatchtypes.StatisticSampleCount},
		{"sample_count", cloudwatchtypes.StatisticSampleCount},
		{"", cloudwatchtypes.StatisticAverage},            // default
		{"unknown", cloudwatchtypes.StatisticAverage},     // default
	}
	for _, tc := range cases {
		got := cloudWatchStatisticFromString(tc.input)
		if got != tc.want {
			t.Errorf("cloudWatchStatisticFromString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ============================================================
// RuntimeFromString
// ============================================================

func TestRuntimeFromString(t *testing.T) {
	cases := []struct {
		input string
		want  lambdatypes.Runtime
	}{
		{"go1.x", lambdatypes.Runtime("go1.x")},
		{"python3.9", lambdatypes.Runtime("python3.9")},
		{"nodejs18.x", lambdatypes.Runtime("nodejs18.x")},
		{"provided.al2023", lambdatypes.Runtime("provided.al2023")},
		{"", lambdatypes.Runtime("provided.al2")},  // default
		{"  ", lambdatypes.Runtime("provided.al2")}, // whitespace → default
	}
	for _, tc := range cases {
		got := runtimeFromString(tc.input)
		if got != tc.want {
			t.Errorf("runtimeFromString(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// ============================================================
// Utility helpers (awsString, awsBool, etc.)
// ============================================================

func TestAwsString(t *testing.T) {
	p := awsString("hello")
	if *p != "hello" {
		t.Errorf("awsString = %q, want hello", *p)
	}
}

func TestAwsBool(t *testing.T) {
	p := awsBool(true)
	if !*p {
		t.Error("awsBool(true) should be true")
	}
	p = awsBool(false)
	if *p {
		t.Error("awsBool(false) should be false")
	}
}

func TestAwsInt32(t *testing.T) {
	p := awsInt32(42)
	if *p != 42 {
		t.Errorf("awsInt32(42) = %d, want 42", *p)
	}
}

func TestAwsFloat64(t *testing.T) {
	p := awsFloat64(3.14)
	if *p != 3.14 {
		t.Errorf("awsFloat64(3.14) = %f, want 3.14", *p)
	}
}

func TestStringValue(t *testing.T) {
	s := "hello"
	if stringValue(&s) != "hello" {
		t.Error("stringValue failed for non-nil")
	}
	if stringValue(nil) != "" {
		t.Error("stringValue(nil) should return empty string")
	}
}

func TestIntValue(t *testing.T) {
	var n int32 = 42
	if intValue(&n) != 42 {
		t.Error("intValue failed for non-nil")
	}
	if intValue(nil) != 0 {
		t.Error("intValue(nil) should return 0")
	}
}

func TestToJSON(t *testing.T) {
	m := map[string]string{"key": "value"}
	s := toJSON(m)
	if !strings.Contains(s, `"key"`) || !strings.Contains(s, `"value"`) {
		t.Errorf("toJSON = %q, expected key/value", s)
	}
}

// ============================================================
// RecordProviderID
// ============================================================

func TestRecordProviderID_WithRecord(t *testing.T) {
	r := ApplyRequest{
		Record: &state.ResourceRecord{ProviderID: "my-id"},
	}
	if r.RecordProviderID() != "my-id" {
		t.Errorf("got %q, want my-id", r.RecordProviderID())
	}
}

func TestRecordProviderID_NilRecord(t *testing.T) {
	r := ApplyRequest{}
	if r.RecordProviderID() != "" {
		t.Errorf("got %q, want empty", r.RecordProviderID())
	}
}
