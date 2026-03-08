package btest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/terracotta-ai/beecon/internal/engine"
	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/resolver"
	"github.com/terracotta-ai/beecon/internal/state"
)

func testPlanResult() *engine.PlanResult {
	return &engine.PlanResult{
		Graph: &ir.Graph{
			Nodes: []ir.IntentNode{
				{
					ID:   "service.api",
					Name: "api",
					Type: ir.NodeService,
					Intent: map[string]string{
						"engine":    "ecs",
						"image_uri": "nginx:latest",
					},
					Performance: map[string]string{
						"cpu":    "256",
						"memory": "512",
					},
					Env: map[string]string{
						"PORT": "8080",
					},
				},
				{
					ID:   "store.db",
					Name: "db",
					Type: ir.NodeStore,
					Intent: map[string]string{
						"engine":      "postgres",
						"storage_gib": "100",
					},
				},
			},
		},
		Plan: &resolver.Plan{
			Actions: []*state.PlanAction{
				{ID: "act-1", NodeID: "service.api", Operation: "CREATE"},
				{ID: "act-2", NodeID: "store.db", Operation: "CREATE"},
			},
		},
	}
}

func writeTestFile(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.beecon-test")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAssertEquals(t *testing.T) {
	path := writeTestFile(t, `assert api intent.engine == "ecs"`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 1 || res.Failed != 0 {
		t.Errorf("expected 1 pass, got %d pass %d fail", res.Passed, res.Failed)
	}
}

func TestAssertNotEquals(t *testing.T) {
	path := writeTestFile(t, `assert api intent.engine != "lambda"`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 1 {
		t.Errorf("expected 1 pass, got %d", res.Passed)
	}
}

func TestAssertContains(t *testing.T) {
	path := writeTestFile(t, `assert api intent.image_uri contains "nginx"`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 1 {
		t.Errorf("expected 1 pass, got %d", res.Passed)
	}
}

func TestAssertFails(t *testing.T) {
	path := writeTestFile(t, `assert api intent.engine == "lambda"`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 {
		t.Errorf("expected 1 fail, got %d", res.Failed)
	}
	if res.Assertions[0].Message == "" {
		t.Error("expected failure message")
	}
}

func TestAssertCount(t *testing.T) {
	path := writeTestFile(t, "assert_count CREATE 2\nassert_count DELETE 0")
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 2 || res.Failed != 0 {
		t.Errorf("expected 2 pass, got %d pass %d fail", res.Passed, res.Failed)
	}
}

func TestAssertCountFails(t *testing.T) {
	path := writeTestFile(t, "assert_count CREATE 5")
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 {
		t.Errorf("expected 1 fail, got %d", res.Failed)
	}
}

func TestAssertByID(t *testing.T) {
	path := writeTestFile(t, `assert service.api intent.engine == "ecs"`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 1 {
		t.Errorf("expected 1 pass, got %d (msg: %s)", res.Passed, res.Assertions[0].Message)
	}
}

func TestAssertPerformanceField(t *testing.T) {
	path := writeTestFile(t, `assert api performance.cpu == "256"`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 1 {
		t.Errorf("expected 1 pass, got %d (msg: %s)", res.Passed, res.Assertions[0].Message)
	}
}

func TestAssertEnvField(t *testing.T) {
	path := writeTestFile(t, `assert api env.PORT == "8080"`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 1 {
		t.Errorf("expected 1 pass, got %d (msg: %s)", res.Passed, res.Assertions[0].Message)
	}
}

func TestAssertTypeField(t *testing.T) {
	path := writeTestFile(t, `assert api type == "SERVICE"`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 1 {
		t.Errorf("expected 1 pass, got %d (msg: %s)", res.Passed, res.Assertions[0].Message)
	}
}

func TestCommentsAndBlankLines(t *testing.T) {
	path := writeTestFile(t, `# This is a comment
// This is also a comment

assert api intent.engine == "ecs"
`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Assertions) != 1 {
		t.Errorf("expected 1 assertion (comments ignored), got %d", len(res.Assertions))
	}
}

func TestUnknownNode(t *testing.T) {
	path := writeTestFile(t, `assert nonexistent intent.foo == "bar"`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 1 {
		t.Errorf("expected 1 fail for unknown node, got %d", res.Failed)
	}
}

func TestMultipleAssertions(t *testing.T) {
	path := writeTestFile(t, `assert api intent.engine == "ecs"
assert db intent.engine == "postgres"
assert_count CREATE 2
assert_count DELETE 0
assert api performance.memory == "512"
`)
	res, err := RunFile(path, testPlanResult())
	if err != nil {
		t.Fatal(err)
	}
	if res.Passed != 5 || res.Failed != 0 {
		t.Errorf("expected 5 pass 0 fail, got %d pass %d fail", res.Passed, res.Failed)
	}
}

func TestTokenizeQuotedStrings(t *testing.T) {
	tokens := tokenize(`assert api intent.name == "hello world"`)
	if len(tokens) != 5 {
		t.Fatalf("expected 5 tokens, got %d: %v", len(tokens), tokens)
	}
	if unquote(tokens[4]) != "hello world" {
		t.Errorf("expected 'hello world', got %q", unquote(tokens[4]))
	}
}
