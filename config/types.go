package config

import (
	"crypto/sha256"
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

// Environment returns the parsed environment — the tier that decides validation
// plus the cell label — derived from app.env, which Load pins to APP_ENV. A
// value that is not a known environment yields the zero Environment (no tier),
// which IsDeployed treats as deployed: unknown fails closed.
func (c *Config) Environment() Environment {
	env, err := ParseEnvironment(c.App.Env)
	if err != nil {
		return Environment{}
	}
	return env
}

// Tier is the environment's security class — staging for both "staging" and the
// cell "staging-eu". Everything that must behave differently per environment
// keys off this, never off the environment name.
func (c *Config) Tier() Tier {
	return c.Environment().Tier
}

func (c *Config) IsDevelopment() bool {
	return c.Tier() == TierDevelopment
}

// IsDeployed reports whether this environment's tier is one the fail-closed
// rules of ADR-0014 apply to — every tier but development, including every cell
// of one (staging-eu, production-eu) and any environment name that does not
// parse at all.
func (c *Config) IsDeployed() bool {
	return c.Tier().IsDeployed()
}

// The tier names, as plain strings, for callers that work in strings (config
// file names, JSON, tests). They track the Tier constants by construction.
const (
	EnvDevelopment = string(TierDevelopment)
	EnvStaging     = string(TierStaging)
	EnvProduction  = string(TierProduction)
)

type AppConfig struct {
	Env          string `json:"env"`
	Port         int    `json:"port"`
	InternalPort int    `json:"internalPort"`

	// PublicBaseURL is the origin users reach the product at — the web tier's
	// public hostname (https://eu.staging.zentax.software), NOT this API's own
	// listener, which sits behind the web tier's /api proxy. It is the base for
	// every absolute link the API hands out of band (an e-mailed invite link is
	// the first planned consumer). Optional; when set it must be an absolute
	// http(s) URL (https in deployed environments), stored without a trailing
	// slash. The SaaS task sets it via PUBLIC_BASE_URL from the cell's
	// publicHostname; empty means "not configured".
	PublicBaseURL string `json:"publicBaseUrl"`
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

// SwaggerConfig controls the OpenAPI document + UI at /swagger. Enabled is an
// explicit opt-in (development + staging ship it, production does not).
type SwaggerConfig struct {
	Enabled bool   `json:"enabled"`
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
	// EncryptionKey is the AES-256 key used for field-level secret encryption
	// (TOTP seeds), in one of two forms — see DecodeEncryptionKey. Interim until
	// ADR-0006 per-tenant KMS keys; required (and validated) outside development.
	EncryptionKey string `json:"encryptionKey"`

	SessionCookieName       string `json:"sessionCookieName"`
	SessionCookieSecure     bool   `json:"sessionCookieSecure"`
	SessionIdleTTLMinutes   int    `json:"sessionIdleTtlMinutes"`
	SessionAbsoluteTTLHours int    `json:"sessionAbsoluteTtlHours"`
}

// EncryptionKeyLen is the AES-256 key size in bytes.
const EncryptionKeyLen = 32

// MinEncryptionPassphraseLen is the shortest passphrase DecodeEncryptionKey
// derives a key from. 32 characters of a generated alphanumeric secret is
// ~190 bits of entropy — comfortably above the 128-bit floor.
const MinEncryptionPassphraseLen = 32

// DecodeEncryptionKey returns the raw 32-byte AES key. Two forms are accepted:
//
//  1. base64 (std) of exactly 32 raw key bytes — the original rule, what
//     `openssl rand -base64 32` produces;
//  2. otherwise, a passphrase of at least MinEncryptionPassphraseLen characters,
//     derived into the key with SHA-256.
//
// Form 2 exists because managed secret stores (AWS Secrets Manager's generated
// secrets, most password managers) hand out alphanumeric strings, not raw key
// bytes, and operators paste those straight into AUTH_ENCRYPTION_KEY. The
// derivation is deterministic, so rotation is simply a new passphrase (the
// old ciphertexts need the old passphrase — a re-encrypt job, as with any key
// rotation). A short or empty value is an error either way.
func (a AuthConfig) DecodeEncryptionKey() ([]byte, error) {
	if a.EncryptionKey == "" {
		return nil, fmt.Errorf("auth.encryptionKey is empty")
	}
	if key, err := base64.StdEncoding.DecodeString(a.EncryptionKey); err == nil && len(key) == EncryptionKeyLen {
		return key, nil
	}
	if len(a.EncryptionKey) < MinEncryptionPassphraseLen {
		return nil, fmt.Errorf(
			"auth.encryptionKey must be base64 of exactly %d bytes or a passphrase of at least %d characters (got %d characters)",
			EncryptionKeyLen, MinEncryptionPassphraseLen, len(a.EncryptionKey))
	}
	sum := sha256.Sum256([]byte(a.EncryptionKey))
	return sum[:], nil
}
