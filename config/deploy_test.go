package config

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/logger"
)

// A 32-byte key that is NOT the development key.
const testKeyB64 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=" // bytes 0..31

// deployedValid returns a production config that passes every fail-closed rule.
func deployedValid() *Config {
	return &Config{
		App:      AppConfig{Env: EnvProduction, Port: 3000},
		Database: DatabaseConfig{URL: "postgres://app:pw@db.internal:5432/zentax?sslmode=require"},
		CORS:     CORSConfig{AllowedOrigins: []string{"https://app.zentax.example"}},
		Log:      &logger.Config{Level: "info", Format: "json"},
		Auth:     AuthConfig{EncryptionKey: testKeyB64, SessionCookieSecure: true},
		Storage:  StorageConfig{Driver: StorageDriverFS, FS: StorageFSConfig{Root: "/var/lib/zentax/documents"}},
	}
}

// --- DecodeEncryptionKey: base64-32 OR passphrase --------------------------

func TestDecodeEncryptionKey_Base64Of32Bytes(t *testing.T) {
	t.Parallel()
	key, err := AuthConfig{EncryptionKey: testKeyB64}.DecodeEncryptionKey()
	require.NoError(t, err)
	assert.Len(t, key, 32)
	assert.Equal(t, byte(0), key[0])
	assert.Equal(t, byte(31), key[31])
}

func TestDecodeEncryptionKey_PassphraseDerivedWithSHA256(t *testing.T) {
	t.Parallel()
	passphrase := "Xk9pQ2mL7vB4nR8tW1yZ5aC3eF6hJ0uI-SecretsManager" // 47 chars, not base64-32
	key, err := AuthConfig{EncryptionKey: passphrase}.DecodeEncryptionKey()
	require.NoError(t, err)
	want := sha256.Sum256([]byte(passphrase))
	assert.Equal(t, want[:], key)

	// Deterministic: the same passphrase always derives the same key.
	again, err := AuthConfig{EncryptionKey: passphrase}.DecodeEncryptionKey()
	require.NoError(t, err)
	assert.Equal(t, key, again)

	// A different passphrase (rotation) derives a different key.
	other, err := AuthConfig{EncryptionKey: passphrase + "2"}.DecodeEncryptionKey()
	require.NoError(t, err)
	assert.NotEqual(t, key, other)
}

func TestDecodeEncryptionKey_Exactly32CharPassphraseAccepted(t *testing.T) {
	t.Parallel()
	passphrase := strings.Repeat("ab", 16) // 32 chars; base64-decodes to 24 bytes → passphrase path
	key, err := AuthConfig{EncryptionKey: passphrase}.DecodeEncryptionKey()
	require.NoError(t, err)
	want := sha256.Sum256([]byte(passphrase))
	assert.Equal(t, want[:], key)
}

func TestDecodeEncryptionKey_ShortValueRejected(t *testing.T) {
	t.Parallel()
	for _, v := range []string{"", "short", strings.Repeat("a", 31), "c2hvcnQ="} {
		_, err := AuthConfig{EncryptionKey: v}.DecodeEncryptionKey()
		require.Error(t, err, "value %q must be rejected", v)
	}
}

func TestDecodeEncryptionKey_Base64OfWrongLengthLongEnoughIsAPassphrase(t *testing.T) {
	t.Parallel()
	// base64 of 33 bytes: valid base64 but not 32 bytes → treated as a
	// passphrase (44 chars), never silently truncated/padded.
	v := base64.StdEncoding.EncodeToString(make([]byte, 33))
	key, err := AuthConfig{EncryptionKey: v}.DecodeEncryptionKey()
	require.NoError(t, err)
	want := sha256.Sum256([]byte(v))
	assert.Equal(t, want[:], key)
}

func TestDevelopmentKeyConstantMatchesDevelopmentJSON(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", configDir, "development.json"))
	require.NoError(t, err)
	var c Config
	require.NoError(t, json.Unmarshal(raw, &c))
	assert.Equal(t, DevelopmentEncryptionKey, c.Auth.EncryptionKey,
		"DevelopmentEncryptionKey must track development.json so the deployed-env check stays meaningful")
	assert.Equal(t, EnvDevelopment, c.App.Env)
	assert.True(t, c.Swagger.Enabled, "development keeps swagger on")
}

// --- Fail-closed rules for staging / production ---------------------------

func TestDeployed_ValidConfigPasses(t *testing.T) {
	t.Parallel()
	for _, env := range []string{EnvStaging, EnvProduction} {
		c := deployedValid()
		c.App.Env = env
		require.NoError(t, c.validate(), env)
	}
}

func TestDeployed_RulesDoNotApplyToDevelopment(t *testing.T) {
	t.Parallel()
	c := &Config{
		App:      AppConfig{Env: EnvDevelopment, Port: 3000},
		Database: DatabaseConfig{URL: "postgres://x?sslmode=disable"},
		CORS:     CORSConfig{AllowedOrigins: []string{"*"}},
		Log:      &logger.Config{Format: "text"},
		Auth:     AuthConfig{EncryptionKey: DevelopmentEncryptionKey},
	}
	require.NoError(t, c.validate())
}

func TestDeployed_MissingEncryptionKey(t *testing.T) {
	t.Parallel()
	c := deployedValid()
	c.Auth.EncryptionKey = ""
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth.encryptionKey")
	assert.Contains(t, err.Error(), EnvAuthEncryptionKey)
}

func TestDeployed_DevelopmentKeyRejected_SameBytesAnyEncoding(t *testing.T) {
	t.Parallel()
	c := deployedValid()
	c.Auth.EncryptionKey = DevelopmentEncryptionKey
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "development key")
	assert.Contains(t, err.Error(), EnvAuthEncryptionKey)

	// The check compares decoded bytes: re-encoding the same 32 bytes with
	// different (but still valid) base64 whitespace-free forms is the same key.
	dev, _ := base64.StdEncoding.DecodeString(DevelopmentEncryptionKey)
	c.Auth.EncryptionKey = base64.StdEncoding.EncodeToString(dev)
	require.Error(t, c.validate())
}

func TestDeployed_CookieSecureRequired(t *testing.T) {
	t.Parallel()
	c := deployedValid()
	c.Auth.SessionCookieSecure = false
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sessionCookieSecure")
	assert.Contains(t, err.Error(), EnvAuthSessionCookieSecure+"=true")
}

func TestDeployed_SSLModeDisableRejected(t *testing.T) {
	t.Parallel()
	for _, u := range []string{
		"postgres://app:pw@db:5432/zentax?sslmode=disable",
		"postgresql://app:pw@db/zentax?application_name=x&sslmode=DISABLE",
		"host=db user=app dbname=zentax sslmode=disable",
	} {
		c := deployedValid()
		c.Database.URL = u
		err := c.validate()
		require.Error(t, err, u)
		assert.Contains(t, err.Error(), "sslmode=disable")
		assert.Contains(t, err.Error(), EnvDatabaseURL)
		assert.Contains(t, err.Error(), EnvDBSSLMode)
	}
	for _, u := range []string{
		"postgres://app:pw@db:5432/zentax?sslmode=require",
		"postgres://app:pw@db:5432/zentax?sslmode=verify-full",
		"postgres://app:pw@db:5432/zentax", // driver default (prefer) — not explicitly disabled
	} {
		c := deployedValid()
		c.Database.URL = u
		require.NoError(t, c.validate(), u)
	}
}

func TestDeployed_CORSWildcardRejected(t *testing.T) {
	t.Parallel()
	c := deployedValid()
	c.CORS.AllowedOrigins = []string{"https://app.zentax.example", " * "}
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), `must not contain "*"`)
	assert.Contains(t, err.Error(), EnvCORSAllowedOrigins)
}

func TestDeployed_CORSEmptyRejected(t *testing.T) {
	t.Parallel()
	// go-chi/cors treats an empty list as "*".
	c := deployedValid()
	c.CORS.AllowedOrigins = nil
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "allows every origin")
	assert.Contains(t, err.Error(), EnvCORSAllowedOrigins)
}

func TestDeployed_LogFormatMustBeJSON(t *testing.T) {
	t.Parallel()
	c := deployedValid()
	c.Log.Format = "text"
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvLogFormat+"=json")

	c.Log = nil
	err = c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), EnvLogFormat+"=json")
}

func TestDeployed_AllViolationsReportedAtOnce(t *testing.T) {
	t.Parallel()
	c := &Config{
		App:      AppConfig{Env: EnvProduction, Port: 3000},
		Database: DatabaseConfig{URL: "postgres://x?sslmode=disable"},
		CORS:     CORSConfig{AllowedOrigins: []string{"*"}},
		Log:      &logger.Config{Format: "text"},
		Auth:     AuthConfig{EncryptionKey: DevelopmentEncryptionKey},
	}
	err := c.validate()
	require.Error(t, err)
	for _, want := range []string{
		EnvAuthEncryptionKey, EnvAuthSessionCookieSecure, EnvDatabaseURL, EnvCORSAllowedOrigins, EnvLogFormat,
	} {
		assert.Contains(t, err.Error(), want)
	}
}

// --- Env overrides ---------------------------------------------------------

func TestEnvOverrides_AuthCORSLogSwagger(t *testing.T) {
	t.Setenv(EnvAuthEncryptionKey, testKeyB64)
	t.Setenv(EnvAuthSessionCookieSecure, "true")
	t.Setenv(EnvCORSAllowedOrigins, " https://a.example ,https://b.example,, ")
	t.Setenv(EnvLogFormat, "json")
	t.Setenv(EnvLogLevel, "warn")
	t.Setenv(EnvSwaggerEnabled, "false")

	c := &Config{
		CORS:    CORSConfig{AllowedOrigins: []string{"*"}},
		Auth:    AuthConfig{EncryptionKey: DevelopmentEncryptionKey},
		Swagger: SwaggerConfig{Enabled: true},
	}
	mergeEnvOverrides(c)

	assert.Equal(t, testKeyB64, c.Auth.EncryptionKey)
	assert.True(t, c.Auth.SessionCookieSecure)
	assert.Equal(t, []string{"https://a.example", "https://b.example"}, c.CORS.AllowedOrigins)
	require.NotNil(t, c.Log, "LOG_* allocates the log section when the file omits it")
	assert.Equal(t, "json", c.Log.Format)
	assert.Equal(t, "warn", c.Log.Level)
	assert.False(t, c.Swagger.Enabled)
}

func TestEnvOverrides_EmptyValuesNeverOverride(t *testing.T) {
	t.Setenv(EnvAuthEncryptionKey, "")
	t.Setenv(EnvAuthSessionCookieSecure, "")
	t.Setenv(EnvCORSAllowedOrigins, "")
	t.Setenv(EnvLogFormat, "")
	t.Setenv(EnvLogLevel, "")
	t.Setenv(EnvSwaggerEnabled, "")
	t.Setenv(EnvDatabaseURL, "")
	t.Setenv(EnvDBHost, "")

	c := &Config{
		Database: DatabaseConfig{URL: "keep"},
		CORS:     CORSConfig{AllowedOrigins: []string{"https://keep.example"}},
		Auth:     AuthConfig{EncryptionKey: "keep", SessionCookieSecure: true},
		Log:      &logger.Config{Level: "debug", Format: "text"},
		Swagger:  SwaggerConfig{Enabled: true},
	}
	mergeEnvOverrides(c)

	assert.Equal(t, "keep", c.Database.URL)
	assert.Equal(t, []string{"https://keep.example"}, c.CORS.AllowedOrigins)
	assert.Equal(t, "keep", c.Auth.EncryptionKey)
	assert.True(t, c.Auth.SessionCookieSecure)
	assert.Equal(t, &logger.Config{Level: "debug", Format: "text"}, c.Log)
	assert.True(t, c.Swagger.Enabled)
}

func TestEnvOverrides_OnlyCommasInCORSKeepsFile(t *testing.T) {
	t.Setenv(EnvCORSAllowedOrigins, " , ,")
	c := &Config{CORS: CORSConfig{AllowedOrigins: []string{"https://keep.example"}}}
	mergeEnvOverrides(c)
	assert.Equal(t, []string{"https://keep.example"}, c.CORS.AllowedOrigins)
}

func TestEnvOverrides_BadBoolIgnored(t *testing.T) {
	t.Setenv(EnvAuthSessionCookieSecure, "yes-please")
	t.Setenv(EnvSwaggerEnabled, "maybe")
	c := &Config{Auth: AuthConfig{SessionCookieSecure: true}, Swagger: SwaggerConfig{Enabled: true}}
	mergeEnvOverrides(c)
	assert.True(t, c.Auth.SessionCookieSecure)
	assert.True(t, c.Swagger.Enabled)
}

// --- DB_* composition -------------------------------------------------------

func TestComposeDatabaseURL_EscapesUserinfoAndName(t *testing.T) {
	t.Parallel()
	password := `p@ss:w/rd?x=1 &%+#"'` // every character a generated secret can throw at a URL
	got := ComposeDatabaseURL("db.internal", "5432", "zen tax", "app user", password, "require")

	u, err := url.Parse(got)
	require.NoError(t, err, got)
	assert.Equal(t, "postgres", u.Scheme)
	assert.Equal(t, "app user", u.User.Username())
	pw, ok := u.User.Password()
	require.True(t, ok)
	assert.Equal(t, password, pw)
	assert.Equal(t, "db.internal:5432", u.Host)
	assert.Equal(t, "/zen tax", u.Path)
	assert.Equal(t, "require", u.Query().Get("sslmode"))
	assert.False(t, strings.Contains(got, " "), "no raw spaces: %s", got)
}

func TestComposeDatabaseURL_IPv6Host(t *testing.T) {
	t.Parallel()
	got := ComposeDatabaseURL("::1", "5432", "zentax", "app", "pw", "require")
	assert.Equal(t, "postgres://app:pw@[::1]:5432/zentax?sslmode=require", got)
}

func TestDBEnv_ComposedWhenDatabaseURLUnset(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "")
	t.Setenv(EnvDBHost, "zentax.abc123.eu-central-1.rds.amazonaws.com")
	t.Setenv(EnvDBPort, "")
	t.Setenv(EnvDBName, "zentax")
	t.Setenv(EnvDBUser, "zentax_app")
	t.Setenv(EnvDBPassword, "s3cr3t/with@chars")
	t.Setenv(EnvDBSSLMode, "")

	c := &Config{Database: DatabaseConfig{URL: "from-file"}}
	mergeEnvOverrides(c)
	assert.Equal(t,
		"postgres://zentax_app:s3cr3t%2Fwith%40chars@zentax.abc123.eu-central-1.rds.amazonaws.com:5432/zentax?sslmode=require",
		c.Database.URL, "port defaults to 5432, sslmode to require")
}

func TestDBEnv_ExplicitPortAndSSLMode(t *testing.T) {
	t.Setenv(EnvDBHost, "db")
	t.Setenv(EnvDBPort, "6432")
	t.Setenv(EnvDBName, "zentax")
	t.Setenv(EnvDBUser, "app")
	t.Setenv(EnvDBPassword, "pw")
	t.Setenv(EnvDBSSLMode, "verify-full")

	c := &Config{}
	mergeEnvOverrides(c)
	assert.Equal(t, "postgres://app:pw@db:6432/zentax?sslmode=verify-full", c.Database.URL)
}

func TestDBEnv_DatabaseURLWinsOverParts(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "postgres://explicit")
	t.Setenv(EnvDBHost, "db")
	t.Setenv(EnvDBName, "zentax")
	t.Setenv(EnvDBUser, "app")
	t.Setenv(EnvDBPassword, "pw")

	c := &Config{}
	mergeEnvOverrides(c)
	assert.Equal(t, "postgres://explicit", c.Database.URL)
}

func TestDBEnv_NoHostKeepsFileURL(t *testing.T) {
	t.Setenv(EnvDatabaseURL, "")
	t.Setenv(EnvDBHost, "")
	t.Setenv(EnvDBName, "zentax") // the other parts alone do not trigger composition
	c := &Config{Database: DatabaseConfig{URL: "from-file"}}
	mergeEnvOverrides(c)
	assert.Equal(t, "from-file", c.Database.URL)
}

// --- Shipped config files ---------------------------------------------------

func TestShippedDeployedConfigFilesAreProductionShaped(t *testing.T) {
	t.Parallel()
	for _, env := range []string{EnvStaging, EnvProduction} {
		raw, err := os.ReadFile(filepath.Join("..", configDir, env+".json"))
		require.NoError(t, err)
		var c Config
		require.NoError(t, json.Unmarshal(raw, &c))

		assert.Equal(t, env, c.App.Env)
		assert.Equal(t, 3000, c.App.Port)
		assert.Empty(t, c.Database.URL, "%s: database.url comes from the environment", env)
		assert.Empty(t, c.Auth.EncryptionKey, "%s: encryption key comes from the environment", env)
		assert.True(t, c.Auth.SessionCookieSecure, env)
		assert.Empty(t, c.CORS.AllowedOrigins, "%s: origins come from the environment", env)
		require.NotNil(t, c.Log, env)
		assert.Equal(t, "json", c.Log.Format, env)
		assert.Equal(t, "info", c.Log.Level, env)
		assert.True(t, c.Metrics.Enabled, env)
		assert.Equal(t, env == EnvStaging, c.Swagger.Enabled, "%s: swagger on in staging only", env)

		// The file alone must NOT boot: every secret-bearing value is required
		// from the environment.
		err = c.validate()
		require.Error(t, err, env)
		assert.Contains(t, err.Error(), EnvDatabaseURL)
	}
}

func TestShippedDeployedConfigFiles_BootWithEnv(t *testing.T) {
	// The full path an ECS task takes: file + env → valid config.
	t.Setenv(EnvDBHost, "db.internal")
	t.Setenv(EnvDBName, "zentax")
	t.Setenv(EnvDBUser, "zentax_app")
	t.Setenv(EnvDBPassword, "generated")
	t.Setenv(EnvAuthEncryptionKey, "Xk9pQ2mL7vB4nR8tW1yZ5aC3eF6hJ0uIabcdefgh")
	t.Setenv(EnvCORSAllowedOrigins, "https://d123.cloudfront.net")
	t.Setenv(EnvStorageS3Bucket, "zentax-documents-staging")
	t.Setenv(EnvStorageS3Region, "eu-central-1")

	for _, env := range []string{EnvStaging, EnvProduction} {
		raw, err := os.ReadFile(filepath.Join("..", configDir, env+".json"))
		require.NoError(t, err)
		c := &Config{}
		require.NoError(t, json.Unmarshal(raw, c))
		mergeEnvOverrides(c)
		require.NoError(t, c.validate(), env)
		assert.Equal(t, "postgres://zentax_app:generated@db.internal:5432/zentax?sslmode=require", c.Database.URL)
		assert.Equal(t, StorageDriverS3, c.Storage.Driver)
	}
}
