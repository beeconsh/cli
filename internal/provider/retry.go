package provider

import (
	"context"
	"math"
	"math/rand/v2"
	"strings"
	"time"

	"github.com/terracotta-ai/beecon/internal/logging"
)

const (
	defaultMaxAttempts = 3
	baseDelay          = 500 * time.Millisecond
	maxDelay           = 10 * time.Second
)

// withRetry executes fn with exponential backoff and jitter on retryable errors.
func withRetry[T any](ctx context.Context, name string, maxAttempts int, fn func() (T, error)) (T, error) {
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAttempts
	}
	var lastErr error
	var zero T
	for attempt := 0; attempt < maxAttempts; attempt++ {
		result, err := fn()
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return zero, err
		}
		if attempt < maxAttempts-1 {
			delay := backoffDelay(attempt)
			logging.Logger.Debug("retry", "operation", name, "attempt", attempt+1, "delay", delay, "error", err)
			select {
			case <-ctx.Done():
				return zero, ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return zero, lastErr
}

// isRetryable returns true for transient/throttling errors that are safe to retry.
func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// AWS throttling
	for _, pattern := range []string{
		"throttling",
		"throttlingexception",
		"toomanyrequestsexception",
		"too many requests",
		"rate exceeded",
		"request limit exceeded",
		"requestlimitexceeded",
		// Transient server errors
		"internal server error",
		"service unavailable",
		"bad gateway",
		"gateway timeout",
		"503",
		"502",
		"504",
	} {
		if strings.Contains(msg, pattern) {
			return true
		}
	}
	return false
}

// backoffDelay calculates exponential backoff with jitter.
func backoffDelay(attempt int) time.Duration {
	base := float64(baseDelay) * math.Pow(2, float64(attempt))
	if base > float64(maxDelay) {
		base = float64(maxDelay)
	}
	// Add jitter: 50-100% of base delay
	jitter := base * (0.5 + rand.Float64()*0.5)
	return time.Duration(jitter)
}
