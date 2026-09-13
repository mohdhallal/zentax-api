package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The window is a promise about personal data, so the interesting cases are the
// ones where a config file or an environment variable says nothing, says
// something out of range, or says something that quietly means nothing.

func TestSecurityEvents_OmittedSectionStillGetsTheWindow(t *testing.T) {
	c := validBase()
	require.NoError(t, c.validate())

	assert.True(t, c.SecurityEvents.RetentionActive(),
		"an environment that says nothing about retention must still run it — a control that only exists where someone wrote it down is one the next cell ships without")
	assert.Equal(t, DefaultSecurityEventsRetentionMonths, c.SecurityEvents.RetentionMonths)
	assert.Equal(t, DefaultSecurityEventsPartitionMonthsAhead, c.SecurityEvents.PartitionMonthsAhead)
}

func TestSecurityEvents_AnExplicitFalseIsHonoured(t *testing.T) {
	c := validBase()
	off := false
	c.SecurityEvents.RetentionEnabled = &off
	require.NoError(t, c.validate())
	assert.False(t, c.SecurityEvents.RetentionActive())
}

// The floor exists so that shortening the window is a decision somebody has to
// argue for, not a typo. Twelve months is a SOC 2 audit period; a stream that
// cannot cover the period being audited is not evidence of anything.
func TestSecurityEvents_AWindowUnderTheFloorFailsClosed(t *testing.T) {
	c := validBase()
	c.SecurityEvents.RetentionMonths = MinSecurityEventsRetentionMonths - 1
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "securityEvents.retentionMonths")
	assert.Contains(t, err.Error(), EnvSecurityEventsRetentionMonths,
		"the message must name the environment variable the operator has to change")
}

func TestSecurityEvents_ANegativeWindowIsRefusedRatherThanDefaulted(t *testing.T) {
	c := validBase()
	c.SecurityEvents.RetentionMonths = -6
	err := c.validate()
	require.Error(t, err,
		"a negative window is a value somebody meant, wrongly; answering it with the default would hide the misconfiguration")
}

// The other direction matters too: a window of decades turns "bounded by
// retention, not by obfuscation" — the product's stated justification for
// keeping client_ip at all — into "bounded by nothing".
func TestSecurityEvents_AWindowBeyondTheCeilingFailsClosed(t *testing.T) {
	c := validBase()
	c.SecurityEvents.RetentionMonths = MaxSecurityEventsRetentionMonths + 1
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "securityEvents.retentionMonths")
}

func TestSecurityEvents_ARunAheadOfNothingFailsClosed(t *testing.T) {
	c := validBase()
	c.SecurityEvents.PartitionMonthsAhead = -1
	err := c.validate()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "securityEvents.partitionMonthsAhead")
}

// A retention window that is switched OFF is not validated, for the same reason
// the audit export's is not: there is nothing to hold to a floor.
func TestSecurityEvents_DisabledRetentionIsNotHeldToTheFloor(t *testing.T) {
	c := validBase()
	off := false
	c.SecurityEvents.RetentionEnabled = &off
	c.SecurityEvents.RetentionMonths = 1
	require.NoError(t, c.validate())
}

// The env override writes an out-of-range window THROUGH rather than dropping
// it, so the boot fails and names it. The alternative — silently keeping the
// default — would mean "SECURITY_EVENTS_RETENTION_MONTHS=6 is being honoured"
// and "it is being ignored" look identical to the operator, and they are two
// different promises about personal data.
func TestSecurityEvents_AnOutOfRangeOverrideIsRefusedNotIgnored(t *testing.T) {
	t.Setenv(EnvSecurityEventsRetentionMonths, "6")

	c := validBase()
	mergeSecurityEventsEnvOverrides(&c.SecurityEvents)
	require.Equal(t, 6, c.SecurityEvents.RetentionMonths, "the override must reach the config")

	err := c.validate()
	require.Error(t, err, "and then be refused by name")
	assert.Contains(t, err.Error(), EnvSecurityEventsRetentionMonths)
}

func TestSecurityEvents_EnvCanSwitchRetentionOffAndBackOn(t *testing.T) {
	t.Setenv(EnvSecurityEventsRetentionEnabled, "false")
	c := validBase()
	mergeSecurityEventsEnvOverrides(&c.SecurityEvents)
	require.NoError(t, c.validate())
	assert.False(t, c.SecurityEvents.RetentionActive())

	t.Setenv(EnvSecurityEventsRetentionEnabled, "true")
	d := validBase()
	mergeSecurityEventsEnvOverrides(&d.SecurityEvents)
	require.NoError(t, d.validate())
	assert.True(t, d.SecurityEvents.RetentionActive())
	assert.Equal(t, DefaultSecurityEventsRetentionMonths, d.SecurityEvents.RetentionMonths)
}

// An EMPTY value never overrides the file — the rule every other section here
// follows, and the one the compose stack depends on when it passes "" for a
// setting the chosen edition does not use.
func TestSecurityEvents_AnEmptyOverrideChangesNothing(t *testing.T) {
	t.Setenv(EnvSecurityEventsRetentionMonths, "")
	t.Setenv(EnvSecurityEventsRetentionEnabled, "")

	c := validBase()
	c.SecurityEvents.RetentionMonths = 24
	mergeSecurityEventsEnvOverrides(&c.SecurityEvents)
	require.NoError(t, c.validate())
	assert.Equal(t, 24, c.SecurityEvents.RetentionMonths)
	assert.True(t, c.SecurityEvents.RetentionActive())
}

// The records say a bad window refuses the boot. That is only true if an
// unreadable value reaches validation instead of being dropped for the default:
// a typo that silently keeps thirteen months would leave every record claiming
// a promise about personal data that nobody chose.
func TestSecurityEvents_AnUnreadableWindowRefusesTheBoot(t *testing.T) {
	for _, val := range []string{"thirteen", "13months", "0", "1.5"} {
		t.Run(val, func(t *testing.T) {
			t.Setenv(EnvSecurityEventsRetentionMonths, val)
			s := SecurityEventsConfig{}
			s.ApplyDefaults()
			mergeSecurityEventsEnvOverrides(&s)
			err := s.validate()
			require.Error(t, err, "an unreadable window must not fall back to the default")
			assert.Contains(t, err.Error(), EnvSecurityEventsRetentionMonths)
		})
	}
}
