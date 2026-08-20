package handlers

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mohamadhallal/zentax-api/delivery/httpkit/types"
)

func TestHealthCheck_External(t *testing.T) {
	t.Parallel()

	handler := NewCheckHandler(types.ModeExternal)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/health", http.NoBody)

	resp, err := handler.Execute(nil, r, nil, nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, resp.Status)
	data, _ := resp.Data.(*types.HealthCheckResponse)
	assert.Equal(t, 200, data.Status)
	assert.Equal(t, types.ModeExternal, data.Mode)
	assert.False(t, data.Timestamp.IsZero())
}

func TestHealthCheck_Internal(t *testing.T) {
	t.Parallel()

	handler := NewCheckHandler(types.ModeInternal)

	r, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "/health", http.NoBody)

	resp, err := handler.Execute(nil, r, nil, nil)

	assert.NoError(t, err)
	assert.Equal(t, 200, resp.Status)
	data, _ := resp.Data.(*types.HealthCheckResponse)
	assert.Equal(t, types.ModeInternal, data.Mode)
}
