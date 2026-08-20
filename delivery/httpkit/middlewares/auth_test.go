package middlewares_test

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	"github.com/mohamadhallal/zentax-api/delivery/httpkit/middlewares"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/modules/auth/domain"
)

func TestMain(m *testing.M) {
	logger.InitBasic()
	os.Exit(m.Run())
}

func basicAuthHeader(key, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(key+":"+secret))
}

func captureRequester(t *testing.T) (next http.Handler, getRequester func() *app.Requester) {
	t.Helper()
	var captured *app.Requester
	next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = app.GetRequester(r.Context())
		w.WriteHeader(http.StatusOK)
	})
	return next, func() *app.Requester { return captured }
}

// --- RequireExternalAuth ---

func TestRequireExternalAuth_ValidHeaders(t *testing.T) {
	next, getRequester := captureRequester(t)
	mw := middlewares.RequireExternalAuth()(next)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Account-Id", "42")
	req.Header.Set("X-API-Key-Id", "7")
	req.Header.Set("X-Customer-Id", "99")
	req.Header.Set("X-Correlation-Id", "corr-abc")
	req.Header.Set("X-Client-IP", "1.2.3.4")
	req.Header.Set("Authorization", basicAuthHeader("mykey", "mysecret"))

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	r := getRequester()
	require.NotNil(t, r)
	assert.Equal(t, app.RequesterUser, r.Kind)
	assert.Equal(t, "42", r.ID)
	assert.Equal(t, 42, r.AccountID)
	assert.Equal(t, 7, r.APIKeyID)
	assert.Equal(t, 99, r.CustomerID)
	assert.Equal(t, "corr-abc", r.CorrelationID)
	assert.Equal(t, "1.2.3.4", r.ClientIP)
	assert.Equal(t, "mykey", r.APIKey)
	assert.Equal(t, "mysecret", r.APISecret)
}

func TestRequireExternalAuth_MissingAccountId(t *testing.T) {
	next, _ := captureRequester(t)
	mw := middlewares.RequireExternalAuth()(next)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireExternalAuth_ZeroAccountId(t *testing.T) {
	next, _ := captureRequester(t)
	mw := middlewares.RequireExternalAuth()(next)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Account-Id", "0")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireExternalAuth_NonNumericAccountId(t *testing.T) {
	next, _ := captureRequester(t)
	mw := middlewares.RequireExternalAuth()(next)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Account-Id", "not-a-number")
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// --- RequireInternalAuth ---

func TestRequireInternalAuth_ValidCredentials(t *testing.T) {
	next, getRequester := captureRequester(t)
	validator := domain.NewValidatorMock(t)
	validator.EXPECT().
		Validate(mock.Anything, "mykey", "mysecret").
		Return(&domain.AuthResult{ServiceName: "payments-svc"}, nil)

	mw := middlewares.RequireInternalAuth(validator)(next)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", basicAuthHeader("mykey", "mysecret"))

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	r := getRequester()
	require.NotNil(t, r)
	assert.Equal(t, app.RequesterService, r.Kind)
	assert.Equal(t, "payments-svc", r.ID)
	assert.Equal(t, "payments-svc", r.ServiceName)
}

func TestRequireInternalAuth_MissingCredentials(t *testing.T) {
	next, _ := captureRequester(t)
	validator := domain.NewValidatorMock(t)
	mw := middlewares.RequireInternalAuth(validator)(next)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestRequireInternalAuth_InvalidCredentials(t *testing.T) {
	next, _ := captureRequester(t)
	validator := domain.NewValidatorMock(t)
	validator.EXPECT().
		Validate(mock.Anything, "badkey", "badsecret").
		Return(nil, errors.New("invalid credentials"))

	mw := middlewares.RequireInternalAuth(validator)(next)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/", http.NoBody)
	req.Header.Set("Authorization", basicAuthHeader("badkey", "badsecret"))

	rr := httptest.NewRecorder()
	mw.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
