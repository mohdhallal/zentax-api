package api_clients

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/config"
)

type CreateAPIKeyRequest struct {
	AccountID         int    `json:"account_id"`
	RequestsPerSecond int    `json:"requests_per_second"`
	Type              string `json:"type"`
}

type CreateAPIKeyResponse struct {
	AccountID         int    `json:"account_id"`
	Active            bool   `json:"active"`
	ID                int    `json:"id"`
	Key               string `json:"key"`
	RequestsPerSecond int    `json:"requests_per_second"`
	Secret            string `json:"secret"`
	Type              string `json:"type"`
}

// NexusInternalAPI provisions and manages API keys via the nexus internal service.
type NexusInternalAPI interface {
	CreateAPIKey(ctx context.Context, nexusAccountID int) (*CreateAPIKeyResponse, error)
}

type nexusInternalAPIClient struct {
	baseURL    string
	httpClient *http.Client
	cfg        *config.NexusInternalAPIConfig
}

func NewNexusInternalAPIClient(cfg *config.NexusInternalAPIConfig) NexusInternalAPI {
	return &nexusInternalAPIClient{
		baseURL: cfg.BaseURL,
		cfg:     cfg,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.TimeoutMs) * time.Millisecond,
		},
	}
}

func (c *nexusInternalAPIClient) CreateAPIKey(ctx context.Context, nexusAccountID int) (*CreateAPIKeyResponse, error) {
	payload := CreateAPIKeyRequest{
		AccountID:         nexusAccountID,
		RequestsPerSecond: c.cfg.DefaultRPS,
		Type:              c.cfg.DefaultKeyType,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("nexus api: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api-keys", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("nexus api: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nexus api: do request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nexus api: unexpected status %d", resp.StatusCode)
	}

	var result CreateAPIKeyResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("nexus api: decode response: %w", err)
	}

	return &result, nil
}

// NewMockNexusInternalAPIClient returns a NexusInternalAPI that returns sample
// data without making a real HTTP call. Use in cmd tooling and manual testing.
type mockNexusInternalAPIClient struct {
	cfg *config.NexusInternalAPIConfig
}

func NewMockNexusInternalAPIClient(cfg *config.NexusInternalAPIConfig) NexusInternalAPI {
	return &mockNexusInternalAPIClient{cfg: cfg}
}

func (m *mockNexusInternalAPIClient) CreateAPIKey(_ context.Context, nexusAccountID int) (*CreateAPIKeyResponse, error) {
	return &CreateAPIKeyResponse{
		ID:                1,
		AccountID:         nexusAccountID,
		Key:               uuid.New().String(),
		Secret:            uuid.New().String(),
		Active:            true,
		RequestsPerSecond: m.cfg.DefaultRPS,
		Type:              m.cfg.DefaultKeyType,
	}, nil
}
