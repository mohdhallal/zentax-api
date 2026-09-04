package config

import (
	"os"

	"github.com/mohamadhallal/zentax-api/config"
)

// DefaultConfig returns a base config for acceptance tests.
// Database.URL is overridden per suite from the Docker Compose PostgreSQL service.
// Storage is the filesystem adapter rooted at a fresh temp directory (under
// os.TempDir()); the suite removes it in TearDownTest.
func DefaultConfig() *config.Config {
	root, err := os.MkdirTemp(os.TempDir(), "zentax-acceptance-documents-*")
	if err != nil {
		panic("acceptance: create storage temp dir: " + err.Error())
	}
	return &config.Config{
		Storage: config.StorageConfig{
			Driver:         config.StorageDriverFS,
			MaxUploadBytes: 2 << 20, // 2 MiB keeps the oversize test cheap (prod default: 25 MiB)
			FS:             config.StorageFSConfig{Root: root},
		},
		EnvName: "test",
		App: config.AppConfig{
			Env:          "test",
			Port:         0,
			InternalPort: 0,
		},
		Database: config.DatabaseConfig{
			URL:                 "", // set at suite startup from TEST_DATABASE_URL or the Docker Compose default
			PoolMax:             5,
			IdleTimeoutMs:       30000,
			ConnectionTimeoutMs: 5000,
			ConnMaxLifetimeMs:   300000,
		},
		CORS: config.CORSConfig{
			AllowedOrigins: []string{"*"},
			AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
			AllowedHeaders: []string{"*"},
		},
		Swagger: config.SwaggerConfig{
			Title:   "Test",
			Version: "test",
		},
		Metrics: config.MetricsConfig{
			Enabled: false,
		},
		Auth: config.AuthConfig{
			EncryptionKey:           "emVudGF4LWRldi1lbmNyeXB0aW9uLWtleS0zMmJ5dGU=", // dev/test key
			SessionCookieName:       "zentax_session",
			SessionCookieSecure:     false,
			SessionIdleTTLMinutes:   480,
			SessionAbsoluteTTLHours: 720,
		},
	}
}
