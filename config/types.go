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
	Storage          StorageConfig          `json:"storage"`
}

// Storage drivers (ADR-0022): the filesystem adapter (self-host default, tests)
// and S3 / S3-compatible object storage (SaaS, MinIO).
const (
	StorageDriverFS = "fs"
	StorageDriverS3 = "s3"

	// DefaultMaxUploadBytes caps a single document upload (25 MiB).
	DefaultMaxUploadBytes int64 = 25 * 1024 * 1024
	defaultStorageFSRoot        = "./var/documents"
)

// StorageConfig selects and configures the document blob store (ADR-0009
// adapter #1 / ADR-0022). Validated fail-closed at startup: the driver must be
// fs or s3, fs needs a root directory, s3 needs a bucket + region.
type StorageConfig struct {
	Driver         string          `json:"driver"`         // fs | s3
	MaxUploadBytes int64           `json:"maxUploadBytes"` // per-file cap; 0 → DefaultMaxUploadBytes
	FS             StorageFSConfig `json:"fs"`
	S3             StorageS3Config `json:"s3"`
}

type StorageFSConfig struct {
	Root string `json:"root"`
}

type StorageS3Config struct {
	Bucket         string `json:"bucket"`
	Region         string `json:"region"`
	Endpoint       string `json:"endpoint"`       // optional: MinIO / S3-compatible
	ForcePathStyle bool   `json:"forcePathStyle"` // required by most S3-compatible stores
}

// applyDefaults fills what an omitted `storage` section leaves empty: a config
// file that says nothing gets the self-host filesystem default (the shipped
// config files spell it out explicitly). An explicitly wrong driver is still a
// startup error (validate).
func (s *StorageConfig) applyDefaults() {
	if s.Driver == "" {
		s.Driver = StorageDriverFS
	}
	if s.MaxUploadBytes <= 0 {
		s.MaxUploadBytes = DefaultMaxUploadBytes
	}
	if s.Driver == StorageDriverFS && s.FS.Root == "" {
		s.FS.Root = defaultStorageFSRoot
	}
}

func (s StorageConfig) validate() error {
	switch s.Driver {
	case StorageDriverFS:
		if s.FS.Root == "" {
			return fmt.Errorf("storage.fs.root is required for the fs driver (or set %s)", EnvStorageFSRoot)
		}
	case StorageDriverS3:
		if s.S3.Bucket == "" {
			return fmt.Errorf("storage.s3.bucket is required for the s3 driver (or set %s)", EnvStorageS3Bucket)
		}
		if s.S3.Region == "" {
			return fmt.Errorf("storage.s3.region is required for the s3 driver (or set %s)", EnvStorageS3Region)
		}
	default:
		return fmt.Errorf("storage.driver must be %q or %q, got %q", StorageDriverFS, StorageDriverS3, s.Driver)
	}
	if s.MaxUploadBytes <= 0 {
		return fmt.Errorf("storage.maxUploadBytes must be positive")
	}
	return nil
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
