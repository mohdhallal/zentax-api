package config

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/logger"
)

const (
	// EnvVarName carries the environment: a tier (development, staging,
	// production) or a cell of one (staging-eu, production-eu — the SaaS
	// deployment passes the cell name, ADR-0024/ADR-0025). ParseEnvironment
	// splits it; the tier half chooses the config file and every rule.
	EnvVarName     = "APP_ENV"
	EnvDatabaseURL = "DATABASE_URL"

	// Database URL composition (ADR-0014): the SaaS deployment injects the RDS
	// endpoint and the app role's credentials as separate values (the
	// Secrets-Manager-generated password is never assembled into a URL by
	// hand). Used ONLY when DATABASE_URL is not set — DATABASE_URL wins.
	EnvDBHost     = "DB_HOST"
	EnvDBPort     = "DB_PORT" // default 5432
	EnvDBName     = "DB_NAME"
	EnvDBUser     = "DB_USER"
	EnvDBPassword = "DB_PASSWORD"
	EnvDBSSLMode  = "DB_SSLMODE" // default require

	DefaultDBPort    = "5432"
	DefaultDBSSLMode = "require"

	// Auth / CORS / logging / swagger overrides. An EMPTY value never overrides
	// the file (same rule as STORAGE_*).
	EnvAuthEncryptionKey       = "AUTH_ENCRYPTION_KEY"
	EnvAuthSessionCookieSecure = "AUTH_SESSION_COOKIE_SECURE"
	EnvCORSAllowedOrigins      = "CORS_ALLOWED_ORIGINS" // comma-separated, trimmed
	EnvLogFormat               = "LOG_FORMAT"           // json | text
	EnvLogLevel                = "LOG_LEVEL"            // debug | info | warn | error
	EnvSwaggerEnabled          = "SWAGGER_ENABLED"

	// EnvPublicBaseURL overrides app.publicBaseUrl: the product's public origin
	// ("https://" + the cell's publicHostname), trimmed, trailing slash
	// stripped, validated as an absolute http(s) URL at startup.
	EnvPublicBaseURL = "PUBLIC_BASE_URL"

	// Storage overrides (ADR-0022). An EMPTY value never overrides the file:
	// the compose stack passes empty strings for the unused driver's settings.
	EnvStorageDriver           = "STORAGE_DRIVER"
	EnvStorageFSRoot           = "STORAGE_FS_ROOT"
	EnvStorageMaxUploadBytes   = "STORAGE_MAX_UPLOAD_BYTES"
	EnvStorageS3Bucket         = "STORAGE_S3_BUCKET"
	EnvStorageS3Region         = "STORAGE_S3_REGION"
	EnvStorageS3Endpoint       = "STORAGE_S3_ENDPOINT"
	EnvStorageS3ForcePathStyle = "STORAGE_S3_FORCE_PATH_STYLE"

	// DefaultEnv is what an UNSET APP_ENV means (local `go run ./cmd/server`).
	// An APP_ENV that is set but empty is an error, not this default.
	DefaultEnv = EnvDevelopment

	// configDir holds one file per tier. A cell (staging-eu) loads its tier's
	// file unless the image also ships one named for the cell — configFilePath.
	configDir = "deployment/config_files"
)

// DevelopmentEncryptionKey is the well-known key shipped in
// deployment/config_files/development.json (and the acceptance config). A
// deployed environment must never run with it — validate() compares the
// decoded bytes, so re-encoding the same key does not slip past the check.
// A test asserts this constant matches development.json.
const DevelopmentEncryptionKey = "emVudGF4LWRldi1lbmNyeXB0aW9uLWtleS0zMmJ5dGU="

var cfg *Config

// Load reads the config file APP_ENV resolves to, applies env overrides, and
// fail-closes (ADR-0014): a missing security-critical value is a startup error,
// never a silent default. APP_ENV names a tier (development, staging,
// production) or a cell of one (staging-eu); anything else is refused here
// rather than booting with the wrong rules — see ParseEnvironment.
func Load() (*Config, error) {
	env, err := ParseEnvironment(getEnvVal(EnvVarName, DefaultEnv))
	if err != nil {
		return nil, err
	}

	conf := &Config{}
	conf.EnvName = env.Name

	path := configFilePath(env)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}
	if err := json.Unmarshal(data, conf); err != nil {
		return nil, fmt.Errorf("parse config file %s: %w", path, err)
	}
	if err := applyEnvironment(conf, env); err != nil {
		return nil, fmt.Errorf("invalid config (%s, from %s): %w", env.Name, path, err)
	}

	mergeEnvOverrides(conf)

	if err := conf.validate(); err != nil {
		return nil, fmt.Errorf("invalid config (%s): %w", conf.EnvName, err)
	}

	cfg = conf
	return conf, nil
}

// configFilePath resolves which file an environment loads: the cell's own
// (deployment/config_files/staging-eu.json) when the image ships one, otherwise
// its tier's (staging.json).
//
// The tier file is the normal case and the only one shipped today: cells of a
// tier differ solely in values the deployment injects as environment variables
// (DB_*, AUTH_ENCRYPTION_KEY, CORS_ALLOWED_ORIGINS, PUBLIC_BASE_URL, STORAGE_*),
// so a new cell needs no new file. A per-cell file is an override, never a way
// to change the tier — applyEnvironment refuses that.
func configFilePath(env Environment) string {
	if env.IsCell() {
		cellPath := filepath.Join(configDir, env.Name+".json")
		if _, err := os.Stat(cellPath); err == nil {
			return cellPath
		}
	}
	return filepath.Join(configDir, env.Tier.String()+".json")
}

// applyEnvironment reconciles the file's app.env with APP_ENV and then pins
// app.env to the environment the deployment declared, so every later decision
// (and every error message) names the real environment — staging-eu, not the
// staging.json it was loaded from.
//
// The file may name its tier ("staging" in staging.json), its own cell, or
// nothing at all, but it may never move the environment to another tier: a
// hand-written staging-eu.json saying "env": "development" is exactly how every
// fail-closed rule would get silently switched off in a real cell.
func applyEnvironment(conf *Config, env Environment) error {
	declared := strings.TrimSpace(conf.App.Env)
	if declared != "" {
		fileEnv, err := ParseEnvironment(declared)
		if err != nil {
			return fmt.Errorf("app.env is %q, which is not a known environment; leave it out or set it to %q (%s=%q)",
				declared, env.Name, EnvVarName, env.Name)
		}
		if fileEnv.Tier != env.Tier {
			return fmt.Errorf("app.env is %q (the %s tier) but %s=%q is the %s tier — the tier decides every fail-closed rule (ADR-0014) and the config file must not change it",
				declared, fileEnv.Tier, EnvVarName, env.Name, env.Tier)
		}
	}
	conf.App.Env = env.Name
	return nil
}

// validate fails closed on missing security-critical configuration (ADR-0014).
func (c *Config) validate() error {
	if !c.Tier().Known() {
		return fmt.Errorf("app.env is %q, which is not a known environment: %s", c.App.Env, environmentHint())
	}
	if c.Database.URL == "" {
		return fmt.Errorf("database.url is required (set %s, or %s + %s + %s + %s)",
			EnvDatabaseURL, EnvDBHost, EnvDBName, EnvDBUser, EnvDBPassword)
	}
	if c.App.Port == 0 {
		return fmt.Errorf("app.port is required")
	}
	c.App.PublicBaseURL = normalizePublicBaseURL(c.App.PublicBaseURL)
	if err := validatePublicBaseURL(c.App.PublicBaseURL); err != nil {
		return err
	}
	if c.IsDeployed() {
		if err := c.validateDeployed(); err != nil {
			return err
		}
	}
	c.Storage.applyDefaults()
	if err := c.Storage.validate(); err != nil {
		return fmt.Errorf("invalid storage config: %w", err)
	}
	return nil
}

// validateDeployed holds the fail-closed rules of every deployed tier
// (ADR-0014) — staging and production, and each of their cells (staging-eu,
// production-eu), which reach this the same way because the tier decides, not
// the name. Every violation is reported at once — one boot failure lists
// everything the operator still has to set — and each message names the env var
// to set.
func (c *Config) validateDeployed() error {
	var errs []error

	key, err := c.Auth.DecodeEncryptionKey()
	switch {
	case err != nil:
		errs = append(errs, fmt.Errorf("auth.encryptionKey: %w (set %s to base64 of 32 random bytes or a passphrase of >= %d characters)",
			err, EnvAuthEncryptionKey, MinEncryptionPassphraseLen))
	case isDevelopmentKey(key):
		errs = append(errs, fmt.Errorf("auth.encryptionKey is the development key from development.json — deployed environments need their own (set %s)",
			EnvAuthEncryptionKey))
	}

	if !c.Auth.SessionCookieSecure {
		errs = append(errs, fmt.Errorf("auth.sessionCookieSecure must be true in %s (set %s=true)", c.App.Env, EnvAuthSessionCookieSecure))
	}

	if sslModeDisabled(c.Database.URL) {
		errs = append(errs, fmt.Errorf("database.url must not carry sslmode=disable in %s (set %s with sslmode=require or stronger, or %s)",
			c.App.Env, EnvDatabaseURL, EnvDBSSLMode))
	}

	// go-chi/cors treats an EMPTY AllowedOrigins list as "*" — so an unset list
	// is the same hole as a wildcard, and both are refused.
	if len(c.CORS.AllowedOrigins) == 0 {
		errs = append(errs, fmt.Errorf("cors.allowedOrigins must list the web origin(s) in %s — an empty list allows every origin (set %s, comma-separated)",
			c.App.Env, EnvCORSAllowedOrigins))
	}
	for _, origin := range c.CORS.AllowedOrigins {
		if strings.TrimSpace(origin) == "*" {
			errs = append(errs, fmt.Errorf("cors.allowedOrigins must not contain \"*\" in %s (set %s to the explicit web origin(s))",
				c.App.Env, EnvCORSAllowedOrigins))
			break
		}
	}

	if c.Log == nil || c.Log.Format != "json" {
		errs = append(errs, fmt.Errorf("log.format must be json in %s (set %s=json)", c.App.Env, EnvLogFormat))
	}

	// The public origin is optional (a cell without its custom domain yet has
	// none), but when present it must be https: a Secure session cookie (required
	// above) never travels over http, so an http origin can only be a mistake.
	if c.App.PublicBaseURL != "" && !strings.HasPrefix(c.App.PublicBaseURL, "https://") {
		errs = append(errs, fmt.Errorf("app.publicBaseUrl must use https in %s, got %q (set %s to the https public origin)",
			c.App.Env, c.App.PublicBaseURL, EnvPublicBaseURL))
	}

	return errors.Join(errs...)
}

// normalizePublicBaseURL trims whitespace and strips trailing slashes so
// callers can append "/path" without producing "//path". Empty stays empty.
func normalizePublicBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

// validatePublicBaseURL accepts "" (not configured) or an absolute http(s)
// origin, optionally with a path prefix: scheme + host, no userinfo, query or
// fragment — the parts that would corrupt every link built on top of it.
func validatePublicBaseURL(value string) error {
	if value == "" {
		return nil
	}
	fail := func(reason string) error {
		return fmt.Errorf("app.publicBaseUrl must be an absolute http(s) URL, got %q: %s (set %s, e.g. https://eu.app.zentax.software)",
			value, reason, EnvPublicBaseURL)
	}
	u, err := url.Parse(value)
	if err != nil {
		return fail(err.Error())
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return fail("scheme must be http or https")
	case u.Host == "" || u.Hostname() == "":
		return fail("host is required")
	case u.User != nil:
		return fail("userinfo is not allowed")
	case u.RawQuery != "" || u.ForceQuery:
		return fail("query string is not allowed")
	case u.Fragment != "" || u.RawFragment != "":
		return fail("fragment is not allowed")
	}
	return nil
}

func isDevelopmentKey(key []byte) bool {
	dev, err := base64.StdEncoding.DecodeString(DevelopmentEncryptionKey)
	if err != nil {
		return false
	}
	return bytes.Equal(key, dev)
}

// sslModeDisabled reports whether a postgres URL (or a key=value DSN) turns
// TLS off. An unparseable URL is left to the driver to reject.
func sslModeDisabled(dbURL string) bool {
	if u, err := url.Parse(dbURL); err == nil && u.Scheme != "" {
		return strings.EqualFold(u.Query().Get("sslmode"), "disable")
	}
	for _, kv := range strings.Fields(dbURL) {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "sslmode" && strings.EqualFold(v, "disable") {
			return true
		}
	}
	return false
}

func mergeEnvOverrides(conf *Config) {
	mergeAppEnvOverrides(&conf.App)
	mergeDatabaseEnvOverrides(&conf.Database)
	mergeAuthEnvOverrides(&conf.Auth)
	mergeCORSEnvOverrides(&conf.CORS)
	mergeLogEnvOverrides(conf)
	if val := os.Getenv(EnvSwaggerEnabled); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			conf.Swagger.Enabled = b
		}
	}
	mergeStorageEnvOverrides(&conf.Storage)
}

// mergeAppEnvOverrides applies PUBLIC_BASE_URL over app.publicBaseUrl. An
// empty (or whitespace-only) value never overrides the file — the ECS task
// passes "" while the cell has no public hostname yet.
func mergeAppEnvOverrides(a *AppConfig) {
	if val := normalizePublicBaseURL(os.Getenv(EnvPublicBaseURL)); val != "" {
		a.PublicBaseURL = val
	}
}

// mergeDatabaseEnvOverrides: DATABASE_URL wins; otherwise, when DB_HOST is
// set, the URL is composed from the DB_* parts. A file-provided URL is kept
// when neither is present.
func mergeDatabaseEnvOverrides(d *DatabaseConfig) {
	if val := os.Getenv(EnvDatabaseURL); val != "" {
		d.URL = val
		return
	}
	if composed := composeDatabaseURLFromEnv(); composed != "" {
		d.URL = composed
	}
}

// composeDatabaseURLFromEnv builds postgres://user:pass@host:port/name?sslmode=…
// from DB_HOST / DB_PORT / DB_NAME / DB_USER / DB_PASSWORD / DB_SSLMODE.
// Returns "" when DB_HOST is unset. User, password and database name are
// URL-escaped so a generated password with '@', '/', ':', '%' or spaces survives.
func composeDatabaseURLFromEnv() string {
	host := os.Getenv(EnvDBHost)
	if host == "" {
		return ""
	}
	return ComposeDatabaseURL(
		host,
		getEnvValNonEmpty(EnvDBPort, DefaultDBPort),
		os.Getenv(EnvDBName),
		os.Getenv(EnvDBUser),
		os.Getenv(EnvDBPassword),
		getEnvValNonEmpty(EnvDBSSLMode, DefaultDBSSLMode),
	)
}

// ComposeDatabaseURL assembles a pgx-parseable postgres URL from its parts,
// percent-encoding the userinfo and the database name.
func ComposeDatabaseURL(host, port, name, user, password, sslmode string) string {
	var sb strings.Builder
	sb.WriteString("postgres://")
	if user != "" {
		sb.WriteString(escapeUserinfo(user))
		if password != "" {
			sb.WriteByte(':')
			sb.WriteString(escapeUserinfo(password))
		}
		sb.WriteByte('@')
	}
	sb.WriteString(net.JoinHostPort(host, port))
	sb.WriteByte('/')
	sb.WriteString(url.PathEscape(name))
	if sslmode != "" {
		sb.WriteString("?sslmode=")
		sb.WriteString(url.QueryEscape(sslmode))
	}
	return sb.String()
}

// escapeUserinfo percent-encodes everything but RFC 3986 unreserved
// characters, which url.Parse's userinfo decoding round-trips exactly.
// (url.UserPassword leaves ':' and '@'-adjacent punctuation alone; QueryEscape
// turns spaces into '+', which userinfo decoding does NOT turn back.)
func escapeUserinfo(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func mergeAuthEnvOverrides(a *AuthConfig) {
	if val := os.Getenv(EnvAuthEncryptionKey); val != "" {
		a.EncryptionKey = val
	}
	if val := os.Getenv(EnvAuthSessionCookieSecure); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			a.SessionCookieSecure = b
		}
	}
}

func mergeCORSEnvOverrides(c *CORSConfig) {
	val := os.Getenv(EnvCORSAllowedOrigins)
	if val == "" {
		return
	}
	if origins := SplitOrigins(val); len(origins) > 0 {
		c.AllowedOrigins = origins
	}
}

// SplitOrigins parses a comma-separated origin list, trimming whitespace and
// dropping empty entries.
func SplitOrigins(val string) []string {
	var origins []string
	for _, o := range strings.Split(val, ",") {
		if o = strings.TrimSpace(o); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}

func mergeLogEnvOverrides(conf *Config) {
	format, level := os.Getenv(EnvLogFormat), os.Getenv(EnvLogLevel)
	if format == "" && level == "" {
		return
	}
	if conf.Log == nil {
		conf.Log = &logger.Config{}
	}
	if format != "" {
		conf.Log.Format = format
	}
	if level != "" {
		conf.Log.Level = level
	}
}

// mergeStorageEnvOverrides applies STORAGE_* over the file. Empty values are
// ignored (not "cleared"): `STORAGE_S3_BUCKET=` must not blank a bucket the
// file configured.
func mergeStorageEnvOverrides(s *StorageConfig) {
	setIf := func(env string, dst *string) {
		if val := os.Getenv(env); val != "" {
			*dst = val
		}
	}
	setIf(EnvStorageDriver, &s.Driver)
	setIf(EnvStorageFSRoot, &s.FS.Root)
	setIf(EnvStorageS3Bucket, &s.S3.Bucket)
	setIf(EnvStorageS3Region, &s.S3.Region)
	setIf(EnvStorageS3Endpoint, &s.S3.Endpoint)
	if val := os.Getenv(EnvStorageMaxUploadBytes); val != "" {
		if n, err := strconv.ParseInt(val, 10, 64); err == nil && n > 0 {
			s.MaxUploadBytes = n
		}
	}
	if val := os.Getenv(EnvStorageS3ForcePathStyle); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			s.S3.ForcePathStyle = b
		}
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

// getEnvValNonEmpty is getEnvVal where an empty (but set) value also falls
// back to the default — deployment manifests routinely pass "" for an unused
// knob.
func getEnvValNonEmpty(env, defaultVal string) string {
	if val := os.Getenv(env); val != "" {
		return val
	}
	return defaultVal
}
