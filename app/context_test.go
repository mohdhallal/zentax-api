package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- RequestId ---

func TestWithRequestId_GetRequestId_RoundTrip(t *testing.T) {
	t.Parallel()

	ctx := WithRequestId(context.Background(), "req-123")
	assert.Equal(t, "req-123", GetRequestId(ctx))
}

func TestGetRequestId_MissingKey_ReturnsEmpty(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "", GetRequestId(context.Background()))
}

func TestWithRequestId_OverwritesPreviousValue(t *testing.T) {
	t.Parallel()

	ctx := WithRequestId(context.Background(), "first")
	ctx = WithRequestId(ctx, "second")
	assert.Equal(t, "second", GetRequestId(ctx))
}

// --- Requester ---

func TestWithRequester_GetRequester_RoundTrip(t *testing.T) {
	t.Parallel()

	req := &Requester{Kind: RequesterUser, ID: "user-1", AccountID: 42}
	ctx := WithRequester(context.Background(), req)
	result := GetRequester(ctx)

	require.NotNil(t, result)
	assert.Equal(t, req, result)
}

func TestGetRequester_MissingKey_ReturnsNil(t *testing.T) {
	t.Parallel()

	assert.Nil(t, GetRequester(context.Background()))
}

func TestWithRequester_ServiceRequester(t *testing.T) {
	t.Parallel()

	req := &Requester{Kind: RequesterService, ID: "payments-svc", ServiceName: "payments-svc"}
	ctx := WithRequester(context.Background(), req)
	result := GetRequester(ctx)

	require.NotNil(t, result)
	assert.True(t, result.IsService())
	assert.False(t, result.IsUser())
	assert.Equal(t, "payments-svc", result.ServiceName)
}

func TestWithRequester_UserRequester(t *testing.T) {
	t.Parallel()

	req := &Requester{
		Kind:          RequesterUser,
		ID:            "99",
		AccountID:     99,
		APIKeyID:      7,
		CustomerID:    55,
		CorrelationID: "corr-abc",
		ClientIP:      "10.0.0.1",
		APIKey:        "k",
		APISecret:     "s",
	}
	ctx := WithRequester(context.Background(), req)
	result := GetRequester(ctx)

	require.NotNil(t, result)
	assert.True(t, result.IsUser())
	assert.False(t, result.IsService())
	assert.Equal(t, 99, result.AccountID)
	assert.Equal(t, 7, result.APIKeyID)
	assert.Equal(t, 55, result.CustomerID)
	assert.Equal(t, "corr-abc", result.CorrelationID)
	assert.Equal(t, "10.0.0.1", result.ClientIP)
}

// --- Requester methods ---

func TestRequester_IsUser_True(t *testing.T) {
	t.Parallel()

	r := &Requester{Kind: RequesterUser}
	assert.True(t, r.IsUser())
	assert.False(t, r.IsService())
}

func TestRequester_IsService_True(t *testing.T) {
	t.Parallel()

	r := &Requester{Kind: RequesterService}
	assert.True(t, r.IsService())
	assert.False(t, r.IsUser())
}
