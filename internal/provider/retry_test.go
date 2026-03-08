package provider

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestWithRetrySucceedsFirstAttempt(t *testing.T) {
	calls := 0
	result, err := withRetry(context.Background(), "test", 3, func() (string, error) {
		calls++
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Errorf("expected ok, got %s", result)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestWithRetryRetriesOnThrottling(t *testing.T) {
	calls := 0
	result, err := withRetry(context.Background(), "test", 3, func() (string, error) {
		calls++
		if calls < 3 {
			return "", errors.New("ThrottlingException: Rate exceeded")
		}
		return "ok", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if result != "ok" {
		t.Errorf("expected ok, got %s", result)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestWithRetryDoesNotRetryNonRetryable(t *testing.T) {
	calls := 0
	_, err := withRetry(context.Background(), "test", 3, func() (string, error) {
		calls++
		return "", errors.New("ValidationException: invalid parameter")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Errorf("expected 1 call for non-retryable, got %d", calls)
	}
}

func TestWithRetryRespectsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	calls := 0
	_, err := withRetry(ctx, "test", 3, func() (string, error) {
		calls++
		return "", errors.New("ThrottlingException")
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	// Should have tried once, then hit cancelled context on retry wait
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestWithRetryExhaustsAttempts(t *testing.T) {
	calls := 0
	_, err := withRetry(context.Background(), "test", 2, func() (string, error) {
		calls++
		return "", errors.New("Too Many Requests")
	})
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls != 2 {
		t.Errorf("expected 2 calls, got %d", calls)
	}
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		err       string
		retryable bool
	}{
		{"ThrottlingException: Rate exceeded", true},
		{"TooManyRequestsException", true},
		{"Too many requests", true},
		{"RequestLimitExceeded", true},
		{"Internal Server Error", true},
		{"Service Unavailable", true},
		{"Bad Gateway", true},
		{"Gateway Timeout", true},
		{"503 Service Temporarily Unavailable", true},
		{"ValidationException: invalid", false},
		{"ResourceNotFoundException", false},
		{"AccessDenied", false},
		{"", false},
	}
	for _, tt := range tests {
		var err error
		if tt.err != "" {
			err = errors.New(tt.err)
		}
		if got := isRetryable(err); got != tt.retryable {
			t.Errorf("isRetryable(%q) = %v, want %v", tt.err, got, tt.retryable)
		}
	}
}

func TestBackoffDelay(t *testing.T) {
	d0 := backoffDelay(0)
	d1 := backoffDelay(1)
	d2 := backoffDelay(2)

	// Each attempt should generally increase (with jitter, not always, but base doubles)
	// Just verify they're positive and within reasonable bounds
	for _, d := range []time.Duration{d0, d1, d2} {
		if d <= 0 {
			t.Errorf("delay should be positive, got %v", d)
		}
		if d > maxDelay {
			t.Errorf("delay %v exceeds max %v", d, maxDelay)
		}
	}
}
