package scale

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// testAsOf pins the generation day so the band statistics below are stable.
var testAsOf = dateonly.New(2026, 9, 8)

func testConfig() Config { return Config{AsOf: testAsOf} }

func generate(t *testing.T, cfg Config) *Result {
	t.Helper()
	res, err := Generate(cfg)
	require.NoError(t, err)
	return res
}

// The fixture is a function of its Config: two runs are structurally
// identical, and a different seed is a different world.
func TestGenerateIsDeterministic(t *testing.T) {
	a := generate(t, testConfig())
	b := generate(t, testConfig())
	require.True(t, reflect.DeepEqual(a.Spec, b.Spec), "same seed must produce the same spec")
	require.True(t, reflect.DeepEqual(a.Plan, b.Plan), "same seed must produce the same plan")
	require.Equal(t, a.Stats, b.Stats)

	// JSON as well, since that is how a spec travels.
	ja, err := json.Marshal(a.Spec)
	require.NoError(t, err)
	jb, err := json.Marshal(b.Spec)
	require.NoError(t, err)
	assert.Equal(t, ja, jb)

	other := generate(t, Config{Seed: 2, AsOf: testAsOf})
	assert.NotEqual(t, a.Stats.ByStatus, other.Stats.ByStatus, "a different seed draws different states")
	assert.Equal(t, a.Stats.Instances, other.Stats.Instances, "but the same shape")
}

// The default configuration is the plan's headline number.
func TestDefaultCounts(t *testing.T) {
	res := generate(t, testConfig())
	assert.Equal(t, 48, len(res.Tenant.Entities))
	assert.Equal(t, 5, len(res.Tenant.ObligationTypes))
	assert.Equal(t, 240, len(res.Tenant.EntityObligations))
	assert.Equal(t, 12, len(res.Tenant.AllUsers()))

	assert.Equal(t, 97152, InstancesFor(48, 8))
	assert.Equal(t, 97152, res.Stats.Instances)
	assert.Equal(t, 97152, len(res.Plan))
	assert.Equal(t, 1920, res.Stats.WorkflowsStarted, "the entity × type × year grid")
	assert.Equal(t, WorkflowsFor(48, 8), len(res.Tenant.Workflows))
	assert.Equal(t, 1925, len(res.Tenant.Workflows), "the grid plus the five drafts")
	assert.Equal(t, 5, res.Stats.ByWorkflowStatus["draft"])

	// A VAT workflow is 108 instances: past the 100-row list cap on purpose.
	vat := 0
	for _, w := range res.Tenant.Workflows {
		if strings.Contains(w.Key, ".vat.") && w.Lifecycle.Start {
			assert.Equal(t, 9, len(w.Templates))
			assert.Equal(t, 12, len(w.SelectedPeriods))
			vat++
		}
	}
	assert.Equal(t, 48*8, vat)
}

// Every dashboard tile and every compliance classification is populated on
// the generation day, and every status occurs.
func TestEveryStatusAndBandOccurs(t *testing.T) {
	res := generate(t, testConfig())
	s := res.Stats
	for _, status := range []string{"not_started", "in_progress", "in_review", "pending_approval", "completed", "blocked"} {
		assert.Positive(t, s.ByStatus[status], "status %s", status)
	}
	for _, band := range []string{BandFarPast, BandRecentPast, BandToday, BandNearFuture, BandFarFuture} {
		assert.Positive(t, s.ByBand[band], "band %s", band)
	}
	for _, class := range []string{ClassOnTime, ClassLate, ClassMissed, ClassNotDue} {
		assert.Positive(t, s.Classification[class], "classification %s", class)
	}
	assert.Positive(t, s.Overdue)
	assert.Positive(t, s.DueToday)
	assert.Positive(t, s.ThisWeek)
	assert.Positive(t, s.AwaitingApproval)
	for _, status := range []string{"active", "completed", "archived", "draft"} {
		assert.Positive(t, s.ByWorkflowStatus[status], "workflow status %s", status)
	}
	assert.Positive(t, s.WithTaxData)
	assert.Positive(t, s.WithNotes)
	// Assignees on every completed instance and most open ones.
	assert.Equal(t, s.Deviations, s.Assigned, "every listed instance carries an assignee")
	assert.Greater(t, float64(s.AssignedOpen)/float64(s.Open), 0.6, "~70 %% of open instances are assigned")
}

// "Due today" must exist on any run day: the jurisdiction-specific offsets
// spread due dates over every day of the month. Checked for a 90-day window
// around the generation day (the days a fixture is realistically used on).
func TestSomethingIsDueOnEveryDay(t *testing.T) {
	res := generate(t, testConfig())
	dueOn := map[string]int{}
	for _, p := range res.Plan {
		dueOn[p.DueDate.String()]++
	}
	for d := addDays(testAsOf, -45); compareDates(d, addDays(testAsOf, 45)) <= 0; d = addDays(d, 1) {
		assert.Positive(t, dueOn[d.String()], "nothing is due on %s", d)
	}
}

// The three names that exercise search escaping are in the tenant, with
// their exact spelling.
func TestHostileNamesArePresent(t *testing.T) {
	res := generate(t, testConfig())
	names := map[string]spec.Entity{}
	for _, e := range res.Tenant.Entities {
		names[e.Name] = e
	}
	for _, want := range []string{HostileNameAmpersand, HostileNameUnderscore, HostileNameBackslash} {
		e, ok := names[want]
		require.True(t, ok, "entity %q", want)
		assert.NotNil(t, e.Parent, "a hostile name belongs to an operating company")
	}
	assert.Equal(t, "Germany", names[HostileNameAmpersand].Country)
	assert.Equal(t, "United Kingdom", names[HostileNameUnderscore].Country)
	assert.Equal(t, "Ireland", names[HostileNameBackslash].Country)
	assert.Equal(t, `O'Brien \ Partners`, HostileNameBackslash, "one backslash, one quote")
}

// The tree: one root, three regional sub-holdings, everything else below a
// region; a 03-31 cluster of six.
func TestEntityTreeShape(t *testing.T) {
	res := generate(t, testConfig())
	roots, regions, opcos, marchYearEnd := 0, 0, 0, 0
	byKey := map[string]spec.Entity{}
	for _, e := range res.Tenant.Entities {
		byKey[e.Key] = e
	}
	countries := map[string]bool{}
	for _, e := range res.Tenant.Entities {
		countries[e.Country] = true
		switch {
		case e.Parent == nil:
			roots++
		case byKey[*e.Parent].Parent == nil:
			regions++
		default:
			opcos++
		}
		if e.FinancialYearEnd == "03-31" {
			marchYearEnd++
		}
		assert.Equal(t, "standard", e.FiscalCalendarPattern)
	}
	assert.Equal(t, 1, roots)
	assert.Equal(t, 3, regions)
	assert.Equal(t, 44, opcos)
	assert.Equal(t, 6, marchYearEnd)
	assert.Equal(t, 12, len(countries))
}

// Users: unique e-mails in the <role>N@slug.test form, one scoped preparer,
// one disabled member, and the sign-in material the README documents.
func TestUsers(t *testing.T) {
	res := generate(t, testConfig())
	seen := map[string]bool{}
	scoped, disabled := 0, 0
	for _, u := range res.Tenant.AllUsers() {
		assert.False(t, seen[u.Email], "duplicate e-mail %s", u.Email)
		seen[u.Email] = true
		assert.True(t, strings.HasSuffix(u.Email, "@scale.test"), u.Email)
		if u.ScopeEntity != nil {
			scoped++
			_, ok := res.Tenant.Entity(*u.ScopeEntity)
			assert.True(t, ok, "scope entity resolves")
		}
		if u.Status == spec.StatusDisabled {
			disabled++
		}
	}
	assert.Equal(t, 1, scoped)
	assert.Equal(t, 1, disabled)
	assert.Equal(t, AdminEmail, res.Tenant.Admin.Email)
	assert.Equal(t, AdminPassword, res.Tenant.Admin.Password)
	for _, u := range res.Tenant.Users {
		assert.Equal(t, MemberPassword, u.Password, "one shared member password (one hash)")
	}
}

// The spec's own invariants hold, so verify's oracle can consume the tenant
// unchanged; and Spec / Tenant agree with Generate.
func TestValidatesAndTheEntryPointsAgree(t *testing.T) {
	res := generate(t, testConfig())
	require.NoError(t, res.Spec.Validate())

	s, err := Spec(testConfig())
	require.NoError(t, err)
	assert.True(t, reflect.DeepEqual(res.Spec, s))

	tenant, err := Tenant(testConfig())
	require.NoError(t, err)
	assert.True(t, reflect.DeepEqual(res.Tenant, tenant))
}

// Instance-level invariants the writer and the oracle rely on.
func TestInstanceInvariants(t *testing.T) {
	res := generate(t, testConfig())
	for _, w := range res.Tenant.Workflows {
		approval := map[string]bool{}
		for _, tmpl := range w.Templates {
			approval[tmpl.Key] = tmpl.ApprovalRequired
		}
		if !w.Lifecycle.Start {
			assert.Empty(t, w.Instances, "a draft has no instances")
			continue
		}
		for _, inst := range w.Instances {
			require.NotEmpty(t, inst.Assignee, "%s %s/%s", w.Key, inst.Period, inst.Task)
			if !inst.CompletedOn.IsZero() {
				assert.LessOrEqual(t, compareDates(inst.CompletedOn, testAsOf), 0,
					"%s: no completion after asOf", w.Key)
			}
			switch inst.Via {
			case spec.ViaApprove:
				assert.True(t, approval[inst.Task], "via=approve only on an approval template")
				assert.Equal(t, "completed", inst.Status)
				assert.False(t, inst.SubmittedOn.IsZero())
				assert.LessOrEqual(t, compareDates(inst.SubmittedOn, inst.CompletedOn), 0)
			case spec.ViaSubmit:
				assert.True(t, approval[inst.Task], "pending_approval only on an approval template")
				assert.Equal(t, "pending_approval", inst.Status)
			}
			if inst.Status == "pending_approval" {
				assert.Equal(t, spec.ViaSubmit, inst.Via)
			}
			if len(inst.TaxData) > 0 {
				assert.Equal(t, "final", inst.TaxDataStatus)
				assert.Equal(t, "prepare", inst.Task, "tax data sits on the Prepare task")
			}
		}
		if w.Lifecycle.FinalStatus == "completed" {
			assert.Equal(t, len(w.SelectedPeriods)*len(w.Templates), len(w.Instances))
			for _, inst := range w.Instances {
				assert.Equal(t, "completed", inst.Status, "a completed workflow has no open instance")
			}
		}
	}
}

// The plan is period-major, orderIndex-minor, and the four dates obey the
// engine's ordering (filing after period end for every rule here).
func TestPlanOrderAndDates(t *testing.T) {
	res := generate(t, testConfig())
	prevKey := ""
	prevOrder := -1
	for _, p := range res.Plan {
		if p.WorkflowKey+"|"+p.PeriodCode != prevKey {
			prevKey = p.WorkflowKey + "|" + p.PeriodCode
			prevOrder = -1
		}
		assert.Greater(t, p.OrderIndex, prevOrder, "orderIndex increases within a period")
		prevOrder = p.OrderIndex
		assert.GreaterOrEqual(t, compareDates(p.FilingDeadline, p.PeriodEnd), 0, "filing after period end")
		assert.False(t, p.PaymentDeadline.IsZero(), "recurring instances carry a payment deadline")
	}
}

// Smaller and larger configurations still validate: the shape scales.
func TestOtherSizesValidate(t *testing.T) {
	for _, cfg := range []Config{
		{Entities: 7, Years: 1, AsOf: testAsOf, Seed: 3},
		{Entities: 20, Years: 2, AsOf: testAsOf, Seed: 4},
	} {
		res := generate(t, cfg)
		assert.Equal(t, InstancesFor(cfg.Entities, cfg.Years), res.Stats.Instances)
		assert.NoError(t, res.Spec.Validate())
	}
	_, err := Generate(Config{Entities: 3, AsOf: testAsOf})
	assert.Error(t, err, "fewer than the minimum entities")
	_, err = Generate(Config{Timezone: "Mars/Olympus", AsOf: testAsOf})
	assert.Error(t, err, "an unknown zone")
}

func TestCurrentFinancialYear(t *testing.T) {
	assert.Equal(t, 2026, currentFinancialYear(dateonly.New(2026, 9, 8), "12-31"))
	assert.Equal(t, 2027, currentFinancialYear(dateonly.New(2026, 9, 8), "03-31"))
	assert.Equal(t, 2026, currentFinancialYear(dateonly.New(2026, 3, 31), "03-31"), "the year end itself is still that year")
	assert.Equal(t, 2026, currentFinancialYear(dateonly.New(2026, 1, 1), "12-31"))
}

func TestBandOf(t *testing.T) {
	asOf := dateonly.New(2026, 9, 8)
	assert.Equal(t, BandFarPast, bandOf(dateonly.New(2026, 7, 9), asOf))
	assert.Equal(t, BandRecentPast, bandOf(dateonly.New(2026, 7, 10), asOf))
	assert.Equal(t, BandRecentPast, bandOf(dateonly.New(2026, 9, 7), asOf))
	assert.Equal(t, BandToday, bandOf(asOf, asOf))
	assert.Equal(t, BandNearFuture, bandOf(dateonly.New(2026, 10, 8), asOf))
	assert.Equal(t, BandFarFuture, bandOf(dateonly.New(2026, 10, 9), asOf))
}

func TestClassify(t *testing.T) {
	asOf := dateonly.New(2026, 9, 8)
	due := dateonly.New(2026, 9, 1)
	assert.Equal(t, ClassOnTime, classify(spec.Instance{Status: "completed", CompletedOn: due}, due, asOf))
	assert.Equal(t, ClassLate, classify(spec.Instance{Status: "completed", CompletedOn: addDays(due, 1)}, due, asOf))
	assert.Equal(t, ClassMissed, classify(spec.Instance{Status: "in_progress"}, due, asOf))
	assert.Equal(t, ClassNotDue, classify(spec.Instance{Status: "not_started"}, asOf, asOf), "due today is not_due")
}
