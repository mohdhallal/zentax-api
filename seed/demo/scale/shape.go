package scale

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/seed/demo/spec"
)

// The static shape of the world: who works in it, which countries the group
// operates in, which taxes it owes, and what a workflow of each tax looks
// like. None of this is random — the PRNG only decides the state of the task
// instances (states.go) and the sprinkle of non-active workflows.

// ---------------------------------------------------------------------------
// People
// ---------------------------------------------------------------------------

// userDef is one member of the tenant. Emails follow <role>N@<slug>.test; the
// admin is created with the tenant (platform/seed) and does not appear here.
type userDef struct {
	role   string
	n      int
	name   string
	scoped bool // grant limited to the EMEA sub-holding's subtree
	off    bool // disabled after creation
}

var userDefs = []userDef{
	{role: "manager", n: 1, name: "Markus Weber"},
	{role: "manager", n: 2, name: "Elena Rossi"},
	{role: "reviewer", n: 1, name: "Thomas Becker"},
	{role: "reviewer", n: 2, name: "Yuki Tanaka"},
	{role: "preparer", n: 1, name: "Petra Lindgren"},
	{role: "preparer", n: 2, name: "Lucas Martin"},
	{role: "preparer", n: 3, name: "Aisha Rahman"},
	{role: "preparer", n: 4, name: "Diego Alvarez"},
	{role: "preparer", n: 5, name: "Nora Fischer", scoped: true},
	{role: "preparer", n: 6, name: "Jan Kowalski", off: true},
	{role: "viewer", n: 1, name: "Victor Chen"},
}

// users fills the admin and the members and decides who can be assigned
// work: every active non-admin member except the viewer (a viewer reads).
// Writers are the active tenant-wide preparers, approvers the reviewers, so
// every workflow's writer ≠ approver (SoD).
func (g *generator) users(t *spec.Tenant) {
	slug := g.cfg.Slug
	t.Admin = spec.User{
		Key:      slug + ".admin",
		Email:    "admin@" + slug + ".test",
		Name:     "Sabine Adler",
		Role:     "tenant_admin",
		Password: AdminPassword,
		Status:   spec.StatusActive,
	}
	g.scopeEntity = slug + ".emea"
	for _, u := range userDefs {
		user := spec.User{
			Key:      fmt.Sprintf("%s.%s%d", slug, u.role, u.n),
			Email:    fmt.Sprintf("%s%d@%s.test", u.role, u.n, slug),
			Name:     u.name,
			Role:     u.role,
			Password: MemberPassword,
			Status:   spec.StatusActive,
		}
		if u.scoped {
			scope := g.scopeEntity
			user.ScopeEntity = &scope
		}
		if u.off {
			user.Status = spec.StatusDisabled
		}
		t.Users = append(t.Users, user)

		if user.Status != spec.StatusActive || u.role == "viewer" {
			continue
		}
		g.assignable = append(g.assignable, user.Key)
		switch u.role {
		case "preparer":
			if !u.scoped {
				g.writers = append(g.writers, user.Key)
			}
		case "reviewer":
			g.approvers = append(g.approvers, user.Key)
		}
	}
}

// ---------------------------------------------------------------------------
// Countries and entities
// ---------------------------------------------------------------------------

// countryDef is one jurisdiction the group operates in. vatRate feeds the
// generated VAT figures; filingDays / paymentOffset shape the VAT deadlines
// (different jurisdictions file on different days of the month, which is
// what spreads due dates across the whole month).
type countryDef struct {
	name      string
	region    int // index into regions
	currency  string
	legalForm string
	vatRate   int
	taxPrefix string
	// stems are the operating-company name stems, in order.
	stems []string
	// vatFilingDays is the VAT filing offset after period end.
	vatFilingDays int
	// vatPayment is the VAT payment offset after period end (months, days).
	vatPayment spec.Offset
}

var countryDefs = []countryDef{
	// EMEA
	{name: "Germany", region: 0, currency: "EUR", legalForm: "GmbH", vatRate: 19, taxPrefix: "DE",
		stems:         []string{"Deutschland", "Rhein-Main Services", "Nord Logistik", "Bayern Vertrieb", "Berlin Technologies"},
		vatFilingDays: 10, vatPayment: spec.Offset{Months: 1, Days: 10}},
	{name: "United Kingdom", region: 0, currency: "GBP", legalForm: "Ltd", vatRate: 20, taxPrefix: "GB",
		stems:         []string{"UK", "London Services", "Northern Trading", "Midlands Manufacturing"},
		vatFilingDays: 37, vatPayment: spec.Offset{Months: 0, Days: 37}},
	{name: "France", region: 0, currency: "EUR", legalForm: "SAS", vatRate: 20, taxPrefix: "FR",
		stems:         []string{"France", "Paris Services", "Lyon Distribution"},
		vatFilingDays: 19, vatPayment: spec.Offset{Months: 0, Days: 19}},
	{name: "Netherlands", region: 0, currency: "EUR", legalForm: "B.V.", vatRate: 21, taxPrefix: "NL",
		stems:         []string{"Nederland", "Rotterdam Logistics"},
		vatFilingDays: 30, vatPayment: spec.Offset{Months: 0, Days: 30}},
	{name: "Italy", region: 0, currency: "EUR", legalForm: "S.r.l.", vatRate: 22, taxPrefix: "IT",
		stems:         []string{"Italia", "Milano Servizi"},
		vatFilingDays: 16, vatPayment: spec.Offset{Months: 0, Days: 16}},
	{name: "Ireland", region: 0, currency: "EUR", legalForm: "Ltd", vatRate: 23, taxPrefix: "IE",
		stems:         []string{"Ireland", "Dublin Operations"},
		vatFilingDays: 23, vatPayment: spec.Offset{Months: 0, Days: 23}},
	// Americas
	{name: "United States", region: 1, currency: "USD", legalForm: "Inc.", vatRate: 8, taxPrefix: "US",
		stems:         []string{"US", "East Coast Services", "West Coast Technologies", "Texas Manufacturing", "Midwest Distribution"},
		vatFilingDays: 20, vatPayment: spec.Offset{Months: 0, Days: 25}},
	{name: "Canada", region: 1, currency: "CAD", legalForm: "Ltd.", vatRate: 5, taxPrefix: "CA",
		stems:         []string{"Canada", "Ontario Services", "Québec Distribution"},
		vatFilingDays: 31, vatPayment: spec.Offset{Months: 0, Days: 31}},
	{name: "Mexico", region: 1, currency: "MXN", legalForm: "S.A. de C.V.", vatRate: 16, taxPrefix: "MX",
		stems:         []string{"México", "Monterrey Manufactura"},
		vatFilingDays: 17, vatPayment: spec.Offset{Months: 0, Days: 17}},
	// APAC
	{name: "Singapore", region: 2, currency: "SGD", legalForm: "Pte. Ltd.", vatRate: 9, taxPrefix: "SG",
		stems:         []string{"Singapore", "Asia Trading"},
		vatFilingDays: 30, vatPayment: spec.Offset{Months: 1, Days: 0}},
	{name: "Japan", region: 2, currency: "JPY", legalForm: "K.K.", vatRate: 10, taxPrefix: "JP",
		stems:         []string{"Japan", "Tokyo Services", "Osaka Manufacturing"},
		vatFilingDays: 30, vatPayment: spec.Offset{Months: 0, Days: 30}},
	{name: "Australia", region: 2, currency: "AUD", legalForm: "Pty Ltd", vatRate: 10, taxPrefix: "AU",
		stems:         []string{"Australia", "Sydney Services", "Melbourne Distribution"},
		vatFilingDays: 28, vatPayment: spec.Offset{Months: 0, Days: 28}},
}

// regionDef is one regional sub-holding under the group holding.
type regionDef struct {
	key     string
	name    string
	legal   string
	country string
}

var regions = []regionDef{
	{key: "emea", name: "Scale EMEA Holding B.V.", legal: "Scale EMEA Holding Besloten Vennootschap", country: "Netherlands"},
	{key: "amer", name: "Scale Americas Holdings Inc.", legal: "Scale Americas Holdings Incorporated", country: "United States"},
	{key: "apac", name: "Scale APAC Holdings Pte. Ltd.", legal: "Scale APAC Holdings Private Limited", country: "Singapore"},
}

// Hostile names: the three operating companies whose names exercise search
// escaping (LIKE wildcards, a quote, a backslash, non-ASCII). The plan and
// the pagination test plan reference these literally.
const (
	HostileNameAmpersand  = "Müller & Söhne 100% GmbH"
	HostileNameUnderscore = "Under_score Holdings Ltd"
	HostileNameBackslash  = `O'Brien \ Partners`
)

// hostileNames maps an operating-company index to its hostile name and the
// country it belongs to (the index is chosen so the regular assignment already
// lands there: opco 0 → Germany, 3 → United Kingdom, 15 → Ireland).
var hostileNames = []struct {
	name, legal, country string
}{
	{HostileNameAmpersand, "Müller & Söhne 100% Gesellschaft mit beschränkter Haftung", "Germany"},
	{HostileNameUnderscore, "Under_score Holdings Limited", "United Kingdom"},
	{HostileNameBackslash, `O'Brien \ Partners`, "Ireland"},
}

// countriesByRegion lists the countries of each region in table order.
func countriesByRegion() [][]int {
	out := make([][]int, len(regions))
	for i, c := range countryDefs {
		out[c.region] = append(out[c.region], i)
	}
	return out
}

// entity is the generator's view of one entity: the spec row plus the country
// table entry it was drawn from.
type entity struct {
	spec    spec.Entity
	country countryDef
	// index is the entity's position in the tenant (holding = 0).
	index int
}

// entities builds the 3-level tree: the holding, one sub-holding per region,
// and the operating companies spread over the regions round-robin, each
// taking the next country of its region and the next name stem of that
// country. Most entities close their year on 12-31; the first six United
// Kingdom / Japan companies close on 03-31 (the fiscal-year cluster).
func (g *generator) entities(t *spec.Tenant) error {
	slug := g.cfg.Slug
	holdKey := slug + ".hold"
	byRegion := countriesByRegion()

	// The holding and the sub-holdings.
	g.entitiesOut = append(g.entitiesOut, entity{
		index:   0,
		country: countryDefs[0],
		spec: spec.Entity{
			Key: holdKey, Name: g.cfg.Name, LegalName: g.cfg.Name + " (Aktiengesellschaft)",
			Country: "Germany", TaxResidency: "Germany",
			FiscalCalendarPattern: "standard", FinancialYearEnd: "12-31",
		},
	})
	regionKeys := make([]string, len(regions))
	for i, r := range regions {
		parent := holdKey
		regionKeys[i] = slug + "." + r.key
		g.entitiesOut = append(g.entitiesOut, entity{
			index:   len(g.entitiesOut),
			country: countryByName(r.country),
			spec: spec.Entity{
				Key: regionKeys[i], Name: r.name, LegalName: r.legal,
				Country: r.country, TaxResidency: r.country, Parent: &parent,
				FiscalCalendarPattern: "standard", FinancialYearEnd: "12-31",
			},
		})
	}

	// The operating companies.
	opcos := g.cfg.Entities - 1 - len(regions)
	hostileAt := map[int]int{}
	if opcos >= 16 {
		hostileAt[0], hostileAt[3], hostileAt[15] = 0, 1, 2
	} else {
		hostileAt[0], hostileAt[1], hostileAt[2] = 0, 1, 2
	}
	seenNames := map[string]bool{g.cfg.Name: true}
	for _, r := range regions {
		seenNames[r.name] = true
	}
	fyeCluster := 0
	stemUse := map[int]int{} // country index → stems consumed
	for i := 0; i < opcos; i++ {
		region := i % len(regions)
		countries := byRegion[region]
		slot := i / len(regions)
		country := countryDefs[countries[slot%len(countries)]]
		countryIdx := countries[slot%len(countries)]

		name, legal := "", ""
		if h, ok := hostileAt[i]; ok {
			name, legal = hostileNames[h].name, hostileNames[h].legal
			country = countryByName(hostileNames[h].country)
		} else {
			use := stemUse[countryIdx]
			stemUse[countryIdx]++
			if use < len(country.stems) {
				name = "Scale " + country.stems[use] + " " + country.legalForm
			} else {
				name = fmt.Sprintf("Scale %s Unit %d %s", country.name, use+1, country.legalForm)
			}
			legal = name
		}
		for n := 2; seenNames[name]; n++ {
			name = strings.TrimSuffix(name, " "+country.legalForm) + " " + roman(n) + " " + country.legalForm
		}
		seenNames[name] = true

		fye := "12-31"
		if fyeCluster < 6 && (country.name == "United Kingdom" || country.name == "Japan") {
			fye = "03-31"
			fyeCluster++
		}
		parent := regionKeys[region]
		g.entitiesOut = append(g.entitiesOut, entity{
			index:   len(g.entitiesOut),
			country: country,
			spec: spec.Entity{
				Key: fmt.Sprintf("%s.e%02d", slug, i+1), Name: name, LegalName: legal,
				Country: country.name, TaxResidency: country.name, Parent: &parent,
				FiscalCalendarPattern: "standard", FinancialYearEnd: fye,
			},
		})
	}

	for _, e := range g.entitiesOut {
		t.Entities = append(t.Entities, e.spec)
	}
	if len(t.Entities) != g.cfg.Entities {
		return fmt.Errorf("scale: built %d entities, want %d", len(t.Entities), g.cfg.Entities)
	}
	return nil
}

func countryByName(name string) countryDef {
	for _, c := range countryDefs {
		if c.name == name {
			return c
		}
	}
	return countryDefs[0]
}

// roman renders small numbers as Roman numerals for name disambiguation.
func roman(n int) string {
	numerals := []string{"", "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X"}
	if n > 0 && n < len(numerals) {
		return numerals[n]
	}
	return strconv.Itoa(n)
}

// ---------------------------------------------------------------------------
// Obligation types and their workflow shape
// ---------------------------------------------------------------------------

// templateDef is one task template of an obligation type's workflow.
type templateDef struct {
	key       string
	name      string
	taskType  string
	role      string
	approval  bool
	reference string // period_end | filing_deadline | payment_deadline
	offset    int
	unit      string
	direction string
	// dataTemplate names the predefined data template ("VAT" | "CIT" | "WHT")
	// the Prepare task carries, "" for none.
	dataTemplate string
}

// obligationTypeDef is one tax the group owes, with the workflow shape every
// (entity, fiscal year) instance of it takes.
type obligationTypeDef struct {
	key         string
	name        string
	code        string
	template    string // obligation_types.template
	category    string
	description string
	periodicity string
	periods     []string
	templates   []templateDef
}

var monthly = []string{"M1", "M2", "M3", "M4", "M5", "M6", "M7", "M8", "M9", "M10", "M11", "M12"}

var obligationTypeDefs = []obligationTypeDef{
	{
		key: "vat", name: "Value Added Tax", code: "VAT", template: "VAT", category: "predefined",
		description: "Monthly VAT / GST return and payment", periodicity: "monthly", periods: monthly,
		// Nine templates: a VAT workflow is 108 instances, past the 100-row
		// list cap on purpose.
		templates: []templateDef{
			{key: "collect", name: "Collect sales and purchase data", taskType: "data_request", role: "Preparer",
				reference: "period_end", offset: 2, unit: "days", direction: "after"},
			{key: "reconcile", name: "Reconcile VAT ledgers", taskType: "preparation", role: "Preparer",
				reference: "period_end", offset: 5, unit: "days", direction: "after"},
			{key: "prepare", name: "Prepare VAT return", taskType: "preparation", role: "Preparer",
				reference: "filing_deadline", offset: 5, unit: "days", direction: "before", dataTemplate: "VAT"},
			{key: "review", name: "Review VAT return", taskType: "review", role: "Reviewer", approval: true,
				reference: "filing_deadline", offset: 3, unit: "days", direction: "before"},
			{key: "approve", name: "Approve filing", taskType: "approval", role: "Reviewer", approval: true,
				reference: "filing_deadline", offset: 2, unit: "days", direction: "before"},
			{key: "file", name: "File VAT return", taskType: "submission", role: "Preparer",
				reference: "filing_deadline", offset: 0, unit: "days", direction: "before"},
			{key: "pay", name: "Pay VAT", taskType: "payment", role: "Finance",
				reference: "payment_deadline", offset: 0, unit: "days", direction: "before"},
			{key: "archive", name: "Archive workings", taskType: "other", role: "Preparer",
				reference: "filing_deadline", offset: 5, unit: "days", direction: "after"},
			{key: "recon-pay", name: "Reconcile payment", taskType: "other", role: "Finance",
				reference: "payment_deadline", offset: 3, unit: "days", direction: "after"},
		},
	},
	{
		key: "wht", name: "Withholding Tax", code: "WHT", template: "WHT", category: "predefined",
		description: "Monthly withholding tax on outbound payments", periodicity: "monthly", periods: monthly,
		templates: []templateDef{
			{key: "collect", name: "Collect payment schedule", taskType: "data_request", role: "Preparer",
				reference: "period_end", offset: 2, unit: "days", direction: "after"},
			{key: "prepare", name: "Prepare WHT return", taskType: "preparation", role: "Preparer",
				reference: "filing_deadline", offset: 5, unit: "days", direction: "before", dataTemplate: "WHT"},
			{key: "review", name: "Review WHT return", taskType: "review", role: "Reviewer", approval: true,
				reference: "filing_deadline", offset: 2, unit: "days", direction: "before"},
			{key: "file", name: "File WHT return", taskType: "submission", role: "Preparer",
				reference: "filing_deadline", offset: 0, unit: "days", direction: "before"},
			{key: "pay", name: "Pay WHT", taskType: "payment", role: "Finance",
				reference: "payment_deadline", offset: 0, unit: "days", direction: "before"},
		},
	},
	{
		key: "env", name: "Environmental Levy", code: "ENV", template: "Custom", category: "custom",
		description: "Monthly environmental levy on packaging and emissions", periodicity: "monthly", periods: monthly,
		templates: []templateDef{
			{key: "measure", name: "Measure emissions and packaging", taskType: "data_request", role: "Preparer",
				reference: "period_end", offset: 3, unit: "days", direction: "after"},
			{key: "prepare", name: "Compute levy", taskType: "preparation", role: "Preparer",
				reference: "filing_deadline", offset: 4, unit: "days", direction: "before"},
			{key: "review", name: "Review levy declaration", taskType: "review", role: "Reviewer", approval: true,
				reference: "filing_deadline", offset: 2, unit: "days", direction: "before"},
			{key: "file", name: "File levy declaration", taskType: "submission", role: "Preparer",
				reference: "filing_deadline", offset: 0, unit: "days", direction: "before"},
			{key: "pay", name: "Pay levy", taskType: "payment", role: "Finance",
				reference: "payment_deadline", offset: 0, unit: "days", direction: "before"},
		},
	},
	{
		key: "cit", name: "Corporate Income Tax", code: "CIT", template: "CIT", category: "predefined",
		description: "Quarterly corporate income tax instalments", periodicity: "quarterly",
		periods: []string{"Q1", "Q2", "Q3", "Q4"},
		templates: []templateDef{
			{key: "collect", name: "Collect quarterly accounts", taskType: "data_request", role: "Preparer",
				reference: "period_end", offset: 5, unit: "days", direction: "after"},
			{key: "prepare", name: "Prepare CIT computation", taskType: "preparation", role: "Preparer",
				reference: "filing_deadline", offset: 10, unit: "days", direction: "before", dataTemplate: "CIT"},
			{key: "review", name: "Review CIT computation", taskType: "review", role: "Reviewer", approval: true,
				reference: "filing_deadline", offset: 5, unit: "days", direction: "before"},
			{key: "file", name: "File CIT instalment return", taskType: "submission", role: "Preparer",
				reference: "filing_deadline", offset: 0, unit: "days", direction: "before"},
			{key: "pay", name: "Pay CIT instalment", taskType: "payment", role: "Finance",
				reference: "payment_deadline", offset: 0, unit: "days", direction: "before"},
		},
	},
	{
		key: "tp", name: "Transfer Pricing Documentation", code: "TP", template: "TP", category: "predefined",
		description: "Annual local file and master file", periodicity: "annual",
		periods: []string{"Y1"},
		templates: []templateDef{
			{key: "collect", name: "Collect intercompany agreements", taskType: "data_request", role: "Preparer",
				reference: "period_end", offset: 30, unit: "days", direction: "after"},
			{key: "prepare", name: "Prepare local file", taskType: "preparation", role: "Preparer",
				reference: "filing_deadline", offset: 20, unit: "days", direction: "before"},
			{key: "review", name: "Review local file", taskType: "review", role: "Reviewer", approval: true,
				reference: "filing_deadline", offset: 10, unit: "days", direction: "before"},
			{key: "submit", name: "Submit documentation", taskType: "submission", role: "Preparer",
				reference: "filing_deadline", offset: 0, unit: "days", direction: "before"},
			{key: "archive", name: "Archive documentation", taskType: "other", role: "Preparer",
				reference: "filing_deadline", offset: 10, unit: "days", direction: "after"},
		},
	},
}

func (g *generator) obligationTypes(t *spec.Tenant) {
	for _, ot := range obligationTypeDefs {
		t.ObligationTypes = append(t.ObligationTypes, spec.ObligationType{
			Key: g.cfg.Slug + "." + ot.key, Name: ot.name, Code: ot.code,
			Template: ot.template, Category: ot.category, Description: ot.description,
		})
	}
}

// deadlineRuleFor is the entity obligation's rule for one (entity, tax):
// realistic, jurisdiction-dependent, and varied enough that due dates land
// on every day of the month across the group.
func deadlineRuleFor(e entity, ot obligationTypeDef) spec.DeadlineRule {
	c := e.country
	countryIdx := 0
	for i, cd := range countryDefs {
		if cd.name == c.name {
			countryIdx = i
		}
	}
	periodOffset := func(filing, payment spec.Offset, adjustment string) spec.DeadlineRule {
		f, p := filing, payment
		return spec.DeadlineRule{
			Type: "period_offset", Reference: "period_end", WeekendAdjustment: adjustment,
			PeriodStart:   &spec.MonthDay{Day: 1, Month: fiscalStartMonth(e.spec.FinancialYearEnd)},
			FilingOffset:  &f,
			PaymentOffset: &p,
		}
	}
	// Half the jurisdictions move a weekend deadline to the next business
	// day, half do not (both exist in practice) — and the "none" half is what
	// lets a due date fall on a Saturday or Sunday at all.
	adjustment := "next-business-day"
	if countryIdx%2 == 0 {
		adjustment = "none"
	}
	switch ot.key {
	case "vat":
		return periodOffset(spec.Offset{Days: c.vatFilingDays}, c.vatPayment, adjustment)
	case "wht":
		filing := 8 + (countryIdx*5)%24 // 8 … 29, so due dates reach every day of the month
		return periodOffset(spec.Offset{Days: filing}, spec.Offset{Days: filing + 2}, adjustment)
	case "env":
		filing := 20 + countryIdx%8
		payment := spec.Offset{Months: 1}
		if countryIdx%2 == 1 {
			payment = spec.Offset{Days: filing + 5}
		}
		return periodOffset(spec.Offset{Days: filing}, payment, adjustment)
	case "cit":
		if countryIdx%2 == 1 {
			// Fixed instalment dates: the first on or after each quarter end.
			return spec.DeadlineRule{
				Type: "fixed", Reference: "period_end", WeekendAdjustment: "none",
				FixedDates:        []string{"04-30", "07-31", "10-31", "01-31"},
				PaymentFixedDates: []string{"05-15", "08-15", "11-15", "02-15"},
			}
		}
		return periodOffset(spec.Offset{Months: 1}, spec.Offset{Months: 1, Days: 15}, "next-business-day")
	default: // tp: documentation, no payment — payment = filing
		return spec.DeadlineRule{
			Type: "period_offset", Reference: "period_end", WeekendAdjustment: "none",
			PeriodStart:  &spec.MonthDay{Day: 1, Month: fiscalStartMonth(e.spec.FinancialYearEnd)},
			FilingOffset: &spec.Offset{Months: 6},
		}
	}
}

// dueDateRuleFor is the workflow's filing rule; it agrees with the entity
// obligation's filingOffset (the API computes the filing deadline from the
// workflow rule alone).
func dueDateRuleFor(e entity, ot obligationTypeDef) spec.DueDateRule {
	rule := deadlineRuleFor(e, ot)
	switch ot.key {
	case "cit":
		return spec.DueDateRule{Reference: "period_end", OffsetUnit: "months", OffsetValue: 1,
			OffsetDirection: "after", WeekendAdjustment: rule.WeekendAdjustment}
	case "tp":
		return spec.DueDateRule{Reference: "period_end", OffsetUnit: "months", OffsetValue: 6,
			OffsetDirection: "after", WeekendAdjustment: "none"}
	default:
		return spec.DueDateRule{Reference: "period_end", OffsetUnit: "days", OffsetValue: rule.FilingOffset.Days,
			OffsetDirection: "after", WeekendAdjustment: rule.WeekendAdjustment}
	}
}

// fiscalStartMonth is the month after the fiscal year end (1-12).
func fiscalStartMonth(fye string) int {
	m, _ := strconv.Atoi(fye[:2])
	return m%12 + 1
}

func (g *generator) entityObligations(t *spec.Tenant) {
	for _, e := range g.entitiesOut {
		for _, ot := range obligationTypeDefs {
			ref := e.country.taxPrefix + fmt.Sprintf("%03d%05d", e.index, 10000+e.index*7) + "-" + ot.code
			t.EntityObligations = append(t.EntityObligations, spec.EntityObligation{
				Key:                entityObligationKey(e.spec.Key, ot.key),
				Entity:             e.spec.Key,
				ObligationType:     g.cfg.Slug + "." + ot.key,
				Periodicity:        ot.periodicity,
				Currency:           e.country.currency,
				Jurisdiction:       e.country.name,
				TaxReferenceNumber: &ref,
				DeadlineRule:       deadlineRuleFor(e, ot),
			})
		}
	}
}

func entityObligationKey(entityKey, otKey string) string { return entityKey + "-" + otKey }

// specTemplates renders an obligation type's templates as spec rows.
func specTemplates(ot obligationTypeDef) []spec.TaskTemplate {
	out := make([]spec.TaskTemplate, 0, len(ot.templates))
	for i, td := range ot.templates {
		tmpl := spec.TaskTemplate{
			Key: td.key, Name: td.name, TaskType: td.taskType, RoleLabel: td.role,
			ApprovalRequired: td.approval, DueDateReference: td.reference,
			DueDateOffsetValue: td.offset, DueDateOffsetUnit: td.unit, DueDateOffsetDirection: td.direction,
			OrderIndex: i,
		}
		if td.dataTemplate != "" {
			dt := td.dataTemplate
			tmpl.DataTemplate = &dt
		}
		out = append(out, tmpl)
	}
	return out
}
