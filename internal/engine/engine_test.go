package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/terracotta-ai/beecon/internal/state"
)

func TestApplyApprovalAndRollback(t *testing.T) {
	dir := t.TempDir()
	beacon := filepath.Join(dir, "infra.beecon")
	content := `domain acme {
  cloud = aws(region: us-east-1)
  owner = team(platform)

  boundary {
    approve = [new_store]
  }
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
`
	if err := os.WriteFile(beacon, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	e := New(dir)
	applied, err := e.Apply(ctx, beacon)
	if err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if applied.ApprovalRequestID == "" {
		t.Fatalf("expected approval request")
	}
	if applied.Executed == 0 {
		t.Fatalf("expected at least one action to execute before approval")
	}
	if len(applied.Actions) == 0 {
		t.Fatal("expected ActionOutcome slice to be populated")
	}
	if !applied.Simulated {
		t.Log("note: Simulated=false (BEECON_EXECUTE=1 or default executor)")
	}
	// Verify at least one pending and one executed outcome.
	var hasExecuted, hasPending bool
	for _, ao := range applied.Actions {
		switch ao.Status {
		case ActionExecuted:
			hasExecuted = true
		case ActionPending:
			hasPending = true
		}
	}
	if !hasExecuted {
		t.Fatal("expected at least one ActionExecuted outcome")
	}
	if !hasPending {
		t.Fatal("expected at least one ActionPending outcome")
	}

	_, err = e.Approve(ctx, applied.ApprovalRequestID, "tester")
	if err != nil {
		t.Fatalf("approve failed: %v", err)
	}

	st, err := e.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Runs[applied.RunID].Status != state.RunApplied {
		t.Fatalf("expected run status APPLIED, got %s", st.Runs[applied.RunID].Status)
	}

	rbID, err := e.Rollback(ctx, applied.RunID)
	if err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	if rbID == "" {
		t.Fatalf("expected rollback run id")
	}
}
