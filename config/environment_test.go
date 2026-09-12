package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/logger"
)

// APP_ENV carries a tier or a cell of one (ADR-0024 / ADR-0025: the SaaS
// deployment passes the cell name — staging-eu, production-eu — because every
// deployed resource is named after it). The tier, never the name, decides which
// validation rules run, and an environment nobody recognises fails closed.

// --- ParseEnvironment -------------------------------------------------------

func TestParseEnvironment_TiersAndCells(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   string
		tier Tier
		cell string
	}{
		{"development", TierDevelopment, ""},
		{"staging", TierStaging, ""},
		{"production", TierProduction, ""},
		{"staging-eu", TierStaging, "eu"},
		{"production-eu", TierProduction, "eu"},
		{"production-us", TierProduction, "us"},
		{"staging-eu-west", TierStaging, "eu-west"},
		{"  staging-eu  ", TierStaging, "eu"}, // surrounding whitespace is trimmed
	} {
		env, err := ParseEnvironment(tc.in)
		require.NoError(t, err, tc.in)
		assert.Equal(t, tc.tier, env.Tier, tc.in)
		assert.Equal(t, tc.cell, env.Cell, tc.in)
		assert.Equal(t, strings.TrimSpace(tc.in), env.Name, tc.in)
		assert.Equal(t, tc.cell != "", env.IsCell(), tc.in)
	}
}

func TestParseEnvironment_UnknownOrMalformedRejected(t *testing.T) {
	t.Parallel()
	for _, in := range []string{
		"",             // APP_ENV set but empty — never a silent fallback to development
		"   ",          //
		"testing",      // not a tier
		"prod",         // nearly production
		"Staging",      // case matters
		"STAGING-EU",   //
		"staging_eu",   // underscore is not the separator
		"staging-EU",   // cell label is lowercase
		"staging-",     // empty cell label
		"-eu",          // empty tier
		"staging--eu",  // doubled dash
		"staging-eu-",  // trailing dash
		"staging eu",   // space inside
		"staging.eu",   //
		"eu-staging",   // tier first, always
		"development2", //
		// Path components: APP_ENV chooses a file name, so traversal and
		// absolute paths must die here rather than at os.ReadFile.
		"../../etc/passwd",
		"staging/../development",
		"/etc/passwd",
		"..",
		".",
		"staging-eu/../../development",
		strings.Repeat("a", 40),                // long garbage
		"staging-" + strings.Repeat("e", 33),   // cell label over the length cap
		"staging-" + strings.Repeat("e", 4000), //
	} {
		_, err := ParseEnvironment(in)
		require.Error(t, err, "%q must be refused", in)
		assert.Contains(t, err.Error(), EnvVarName, "%q: the error names the variable to fix", in)
	}
}

func TestParseEnvironment_CellLabelLengthBoundary(t *testing.T) {
	t.Parallel()
	_, err := ParseEnvironment("staging-" + strings.Repeat("e", maxCellLabelLen))
	require.NoError(t, err)
	_, err = ParseEnvironment("staging-" + strings.Repeat("e", maxCellLabelLen+1))
	require.Error(t, err)
}

// --- Tier → validation path -------------------------------------------------

func TestTier_UnknownIsDeployed(t *testing.T) {
	t.Parallel()
	// The zero Tier is what an unparseable environment yields. Deployed is the
	// safe answer: unknown means "apply every rule", never "apply none".
	assert.True(t, Tier("").IsDeployed())
	assert.True(t, Tier("testing").IsDeployed())
	assert.True(t, TierStaging.IsDeployed())
	assert.True(t, TierProduction.IsDeployed())
	assert.False(t, TierDevelopment.IsDeployed())
}

func TestConfig_TierAndDeployedFollowTheCellName(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		env      string
		tier     Tier
		deployed bool
		dev      bool
	}{
		{"development", TierDevelopment, false, true},
		{"staging", TierStaging, true, false},
		{"production", TierProduction, true, false},
		{"staging-eu", TierStaging, true, false},
		{"production-eu", TierProduction, true, false},
		{"testing", "", true, false},   // unrecognised → no tier → deployed
		{"", "", true, false},          // ditto
		{"stagingeu", "", true, false}, // a typo must not read as staging
	} {
		c := &Config{App: AppConfig{Env: tc.env}}
		assert.Equal(t, tc.tier, c.Tier(), "env=%q", tc.env)
		assert.Equal(t, tc.deployed, c.IsDeployed(), "env=%q", tc.env)
		assert.Equal(t, tc.dev, c.IsDevelopment(), "env=%q", tc.env)
	}
}

// TestCellNameTakesTheDeployedValidationPath is the regression this whole file
// exists for: before the tier split, IsDeployed compared app.env to the literals
// "staging"/"production", so a cell called staging-eu skipped validateDeployed
// entirely and would have booted in a real AWS cell with the development
// encryption key, a non-Secure session cookie, a plaintext database connection
// and wildcard CORS.
func TestCellNameTakesTheDeployedValidationPath(t *testing.T) {
	t.Parallel()
	for _, cell := range []string{"staging-eu", "production-eu"} {
		c := &Config{
			App:      AppConfig{Env: cell, Port: 3000, PublicBaseURL: "http://eu.app.zentax.software"},
			Database: DatabaseConfig{URL: "postgres://x?sslmode=disable"},
			CORS:     CORSConfig{AllowedOrigins: []string{"*"}},
			Log:      &logger.Config{Format: "text"},
			Auth:     AuthConfig{EncryptionKey: DevelopmentEncryptionKey, SessionCookieSecure: false},
		}
		err := c.validate()
		require.Error(t, err, cell)
		// Every fail-closed rule of ADR-0014 must have fired, and each message
		// must name the cell so the operator knows which deployment refused.
		for _, want := range []string{
			"development key", EnvAuthEncryptionKey,
			"sessionCookieSecure", EnvAuthSessionCookieSecure,
			"sslmode=disable", EnvDatabaseURL,
			`must not contain "*"`, EnvCORSAllowedOrigins,
			EnvLogFormat + "=json",
			"app.publicBaseUrl", EnvPublicBaseURL,
			cell,
		} {
			assert.Contains(t, err.Error(), want, cell)
		}

		// ... and the same config, put right, passes as the tier does.
		c.Auth.EncryptionKey = testKeyB64
		c.Auth.SessionCookieSecure = true
		c.Database.URL = "postgres://app:pw@db.internal:5432/zentax?sslmode=require"
		c.CORS.AllowedOrigins = []string{"https://eu.app.zentax.software"}
		c.Log.Format = "json"
		c.App.PublicBaseURL = "https://eu.app.zentax.software"
		require.NoError(t, c.validate(), cell)
	}
}

func TestValidate_UnknownEnvironmentRefused(t *testing.T) {
	t.Parallel()
	// A config that development would accept: with an environment nobody
	// recognises it must be refused, not waved through.
	for _, env := range []string{"testing", "", "prod", "staging_eu"} {
		c := &Config{
			App:      AppConfig{Env: env, Port: 3000},
			Database: DatabaseConfig{URL: "postgres://x?sslmode=disable"},
			CORS:     CORSConfig{AllowedOrigins: []string{"*"}},
			Log:      &logger.Config{Format: "text"},
			Auth:     AuthConfig{EncryptionKey: DevelopmentEncryptionKey},
		}
		err := c.validate()
		require.Error(t, err, "env=%q", env)
		assert.Contains(t, err.Error(), "app.env", env)
		assert.Contains(t, err.Error(), EnvVarName, env)
	}
}

// --- Load(): cell → tier file ------------------------------------------------

// stageShippedConfigs copies the named shipped config files into a temp cwd
// (deployment/config_files/<name>.json) and points APP_ENV at env, the way an
// ECS task does. The shipped bytes are read before the chdir.
func stageShippedConfigs(t *testing.T, env string, names ...string) string {
	t.Helper()
	shipped := make(map[string][]byte, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("..", configDir, name+".json"))
		require.NoError(t, err, name)
		shipped[name] = raw
	}

	root := t.TempDir()
	cfgDir := filepath.Join(root, configDir)
	require.NoError(t, os.MkdirAll(cfgDir, 0o755))
	for name, raw := range shipped {
		require.NoError(t, os.WriteFile(filepath.Join(cfgDir, name+".json"), raw, 0o600))
	}

	orig, _ := os.Getwd()
	require.NoError(t, os.Chdir(root))
	t.Cleanup(func() { _ = os.Chdir(orig) })

	t.Setenv(EnvVarName, env)

	prevCfg := cfg
	cfg = nil
	t.Cleanup(func() { cfg = prevCfg })

	return cfgDir
}

// deployedTaskEnv is the non-secret + secret block the ECS task definitions
// inject (infra/lib/cluster-stack.ts: apiDbEnvironment + apiStorageEnvironment
// + apiDbSecrets).
func deployedTaskEnv(t *testing.T) {
	t.Helper()
	t.Setenv(EnvDBHost, "zentax.abc123.eu-central-1.rds.amazonaws.com")
	t.Setenv(EnvDBName, "zentax")
	t.Setenv(EnvDBUser, "zentax_app")
	t.Setenv(EnvDBPassword, "generated")
	t.Setenv(EnvDBSSLMode, "require")
	t.Setenv(EnvAuthEncryptionKey, testKeyB64)
	t.Setenv(EnvCORSAllowedOrigins, "https://eu.staging.zentax.software")
	t.Setenv(EnvStorageS3Bucket, "zentax-documents-staging-eu")
	t.Setenv(EnvStorageS3Region, "eu-central-1")
	t.Setenv(EnvLogFormat, "json")
}

// TestLoad_CellBootsFromItsTierFile is the other half of the regression: the
// cell name the Cluster stack passes as APP_ENV used to look for a
// staging-eu.json that has never existed, so the api and seed containers exited
// 1 before reaching the database.
func TestLoad_CellBootsFromItsTierFile(t *testing.T) {
	for _, tc := range []struct {
		cell string
		tier Tier
		file string
	}{
		{"staging-eu", TierStaging, EnvStaging},
		{"production-eu", TierProduction, EnvProduction},
	} {
		t.Run(tc.cell, func(t *testing.T) {
			stageShippedConfigs(t, tc.cell, tc.file)
			deployedTaskEnv(t)

			loaded, err := Load()
			require.NoError(t, err)
			assert.Equal(t, tc.cell, loaded.EnvName)
			assert.Equal(t, tc.cell, loaded.App.Env, "app.env is pinned to the environment the deployment declared")
			assert.Equal(t, tc.tier, loaded.Tier())
			assert.Equal(t, "eu", loaded.Environment().Cell)
			assert.True(t, loaded.IsDeployed())
			assert.False(t, loaded.IsDevelopment())
			assert.True(t, loaded.Auth.SessionCookieSecure, "the tier file's hardened values are in force")
		})
	}
}

func TestLoad_CellStillFailsClosedOnTheDevelopmentKey(t *testing.T) {
	stageShippedConfigs(t, "staging-eu", EnvStaging)
	deployedTaskEnv(t)
	t.Setenv(EnvAuthEncryptionKey, DevelopmentEncryptionKey)

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "development key")
	assert.Contains(t, err.Error(), "staging-eu", "the failure names the cell, not the tier file it came from")
}

func TestLoad_CellFileWinsOverTheTierFile(t *testing.T) {
	cfgDir := stageShippedConfigs(t, "staging-eu", EnvStaging)
	deployedTaskEnv(t)

	// A cell may ship its own file; it overrides the tier's values, never the tier.
	override := map[string]any{
		"app":      map[string]any{"env": "staging-eu", "port": 3100},
		"database": map[string]any{"url": ""},
		"log":      map[string]any{"level": "info", "format": "json"},
		"auth":     map[string]any{"sessionCookieSecure": true},
		"storage":  map[string]any{"driver": "s3"},
	}
	b, err := json.Marshal(override)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "staging-eu.json"), b, 0o600))

	loaded, err := Load()
	require.NoError(t, err)
	assert.Equal(t, 3100, loaded.App.Port, "the cell file was the one loaded")
	assert.Equal(t, TierStaging, loaded.Tier())
	assert.True(t, loaded.IsDeployed())
}

// TestLoad_CellFileCannotDowngradeTheTier closes the trap in the obvious fix:
// adding staging-eu.json is fine, but its app.env cannot move the cell to a
// tier with no fail-closed rules.
func TestLoad_CellFileCannotDowngradeTheTier(t *testing.T) {
	for _, declared := range []string{"development", "production", "testing"} {
		t.Run(declared, func(t *testing.T) {
			cfgDir := stageShippedConfigs(t, "staging-eu", EnvStaging)
			deployedTaskEnv(t)

			b, err := json.Marshal(map[string]any{
				"app":      map[string]any{"env": declared, "port": 3000},
				"database": map[string]any{"url": ""},
				"log":      map[string]any{"format": "json"},
				"auth":     map[string]any{"sessionCookieSecure": true},
				"storage":  map[string]any{"driver": "s3"},
			})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(cfgDir, "staging-eu.json"), b, 0o600))

			_, err = Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), "app.env")
			assert.Contains(t, err.Error(), EnvVarName)
		})
	}
}

func TestLoad_UnknownEnvironmentRefusedBeforeReadingAnyFile(t *testing.T) {
	for _, env := range []string{"testing", "staging_eu", "../../etc/passwd", "Staging"} {
		t.Run(env, func(t *testing.T) {
			stageShippedConfigs(t, env, EnvDevelopment, EnvStaging, EnvProduction)

			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), EnvVarName,
				"the error explains APP_ENV, not a missing file")
			assert.NotContains(t, err.Error(), "read config file")
		})
	}
}

func TestLoad_TierNamesStillLoadUnchanged(t *testing.T) {
	// The three shipped files keep working exactly as before.
	t.Run(EnvDevelopment, func(t *testing.T) {
		stageShippedConfigs(t, EnvDevelopment, EnvDevelopment)
		loaded, err := Load()
		require.NoError(t, err)
		assert.Equal(t, EnvDevelopment, loaded.App.Env)
		assert.True(t, loaded.IsDevelopment())
		assert.False(t, loaded.IsDeployed())
	})
	for _, tier := range []string{EnvStaging, EnvProduction} {
		t.Run(tier, func(t *testing.T) {
			stageShippedConfigs(t, tier, tier)
			deployedTaskEnv(t)
			loaded, err := Load()
			require.NoError(t, err)
			assert.Equal(t, tier, loaded.App.Env)
			assert.True(t, loaded.IsDeployed())
		})
	}
}

// --- the cells the infrastructure actually declares ---------------------------

// TestEveryCellInCdkJSONResolvesToAShippedProfile ties the two repositories'
// halves of the contract together: infra/cdk.json declares the cells and
// lib/cluster-stack.ts passes each one's name as APP_ENV, so every declared cell
// must parse and its tier must have a config file in the image. Adding a cell
// (production-us) without a tier file — or with a tier this loader does not know
// — fails here instead of in a crash-looping ECS service.
func TestEveryCellInCdkJSONResolvesToAShippedProfile(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "infra", "cdk.json"))
	require.NoError(t, err)

	var doc struct {
		Context struct {
			Environments map[string]struct {
				Tier        string `json:"tier"`
				RegionLabel string `json:"regionLabel"`
			} `json:"environments"`
		} `json:"context"`
	}
	require.NoError(t, json.Unmarshal(raw, &doc))
	require.NotEmpty(t, doc.Context.Environments, "cdk.json must declare the cells")

	for name, cell := range doc.Context.Environments {
		env, err := ParseEnvironment(name)
		require.NoError(t, err, "cell %q must be a loadable environment", name)
		assert.Equal(t, cell.Tier, env.Tier.String(), "cell %q: cdk.json's tier must be the one APP_ENV resolves to", name)
		assert.Equal(t, cell.RegionLabel, env.Cell, "cell %q: cdk.json's regionLabel is the cell label", name)
		assert.True(t, env.Tier.IsDeployed(), "cell %q: a deployed cell must take the deployed validation path", name)

		_, err = os.Stat(filepath.Join("..", configDir, env.Tier.String()+".json"))
		require.NoError(t, err, "cell %q has no shipped config profile for its %s tier", name, env.Tier)
	}
}
