package witness

import "testing"

func TestEvaluateBreachLatency(t *testing.T) {
	out := EvaluateBreach("latency_p95", "380ms", "200ms")
	if len(out) < 3 {
		t.Fatalf("expected at least 3 candidates, got %d", len(out))
	}
	if out[0].Action != "scale_out" {
		t.Fatalf("unexpected first action: %s", out[0].Action)
	}
}

func TestEvaluateBreachGeneric(t *testing.T) {
	out := EvaluateBreach("custom_metric", "10", "5")
	if len(out) != 1 || out[0].Action != "re_evaluate" {
		t.Fatalf("unexpected generic output: %#v", out)
	}
}
