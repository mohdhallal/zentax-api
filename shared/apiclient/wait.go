package apiclient

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const (
	// WaitReadyAttempts and WaitReadyInterval bound WaitReady: ~6 s, enough for
	// a compose stack whose API container is still opening its pool, short
	// enough that a genuinely dead server fails fast.
	WaitReadyAttempts = 30
	WaitReadyInterval = 200 * time.Millisecond
)

// WaitReady blocks until GET /health/ready answers 200, or gives up after
// WaitReadyAttempts tries WaitReadyInterval apart.
//
// This is the only place the client retries a request the server ANSWERED.
// Every other method performs exactly one request: a seeder that silently
// repeats a POST would create duplicate rows, and a verifier that repeats a
// GET would hide a flapping API. The single exception is a 429, which the
// rate limiter returns without running the handler at all — roundTrip waits
// out its Retry-After and re-sends, and says why there.
func (c *Client) WaitReady(ctx context.Context) error {
	return c.WaitReadyFor(ctx, WaitReadyAttempts, WaitReadyInterval)
}

// WaitReadyFor is WaitReady with an explicit budget (attempts < 1 means one try).
func (c *Client) WaitReadyFor(ctx context.Context, attempts int, interval time.Duration) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("apiclient: wait for %s to be ready: %w", c.baseURL, err)
		}
		raw, err := c.roundTrip(ctx, http.MethodGet, "/health/ready", nil, "")
		switch {
		case err != nil:
			// Connection refused / reset / DNS while the server boots.
			lastErr = err
		case raw.statusCode == http.StatusOK:
			return nil
		case isTransientStatus(raw.statusCode):
			// 502/503/504: a proxy in front of a server that is not up yet, or
			// the readiness probe reporting the database is not answering.
			lastErr = newAPIError(http.MethodGet, "/health/ready", raw)
		default:
			// Anything else is a real answer from a live server: stop.
			return newAPIError(http.MethodGet, "/health/ready", raw)
		}
		if attempt == attempts {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("apiclient: wait for %s to be ready: %w", c.baseURL, ctx.Err())
		case <-time.After(interval):
		}
	}
	return fmt.Errorf("apiclient: %s not ready after %d attempts over %s: %w",
		c.baseURL, attempts, time.Duration(attempts)*interval, lastErr)
}

func isTransientStatus(status int) bool {
	switch status {
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	}
	return false
}
