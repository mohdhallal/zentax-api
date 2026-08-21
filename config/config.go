package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

const (
	EnvVarName     = "APP_ENV"
	EnvDatabaseURL = "DATABASE_URL"

	DefaultEnv = "development"

	configDir = "deployment/config_files"
)

var cfg *Config

// Load reads deployment/config_files/{APP_ENV}.json, applies env overrides, and
// fail-closes (ADR-0014): a missing security-critical value is a startup error,
// never a silent default.
func Load() (*Config, error) {
	conf := &Config{}
	conf.EnvName = getEnvVal(EnvVarName, DefaultEnv)

	path := filepath.Join(configDir, conf.EnvName+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}
	if err := json.Unmarshal(data, conf); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}

	mergeEnvOverrides(conf)

	if err := conf.validate(); err != nil {
		return nil, fmt.Errorf("invalid config (%s): %w", conf.EnvName, err)
	}

	cfg = conf
	return conf, nil
}

// validate fails closed on missing security-critical configuration (ADR-0014).
func (c *Config) validate() error {
	if c.Database.URL == "" {
		return fmt.Errorf("database.url is required (set %s or the config file)", EnvDatabaseURL)
	}
	if c.App.Port == 0 {
		return fmt.Errorf("app.port is required")
	}
	// Fail closed on missing/invalid auth secrets in deployed environments
	// (ADR-0014). Development and testing may omit them.
	if c.App.Env == "production" || c.App.Env == "staging" {
		if _, err := c.Auth.DecodeEncryptionKey(); err != nil {
			return fmt.Errorf("invalid auth config: %w", err)
		}
	}
	return nil
}

func mergeEnvOverrides(conf *Config) {
	if val := os.Getenv(EnvDatabaseURL); val != "" {
		conf.Database.URL = val
	}
}

func GetConfig() *Config {
	if cfg == nil {
		panic("Config not loaded. Call Load() first.")
	}
	return cfg
}

func IsDevelopment() bool {
	if cfg == nil {
		return true
	}
	return cfg.IsDevelopment()
}

func getEnvVal(env, defaultVal string) string {
	val, ok := os.LookupEnv(env)
	if !ok {
		return defaultVal
	}
	return val
}
