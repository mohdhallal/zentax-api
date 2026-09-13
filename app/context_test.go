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

// NormalizeRequestID is the shape gate on the one value in the request envelope
// a caller supplies. Everything it lets through is written into an append-only,
// hash-covered, WORM-exported table, so the accepted set is exactly the
// canonical UUID and the returned form is always lowercase.
func TestNormalizeRequestID(t *testing.T) {
	t.Parallel()

	const canonical = "0f8fad5b-d9cb-469f-a165-70867728950e"

	accepted := []struct{ in, want string }{
		{canonical, canonical},
		{"0F8FAD5B-D9CB-469F-A165-70867728950E", canonical},
		{"0f8FAD5b-d9cb-469F-a165-70867728950e", canonical},
		{"00000000-0000-0000-0000-000000000000", "00000000-0000-0000-0000-000000000000"},
	}
	for _, tc := range accepted {
		got, ok := NormalizeRequestID(tc.in)
		require.True(t, ok, "%q must be accepted", tc.in)
		assert.Equal(t, tc.want, got)
	}

	minted := NewRequestID()
	got, ok := NormalizeRequestID(minted)
	require.True(t, ok, "a minted id must satisfy the gate that admits one")
	assert.Equal(t, minted, got)

	refused := []string{
		"",
		"trace-abc",
		"my-custom-request-id-123",
		"'; DROP TABLE audit_log; --",
		"jane.doe@example.com",
		"0f8fad5b-d9cb-469f-a165-70867728950",  // one short
		"0f8fad5bd9cb469fa16570867728950e",     // unhyphenated
		"0f8fad5b-d9cb-469f-a165-7086772895ZZ", // not hex
		"0f8fad5b_d9cb_469f_a165_70867728950e", // wrong separators
		" 0f8fad5b-d9cb-469f-a165-70867728950e",
		"urn:uuid:0f8fad5b-d9cb-469f-a165-70867728950e",
		"{0f8fad5b-d9cb-469f-a165-70867728950e}",
	}
	for _, in := range refused {
		got, ok := NormalizeRequestID(in)
		assert.False(t, ok, "%q must be refused", in)
		assert.Empty(t, got, "a refused id must yield nothing to store")
	}
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
