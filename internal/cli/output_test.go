package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestSymbolsFallbackWhenNotTTY(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf) // bytes.Buffer is not a TTY
	if w.ColorEnabled() {
		t.Fatal("expected color disabled for non-TTY")
	}
	if w.OK() != "+" {
		t.Errorf("OK() = %q, want +", w.OK())
	}
	if w.Fail() != "x" {
		t.Errorf("Fail() = %q, want x", w.Fail())
	}
	if w.Warn() != "!" {
		t.Errorf("Warn() = %q, want !", w.Warn())
	}
	if w.Arrow() != ">" {
		t.Errorf("Arrow() = %q, want >", w.Arrow())
	}
	if w.Dot() != "*" {
		t.Errorf("Dot() = %q, want *", w.Dot())
	}
}

func TestColorsNoOpWhenNotTTY(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	tests := []struct {
		name string
		fn   func(string) string
	}{
		{"Green", w.Green},
		{"Yellow", w.Yellow},
		{"Red", w.Red},
		{"Bold", w.Bold},
		{"Dim", w.Dim},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fn("hello")
			if got != "hello" {
				t.Errorf("%s(hello) = %q, want plain hello", tc.name, got)
			}
		})
	}
}

func TestLineOutput(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	w.Line(w.OK(), "created %s", "infra.beecon")
	got := buf.String()
	if !strings.Contains(got, "+ created infra.beecon") {
		t.Errorf("Line output = %q, want to contain '+ created infra.beecon'", got)
	}
}

func TestActionLineOutput(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	w.ActionLine("+", "CREATE", "network.core", "")
	got := buf.String()
	if !strings.Contains(got, "CREATE") || !strings.Contains(got, "network.core") {
		t.Errorf("ActionLine output = %q", got)
	}
}

func TestActionLineWithAnnotation(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	w.ActionLine("!", "PENDING", "store.postgres", "requires approval (new_store)")
	got := buf.String()
	if !strings.Contains(got, "requires approval") {
		t.Errorf("ActionLine annotation missing: %q", got)
	}
}

func TestHeaderOutput(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	w.Header("Plan: %d actions", 3)
	got := buf.String()
	if !strings.Contains(got, "Plan: 3 actions") {
		t.Errorf("Header output = %q", got)
	}
}

func TestNextOutput(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	w.Next("run `beecon plan` to see actions.", "then run `beecon apply`.")
	got := buf.String()
	if !strings.Contains(got, "Next:") {
		t.Errorf("Next output missing 'Next:': %q", got)
	}
	if !strings.Contains(got, "then run") {
		t.Errorf("Next continuation line missing: %q", got)
	}
}

func TestBlankOutput(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	w.Blank()
	if buf.String() != "\n" {
		t.Errorf("Blank() = %q, want newline", buf.String())
	}
}

func TestSummaryOutput(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	w.Summary("Resources: %d", 5)
	got := buf.String()
	if !strings.Contains(got, "Resources: 5") {
		t.Errorf("Summary output = %q", got)
	}
}

func TestNumberedAction(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	w.NumberedAction(1, "CREATE", "network.core", "")
	got := buf.String()
	if !strings.Contains(got, "1.") || !strings.Contains(got, "CREATE") {
		t.Errorf("NumberedAction output = %q", got)
	}
}

func TestStatusLine(t *testing.T) {
	var buf bytes.Buffer
	w := New(&buf)
	w.StatusLine("network.core", "MATCHED", "last: CREATE")
	got := buf.String()
	if !strings.Contains(got, "network.core") || !strings.Contains(got, "MATCHED") {
		t.Errorf("StatusLine output = %q", got)
	}
}

func TestNOCOLORRespected(t *testing.T) {
	// Even if we could somehow fake a TTY, NO_COLOR should disable color.
	// Since we can't easily make a TTY in tests, just verify the env check path.
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	w := New(&buf)
	if w.ColorEnabled() {
		t.Fatal("expected color disabled when NO_COLOR is set")
	}
}

func TestIsTTYWithFile(t *testing.T) {
	// A regular file should not be detected as a TTY.
	f, err := os.CreateTemp(t.TempDir(), "test")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTTY(f) {
		t.Error("regular file should not be detected as TTY")
	}
}
