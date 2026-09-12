package config

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"
)

// Rate-limit environment overrides. As everywhere else in this package, an
// EMPTY value never overrides the file — deployment manifests routinely pass ""
// for a knob they are not setting.
const (
	// EnvRateLimitEnabled turns the whole thing off (a bool). It exists as an
	// escape hatch for an incident, not as a normal setting.
	EnvRateLimitEnabled = "RATE_LIMIT_ENABLED"

	// EnvRateLimitTrustedProxies is the comma-separated list of CIDRs (or bare
	// addresses) whose X-Forwarded-For is believed. THE security-critical knob
	// of this section: see RateLimitConfig.TrustedProxies.
	EnvRateLimitTrustedProxies  = "RATE_LIMIT_TRUSTED_PROXIES"
	EnvRateLimitForwardedHeader = "RATE_LIMIT_FORWARDED_HEADER"

	EnvRateLimitAnonymousRequests      = "RATE_LIMIT_ANONYMOUS_REQUESTS"
	EnvRateLimitAnonymousBurst         = "RATE_LIMIT_ANONYMOUS_BURST"
	EnvRateLimitAnonymousWindowSeconds = "RATE_LIMIT_ANONYMOUS_WINDOW_SECONDS"

	EnvRateLimitAuthenticatedRequests      = "RATE_LIMIT_AUTHENTICATED_REQUESTS"
	EnvRateLimitAuthenticatedBurst         = "RATE_LIMIT_AUTHENTICATED_BURST"
	EnvRateLimitAuthenticatedWindowSeconds = "RATE_LIMIT_AUTHENTICATED_WINDOW_SECONDS"

	EnvRateLimitVerifyMaxConcurrent = "RATE_LIMIT_VERIFY_MAX_CONCURRENT"
	EnvRateLimitVerifyMaxQueued     = "RATE_LIMIT_VERIFY_MAX_QUEUED"
	EnvRateLimitVerifyMaxWaitMs     = "RATE_LIMIT_VERIFY_MAX_WAIT_MS"
)

// Rate-limit defaults. They are code, not file, so a config file that says
// nothing about rate limiting still boots with the limits on — a security
// control nobody remembered to write down is a security control nobody has.
const (
	// DefaultRateLimitAnonymousRequests is generous against a human and
	// ruinous against a script: a person logging in makes a handful of
	// requests a minute, and the pre-auth surface has nothing that needs more.
	DefaultRateLimitAnonymousRequests      = 60
	DefaultRateLimitAnonymousBurst         = 30
	DefaultRateLimitAnonymousWindowSeconds = 60

	// The authenticated budget is per PRINCIPAL, and a single page of the app
	// legitimately fans out to a dozen endpoints, so it is much larger — its
	// job is to stop a runaway script or a stolen session from monopolising a
	// task, not to police normal use.
	// Raised from 300/120 after measurement: a signed-in principal legitimately
	// walks whole lists a page at a time (an export of the audit trail is pages
	// of 500), and at 120 burst the demo oracle's own run was shed 13 times and
	// took 6 to 30 times longer. The budget has to sit above what first-party
	// tooling issues in a burst, or the only client that survives it is the one
	// that retries.
	DefaultRateLimitAuthenticatedRequests      = 1200
	DefaultRateLimitAuthenticatedBurst         = 600
	DefaultRateLimitAuthenticatedWindowSeconds = 60

	// 4 concurrent argon2id verifications × 64 MiB = 256 MiB of working set,
	// which fits inside the 1 GiB API task with room for everything else.
	DefaultRateLimitVerifyMaxConcurrent = 4
	DefaultRateLimitVerifyMaxQueued     = 16
	DefaultRateLimitVerifyMaxWaitMs     = 500
)

// DefaultTrustedProxies is EMPTY: out of the box no forwarded header is
// believed and every request is charged to its socket peer.
//
// It used to be the whole private space, which measurement showed was a hole
// rather than a convenience. Docker publishes a port through a host gateway
// inside 192.168/16, so any caller that could reach the published port was a
// "trusted proxy" and got its own X-Forwarded-For believed: forty logins with
// a rotating header were shed zero times, while the same forty with no header
// were shed. A budget an attacker can refill by adding one header is not a
// weaker budget, it is no budget.
//
// So trust is now named per deployment, and the operator's side of the bargain
// is that the named proxy APPENDS the peer it saw rather than passing a
// client-supplied value through. In a cell that is the VPC range, which the
// API stack passes from the network stack, and the load balancer appends. In
// the self-host stack it is the web container's network, and the adapter
// appends. Set it with RATE_LIMIT_TRUSTED_PROXIES or rateLimit.trustedProxies.
var DefaultTrustedProxies = []string{}

// DefaultVerificationPaths are the routes whose handler performs a password or
// token verification, and so must hold a slot of the concurrency bound.
//
//   - /auth/login verifies argon2id, and verifies a DUMMY hash for an unknown
//     address so that timing does not disclose which addresses exist — which
//     means the full 64 MiB cost is paid for a caller who supplies no valid
//     identity at all. That is the measured exhaustion lever.
//   - /auth/accept-invite HASHES a new password, the same cost in the same
//     units, and is likewise reachable without a session.
//   - /auth/mfa/verify is cheap per call but is the brute-force surface of a
//     six-digit code; it is bounded here as well so the two shed paths agree.
var DefaultVerificationPaths = []string{
	"/auth/login",
	"/auth/accept-invite",
	"/auth/mfa/verify",
}

// DefaultRateLimitExemptPaths are charged no budget. The health probes are here
// because shedding a liveness check is how a rate limiter talks the
// orchestrator into killing a perfectly healthy task — precisely the outcome
// this whole section exists to prevent.
var DefaultRateLimitExemptPaths = []string{
	"/health",
	"/health/ready",
}

// RateLimitConfig configures the two shed valves (delivery/httpkit/middlewares/
// ratelimit): a bound on concurrent password/token verifications, and per-
// address / per-principal request budgets.
type RateLimitConfig struct {
	// Enabled is a POINTER so "absent" and "explicitly false" stay
	// distinguishable: ApplyDefaults turns an absent value on, while a config
	// that says `"enabled": false` is honoured. A Config assembled in code
	// (the acceptance harness) that never calls ApplyDefaults therefore leaves
	// rate limiting off, which is what a test that bursts deliberately wants.
	Enabled *bool `json:"enabled"`

	// TrustedProxies lists the CIDRs (or bare addresses) whose X-Forwarded-For
	// this service believes. Getting it wrong is total in both directions:
	// believing the header from anywhere lets an attacker mint a fresh budget
	// per request by writing a new address, and believing it from nowhere keys
	// every request behind a load balancer to the balancer's own address, so
	// one burst sheds every customer at once. Absent → DefaultTrustedProxies;
	// an explicitly empty list → trust nothing, key on the socket address.
	TrustedProxies []string `json:"trustedProxies"`

	// ForwardedHeader is the header the proxy appends to (default
	// X-Forwarded-For).
	ForwardedHeader string `json:"forwardedHeader"`

	// Anonymous is charged per client address on routes with no authenticated
	// principal; Authenticated is charged per principal on tenant routes.
	Anonymous     RateLimitRuleConfig `json:"anonymous"`
	Authenticated RateLimitRuleConfig `json:"authenticated"`

	// Verification bounds concurrent password/token verification.
	Verification VerificationLimitConfig `json:"verification"`

	// ExemptPaths are full route paths no budget is charged for.
	ExemptPaths []string `json:"exemptPaths"`
}

// RateLimitRuleConfig is one token-bucket budget: Requests per WindowSeconds,
// with Burst tokens of headroom for traffic that arrives in clumps.
type RateLimitRuleConfig struct {
	Requests      int `json:"requests"`
	Burst         int `json:"burst"`
	WindowSeconds int `json:"windowSeconds"`
}

// VerificationLimitConfig bounds the expensive verifications.
type VerificationLimitConfig struct {
	// MaxConcurrent is the memory bound: this many argon2id verifications may
	// run at once, each holding 64 MiB.
	MaxConcurrent int `json:"maxConcurrent"`
	// MaxQueued caps how many requests may WAIT for a slot. An unbounded queue
	// only moves the exhaustion from argon2's heap to the goroutines and
	// buffers of everyone waiting.
	MaxQueued int `json:"maxQueued"`
	// MaxWaitMs is how long a queued request waits before it is shed.
	MaxWaitMs int `json:"maxWaitMs"`
	// Paths are the full route paths the bound applies to.
	Paths []string `json:"paths"`
}

// Active reports whether rate limiting should be wired. Absent (nil) is NOT
// active — only ApplyDefaults, which Load always runs, turns it on.
func (r RateLimitConfig) Active() bool {
	return r.Enabled != nil && *r.Enabled
}

// Window is the rule's window as a duration.
func (r RateLimitRuleConfig) Window() time.Duration {
	return time.Duration(r.WindowSeconds) * time.Second
}

// MaxWait is the verification queue's wait as a duration.
func (v VerificationLimitConfig) MaxWait() time.Duration {
	return time.Duration(v.MaxWaitMs) * time.Millisecond
}

// ApplyDefaults fills everything the file (or a Config built in code) left
// empty. Exported because the acceptance harness lives in its own module and
// builds a Config by hand; Load calls it for every environment.
func (r *RateLimitConfig) ApplyDefaults() {
	if r.Enabled == nil {
		on := true
		r.Enabled = &on
	}
	if r.TrustedProxies == nil {
		// Non-nil even when the default is empty: absent and explicitly empty
		// mean the same thing now (trust nothing), and the distinction is only
		// kept so a future default cannot silently override an operator's [].
		r.TrustedProxies = append(make([]string, 0, len(DefaultTrustedProxies)), DefaultTrustedProxies...)
	}
	if r.ForwardedHeader == "" {
		r.ForwardedHeader = "X-Forwarded-For"
	}
	r.Anonymous.applyDefaults(
		DefaultRateLimitAnonymousRequests,
		DefaultRateLimitAnonymousBurst,
		DefaultRateLimitAnonymousWindowSeconds,
	)
	r.Authenticated.applyDefaults(
		DefaultRateLimitAuthenticatedRequests,
		DefaultRateLimitAuthenticatedBurst,
		DefaultRateLimitAuthenticatedWindowSeconds,
	)
	if r.Verification.MaxConcurrent <= 0 {
		r.Verification.MaxConcurrent = DefaultRateLimitVerifyMaxConcurrent
	}
	if r.Verification.MaxQueued <= 0 {
		r.Verification.MaxQueued = DefaultRateLimitVerifyMaxQueued
	}
	if r.Verification.MaxWaitMs <= 0 {
		r.Verification.MaxWaitMs = DefaultRateLimitVerifyMaxWaitMs
	}
	if r.Verification.Paths == nil {
		r.Verification.Paths = append([]string(nil), DefaultVerificationPaths...)
	}
	if r.ExemptPaths == nil {
		r.ExemptPaths = append([]string(nil), DefaultRateLimitExemptPaths...)
	}
}

func (r *RateLimitRuleConfig) applyDefaults(requests, burst, windowSeconds int) {
	if r.Requests <= 0 {
		r.Requests = requests
	}
	if r.Burst <= 0 {
		r.Burst = burst
	}
	if r.WindowSeconds <= 0 {
		r.WindowSeconds = windowSeconds
	}
}

// validate refuses a configuration that would silently not limit anything, and
// refuses a trusted-proxy entry that is not an address or a CIDR — the value
// whose typo would otherwise be discovered as "the limiter never fires".
func (r RateLimitConfig) validate() error {
	if !r.Active() {
		return nil
	}

	var errs []error
	if err := r.Anonymous.validate("rateLimit.anonymous"); err != nil {
		errs = append(errs, err)
	}
	if err := r.Authenticated.validate("rateLimit.authenticated"); err != nil {
		errs = append(errs, err)
	}
	if r.Verification.MaxConcurrent <= 0 {
		errs = append(errs, fmt.Errorf(
			"rateLimit.verification.maxConcurrent must be positive (set %s)", EnvRateLimitVerifyMaxConcurrent))
	}
	if r.Verification.MaxQueued < 0 {
		errs = append(errs, fmt.Errorf(
			"rateLimit.verification.maxQueued must not be negative (set %s)", EnvRateLimitVerifyMaxQueued))
	}
	if r.Verification.MaxWaitMs <= 0 {
		errs = append(errs, fmt.Errorf(
			"rateLimit.verification.maxWaitMs must be positive (set %s)", EnvRateLimitVerifyMaxWaitMs))
	}
	if r.ForwardedHeader == "" {
		errs = append(errs, fmt.Errorf(
			"rateLimit.forwardedHeader must not be empty (set %s)", EnvRateLimitForwardedHeader))
	}
	for _, entry := range r.TrustedProxies {
		if err := validateCIDROrAddr(entry); err != nil {
			errs = append(errs, fmt.Errorf("rateLimit.trustedProxies: %w (set %s, comma-separated)",
				err, EnvRateLimitTrustedProxies))
		}
	}
	return errors.Join(errs...)
}

func (r RateLimitRuleConfig) validate(name string) error {
	if r.Requests <= 0 {
		return fmt.Errorf("%s.requests must be positive", name)
	}
	if r.WindowSeconds <= 0 {
		return fmt.Errorf("%s.windowSeconds must be positive", name)
	}
	if r.Burst < 0 {
		return fmt.Errorf("%s.burst must not be negative", name)
	}
	return nil
}

func mergeRateLimitEnvOverrides(r *RateLimitConfig) {
	if val := os.Getenv(EnvRateLimitEnabled); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			r.Enabled = &b
		}
	}
	if val := os.Getenv(EnvRateLimitTrustedProxies); val != "" {
		// SplitOrigins is just "comma-separated, trimmed, empties dropped".
		// The literal "none" is how a deployment says "trust nothing" through
		// an environment variable, where an empty value means "not set".
		if val == "none" {
			r.TrustedProxies = []string{}
		} else if entries := SplitOrigins(val); len(entries) > 0 {
			r.TrustedProxies = entries
		}
	}
	if val := os.Getenv(EnvRateLimitForwardedHeader); val != "" {
		r.ForwardedHeader = val
	}

	setPositiveInt(EnvRateLimitAnonymousRequests, &r.Anonymous.Requests)
	setPositiveInt(EnvRateLimitAnonymousBurst, &r.Anonymous.Burst)
	setPositiveInt(EnvRateLimitAnonymousWindowSeconds, &r.Anonymous.WindowSeconds)

	setPositiveInt(EnvRateLimitAuthenticatedRequests, &r.Authenticated.Requests)
	setPositiveInt(EnvRateLimitAuthenticatedBurst, &r.Authenticated.Burst)
	setPositiveInt(EnvRateLimitAuthenticatedWindowSeconds, &r.Authenticated.WindowSeconds)

	setPositiveInt(EnvRateLimitVerifyMaxConcurrent, &r.Verification.MaxConcurrent)
	setPositiveInt(EnvRateLimitVerifyMaxQueued, &r.Verification.MaxQueued)
	setPositiveInt(EnvRateLimitVerifyMaxWaitMs, &r.Verification.MaxWaitMs)
}

// validateCIDROrAddr mirrors the parsing the ratelimit package does, so a typo
// fails the boot instead of quietly widening the trust boundary. It is
// duplicated rather than imported because the ratelimit package reaches this
// one through httperr, and config importing it back would be a cycle.
func validateCIDROrAddr(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("empty entry")
	}
	if strings.Contains(raw, "/") {
		if _, err := netip.ParsePrefix(raw); err != nil {
			return fmt.Errorf("%q is not a valid CIDR: %w", raw, err)
		}
		return nil
	}
	if _, err := netip.ParseAddr(raw); err != nil {
		return fmt.Errorf("%q is not a valid IP address or CIDR: %w", raw, err)
	}
	return nil
}

func setPositiveInt(env string, dst *int) {
	val := os.Getenv(env)
	if val == "" {
		return
	}
	if n, err := strconv.Atoi(val); err == nil && n > 0 {
		*dst = n
	}
}
