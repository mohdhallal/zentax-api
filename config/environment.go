package config

import (
	"fmt"
	"strings"
)

// Tier is the security class of an environment, and the only thing the
// fail-closed rules of ADR-0014 key off.
//
// A tier is deliberately NOT the same thing as an environment name. The SaaS
// deployment runs cells named <tier>-<regionLabel> (ADR-0024 / ADR-0025:
// staging-eu, production-eu) and passes the cell name as APP_ENV, because every
// other deployed name — cluster, VPC, ECR repository, log group, secret, Cloud
// Map namespace, CloudFormation export — is derived from it. Validation must
// follow the tier instead: staging-eu is the staging tier and takes the full
// deployed validation path, exactly as a bare "staging" does.
type Tier string

const (
	TierDevelopment Tier = "development"
	TierStaging     Tier = "staging"
	TierProduction  Tier = "production"
)

// Tiers is the closed set of shipped tiers, in the order errors list them. An
// environment whose tier is not one of these is refused at startup
// (ParseEnvironment) rather than guessed at.
var Tiers = []Tier{TierDevelopment, TierStaging, TierProduction}

// Known reports whether t is one of the shipped tiers.
func (t Tier) Known() bool {
	for _, known := range Tiers {
		if t == known {
			return true
		}
	}
	return false
}

// IsDeployed reports whether the ADR-0014 fail-closed rules apply to this tier.
//
// It is written as "anything that is not development" on purpose. The zero Tier
// — what an unparseable environment yields — is therefore deployed, so an
// environment nobody recognises costs a refused boot rather than a silently
// unvalidated one (development encryption key, non-Secure cookie, plaintext
// database connection, wildcard CORS). A tier added later is deployed until
// someone deliberately says otherwise.
func (t Tier) IsDeployed() bool { return t != TierDevelopment }

func (t Tier) String() string { return string(t) }

// Environment is a parsed APP_ENV value: the tier that decides validation, plus
// the optional cell label that distinguishes deployments of the same tier.
//
//	development → {Tier: development, Cell: ""}
//	staging     → {Tier: staging,     Cell: ""}
//	staging-eu  → {Tier: staging,     Cell: "eu"}
type Environment struct {
	// Name is the whole APP_ENV value, whitespace-trimmed: what every deployed
	// resource is named after, and what app.env is pinned to.
	Name string
	// Tier decides which validation rules run. Never read from the config file.
	Tier Tier
	// Cell is the label after the tier ("eu"), empty for a bare tier.
	Cell string
}

// IsCell reports whether this environment is one cell of a tier rather than the
// bare tier.
func (e Environment) IsCell() bool { return e.Cell != "" }

// maxCellLabelLen bounds the cell label. It is a path component (a cell may
// ship its own config file) and part of every deployed resource name, so it
// stays short and boring.
const maxCellLabelLen = 32

// ParseEnvironment splits an APP_ENV value into its tier and optional cell
// label: "production" → {production, ""}, "staging-eu" → {staging, "eu"}.
//
// It is the single gate on what APP_ENV may be. The tier must be one of Tiers
// and the cell label must be lowercase alphanumerics separated by single
// dashes, which also makes the name safe as a file name: neither "..", nor a
// path separator, nor an absolute path can survive it.
//
// An unknown or malformed value is an error — never a guess and never a
// fallback to development, which is the one tier with no fail-closed rules.
func ParseEnvironment(name string) (Environment, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return Environment{}, fmt.Errorf("%s is empty: %s", EnvVarName, environmentHint())
	}

	tierName, cell, hasCell := strings.Cut(trimmed, "-")
	tier := Tier(tierName)
	if !tier.Known() {
		return Environment{}, fmt.Errorf("%s=%q is not a known environment: %s", EnvVarName, name, environmentHint())
	}
	if hasCell && !validCellLabel(cell) {
		return Environment{}, fmt.Errorf(
			"%s=%q has a malformed cell label %q: lowercase letters, digits and single inner dashes only, at most %d characters (e.g. %q)",
			EnvVarName, name, cell, maxCellLabelLen, "staging-eu")
	}
	return Environment{Name: trimmed, Tier: tier, Cell: cell}, nil
}

// environmentHint is the one sentence every environment error ends with.
func environmentHint() string {
	names := make([]string, 0, len(Tiers))
	for _, t := range Tiers {
		names = append(names, t.String())
	}
	return fmt.Sprintf("set %s to a tier (%s) or to a cell of one, <tier>-<label> (e.g. staging-eu)",
		EnvVarName, strings.Join(names, ", "))
}

// validCellLabel accepts lowercase alphanumeric groups joined by single dashes
// ("eu", "eu-west", "us2"); it rejects the empty string, leading/trailing or
// doubled dashes, uppercase, underscores, dots and anything else.
func validCellLabel(label string) bool {
	if label == "" || len(label) > maxCellLabelLen {
		return false
	}
	prevDash := true // a leading dash (and, further in, a doubled one) is invalid
	for i := 0; i < len(label); i++ {
		switch ch := label[i]; {
		case ch >= 'a' && ch <= 'z', ch >= '0' && ch <= '9':
			prevDash = false
		case ch == '-':
			if prevDash {
				return false
			}
			prevDash = true
		default:
			return false
		}
	}
	return !prevDash // no trailing dash
}
