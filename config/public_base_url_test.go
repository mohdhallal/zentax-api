package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// app.publicBaseUrl / PUBLIC_BASE_URL — the product's public origin, the base
// for absolute links the API hands out of band. Optional; validated fail-closed
// when present (absolute http(s), https in deployed environments).

// --- PUBLIC_BASE_URL override ------------------------------------------------

func TestPublicBaseURL_EnvOverride_TrimmedAndTrailingSlashStripped(t *testing.T) {
	t.Setenv(EnvPublicBaseURL, "  https://eu.app.zentax.software/  ")

	c := &Config{App: AppConfig{PublicBaseURL: "http://localhost:5000"}}
	mergeEnvOverrides(c)

	assert.Equal(t, "https://eu.app.zentax.software", c.App.PublicBaseURL)
}

func TestPublicBaseURL_EnvOverride_MultipleTrailingSlashesStripped(t *testing.T) {
	t.Setenv(EnvPublicBaseURL, "https://eu.app.zentax.software///")

	c := &Config{}
	mergeEnvOverrides(c)

	assert.Equal(t, "https://eu.app.zentax.software", c.App.PublicBaseURL)
}

func TestPublicBaseURL_EnvUnset_KeepsFile(t *testing.T) {
	// Not set at all — LookupEnv returns ("", false).
	c := &Config{App: AppConfig{PublicBaseURL: "http://localhost:5000"}}
	mergeEnvOverrides(c)

	assert.Equal(t, "http://localhost:5000", c.App.PublicBaseURL)
}

func TestPublicBaseURL_EnvEmptyOrBlank_NeverOverrides(t *testing.T) {
	// The ECS task passes "" while the cell has no public hostname yet: that
	// must not blank a file-configured value (same rule as every other override).
	for _, blank := range []string{"", "   ", "/"} {
		t.Setenv(EnvPublicBaseURL, blank)
		c := &Config{App: AppConfig{PublicBaseURL: "http://localhost:5000"}}
		mergeEnvOverrides(c)
		assert.Equal(t, "http://localhost:5000", c.App.PublicBaseURL, "env %q", blank)
	}
}

// --- validation (every environment) -------------------------------------------

func devValid() *Config {
	return &Config{
		App:      AppConfig{Env: EnvDevelopment, Port: 3000},
		Database: DatabaseConfig{URL: "postgres://localhost/zentax?sslmode=disable"},
	}
}

func TestPublicBaseURL_EmptyIsOptional(t *testing.T) {
	t.Parallel()
	dev := devValid()
	require.NoError(t, dev.validate())
	assert.Empty(t, dev.App.PublicBaseURL)

	prod := deployedValid()
	require.NoError(t, prod.validate())
	assert.Empty(t, prod.App.PublicBaseURL)
}

func TestPublicBaseURL_FileValueNormalizedByValidate(t *testing.T) {
	t.Parallel()
	c := devValid()
	c.App.PublicBaseURL = " http://localhost:5000/ "
	require.NoError(t, c.validate())
	assert.Equal(t, "http://localhost:5000", c.App.PublicBaseURL)
}

func TestPublicBaseURL_ValidValuesAccepted(t *testing.T) {
	t.Parallel()
	for _, v := range []string{
		"http://localhost:5000",
		"http://127.0.0.1:5001",
		"https://eu.app.zentax.software",
		"https://eu.staging.zentax.software",
		"https://zentax.internal.example/app", // path prefix behind a reverse proxy
		"https://[::1]:8443",
	} {
		c := devValid()
		c.App.PublicBaseURL = v
		assert.NoError(t, c.validate(), "value %q must be accepted", v)
	}
}

func TestPublicBaseURL_InvalidValuesRejected(t *testing.T) {
	t.Parallel()
	for _, v := range []string{
		"eu.app.zentax.software",       // no scheme
		"localhost:5000",               // parses as scheme "localhost"
		"ftp://eu.app.zentax.software", // wrong scheme
		"https://",                     // no host
		"https:///accept-invite",       // no host, path only
		"https://:8443",                // port without host
		"https://admin:pw@app.example", // userinfo
		"https://app.example?next=x",   // query
		"https://app.example/?",        // forced empty query
		"https://app.example#fragment", // fragment
		"http://[::1",                  // unparseable
		"/accept-invite",               // relative
		"not a url",                    // garbage
	} {
		c := devValid()
		c.App.PublicBaseURL = v
		err := c.validate()
		require.Error(t, err, "value %q must be rejected", v)
		assert.Contains(t, err.Error(), "app.publicBaseUrl", v)
		assert.Contains(t, err.Error(), EnvPublicBaseURL, "the error names the env var to set")
	}
}

// --- deployed rule: https only --------------------------------------------------

func TestDeployed_PublicBaseURLMustBeHTTPS(t *testing.T) {
	t.Parallel()
	for _, env := range []string{EnvStaging, EnvProduction} {
		c := deployedValid()
		c.App.Env = env
		c.App.PublicBaseURL = "http://eu.app.zentax.software"
		err := c.validate()
		require.Error(t, err, env)
		assert.Contains(t, err.Error(), "https")
		assert.Contains(t, err.Error(), EnvPublicBaseURL)

		c.App.PublicBaseURL = "https://eu.app.zentax.software/"
		require.NoError(t, c.validate(), env)
		assert.Equal(t, "https://eu.app.zentax.software", c.App.PublicBaseURL)
	}
}

func TestDevelopment_PublicBaseURLMayBeHTTP(t *testing.T) {
	t.Parallel()
	c := devValid()
	c.App.PublicBaseURL = "http://localhost:5000"
	require.NoError(t, c.validate())
}

// --- shipped config files -------------------------------------------------------

func TestShippedDevelopmentConfig_PublicBaseURLIsLocalhost(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", configDir, "development.json"))
	require.NoError(t, err)
	var c Config
	require.NoError(t, json.Unmarshal(raw, &c))
	assert.Equal(t, "http://localhost:5000", c.App.PublicBaseURL,
		"development points at the local web tier (npm run dev on :5000)")
	require.NoError(t, validatePublicBaseURL(c.App.PublicBaseURL))
}

// --- end to end through Load() --------------------------------------------------

func writeTestingConfig(t *testing.T, payload map[string]any) {
	t.Helper()
	root := t.TempDir()
	cfgDir := filepath.Join(root, "deployment", "config_files")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	b, err := json.Marshal(payload)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "testing.json"), b, 0o600))

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Setenv(EnvVarName, "testing")

	prevCfg := cfg
	cfg = nil
	t.Cleanup(func() { cfg = prevCfg })
}

func TestLoad_PublicBaseURL_EnvOverrideApplied(t *testing.T) {
	// Mutates cwd and package-level cfg — not parallel.
	writeTestingConfig(t, map[string]any{
		"app":      map[string]any{"env": "testing", "port": 3000, "publicBaseUrl": "http://localhost:5000"},
		"database": map[string]any{"url": "postgres://localhost/test"},
	})
	t.Setenv(EnvPublicBaseURL, "https://eu.staging.zentax.software/")

	loaded, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "https://eu.staging.zentax.software", loaded.App.PublicBaseURL)
}

func TestLoad_PublicBaseURL_InvalidEnvFailsStartup(t *testing.T) {
	writeTestingConfig(t, map[string]any{
		"app":      map[string]any{"env": "testing", "port": 3000},
		"database": map[string]any{"url": "postgres://localhost/test"},
	})
	t.Setenv(EnvPublicBaseURL, "eu.staging.zentax.software")

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvPublicBaseURL)
	assert.Contains(t, err.Error(), "app.publicBaseUrl")
}
