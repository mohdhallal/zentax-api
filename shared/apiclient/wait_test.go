package apiclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWaitReady_RetriesUntilHealthy(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/health/ready", r.URL.Path)
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{}})
	}))
	defer srv.Close()

	client := New(srv.URL)
	require.NoError(t, client.WaitReadyFor(t.Context(), 10, time.Millisecond))
	assert.Equal(t, int32(3), calls.Load())
}

func TestWaitReady_GivesUpAfterTheBudget(t *testing.T) {
	t.Parallel()
	// Port 1 refuses connections: the transport-error path.
	client := New("http://127.0.0.1:1")
	err := client.WaitReadyFor(t.Context(), 3, time.Millisecond)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not ready after 3 attempts")
	assert.Contains(t, err.Error(), "/health/ready")
}

func TestWaitReady_StopsOnANonTransientAnswer(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeEnvelope(t, w, http.StatusNotFound, map[string]any{
			"status": false,
			"error":  map[string]any{"code": "NOT_FOUND", "message": "no such route"},
		})
	}))
	defer srv.Close()

	err := New(srv.URL).WaitReadyFor(t.Context(), 10, time.Millisecond)
	require.Error(t, err)
	assert.True(t, IsNotFound(err))
	assert.Equal(t, int32(1), calls.Load(), "a live server answering 404 is not worth retrying")
}

func TestWaitReady_HonoursContextCancellation(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	err := New(srv.URL).WaitReadyFor(ctx, 1000, 5*time.Millisecond)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestWaitReady_DefaultBudget(t *testing.T) {
	t.Parallel()
	// The exported budget is what callers rely on when they size a start-up wait.
	assert.Equal(t, 30, WaitReadyAttempts)
	assert.Equal(t, 200*time.Millisecond, WaitReadyInterval)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		writeEnvelope(t, w, http.StatusOK, map[string]any{"status": true, "data": map[string]any{}})
	}))
	defer srv.Close()
	require.NoError(t, New(srv.URL).WaitReady(t.Context()))
	assert.Equal(t, int32(1), calls.Load(), "a ready server is not polled twice")
}
