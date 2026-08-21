package config

import (
	"encoding/base64"
	"fmt"

	"github.com/mohamadhallal/zentax-api/logger"
)

type Config struct {
	EnvName          string                 `json:"-"`
	App              AppConfig              `json:"app"`
	Server           ServerConfig           `json:"server"`
	Database         DatabaseConfig         `json:"database"`
	CORS             CORSConfig             `json:"cors"`
	Swagger          SwaggerConfig          `json:"swagger"`
	Metrics          MetricsConfig          `json:"metrics"`
	Log              *logger.Config         `json:"log"`
	NexusInternalAPI NexusInternalAPIConfig `json:"nexusInternalApi"`
	Auth             AuthConfig             `json:"auth"`
}

func (c *Config) IsDevelopment() bool {
	return c.App.Env == "development"
}

type AppConfig struct {
	Env          string `json:"env"`
	Port         int    `json:"port"`
	InternalPort int    `json:"internalPort"`
}

type ServerConfig struct {
	ReadHeaderTimeoutMs int `json:"readHeaderTimeoutMs"`
	WriteTimeoutMs      int `json:"writeTimeoutMs"`
	IdleTimeoutMs       int `json:"idleTimeoutMs"`
	ShutdownTimeoutMs   int `json:"shutdownTimeoutMs"`
}

type DatabaseConfig struct {
	URL                 string `json:"url"`
	PoolMax             int    `json:"poolMax"`
	IdleTimeoutMs       int    `json:"idleTimeoutMs"`
	ConnectionTimeoutMs int    `json:"connectionTimeoutMs"`
	ConnMaxLifetimeMs   int    `json:"connMaxLifetimeMs"`
}

type CORSConfig struct {
	AllowedOrigins   []string `json:"allowedOrigins"`
	AllowedMethods   []string `json:"allowedMethods"`
	AllowedHeaders   []string `json:"allowedHeaders"`
	ExposedHeaders   []string `json:"exposedHeaders"`
	AllowCredentials bool     `json:"allowCredentials"`
	MaxAgeSec        int      `json:"maxAgeSec"`
}

type SwaggerConfig struct {
	Title   string `json:"title"`
	Version string `json:"version"`
}

type MetricsConfig struct {
	Enabled   bool   `json:"enabled"`
	Namespace string `json:"namespace"`
}

type NexusInternalAPIConfig struct {
	BaseURL        string `json:"baseUrl"`
	TimeoutMs      int    `json:"timeoutMs"`
	DefaultRPS     int    `json:"defaultRps"`
	DefaultKeyType string `json:"defaultKeyType"`
}

// AuthConfig configures first-party auth (ADR-0011).
type AuthConfig struct {
	// EncryptionKey is a base64 (std) encoding of a 32-byte AES-256 key used for
	// field-level secret encryption (TOTP seeds). Interim until ADR-0006 per-tenant
	// KMS keys; required (and validated) outside development.
	EncryptionKey string `json:"encryptionKey"`

	SessionCookieName       string `json:"sessionCookieName"`
	SessionCookieSecure     bool   `json:"sessionCookieSecure"`
	SessionIdleTTLMinutes   int    `json:"sessionIdleTtlMinutes"`
	SessionAbsoluteTTLHours int    `json:"sessionAbsoluteTtlHours"`
}

// DecodeEncryptionKey returns the raw 32-byte AES key, or an error if it is
// missing or the wrong length.
func (a AuthConfig) DecodeEncryptionKey() ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(a.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("auth.encryptionKey is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("auth.encryptionKey must decode to 32 bytes, got %d", len(key))
	}
	return key, nil
}
