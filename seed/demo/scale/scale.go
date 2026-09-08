// Package scale generates the scale fixture: one large tenant, built in
// memory as a spec.Tenant so the demo oracle (cmd/seed-demo verify) can check
// it exactly as it checks the hand-written dataset.
//
// The fixture is GENERATED, not declared. Nothing in it is a number somebody
// typed: entities, obligations, workflows, templates and the state of every
// task instance are derived from a Config through a seeded PRNG, and the only
// reference for what the API must return is a recomputation from that same
// Config (the oracle). Two runs with the same Config produce byte-identical
// structures; a different Seed produces a different — but equally valid —
// world.
//
// Why it exists: ADR-0021 rule 7 promises p95 ≤ 500 ms list reads at 10⁵ task
// instances and every list is paged. The demo dataset (243 instances) cannot
// exercise either. The default configuration yields 48 entities × 5
// obligation types × 8 fiscal years = 1,920 started workflows and 97,152
// instances (48 × 8 × 253 per entity-year: VAT 12 × 9, WHT 12 × 5, ENV 12 × 5,
// CIT 4 × 5, TP 1 × 5), plus five never-started draft workflows for the next
// fiscal year.
//
// Dates come from the same engine the generator behind POST /workflows/{id}/
// start uses (shared/deadline through the entity calendar), applied with the
// same rules the verify oracle applies (cmd/seed-demo/oracle.go planRecurring
// and oraclePaymentDeadline). Those rules are mirrored here rather than
// imported because the oracle lives in package main; cmd/seed-demo carries a
// tripwire test that diffs the two plans instance by instance.
package scale

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
	_ "time/tzdata" // the tenant zone must resolve on any host

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
	"github.com/mohamadhallal/zentax-api/shared/dateonly"
)

// Defaults of Config.
const (
	DefaultSeed     int64 = 1
	DefaultEntities       = 48
	DefaultYears          = 8
	DefaultSlug           = "scale"
	DefaultName           = "Scale Group AG"
	DefaultTimezone       = "Europe/Berlin"

	// AdminEmail / AdminPassword / MemberPassword are the fixture's sign-in
	// material. Local-only demo values — the tenant is never a real one.
	AdminEmail     = "admin@scale.test"
	AdminPassword  = "Scale-Admin-2026!"
	MemberPassword = "Scale-Member-2026!"

	// minEntities is the smallest tree the shape supports: the holding, three
	// regional sub-holdings and the three hostile-named operating companies.
	minEntities = 7
	// DraftWorkflows is the number of never-started workflows added on top of
	// the entity × type × year grid (one per obligation type, next fiscal year).
	DraftWorkflows = 5
)

// Config is everything the fixture derives from. The zero value is not
// usable; call WithDefaults (Generate does).
type Config struct {
	// Seed drives every random choice; the same seed yields the same tenant.
	Seed int64
	// Entities is the size of the entity tree (default 48: 1 holding + 3
	// regional sub-holdings + 44 operating companies).
	Entities int
	// Years is the number of fiscal years per (entity, obligation type),
	// ending with the entity's fiscal year running at AsOf.
	Years int
	// AsOf is the civil date the status distribution is computed for — the
	// generation-day "today" in the tenant's zone. It decides which instances
	// are past, due today or future, and no completion is dated after it.
	AsOf dateonly.Date
	// Slug / Name / Timezone describe the tenant row.
	Slug     string
	Name     string
	Timezone string
}

// WithDefaults fills the zero fields. AsOf defaults to today in Timezone.
func (c Config) WithDefaults() Config {
	if c.Seed == 0 {
		c.Seed = DefaultSeed
	}
	if c.Entities == 0 {
		c.Entities = DefaultEntities
	}
	if c.Years == 0 {
		c.Years = DefaultYears
	}
	if c.Slug == "" {
		c.Slug = DefaultSlug
	}
	if c.Name == "" {
		c.Name = DefaultName
	}
	if c.Timezone == "" {
		c.Timezone = DefaultTimezone
	}
	if c.AsOf.IsZero() {
		if zone, err := time.LoadLocation(c.Timezone); err == nil {
			c.AsOf = dateonly.FromTime(time.Now().In(zone))
		}
	}
	return c
}

// validate rejects a Config the shape cannot honour.
func (c Config) validate() error {
	switch {
	case c.Entities < minEntities:
		return fmt.Errorf("scale: entities must be at least %d (a holding, three regions and the three hostile names), got %d",
			minEntities, c.Entities)
	case c.Years < 1:
		return fmt.Errorf("scale: years must be at least 1, got %d", c.Years)
	case c.AsOf.IsZero():
		return errors.New("scale: asOf is required (no loadable timezone to default it from)")
	case c.Slug == "" || c.Name == "":
		return errors.New("scale: slug and name are required")
	}
	if _, err := time.LoadLocation(c.Timezone); err != nil {
		return fmt.Errorf("scale: load timezone %q: %w", c.Timezone, err)
	}
	return nil
}

// InstancesFor is the instance-count formula: every workflow of the grid is
// started, so the count is entities × years × the instances of one
// entity-year (Σ over obligation types of periods × templates).
func InstancesFor(entities, years int) int {
	perEntityYear := 0
	for _, ot := range obligationTypeDefs {
		perEntityYear += len(ot.periods) * len(ot.templates)
	}
	return entities * years * perEntityYear
}

// WorkflowsFor is the workflow-count formula: the grid plus the drafts.
func WorkflowsFor(entities, years int) int {
	return entities*len(obligationTypeDefs)*years + DraftWorkflows
}

// PlannedInstance is one generated instance with the four engine dates —
// what the writer persists and the tripwire test compares with the oracle.
type PlannedInstance struct {
	WorkflowKey     string
	PeriodCode      string
	TemplateKey     string
	OrderIndex      int
	PeriodEnd       dateonly.Date
	FilingDeadline  dateonly.Date
	PaymentDeadline dateonly.Date
	DueDate         dateonly.Date
	// Band is the due-date band the status was drawn from (see bands.go).
	Band string
	// Status is the status drawn for the instance.
	Status string
}

// Stats summarises the generated world — printed by the writer, asserted by
// the tests. Classification and the dashboard tiles are computed at AsOf with
// the oracle's rules (heatmap classification on due_date).
type Stats struct {
	Entities          int
	Users             int
	ObligationTypes   int
	EntityObligations int
	Workflows         int
	WorkflowsStarted  int
	WorkflowTasks     int
	Instances         int
	// Deviations is the number of instances listed on their workflows (every
	// instance that is not "not_started, unassigned, no data").
	Deviations int

	ByStatus         map[string]int
	ByWorkflowStatus map[string]int
	ByBand           map[string]int
	// Classification is on_time / late / missed / not_due by due date at AsOf.
	Classification map[string]int

	Overdue          int
	DueToday         int
	ThisWeek         int
	AwaitingApproval int
	Completed        int
	WithTaxData      int
	WithNotes        int
	Assigned         int
	AssignedOpen     int
	Open             int
}

// Result is a generated fixture.
type Result struct {
	// Config is the effective configuration (defaults applied).
	Config Config
	// Spec wraps the tenant so the verify oracle can consume it; it passes
	// spec.Validate.
	Spec *spec.Spec
	// Tenant is &Spec.Tenants[0].
	Tenant *spec.Tenant
	// Plan lists every started workflow's instances with their dates, in
	// workflow order then period-major, template orderIndex.
	Plan []PlannedInstance
	// Stats summarises the world.
	Stats Stats
}

// Tenant generates the fixture and returns the tenant alone — what `verify
// --scale` consumes.
func Tenant(cfg Config) (*spec.Tenant, error) {
	res, err := Generate(cfg)
	if err != nil {
		return nil, err
	}
	return res.Tenant, nil
}

// Spec generates the fixture wrapped in a one-tenant spec.Spec.
func Spec(cfg Config) (*spec.Spec, error) {
	res, err := Generate(cfg)
	if err != nil {
		return nil, err
	}
	return res.Spec, nil
}

// Generate builds the fixture. It is deterministic in cfg and validates its
// own output: the returned Spec passes spec.Validate.
func Generate(cfg Config) (*Result, error) {
	cfg = cfg.WithDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	zone, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, fmt.Errorf("scale: load timezone %q: %w", cfg.Timezone, err)
	}

	g := &generator{
		cfg:  cfg,
		zone: zone,
		rng:  rand.New(rand.NewPCG(uint64(cfg.Seed), uint64(cfg.Seed)^0x9E3779B97F4A7C15)),
		stats: Stats{
			ByStatus:         map[string]int{},
			ByWorkflowStatus: map[string]int{},
			ByBand:           map[string]int{},
			Classification:   map[string]int{},
		},
	}

	tenant := spec.Tenant{
		Key:      cfg.Slug,
		Slug:     cfg.Slug,
		Name:     cfg.Name,
		Timezone: cfg.Timezone,
	}
	g.users(&tenant)
	if err := g.entities(&tenant); err != nil {
		return nil, err
	}
	g.obligationTypes(&tenant)
	g.entityObligations(&tenant)
	if err := g.workflows(&tenant); err != nil {
		return nil, err
	}

	g.stats.Entities = len(tenant.Entities)
	g.stats.Users = len(tenant.AllUsers())
	g.stats.ObligationTypes = len(tenant.ObligationTypes)
	g.stats.EntityObligations = len(tenant.EntityObligations)
	g.stats.Workflows = len(tenant.Workflows)

	s := &spec.Spec{
		AsOf: cfg.AsOf,
		ValidityWindow: spec.ValidityWindow{
			From: cfg.AsOf,
			To:   cfg.AsOf,
			Note: "Generated fixture: no static expectations. The verify oracle recomputes every " +
				"number for the run day; the status distribution was drawn for asOf.",
		},
		Conventions: spec.Conventions{
			CompletionLocalTime: spec.DefaultCompletionLocalTime,
			EmailPattern:        "<role>N@" + cfg.Slug + ".test",
			PasswordPolicy:      ">= 12 chars, demo only; one shared member password",
		},
		Tenants: []spec.Tenant{tenant},
	}
	if err := s.Validate(); err != nil {
		return nil, fmt.Errorf("scale: the generated tenant does not validate: %w", err)
	}
	return &Result{
		Config: cfg,
		Spec:   s,
		Tenant: &s.Tenants[0],
		Plan:   g.plan,
		Stats:  g.stats,
	}, nil
}

// generator carries the state of one Generate call.
type generator struct {
	cfg  Config
	zone *time.Location
	rng  *rand.Rand

	// assignable are the user keys tasks are assigned to, round-robin.
	assignable []string
	nextAssign int
	// writers / approvers cycle per workflow.
	writers   []string
	approvers []string
	// scopeEntity is the key of the entity the scoped preparer is limited to.
	scopeEntity string
	// entitiesOut are the entities in tenant order, with their country.
	entitiesOut []entity

	plan  []PlannedInstance
	stats Stats
	// noteSeq numbers the payment references in notes.
	noteSeq int
}

// chance draws a Bernoulli trial.
func (g *generator) chance(p float64) bool { return g.rng.Float64() < p }

// between draws an integer in [lo, hi].
func (g *generator) between(lo, hi int) int {
	if hi <= lo {
		return lo
	}
	return lo + g.rng.IntN(hi-lo+1)
}

// nextAssignee is the round-robin over the assignable users.
func (g *generator) nextAssignee() string {
	key := g.assignable[g.nextAssign%len(g.assignable)]
	g.nextAssign++
	return key
}
