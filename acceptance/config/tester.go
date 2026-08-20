package config

import "github.com/mohamadhallal/zentax-api/config"

// DefaultConfig returns a base config for acceptance tests.
// Database.URL is overridden per suite from the Docker Compose PostgreSQL service.
func DefaultConfig() *config.Config {
	return &config.Config{
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
	}
}
