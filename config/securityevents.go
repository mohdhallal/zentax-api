package config

import (
	"fmt"
	"os"
	"strconv"
)

// Security-event retention overrides. An EMPTY value never overrides the file —
// the same rule STORAGE_* and AUDIT_EXPORT_* follow.
const (
	EnvSecurityEventsRetentionEnabled     = "SECURITY_EVENTS_RETENTION_ENABLED"
	EnvSecurityEventsRetentionMonths      = "SECURITY_EVENTS_RETENTION_MONTHS"
	EnvSecurityEventsPartitionMonthsAhead = "SECURITY_EVENTS_PARTITION_MONTHS_AHEAD"
)

const (
	// DefaultSecurityEventsRetentionMonths is the window ADR-0007 fixed for the
	// authentication stream: a SOC 2 audit period plus a month of overlap. It is
	// deliberately NOT the audit trail's window — the domain trail is evidence a
	// customer is legally obliged to be able to produce for years, an
	// authentication record is operational security evidence whose value decays
	// — and it is the only thing that bounds how long client_ip exists at all.
	//
	// Partitions are monthly, so a month can only be aged out whole: this number
	// is a FLOOR, not an exact age. Nothing is destroyed before it is thirteen
	// months old, and a row lives thirteen to fourteen months depending on where
	// in its month it fell. The rounding is in the direction of keeping.
	DefaultSecurityEventsRetentionMonths = 13

	// MinSecurityEventsRetentionMonths is the shortest window this product will
	// run with. A SOC 2 audit period is twelve months, and an authentication
	// stream that cannot cover the period being audited is not evidence of
	// anything; thirteen is that period plus the overlap. An operator may keep
	// longer (their disk, their call) and may shave the overlap month, but not
	// go under the period itself. The same floor is enforced in the schema
	// (SQLSTATE ZT032) — this copy is what turns a bad environment variable into
	// a refusal to boot rather than a job that fails every six hours.
	MinSecurityEventsRetentionMonths = 12

	// MaxSecurityEventsRetentionMonths guards the other direction: a decade of
	// authentication events is the audit trail's obligation, not this stream's,
	// and a window that large quietly turns "bounded by retention, not by
	// obfuscation" into "bounded by nothing" for the one personal datum on the
	// row.
	MaxSecurityEventsRetentionMonths = 120

	// DefaultSecurityEventsPartitionMonthsAhead is how far a maintenance run
	// creates forward. Three months matches deployment/docker/migrate.sh's
	// create-ahead for every other time-partitioned table; the job runs far more
	// often than a deploy does, so the run-ahead is slack, not the mechanism.
	DefaultSecurityEventsPartitionMonthsAhead = 3

	// MaxSecurityEventsPartitionMonthsAhead — the schema refuses more than this
	// too; it is a run-ahead, not a capacity plan.
	MaxSecurityEventsPartitionMonthsAhead = 60
)

// SecurityEventsConfig configures the retention of ADR-0008 stream 2 — the
// scheduled partition maintenance that is the only thing standing between the
// thirteen-month promise in ADR-0007 and a store that grows without bound.
type SecurityEventsConfig struct {
	// RetentionEnabled is a POINTER so "absent" and "explicitly false" stay
	// distinguishable, exactly as AuditExportConfig.Enabled is. ApplyDefaults
	// turns an absent value ON — a retention control that only runs where
	// somebody remembered to write it down is a control the next cell ships
	// without — while a config that says `"retentionEnabled": false` is
	// honoured. A Config assembled in code that never calls ApplyDefaults (the
	// acceptance harness) therefore leaves it OFF, which is what a test suite
	// that must not drop partitions out from under itself wants.
	RetentionEnabled *bool `json:"retentionEnabled"`

	// RetentionMonths is the window. It is a promise about personal data, so it
	// is bounded at both ends rather than trusted.
	RetentionMonths int `json:"retentionMonths"`

	// PartitionMonthsAhead is how far ahead the same run creates. It is part of
	// retention rather than a separate concern: a month with no partition of its
	// own lands in security_events_default, which retention must never drop, and
	// its rows are then beyond the reach of the window forever.
	PartitionMonthsAhead int `json:"partitionMonthsAhead"`
}

// RetentionActive reports whether the maintenance job should be registered.
func (s SecurityEventsConfig) RetentionActive() bool {
	return s.RetentionEnabled != nil && *s.RetentionEnabled
}

// ApplyDefaults fills what an omitted `securityEvents` section leaves empty, and
// turns retention ON.
//
// The comparisons are against zero (absent) rather than "not positive": a
// NEGATIVE window is a value somebody meant, wrongly, and defaulting it would
// answer a misconfiguration by quietly doing something else. validate refuses it
// by name instead.
func (s *SecurityEventsConfig) ApplyDefaults() {
	if s.RetentionEnabled == nil {
		on := true
		s.RetentionEnabled = &on
	}
	if s.RetentionMonths == 0 {
		s.RetentionMonths = DefaultSecurityEventsRetentionMonths
	}
	if s.PartitionMonthsAhead == 0 {
		s.PartitionMonthsAhead = DefaultSecurityEventsPartitionMonthsAhead
	}
}

// invalidRetentionMonths marks a retention window the environment supplied and
// the loader could not read. It is deliberately far outside the accepted range
// so validate() reports it by the same path as any other bad window.
const invalidRetentionMonths = -1

func (s SecurityEventsConfig) validate() error {
	if !s.RetentionActive() {
		return nil
	}
	if s.RetentionMonths == invalidRetentionMonths {
		return fmt.Errorf("securityEvents.retentionMonths is not a number of months: %s must be an integer between %d and %d",
			EnvSecurityEventsRetentionMonths, MinSecurityEventsRetentionMonths, MaxSecurityEventsRetentionMonths)
	}
	if s.RetentionMonths < MinSecurityEventsRetentionMonths {
		return fmt.Errorf("securityEvents.retentionMonths must be at least %d — a SOC 2 audit period is twelve months, and an authentication stream that cannot cover the period being audited is not evidence of anything (got %d; set %s)",
			MinSecurityEventsRetentionMonths, s.RetentionMonths, EnvSecurityEventsRetentionMonths)
	}
	if s.RetentionMonths > MaxSecurityEventsRetentionMonths {
		return fmt.Errorf("securityEvents.retentionMonths must be at most %d — the statutory decade is the audit trail's obligation, not the authentication stream's, and a window that large leaves client_ip bounded by nothing (got %d; set %s)",
			MaxSecurityEventsRetentionMonths, s.RetentionMonths, EnvSecurityEventsRetentionMonths)
	}
	if s.PartitionMonthsAhead < 1 || s.PartitionMonthsAhead > MaxSecurityEventsPartitionMonthsAhead {
		return fmt.Errorf("securityEvents.partitionMonthsAhead must be between 1 and %d (got %d; set %s)",
			MaxSecurityEventsPartitionMonthsAhead, s.PartitionMonthsAhead, EnvSecurityEventsPartitionMonthsAhead)
	}
	return nil
}

// mergeSecurityEventsEnvOverrides applies SECURITY_EVENTS_* over the file.
//
// The window is written through EXACTLY AS PARSED, out-of-range values included,
// rather than through setPositiveInt — which drops a value it dislikes and
// leaves the default standing. For a cadence that is harmless; for a retention
// window, "SECURITY_EVENTS_RETENTION_MONTHS=6 is being honoured" and "it is
// being ignored" are two different promises about personal data, and the
// operator must be told which one they got. validate() refuses the boot.
func mergeSecurityEventsEnvOverrides(s *SecurityEventsConfig) {
	if val := os.Getenv(EnvSecurityEventsRetentionEnabled); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			s.RetentionEnabled = &b
		}
	}
	if val := os.Getenv(EnvSecurityEventsRetentionMonths); val != "" {
		// A value nobody can parse must not fall back to the default and leave
		// the records claiming a window the operator did not choose: the
		// retention is a promise about personal data, so a typo here is a
		// refused boot, not a silent thirteen months. An unparseable value and
		// a zero both become a sentinel that validate() rejects by name.
		n, err := strconv.Atoi(val)
		if err != nil || n == 0 {
			n = invalidRetentionMonths
		}
		s.RetentionMonths = n
	}
	if val := os.Getenv(EnvSecurityEventsPartitionMonthsAhead); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n != 0 {
			s.PartitionMonthsAhead = n
		}
	}
}
