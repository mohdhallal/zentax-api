package main

// How verify records and renders what it found.
//
// One check = one endpoint with one filter (or one named plumbing assertion).
// A check collects differences; a difference names the field, the offending
// key (a heatmap cell, an instance, a group bucket) and the two values, so a
// failing run says what is wrong rather than that something is.

import (
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"sort"
	"strings"
	"time"

	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// Difference levels. INFO never fails the run: it is used for the dataset's
// own static tables, which are a human cross-check — the recomputed oracle is
// the authority (--strict-static promotes them to failures).
const (
	verifyLevelFail = "fail"
	verifyLevelInfo = "info"
)

// Check outcomes.
const (
	verifyOutcomePass  = "pass"
	verifyOutcomeFail  = "fail"
	verifyOutcomeSkip  = "skip"
	verifyOutcomeError = "error"
)

type verifyDiff struct {
	Tenant   string `json:"tenant"`
	Check    string `json:"check"`
	Filter   string `json:"filter,omitempty"`
	Key      string `json:"key,omitempty"`
	Field    string `json:"field"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Level    string `json:"level"`
}

// String renders one difference as a single readable line.
func (d verifyDiff) String() string {
	var b strings.Builder
	b.WriteString(d.Tenant)
	b.WriteString(" ")
	b.WriteString(d.Check)
	if d.Filter != "" {
		b.WriteString(" [" + d.Filter + "]")
	}
	if d.Key != "" {
		b.WriteString(" " + d.Key)
	}
	b.WriteString(": ")
	b.WriteString(d.Field)
	b.WriteString(" expected ")
	b.WriteString(d.Expected)
	b.WriteString(", got ")
	b.WriteString(d.Actual)
	return b.String()
}

// verifyMaxDiffsPerCheck caps how many differences one check spells out. A
// broken shared rule can miss on every row; the first few say what it is, and
// the suppressed count says how far it goes.
const verifyMaxDiffsPerCheck = 25

// verifyCheck is one endpoint+filter comparison.
type verifyCheck struct {
	Tenant  string       `json:"tenant"`
	Name    string       `json:"check"`
	Filter  string       `json:"filter,omitempty"`
	Outcome string       `json:"outcome"`
	Reason  string       `json:"reason,omitempty"`
	Diffs   []verifyDiff `json:"differences,omitempty"`
	// Suppressed counts differences past the per-check cap.
	Suppressed int `json:"suppressedDifferences,omitempty"`

	report *verifyReport
}

// verifyTenantInfo records the ground the run stood on for one tenant.
type verifyTenantInfo struct {
	Key            string `json:"key"`
	Slug           string `json:"slug"`
	Timezone       string `json:"timezone"`
	Today          string `json:"today"`
	TodayOverride  bool   `json:"todayOverridden"`
	Instances      int    `json:"instancesExpected"`
	LiveInstances  int    `json:"instancesLive"`
	SeedDatesFound string `json:"workflowCreatedDates,omitempty"`
}

// verifyReport is the whole run.
type verifyReport struct {
	Tenants []verifyTenantInfo `json:"tenants"`
	Checks  []*verifyCheck     `json:"checks"`
	// StrictStatic records whether static-table drift failed the run.
	StrictStatic bool `json:"strictStatic"`
}

func (r *verifyReport) check(tenant, name, filter string) *verifyCheck {
	c := &verifyCheck{Tenant: tenant, Name: name, Filter: filter, Outcome: verifyOutcomePass, report: r}
	r.Checks = append(r.Checks, c)
	return c
}

// diff records a failing difference.
func (c *verifyCheck) diff(field, key string, expected, actual any) {
	c.record(verifyLevelFail, field, key, expected, actual)
}

// info records a non-failing difference (static-table drift).
func (c *verifyCheck) info(field, key string, expected, actual any) {
	c.record(verifyLevelInfo, field, key, expected, actual)
}

func (c *verifyCheck) record(level, field, key string, expected, actual any) {
	if level == verifyLevelFail && c.Outcome == verifyOutcomePass {
		c.Outcome = verifyOutcomeFail
	}
	if len(c.Diffs) >= verifyMaxDiffsPerCheck {
		c.Suppressed++
		return
	}
	c.Diffs = append(c.Diffs, verifyDiff{
		Tenant: c.Tenant, Check: c.Name, Filter: c.Filter, Key: key, Field: field,
		Expected: verifyValue(expected), Actual: verifyValue(actual), Level: level,
	})
}

// equal records a difference unless the two values render identically. It
// returns whether they matched, so a caller can stop drilling down.
func (c *verifyCheck) equal(field, key string, expected, actual any) bool {
	if verifyValue(expected) == verifyValue(actual) {
		return true
	}
	c.diff(field, key, expected, actual)
	return false
}

// errf marks the check as failed for a reason that is not a value difference
// (a request that did not answer, a key that does not resolve).
func (c *verifyCheck) errf(format string, args ...any) {
	c.Outcome = verifyOutcomeError
	reason := fmt.Sprintf(format, args...)
	if c.Reason == "" {
		c.Reason = reason
	} else {
		c.Reason += "; " + reason
	}
}

// skip records a check that could not run and must not count as a pass.
func (c *verifyCheck) skip(format string, args ...any) {
	if c.Outcome == verifyOutcomePass {
		c.Outcome = verifyOutcomeSkip
	}
	c.Reason = fmt.Sprintf(format, args...)
}

func (c *verifyCheck) failed() bool {
	return c.Outcome == verifyOutcomeFail || c.Outcome == verifyOutcomeError
}

// verifyValue renders any expected/actual value the same way on both sides,
// so a comparison is a string comparison and a difference line is readable.
func verifyValue(v any) string {
	switch x := v.(type) {
	case nil:
		return "null"
	case string:
		return x
	case dateonly.Date:
		if x.IsZero() {
			return "null"
		}
		return x.String()
	case *dateonly.Date:
		if x == nil || x.IsZero() {
			return "null"
		}
		return x.String()
	case *string:
		if x == nil {
			return "null"
		}
		return *x
	case time.Time:
		if x.IsZero() {
			return "null"
		}
		return x.UTC().Format(time.RFC3339Nano)
	case *time.Time:
		if x == nil {
			return "null"
		}
		return x.UTC().Format(time.RFC3339Nano)
	case *big.Rat:
		if x == nil {
			return "null"
		}
		return oracleRatString(x)
	case json.Number:
		return x.String()
	case bool:
		return fmt.Sprintf("%t", x)
	case int:
		return fmt.Sprintf("%d", x)
	case []string:
		return "[" + strings.Join(x, ", ") + "]"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// verifySummary counts the run.
type verifySummary struct {
	Checks   int `json:"checks"`
	Passed   int `json:"passed"`
	Failed   int `json:"failed"`
	Skipped  int `json:"skipped"`
	Diffs    int `json:"differences"`
	InfoOnly int `json:"infoDifferences"`
}

func (r *verifyReport) summary() verifySummary {
	var s verifySummary
	for _, c := range r.Checks {
		s.Checks++
		switch {
		case c.failed():
			s.Failed++
		case c.Outcome == verifyOutcomeSkip:
			s.Skipped++
		default:
			s.Passed++
		}
		for _, d := range c.Diffs {
			if d.Level == verifyLevelInfo {
				s.InfoOnly++
			} else {
				s.Diffs++
			}
		}
		s.Diffs += c.Suppressed
	}
	return s
}

// diffs returns every difference of a level, in check order.
func (r *verifyReport) diffs(level string) []verifyDiff {
	out := []verifyDiff{}
	for _, c := range r.Checks {
		for _, d := range c.Diffs {
			if d.Level == level {
				out = append(out, d)
			}
		}
	}
	return out
}

// writeText prints the human report: the ground each tenant stood on, every
// difference, then the counts.
func (r *verifyReport) writeText(w io.Writer) {
	for _, t := range r.Tenants {
		suffix := ""
		if t.TodayOverride {
			suffix = " (--today override)"
		}
		fmt.Fprintf(w, "tenant %-8s slug=%-10s zone=%-18s today=%s%s  instances: expected %d, live %d\n",
			t.Key, t.Slug, t.Timezone, t.Today, suffix, t.Instances, t.LiveInstances)
	}

	failures := r.diffs(verifyLevelFail)
	infos := r.diffs(verifyLevelInfo)

	if len(infos) > 0 {
		fmt.Fprintf(w, "\nINFO — the dataset's static expectedAsOf table differs from the recomputed oracle (%d):\n", len(infos))
		for _, d := range infos {
			fmt.Fprintf(w, "  %s\n", d)
		}
		if r.StrictStatic {
			fmt.Fprintln(w, "  (--strict-static: these count as failures)")
		}
	}

	if len(failures) > 0 {
		fmt.Fprintf(w, "\nDIFFERENCES (%d):\n", len(failures))
		for _, d := range failures {
			fmt.Fprintf(w, "  %s\n", d)
		}
		for _, c := range r.Checks {
			if c.Suppressed > 0 {
				fmt.Fprintf(w, "  %s %s [%s]: +%d further differences not listed\n",
					c.Tenant, c.Name, c.Filter, c.Suppressed)
			}
		}
	}

	errored := []*verifyCheck{}
	skipped := []*verifyCheck{}
	for _, c := range r.Checks {
		if c.Outcome == verifyOutcomeError {
			errored = append(errored, c)
		}
		if c.Outcome == verifyOutcomeSkip {
			skipped = append(skipped, c)
		}
	}
	if len(errored) > 0 {
		fmt.Fprintf(w, "\nCHECKS THAT COULD NOT RUN (%d):\n", len(errored))
		for _, c := range errored {
			fmt.Fprintf(w, "  %s %s [%s]: %s\n", c.Tenant, c.Name, c.Filter, c.Reason)
		}
	}
	if len(skipped) > 0 {
		fmt.Fprintf(w, "\nSKIPPED (%d):\n", len(skipped))
		for _, c := range skipped {
			fmt.Fprintf(w, "  %s %s [%s]: %s\n", c.Tenant, c.Name, c.Filter, c.Reason)
		}
	}

	s := r.summary()
	fmt.Fprintf(w, "\n%d checks, %d passed, %d failed, %d skipped — %d differences",
		s.Checks, s.Passed, s.Failed, s.Skipped, s.Diffs)
	if s.InfoOnly > 0 {
		fmt.Fprintf(w, " (+%d informational)", s.InfoOnly)
	}
	fmt.Fprintln(w)
}

// writeJSON prints the machine-readable result.
func (r *verifyReport) writeJSON(w io.Writer) error {
	payload := struct {
		Summary verifySummary      `json:"summary"`
		Tenants []verifyTenantInfo `json:"tenants"`
		Checks  []*verifyCheck     `json:"checks"`
	}{Summary: r.summary(), Tenants: r.Tenants, Checks: r.Checks}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}

// verifySortedStrings is a small helper for stable difference keys.
func verifySortedStrings(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
