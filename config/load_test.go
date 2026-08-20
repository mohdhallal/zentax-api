package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoad_ParsesJSONAndSetsEnvName(t *testing.T) {
	payload := map[string]any{
		"app":              map[string]any{"env": "testing", "port": 9090, "internalPort": 9091},
		"server":           map[string]any{},
		"database":         map[string]any{"url": "postgres://localhost/test"},
		"cors":             map[string]any{},
		"swagger":          map[string]any{"title": "Test", "version": "0.1"},
		"metrics":          map[string]any{},
		"log":              nil,
		"nexusInternalApi": map[string]any{},
	}

	// Load() reads from configDir (relative to cwd). Change cwd to a temp root
	// where deployment/config_files/ contains the fixture JSON.
	root := t.TempDir()
	cfgDir := filepath.Join(root, "deployment", "config_files")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	b, _ := json.Marshal(payload)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "testing.json"), b, 0o600))

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Setenv(EnvVarName, "testing")

	// Reset package-level cfg so GetConfig isn't polluted.
	prevCfg := cfg
	cfg = nil
	t.Cleanup(func() { cfg = prevCfg })

	loaded, err := Load()
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, "testing", loaded.EnvName)
	assert.Equal(t, 9090, loaded.App.Port)
	assert.Equal(t, "postgres://localhost/test", loaded.Database.URL)
	assert.Equal(t, "Test", loaded.Swagger.Title)
}

func TestLoad_DatabaseURL_EnvOverrideApplied(t *testing.T) {
	payload := map[string]any{
		"app":      map[string]any{"env": "testing", "port": 3000},
		"database": map[string]any{"url": "postgres://original"},
	}

	root := t.TempDir()
	cfgDir := filepath.Join(root, "deployment", "config_files")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	b, _ := json.Marshal(payload)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "testing.json"), b, 0o600))

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Setenv(EnvVarName, "testing")
	t.Setenv(EnvDatabaseURL, "postgres://override")

	prevCfg := cfg
	cfg = nil
	t.Cleanup(func() { cfg = prevCfg })

	loaded, err := Load()
	require.NoError(t, err)
	assert.Equal(t, "postgres://override", loaded.Database.URL)
}

func TestLoad_SetsGlobalCfg(t *testing.T) {
	// port + database.url are the minimum the fail-closed validate() requires
	// (ADR-0014); this test asserts Load sets the global cfg, so the payload must
	// be valid enough to pass validation.
	payload := map[string]any{
		"app":      map[string]any{"env": "testing", "port": 8080},
		"database": map[string]any{"url": "postgres://localhost/test"},
	}

	root := t.TempDir()
	cfgDir := filepath.Join(root, "deployment", "config_files")
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	b, _ := json.Marshal(payload)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "testing.json"), b, 0o600))

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Setenv(EnvVarName, "testing")

	prevCfg := cfg
	cfg = nil
	t.Cleanup(func() { cfg = prevCfg })

	loaded, err := Load()
	require.NoError(t, err)

	// GetConfig should now return the loaded config without panicking.
	assert.Same(t, loaded, GetConfig())
}

// Note: these tests mutate cwd and package-level cfg — do not use t.Parallel().
