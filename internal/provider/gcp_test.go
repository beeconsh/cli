package provider

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsGCPNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"gRPC NotFound", status.Error(codes.NotFound, "resource not found"), true},
		{"gRPC PermissionDenied", status.Error(codes.PermissionDenied, "denied"), false},
		{"gRPC Internal", status.Error(codes.Internal, "internal error"), false},
		{"googleapi 404", &googleapi.Error{Code: 404, Message: "not found"}, true},
		{"googleapi 403", &googleapi.Error{Code: 403, Message: "forbidden"}, false},
		{"googleapi 500", &googleapi.Error{Code: 500, Message: "server error"}, false},
		{"storage ErrBucketNotExist", storage.ErrBucketNotExist, true},
		{"storage ErrObjectNotExist", storage.ErrObjectNotExist, true},
		{"wrapped storage ErrBucketNotExist", fmt.Errorf("bucket check: %w", storage.ErrBucketNotExist), true},
		{"string fallback not found", errors.New("The requested resource was not found"), true},
		{"string fallback notfound", errors.New("googleapi: Error 404: notfound"), true},
		{"string fallback does not exist", errors.New("instance does not exist"), true},
		{"unrelated error", errors.New("connection refused"), false},
		{"empty message", errors.New(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGCPNotFound(tt.err)
			if got != tt.want {
				t.Errorf("isGCPNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsGCPTransient(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"gRPC Unavailable", status.Error(codes.Unavailable, "service unavailable"), true},
		{"gRPC ResourceExhausted", status.Error(codes.ResourceExhausted, "quota exceeded"), true},
		{"gRPC DeadlineExceeded", status.Error(codes.DeadlineExceeded, "deadline"), true},
		{"gRPC Aborted", status.Error(codes.Aborted, "aborted"), true},
		{"gRPC NotFound", status.Error(codes.NotFound, "not found"), false},
		{"gRPC PermissionDenied", status.Error(codes.PermissionDenied, "denied"), false},
		{"googleapi 429", &googleapi.Error{Code: 429, Message: "rate limited"}, true},
		{"googleapi 500", &googleapi.Error{Code: 500, Message: "server error"}, true},
		{"googleapi 502", &googleapi.Error{Code: 502, Message: "bad gateway"}, true},
		{"googleapi 503", &googleapi.Error{Code: 503, Message: "service unavailable"}, true},
		{"googleapi 504", &googleapi.Error{Code: 504, Message: "timeout"}, true},
		{"googleapi 404", &googleapi.Error{Code: 404, Message: "not found"}, false},
		{"googleapi 403", &googleapi.Error{Code: 403, Message: "forbidden"}, false},
		{"string temporarily unavailable", errors.New("server is temporarily unavailable"), true},
		{"string rate limit", errors.New("rate limit exceeded"), true},
		{"unrelated error", errors.New("invalid argument"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGCPTransient(tt.err)
			if got != tt.want {
				t.Errorf("isGCPTransient(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsAlreadyExists(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"already exists lowercase", errors.New("resource already exists"), true},
		{"already exists mixed case", errors.New("Resource Already Exists"), true},
		{"AlreadyExists gRPC style", errors.New("rpc error: code = AlreadyExists"), true},
		{"alreadyexist no space", errors.New("409: alreadyexist"), true},
		{"wrapped already exists", fmt.Errorf("create failed: %w", errors.New("instance already exists")), true},
		{"not found", errors.New("resource not found"), false},
		{"permission denied", errors.New("permission denied"), false},
		{"empty message", errors.New(""), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAlreadyExists(tt.err)
			if got != tt.want {
				t.Errorf("isAlreadyExists(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsGCPNotFoundConsistency(t *testing.T) {
	// Verify that isGCPNotFound catches all the patterns that raw
	// status.Code(err) == codes.NotFound would catch, plus more.
	// This validates Finding 1: replacing raw checks with the helper
	// doesn't change behavior for gRPC errors and adds coverage for
	// REST/string-based not-found errors.
	grpcNotFound := status.Error(codes.NotFound, "topic not found")
	if !isGCPNotFound(grpcNotFound) {
		t.Error("isGCPNotFound should detect gRPC NotFound (was previously raw status.Code check)")
	}

	// REST-based 404 that raw status.Code would miss
	restNotFound := &googleapi.Error{Code: 404, Message: "secret not found"}
	if !isGCPNotFound(restNotFound) {
		t.Error("isGCPNotFound should detect googleapi 404")
	}

	// String-based not-found that raw status.Code would miss
	stringNotFound := errors.New("the topic does not exist")
	if !isGCPNotFound(stringNotFound) {
		t.Error("isGCPNotFound should detect string-based 'does not exist'")
	}

	// Non-not-found errors should not match
	permDenied := status.Error(codes.PermissionDenied, "access denied")
	if isGCPNotFound(permDenied) {
		t.Error("isGCPNotFound should not match PermissionDenied")
	}
}

func TestWithGCPRetry(t *testing.T) {
	t.Run("succeeds on first attempt", func(t *testing.T) {
		calls := 0
		err := withGCPRetry(context.Background(), "test_op", func() error {
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
		err := withGCPRetry(context.Background(), "test_op", func() error {
			calls++
			if calls < 3 {
				return status.Error(codes.Unavailable, "temporarily unavailable")
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
		err := withGCPRetry(context.Background(), "test_op", func() error {
			calls++
			return status.Error(codes.NotFound, "not found")
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
		err := withGCPRetry(context.Background(), "test_op", func() error {
			calls++
			return status.Error(codes.Unavailable, "still unavailable")
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// 1 initial + 3 retries = 4 total
		if calls != 4 {
			t.Fatalf("expected 4 calls (1 + 3 retries), got %d", calls)
		}
	})

	t.Run("already-exists treated as success on retry", func(t *testing.T) {
		// Simulates Cloud SQL CREATE retry: first attempt gets transient error,
		// second attempt gets already-exists because the resource was created.
		calls := 0
		err := withGCPRetry(context.Background(), "test_create", func() error {
			calls++
			if calls == 1 {
				return status.Error(codes.Unavailable, "temporarily unavailable")
			}
			// On retry, the resource already exists — caller should treat as success
			alreadyExistsErr := errors.New("instance already exists")
			if isAlreadyExists(alreadyExistsErr) {
				return nil
			}
			return alreadyExistsErr
		})
		if err != nil {
			t.Fatalf("expected nil error (already-exists treated as success), got %v", err)
		}
		if calls != 2 {
			t.Fatalf("expected 2 calls, got %d", calls)
		}
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		// Cancel after first call
		err := withGCPRetry(ctx, "test_op", func() error {
			calls++
			cancel()
			return status.Error(codes.Unavailable, "unavailable")
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		// Should stop after 1 or 2 calls due to context cancellation
		if calls > 2 {
			t.Fatalf("expected at most 2 calls due to cancellation, got %d", calls)
		}
	})
}
