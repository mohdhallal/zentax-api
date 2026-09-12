package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rateLimitBase is a development config — the one tier whose validation is
// limited to the always-on rules, so these tests exercise the rate-limit
// section alone.
func rateLimitBase() *Config {
	return &Config{
		App:      AppConfig{Env: EnvDevelopment, Port: 3000},
		Database: DatabaseConfig{URL: "postgres://x"},
	}
}

// A config file that says nothing about rate limiting must still boot WITH the
// limits: a security control that only exists where someone remembered to write
// it down is a control the next cell ships without.
func TestRateLimit_OmittedSectionIsOnWithDefaults(t *testing.T) {
	c := rateLimitBase()
	require.NoError(t, c.validate())

	require.True(t, c.RateLimit.Active(), "an omitted section must default to ON")
	assert.Equal(t, DefaultRateLimitAnonymousRequests, c.RateLimit.Anonymous.Requests)
	assert.Equal(t, DefaultRateLimitAnonymousBurst, c.RateLimit.Anonymous.Burst)
	assert.Equal(t, time.Minute, c.RateLimit.Anonymous.Window())
	assert.Equal(t, DefaultRateLimitAuthenticatedRequests, c.RateLimit.Authenticated.Requests)
	assert.Equal(t, DefaultRateLimitVerifyMaxConcurrent, c.RateLimit.Verification.MaxConcurrent)
	assert.Equal(t, 500*time.Millisecond, c.RateLimit.Verification.MaxWait())
	assert.Equal(t, DefaultTrustedProxies, c.RateLimit.TrustedProxies)
	assert.Equal(t, DefaultVerificationPaths, c.RateLimit.Verification.Paths)
	assert.Equal(t, DefaultRateLimitExemptPaths, c.RateLimit.ExemptPaths)
	assert.Equal(t, "X-Forwarded-For", c.RateLimit.ForwardedHeader)
}

// The measured exhaustion lever is /auth/login, which pays 64 MiB of argon2
// even for an address that does not exist. It must be gated out of the box.
func TestRateLimit_DefaultsGateTheVerificationRoutes(t *testing.T) {
	c := rateLimitBase()
	require.NoError(t, c.validate())

	assert.Contains(t, c.RateLimit.Verification.Paths, "/auth/login")
	assert.Contains(t, c.RateLimit.Verification.Paths, "/auth/accept-invite")
	assert.Contains(t, c.RateLimit.Verification.Paths, "/auth/mfa/verify")
}

// Shedding a liveness probe is how a rate limiter talks the orchestrator into
// killing a healthy task — the exact failure the section exists to prevent.
func TestRateLimit_HealthProbesAreExemptByDefault(t *testing.T) {
	c := rateLimitBase()
	require.NoError(t, c.validate())

	assert.Contains(t, c.RateLimit.ExemptPaths, "/health")
	assert.Contains(t, c.RateLimit.ExemptPaths, "/health/ready")
}

// "Absent" and "explicitly false" must stay distinguishable, which is why
// Enabled is a pointer: an operator can switch the limits off during an
// incident, and a Config built in code (the acceptance harness) that never
// calls ApplyDefaults starts with them off.
func TestRateLimit_ExplicitFalseIsHonoured(t *testing.T) {
	c := rateLimitBase()
	off := false
	c.RateLimit.Enabled = &off

	require.NoError(t, c.validate())
	assert.False(t, c.RateLimit.Active())
}

func TestRateLimit_UncalledApplyDefaultsLeavesItOff(t *testing.T) {
	t.Parallel()

	var cfg RateLimitConfig
	assert.False(t, cfg.Active())
}

func TestRateLimit_ApplyDefaultsKeepsExplicitValues(t *testing.T) {
	t.Parallel()

	cfg := RateLimitConfig{
		TrustedProxies:  []string{"10.1.2.0/24"},
		ForwardedHeader: "X-Real-IP",
		Anonymous:       RateLimitRuleConfig{Requests: 5, Burst: 2, WindowSeconds: 10},
		Verification:    VerificationLimitConfig{MaxConcurrent: 2, Paths: []string{"/auth/login"}},
		ExemptPaths:     []string{},
	}
	cfg.ApplyDefaults()

	assert.Equal(t, []string{"10.1.2.0/24"}, cfg.TrustedProxies)
	assert.Equal(t, "X-Real-IP", cfg.ForwardedHeader)
	assert.Equal(t, 5, cfg.Anonymous.Requests)
	assert.Equal(t, 2, cfg.Anonymous.Burst)
	assert.Equal(t, 10*time.Second, cfg.Anonymous.Window())
	assert.Equal(t, 2, cfg.Verification.MaxConcurrent)
	assert.Equal(t, []string{"/auth/login"}, cfg.Verification.Paths)
	assert.Empty(t, cfg.ExemptPaths, "an explicitly empty exempt list must not be refilled")
}

// An EXPLICITLY empty trusted list means "trust nothing", and must survive
// defaulting: it is how an operator pins every request to its socket address.
func TestRateLimit_ExplicitlyEmptyTrustedProxiesIsKept(t *testing.T) {
	t.Parallel()

	cfg := RateLimitConfig{TrustedProxies: []string{}}
	cfg.ApplyDefaults()

	assert.Empty(t, cfg.TrustedProxies)
}

// A typo in the trusted-proxy list would otherwise be discovered as "the
// limiter never fires" — or worse, as "the limiter trusts the wrong network".
func TestRateLimit_InvalidTrustedProxyFailsClosed(t *testing.T) {
	c := rateLimitBase()
	c.RateLimit.TrustedProxies = []string{"10.0.0.0/8", "not-a-network"}

	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "trustedProxies")
	assert.Contains(t, err.Error(), EnvRateLimitTrustedProxies)
}

func TestRateLimit_ValidTrustedProxyForms(t *testing.T) {
	c := rateLimitBase()
	c.RateLimit.TrustedProxies = []string{"10.0.0.0/8", "127.0.0.1", "fc00::/7", "::1"}

	assert.NoError(t, c.validate())
}

func TestRateLimit_EnvOverrides(t *testing.T) {
	t.Setenv(EnvRateLimitAnonymousRequests, "7")
	t.Setenv(EnvRateLimitAnonymousBurst, "3")
	t.Setenv(EnvRateLimitAnonymousWindowSeconds, "30")
	t.Setenv(EnvRateLimitAuthenticatedRequests, "900")
	t.Setenv(EnvRateLimitVerifyMaxConcurrent, "2")
	t.Setenv(EnvRateLimitVerifyMaxQueued, "5")
	t.Setenv(EnvRateLimitVerifyMaxWaitMs, "120")
	t.Setenv(EnvRateLimitTrustedProxies, "10.1.0.0/16, 10.2.0.0/16")
	t.Setenv(EnvRateLimitForwardedHeader, "X-Real-IP")

	c := &Config{}
	mergeEnvOverrides(c)

	assert.Equal(t, 7, c.RateLimit.Anonymous.Requests)
	assert.Equal(t, 3, c.RateLimit.Anonymous.Burst)
	assert.Equal(t, 30, c.RateLimit.Anonymous.WindowSeconds)
	assert.Equal(t, 900, c.RateLimit.Authenticated.Requests)
	assert.Equal(t, 2, c.RateLimit.Verification.MaxConcurrent)
	assert.Equal(t, 5, c.RateLimit.Verification.MaxQueued)
	assert.Equal(t, 120, c.RateLimit.Verification.MaxWaitMs)
	assert.Equal(t, []string{"10.1.0.0/16", "10.2.0.0/16"}, c.RateLimit.TrustedProxies)
	assert.Equal(t, "X-Real-IP", c.RateLimit.ForwardedHeader)
}

// An EMPTY environment value never overrides the file (the rule the whole
// package follows — deployment manifests routinely pass "" for an unused knob).
func TestRateLimit_EmptyEnvValuesDoNotOverride(t *testing.T) {
	t.Setenv(EnvRateLimitAnonymousRequests, "")
	t.Setenv(EnvRateLimitTrustedProxies, "")
	t.Setenv(EnvRateLimitEnabled, "")

	on := true
	c := &Config{RateLimit: RateLimitConfig{
		Enabled:        &on,
		TrustedProxies: []string{"10.0.0.0/8"},
		Anonymous:      RateLimitRuleConfig{Requests: 42},
	}}
	mergeEnvOverrides(c)

	assert.Equal(t, 42, c.RateLimit.Anonymous.Requests)
	assert.Equal(t, []string{"10.0.0.0/8"}, c.RateLimit.TrustedProxies)
	assert.True(t, c.RateLimit.Active())
}

// "" means "not set", so a deployment needs a literal to say "trust nothing".
func TestRateLimit_EnvNoneTrustsNothing(t *testing.T) {
	t.Setenv(EnvRateLimitTrustedProxies, "none")

	c := &Config{RateLimit: RateLimitConfig{TrustedProxies: []string{"10.0.0.0/8"}}}
	mergeEnvOverrides(c)
	c.RateLimit.ApplyDefaults()

	assert.Empty(t, c.RateLimit.TrustedProxies,
		"an explicitly empty list must survive ApplyDefaults, or 'trust nothing' is unsayable")
}

func TestRateLimit_EnvCanSwitchItOff(t *testing.T) {
	t.Setenv(EnvRateLimitEnabled, "false")

	c := rateLimitBase()
	mergeEnvOverrides(c)
	require.NoError(t, c.validate())

	assert.False(t, c.RateLimit.Active())
}

func TestRateLimit_NonPositiveRuleFailsClosed(t *testing.T) {
	t.Parallel()

	on := true
	cases := []struct {
		name string
		cfg  RateLimitConfig
		want string
	}{
		{
			name: "anonymous requests",
			cfg: RateLimitConfig{Enabled: &on, ForwardedHeader: "X-Forwarded-For",
				Anonymous:     RateLimitRuleConfig{Requests: 0, WindowSeconds: 60},
				Authenticated: RateLimitRuleConfig{Requests: 10, WindowSeconds: 60},
				Verification:  VerificationLimitConfig{MaxConcurrent: 1, MaxWaitMs: 1}},
			want: "rateLimit.anonymous.requests",
		},
		{
			name: "authenticated window",
			cfg: RateLimitConfig{Enabled: &on, ForwardedHeader: "X-Forwarded-For",
				Anonymous:     RateLimitRuleConfig{Requests: 10, WindowSeconds: 60},
				Authenticated: RateLimitRuleConfig{Requests: 10, WindowSeconds: 0},
				Verification:  VerificationLimitConfig{MaxConcurrent: 1, MaxWaitMs: 1}},
			want: "rateLimit.authenticated.windowSeconds",
		},
		{
			name: "verification concurrency",
			cfg: RateLimitConfig{Enabled: &on, ForwardedHeader: "X-Forwarded-For",
				Anonymous:     RateLimitRuleConfig{Requests: 10, WindowSeconds: 60},
				Authenticated: RateLimitRuleConfig{Requests: 10, WindowSeconds: 60},
				Verification:  VerificationLimitConfig{MaxConcurrent: 0, MaxWaitMs: 1}},
			want: "rateLimit.verification.maxConcurrent",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.cfg.validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// A disabled section is never validated: switching the limits off during an
// incident must not also be a boot failure.
func TestRateLimit_DisabledSkipsValidation(t *testing.T) {
	t.Parallel()

	cfg := RateLimitConfig{TrustedProxies: []string{"nonsense"}}
	assert.NoError(t, cfg.validate())
}
