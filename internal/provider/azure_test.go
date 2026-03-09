package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestIsAzureNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"azcore 404", &azcore.ResponseError{StatusCode: 404}, true},
		{"azcore ResourceNotFound", &azcore.ResponseError{StatusCode: 400, ErrorCode: "ResourceNotFound"}, true},
		{"azcore ResourceGroupNotFound", &azcore.ResponseError{StatusCode: 400, ErrorCode: "ResourceGroupNotFound"}, true},
		{"azcore ContainerNotFound", &azcore.ResponseError{StatusCode: 404, ErrorCode: "ContainerNotFound"}, true},
		{"azcore SecretNotFound", &azcore.ResponseError{StatusCode: 404, ErrorCode: "SecretNotFound"}, true},
		{"azcore BlobNotFound", &azcore.ResponseError{StatusCode: 404, ErrorCode: "BlobNotFound"}, true},
		{"azcore ParentResourceNotFound", &azcore.ResponseError{StatusCode: 404, ErrorCode: "ParentResourceNotFound"}, true},
		{"azcore NotFound", &azcore.ResponseError{StatusCode: 404, ErrorCode: "NotFound"}, true},
		{"azcore 403", &azcore.ResponseError{StatusCode: 403, ErrorCode: "Forbidden"}, false},
		{"azcore 500", &azcore.ResponseError{StatusCode: 500, ErrorCode: "InternalError"}, false},
		{"azcore 429", &azcore.ResponseError{StatusCode: 429, ErrorCode: "TooManyRequests"}, false},
		{"wrapped azcore 404", fmt.Errorf("delete: %w", &azcore.ResponseError{StatusCode: 404}), true},
		// String fallback removed — these should NOT match (prevents false positives)
		{"string not found (no match)", errors.New("The requested resource was not found"), false},
		{"credential not found (no match)", errors.New("DefaultAzureCredential: credential not found"), false},
		{"subscription does not exist (no match)", errors.New("subscription does not exist"), false},
		{"unrelated error", errors.New("connection refused"), false},
		{"empty message", errors.New(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAzureNotFound(tt.err)
			if got != tt.want {
				t.Errorf("isAzureNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsAzureTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"azcore 429", &azcore.ResponseError{StatusCode: 429, ErrorCode: "TooManyRequests"}, true},
		{"azcore 500", &azcore.ResponseError{StatusCode: 500, ErrorCode: "InternalServerError"}, true},
		{"azcore 502", &azcore.ResponseError{StatusCode: 502, ErrorCode: "BadGateway"}, true},
		{"azcore 503", &azcore.ResponseError{StatusCode: 503, ErrorCode: "ServiceUnavailable"}, true},
		{"azcore 504", &azcore.ResponseError{StatusCode: 504, ErrorCode: "GatewayTimeout"}, true},
		{"azcore ServerBusy", &azcore.ResponseError{StatusCode: 503, ErrorCode: "ServerBusy"}, true},
		{"azcore 404", &azcore.ResponseError{StatusCode: 404, ErrorCode: "NotFound"}, false},
		{"azcore 403", &azcore.ResponseError{StatusCode: 403, ErrorCode: "Forbidden"}, false},
		// context errors are NOT transient — withAzureRetry checks ctx.Done() directly
		{"context deadline exceeded", context.DeadlineExceeded, false},
		{"wrapped deadline exceeded", fmt.Errorf("op: %w", context.DeadlineExceeded), false},
		{"context canceled", context.Canceled, false},
		{"wrapped canceled", fmt.Errorf("op: %w", context.Canceled), false},
		{"string temporarily unavailable", errors.New("server is temporarily unavailable"), true},
		{"string rate limit", errors.New("rate limit exceeded"), true},
		{"unrelated error", errors.New("invalid argument"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAzureTransient(tt.err)
			if got != tt.want {
				t.Errorf("isAzureTransient(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsAzureConflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"azcore 409", &azcore.ResponseError{StatusCode: 409, ErrorCode: "Conflict"}, true},
		{"azcore 404", &azcore.ResponseError{StatusCode: 404}, false},
		{"azcore 500", &azcore.ResponseError{StatusCode: 500}, false},
		{"non-azcore error", errors.New("conflict"), false},
		{"wrapped 409", fmt.Errorf("create: %w", &azcore.ResponseError{StatusCode: 409}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAzureConflict(tt.err)
			if got != tt.want {
				t.Errorf("isAzureConflict(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsAzureNotFoundConsistency(t *testing.T) {
	// Verify isAzureNotFound catches all patterns that raw azureStatusCode == 404
	// would catch, plus error code based patterns.
	azcore404 := &azcore.ResponseError{StatusCode: 404}
	if !isAzureNotFound(azcore404) {
		t.Error("isAzureNotFound should detect azcore 404")
	}

	// Error code based detection that raw status code check would miss
	resourceNotFound := &azcore.ResponseError{StatusCode: 400, ErrorCode: "ResourceNotFound"}
	if !isAzureNotFound(resourceNotFound) {
		t.Error("isAzureNotFound should detect ResourceNotFound error code")
	}

	// Service-specific error codes
	containerNotFound := &azcore.ResponseError{StatusCode: 404, ErrorCode: "ContainerNotFound"}
	if !isAzureNotFound(containerNotFound) {
		t.Error("isAzureNotFound should detect ContainerNotFound error code")
	}

	// Non-not-found errors should not match
	forbidden := &azcore.ResponseError{StatusCode: 403, ErrorCode: "Forbidden"}
	if isAzureNotFound(forbidden) {
		t.Error("isAzureNotFound should not match 403 Forbidden")
	}

	// String-only errors should NOT match (prevents false positives on credential/config errors)
	credentialError := errors.New("DefaultAzureCredential: credential not found")
	if isAzureNotFound(credentialError) {
		t.Error("isAzureNotFound should not match credential configuration errors")
	}
}

func TestWithAzureRetry(t *testing.T) {
	t.Run("succeeds on first attempt", func(t *testing.T) {
		calls := 0
		err := withAzureRetry(context.Background(), "test_op", func() error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 call, got %d", calls)
		}
	})

	t.Run("retries on transient error then succeeds", func(t *testing.T) {
		calls := 0
		err := withAzureRetry(context.Background(), "test_op", func() error {
			calls++
			if calls < 3 {
				return &azcore.ResponseError{StatusCode: 503, ErrorCode: "ServiceUnavailable"}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if calls != 3 {
			t.Fatalf("expected 3 calls, got %d", calls)
		}
	})

	t.Run("does not retry non-transient error", func(t *testing.T) {
		calls := 0
		err := withAzureRetry(context.Background(), "test_op", func() error {
			calls++
			return &azcore.ResponseError{StatusCode: 404, ErrorCode: "NotFound"}
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if calls != 1 {
			t.Fatalf("expected 1 call (no retry), got %d", calls)
		}
	})

	t.Run("returns last error after max retries", func(t *testing.T) {
		calls := 0
		err := withAzureRetry(context.Background(), "test_op", func() error {
			calls++
			return &azcore.ResponseError{StatusCode: 429, ErrorCode: "TooManyRequests"}
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// 1 initial + 3 retries = 4 total
		if calls != 4 {
			t.Fatalf("expected 4 calls (1 + 3 retries), got %d", calls)
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := withAzureRetry(ctx, "test_op", func() error {
			calls++
			cancel()
			return &azcore.ResponseError{StatusCode: 503, ErrorCode: "ServiceUnavailable"}
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if calls > 2 {
			t.Fatalf("expected at most 2 calls due to cancellation, got %d", calls)
		}
	})

	t.Run("nil context defaults to background", func(t *testing.T) {
		calls := 0
		err := withAzureRetry(nil, "test_op", func() error {
			calls++
			return nil
		})
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}
		if calls != 1 {
			t.Fatalf("expected 1 call, got %d", calls)
		}
	})

	t.Run("already cancelled context returns immediately", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel before calling
		calls := 0
		err := withAzureRetry(ctx, "test_op", func() error {
			calls++
			return nil
		})
		if err == nil {
			t.Fatal("expected error for cancelled context")
		}
		if calls != 0 {
			t.Fatalf("expected 0 calls for pre-cancelled context, got %d", calls)
		}
	})
}

func TestAzureStatusCodeDeprecated(t *testing.T) {
	// Verify the deprecated azureStatusCode still works for backward compat
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"azcore 404", &azcore.ResponseError{StatusCode: 404}, 404},
		{"azcore 500", &azcore.ResponseError{StatusCode: 500}, 500},
		{"non-azcore error", errors.New("generic"), 0},
		{"nil error", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := azureStatusCode(tt.err)
			if got != tt.want {
				t.Errorf("azureStatusCode(%v) = %d, want %d", tt.err, got, tt.want)
			}
		})
	}
}
