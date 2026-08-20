package api_clients_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/platform/api_clients"
)

func testConfig(baseURL string) *config.NexusInternalAPIConfig {
	return &config.NexusInternalAPIConfig{
		BaseURL:        baseURL,
		TimeoutMs:      3000,
		DefaultRPS:     1,
		DefaultKeyType: "DVS",
	}
}

func TestNexusInternalAPIClient_CreateAPIKey(t *testing.T) {
	t.Run("returns api key on 200", func(t *testing.T) {
		want := api_clients.CreateAPIKeyResponse{
			ID:                99,
			AccountID:         42,
			Key:               "test-key-uuid",
			Secret:            "test-secret-uuid",
			Active:            true,
			RequestsPerSecond: 1,
			Type:              "DVS",
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "/api-keys", r.URL.Path)
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var req api_clients.CreateAPIKeyRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Equal(t, 42, req.AccountID)
			assert.Equal(t, 1, req.RequestsPerSecond)
			assert.Equal(t, "DVS", req.Type)

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(want)
		}))
		defer srv.Close()

		client := api_clients.NewNexusInternalAPIClient(testConfig(srv.URL))
		got, err := client.CreateAPIKey(context.Background(), 42)

		require.NoError(t, err)
		assert.Equal(t, want.Key, got.Key)
		assert.Equal(t, want.Secret, got.Secret)
		assert.Equal(t, want.AccountID, got.AccountID)
		assert.Equal(t, want.ID, got.ID)
	})

	t.Run("error on non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		client := api_clients.NewNexusInternalAPIClient(testConfig(srv.URL))
		_, err := client.CreateAPIKey(context.Background(), 1)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "unexpected status 500")
	})

	t.Run("error on invalid json response", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte("not json"))
		}))
		defer srv.Close()

		client := api_clients.NewNexusInternalAPIClient(testConfig(srv.URL))
		_, err := client.CreateAPIKey(context.Background(), 1)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "decode response")
	})

	t.Run("error on unreachable server", func(t *testing.T) {
		client := api_clients.NewNexusInternalAPIClient(testConfig("http://127.0.0.1:1"))
		_, err := client.CreateAPIKey(context.Background(), 1)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "do request")
	})

	t.Run("respects context cancellation", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// block until client cancels
			<-r.Context().Done()
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		client := api_clients.NewNexusInternalAPIClient(testConfig(srv.URL))
		_, err := client.CreateAPIKey(ctx, 1)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "do request")
	})
}
