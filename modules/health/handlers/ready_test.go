package handlers

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/httperr"
	"github.com/mohamadhallal/zentax-api/logger"
)

type pingFunc func(ctx context.Context) error

func (f pingFunc) Ping(ctx context.Context) error { return f(ctx) }

func TestReady_DBOk(t *testing.T) {
	logger.InitBasic()

	h := NewReadyHandler(pingFunc(func(ctx context.Context) error {
		_, hasDeadline := ctx.Deadline()
		assert.True(t, hasDeadline, "ping must run under the readiness timeout")
		return nil
	}))
	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)

	resp, err := h.Execute(nil, r, nil, nil)
	require.NoError(t, err)
	assert.Equal(t, 200, resp.Status)
	assert.Equal(t, &ReadyResponse{DB: "ok"}, resp.Data)
}

func TestReady_DBDown_503(t *testing.T) {
	logger.InitBasic()

	h := NewReadyHandler(pingFunc(func(context.Context) error { return errors.New("connection refused") }))
	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)

	resp, err := h.Execute(nil, r, nil, nil)
	require.Error(t, err)
	assert.Nil(t, resp)
	appErr := httperr.Classify(err)
	assert.Equal(t, 503, appErr.Status)
	assert.Equal(t, httperr.ErrUnavailable, appErr.Code)
	assert.NotContains(t, appErr.Message, "connection refused", "the driver error stays in the log")
}

func TestReady_DBHangs_TimesOut(t *testing.T) {
	logger.InitBasic()

	h := NewReadyHandler(pingFunc(func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}))
	h.timeout = 20 * time.Millisecond
	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)

	start := time.Now()
	_, err := h.Execute(nil, r, nil, nil)
	require.Error(t, err)
	assert.Less(t, time.Since(start), 2*time.Second)
	assert.Equal(t, 503, httperr.Classify(err).Status)
}

func TestReady_NoDB_503(t *testing.T) {
	logger.InitBasic()

	h := NewReadyHandler(nil)
	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/health/ready", http.NoBody)
	_, err := h.Execute(nil, r, nil, nil)
	require.Error(t, err)
	assert.Equal(t, 503, httperr.Classify(err).Status)
}

func TestReady_Route(t *testing.T) {
	def := NewReadyHandler(nil).DefineRoute()
	assert.Equal(t, http.MethodGet, def.Method)
	assert.Equal(t, "/ready", def.Path)
	assert.False(t, def.Auth)
	assert.False(t, def.Tenant)
	assert.False(t, def.Tx)
}
