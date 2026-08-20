package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Config.IsDevelopment ---

func TestConfig_IsDevelopment_True(t *testing.T) {
	t.Parallel()

	c := &Config{App: AppConfig{Env: "development"}}
	assert.True(t, c.IsDevelopment())
}

func TestConfig_IsDevelopment_False(t *testing.T) {
	t.Parallel()

	for _, env := range []string{"staging", "production", ""} {
		c := &Config{App: AppConfig{Env: env}}
		assert.False(t, c.IsDevelopment(), "env=%q should not be development", env)
	}
}

// --- mergeEnvOverrides ---

func TestMergeEnvOverrides_DatabaseURL_Override(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "postgres://user:pass@testhost:5432/testdb")

	c := &Config{}
	mergeEnvOverrides(c)

	assert.Equal(t, "postgres://user:pass@testhost:5432/testdb", c.Database.URL)
}

func TestMergeEnvOverrides_NoDatabaseURL_NoChange(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "")

	c := &Config{Database: DatabaseConfig{URL: "original-url"}}
	mergeEnvOverrides(c)

	assert.Equal(t, "original-url", c.Database.URL)
}

func TestMergeEnvOverrides_UnsetDatabaseURL_NoChange(t *testing.T) {
	// Do NOT set the env var — LookupEnv returns ("", false).
	c := &Config{Database: DatabaseConfig{URL: "keep-me"}}
	mergeEnvOverrides(c)

	assert.Equal(t, "keep-me", c.Database.URL)
}

// --- getEnvVal ---

func TestGetEnvVal_SetVar_ReturnsValue(t *testing.T) {
	t.Setenv("TEST_ENV_VAL", "hello")

	result := getEnvVal("TEST_ENV_VAL", "default")
	assert.Equal(t, "hello", result)
}

func TestGetEnvVal_UnsetVar_ReturnsDefault(t *testing.T) {
	result := getEnvVal("TEST_ENV_VAL_UNSET_XYZ", "fallback")
	assert.Equal(t, "fallback", result)
}

func TestGetEnvVal_EmptyStringSet_ReturnsEmpty(t *testing.T) {
	// t.Setenv sets the var (LookupEnv returns ("", true)) — empty beats default.
	t.Setenv("TEST_ENV_EMPTY", "")

	result := getEnvVal("TEST_ENV_EMPTY", "default")
	assert.Equal(t, "", result)
}

// --- IsDevelopment package func ---

func TestIsDevelopment_NilCfg_ReturnsTrue(t *testing.T) {
	// Save and restore package-level cfg to avoid polluting other tests.
	prev := cfg
	cfg = nil
	t.Cleanup(func() { cfg = prev })

	assert.True(t, IsDevelopment())
}

func TestIsDevelopment_ProductionCfg_ReturnsFalse(t *testing.T) {
	prev := cfg
	cfg = &Config{App: AppConfig{Env: "production"}}
	t.Cleanup(func() { cfg = prev })

	assert.False(t, IsDevelopment())
}

func TestIsDevelopment_DevelopmentCfg_ReturnsTrue(t *testing.T) {
	prev := cfg
	cfg = &Config{App: AppConfig{Env: "development"}}
	t.Cleanup(func() { cfg = prev })

	assert.True(t, IsDevelopment())
}

// --- GetConfig ---

func TestGetConfig_NilCfg_Panics(t *testing.T) {
	prev := cfg
	cfg = nil
	t.Cleanup(func() { cfg = prev })

	require.Panics(t, func() { GetConfig() })
}

func TestGetConfig_LoadedCfg_ReturnsCfg(t *testing.T) {
	prev := cfg
	loaded := &Config{App: AppConfig{Env: "staging"}}
	cfg = loaded
	t.Cleanup(func() { cfg = prev })

	result := GetConfig()
	assert.Same(t, loaded, result)
}
