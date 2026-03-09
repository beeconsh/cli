package mcp

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	mcplib "github.com/mark3labs/mcp-go/mcp"
	"github.com/terracotta-ai/beecon/internal/engine"
	"github.com/terracotta-ai/beecon/internal/state"
)

func TestNew(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")
	if s == nil {
		t.Fatal("expected non-nil server")
	}
	if s.eng != eng {
		t.Fatal("expected engine to be set")
	}
	if s.mcp == nil {
		t.Fatal("expected mcp server to be set")
	}
}

func TestHandleValidateBeacon_MissingFile(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleValidateBeacon(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing beacon_file")
	}
}

func TestHandleValidateBeacon_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"beacon_file": "/nonexistent/path.beecon",
	}

	res, err := s.handleValidateBeacon(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for nonexistent file")
	}
}

func TestHandleDiscoverBeacons(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleDiscoverBeacons(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatal("expected success for discover")
	}

	// Should return valid JSON with beacons array.
	text := res.Content[0].(mcplib.TextContent).Text
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if _, ok := data["beacons"]; !ok {
		t.Fatal("expected beacons key in response")
	}
}

func TestHandleGetHistory_MissingResourceID(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleGetHistory(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing resource_id")
	}
}

func TestHandleRollback_MissingRunID(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleRollback(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing run_id")
	}
}

func TestHandleApply_MissingBeaconFile(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleApply(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing beacon_file")
	}
}

func TestHandleApprove_MissingRequestID(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleApprove(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing request_id")
	}
}

func TestHandleReject_MissingRequestID(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleReject(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing request_id")
	}
}

func TestHandleConnectProvider_MissingProvider(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleConnectProvider(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for missing provider")
	}
}

// --- Happy-path tests ---

func writeTestBeacon(t *testing.T, dir string) string {
	t.Helper()
	content := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
store postgres {
  engine = postgres
}
`
	path := dir + "/infra.beecon"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func keys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}

func TestHandleValidateBeacon_HappyPath(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")
	beaconPath := writeTestBeacon(t, dir)

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"beacon_file": beaconPath}

	res, err := s.handleValidateBeacon(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %v", res.Content)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if data["valid"] != true {
		t.Fatal("expected valid=true")
	}
}

func TestHandlePlan_HappyPath(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")
	beaconPath := writeTestBeacon(t, dir)

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"beacon_file": beaconPath}

	res, err := s.handlePlan(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %v", res.Content)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	// Should have plan with actions.
	plan, ok := data["plan"].(map[string]any)
	if !ok {
		t.Fatalf("expected plan key in response, got keys: %v", keys(data))
	}
	actions, ok := plan["actions"].([]any)
	if !ok {
		t.Fatalf("expected plan.actions array, got keys: %v", keys(plan))
	}
	if len(actions) == 0 {
		t.Fatal("expected at least one action")
	}
	// Check enrichment fields present.
	action := actions[0].(map[string]any)
	if _, ok := action["risk_score"]; !ok {
		t.Error("expected risk_score on action")
	}
	if _, ok := action["risk_level"]; !ok {
		t.Error("expected risk_level on action")
	}
	if _, ok := action["rollback_feasibility"]; !ok {
		t.Error("expected rollback_feasibility on action")
	}
	// Should have summary.
	if _, ok := data["summary"]; !ok {
		t.Error("expected summary in plan result")
	}
}

func TestHandleShowStatus_HappyPath(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleShowStatus(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %v", res.Content)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
}

func TestHandleListRuns_HappyPath(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleListRuns(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %v", res.Content)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if _, ok := data["runs"]; !ok {
		t.Fatal("expected runs key in response")
	}
}

func TestHandleListApprovals_HappyPath(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{}

	res, err := s.handleListApprovals(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %v", res.Content)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var data map[string]any
	if err := json.Unmarshal([]byte(text), &data); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if _, ok := data["approvals"]; !ok {
		t.Fatal("expected approvals key in response")
	}
}

func TestHandlePlan_ScrubsSensitiveData(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")
	// Beacon with env vars that should be scrubbed.
	content := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)
}
service api {
  runtime = container(from: ./Dockerfile)
  env {
    DATABASE_URL = postgres://secret
  }
}
`
	path := dir + "/infra.beecon"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{"beacon_file": path}

	res, err := s.handlePlan(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatalf("expected success, got error: %v", res.Content)
	}
	text := res.Content[0].(mcplib.TextContent).Text
	// The DATABASE_URL value should be scrubbed.
	if strings.Contains(text, "postgres://secret") {
		t.Error("expected DATABASE_URL value to be scrubbed from output")
	}
}

func TestResultJSON(t *testing.T) {
	data := map[string]any{"key": "value", "count": 42}
	res, err := resultJSON(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.IsError {
		t.Fatal("expected success")
	}
	text := res.Content[0].(mcplib.TextContent).Text
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("expected valid JSON: %v", err)
	}
	if parsed["key"] != "value" {
		t.Fatalf("expected key=value, got %v", parsed["key"])
	}
}

// --- Fix verification tests ---

// Fix #1: Path traversal protection on beacon_file params.
func TestHandleValidateBeacon_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"beacon_file": "../../etc/passwd",
	}

	res, err := s.handleValidateBeacon(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for path traversal")
	}
	text := res.Content[0].(mcplib.TextContent).Text
	if !strings.Contains(text, "invalid beacon_file") {
		t.Errorf("expected path traversal error, got: %s", text)
	}
}

func TestHandlePlan_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"beacon_file": "../../../etc/shadow",
	}

	res, err := s.handlePlan(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for path traversal")
	}
}

func TestHandleApply_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"beacon_file": "../../etc/passwd",
	}

	res, err := s.handleApply(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for path traversal")
	}
}

func TestHandleDetectDrift_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	eng := engine.New(dir)
	s := New(eng, "test")

	req := mcplib.CallToolRequest{}
	req.Params.Arguments = map[string]any{
		"beacon_file": "/etc/passwd",
	}

	res, err := s.handleDetectDrift(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for path traversal")
	}
}

// Fix #2: scrubState completeness — Wiring, BeaconPath, Audit.
func TestScrubState_Complete(t *testing.T) {
	st := &state.State{
		Resources: map[string]*state.ResourceRecord{
			"res-1": {
				IntentSnapshot: map[string]interface{}{"password": "secret123"},
				LiveState:      map[string]interface{}{"api_key": "key-abc"},
				Wiring: &state.WiringMetadata{
					InferredEnvVars: map[string]string{"DATABASE_URL": "postgres://secret"},
				},
			},
		},
		Runs: map[string]*state.RunRecord{
			"run-1": {BeaconPath: "/home/user/project/infra.beecon"},
		},
		Approvals: map[string]*state.ApprovalRequest{
			"apr-1": {BeaconPath: "/home/user/project/infra.beecon"},
		},
		Audit: []state.AuditEvent{
			{Data: map[string]interface{}{"password": "secret", "action": "create"}},
		},
		Actions: map[string]*state.PlanAction{
			"act-1": {Changes: map[string]string{"token": "old -> new"}},
		},
	}

	scrubState(st)

	// Wiring env vars scrubbed.
	rec := st.Resources["res-1"]
	if rec.Wiring.InferredEnvVars["DATABASE_URL"] != "**REDACTED**" {
		t.Errorf("expected Wiring.InferredEnvVars to be scrubbed, got %v", rec.Wiring.InferredEnvVars["DATABASE_URL"])
	}

	// BeaconPath truncated to base name.
	if st.Runs["run-1"].BeaconPath != "infra.beecon" {
		t.Errorf("expected run BeaconPath to be truncated, got %s", st.Runs["run-1"].BeaconPath)
	}
	if st.Approvals["apr-1"].BeaconPath != "infra.beecon" {
		t.Errorf("expected approval BeaconPath to be truncated, got %s", st.Approvals["apr-1"].BeaconPath)
	}

	// Audit events scrubbed.
	if st.Audit[0].Data["password"] != "**REDACTED**" {
		t.Errorf("expected audit event Data to be scrubbed, got %v", st.Audit[0].Data["password"])
	}
	if st.Audit[0].Data["action"] != "create" {
		t.Errorf("expected non-sensitive audit data preserved, got %v", st.Audit[0].Data["action"])
	}

	// Original scrubbing still works.
	if rec.IntentSnapshot["password"] != "**REDACTED**" {
		t.Errorf("expected IntentSnapshot scrubbed")
	}
	if rec.LiveState["api_key"] != "**REDACTED**" {
		t.Errorf("expected LiveState scrubbed")
	}
	if st.Actions["act-1"].Changes["token"] != "**REDACTED** -> **REDACTED**" {
		t.Errorf("expected action Changes scrubbed")
	}
}

// Fix #7: scrubApplyResult nil safety.
func TestScrubApplyResult_NilSafe(t *testing.T) {
	// Should not panic on nil input.
	scrubApplyResult(nil)

	// Should not panic on nil Action within outcome.
	res := &engine.ApplyResult{
		Actions: []engine.ActionOutcome{
			{Action: nil, Status: engine.ActionExecuted},
		},
	}
	scrubApplyResult(res)
}
