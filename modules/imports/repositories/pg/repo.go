// Package pg persists spreadsheet ingest: the natural-key resolution the dry
// run needs, the writes the commit makes, and the plan itself.
//
// It writes the entities and entity_obligations tables directly rather than
// through those modules' repositories, because modules here are vertical slices
// that do not reach into one another's persistence. What it may NOT do is
// bypass their RULES, and it does not: the authorizing and auditing this
// repository's caller performs are the same calls the single-record use cases
// make, against the same capabilities, so a record that arrives by file is
// governed exactly as one that arrives by hand.
package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

// isNoRows is how both version-pinned updates report "the record has moved":
// the RETURNING produced nothing because the WHERE did not match.
func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

var _ domain.ImportRepository = (*ImportRepo)(nil)

// maxResolvableRecords bounds the single-tenant scans the natural-key
// resolution performs. It is a REFUSAL, not a truncation: a tenant holding more
// entities than this would have its keys resolved from an incomplete index, and
// an incomplete index answers "create" for records that already exist. Telling
// the operator is the only safe failure.
//
// 50,000 is far above a tax book (the largest demo fixture is ~200 entities and
// ~97,000 task instances) and far below anything that would strain one scan.
const maxResolvableRecords = 50000

type ImportRepo struct {
	db database.ExecerPg
}

func NewImportRepo(db database.ExecerPg) *ImportRepo {
	return &ImportRepo{db: db}
}

// entityRow is the scan target for the entity index and for the two entity
// writes. json.RawMessage / *string map the nullable columns; updated_at is
// carried because it IS the version the plan is pinned to.
type entityRow struct {
	ID           string    `db:"id"`
	ParentID     *string   `db:"parent_entity_id"`
	Name         string    `db:"name"`
	LegalName    *string   `db:"legal_name"`
	Country      string    `db:"country"`
	TaxResidency *string   `db:"tax_residency"`
	Pattern      string    `db:"fiscal_calendar_pattern"`
	YearEnd      *string   `db:"financial_year_end"`
	WeekEndDay   string    `db:"fiscal_week_end_day"`
	YearEndRule  string    `db:"fiscal_year_end_rule"`
	Status       string    `db:"status"`
	UpdatedAt    time.Time `db:"updated_at"`
}

func (r entityRow) toDomain() domain.ExistingEntity {
	return domain.ExistingEntity{
		ID: r.ID, UpdatedAt: r.UpdatedAt, ParentID: r.ParentID,
		Name: r.Name, LegalName: r.LegalName, Country: r.Country, TaxResidency: r.TaxResidency,
		Pattern: r.Pattern, YearEnd: r.YearEnd, WeekEndDay: r.WeekEndDay,
		YearEndRule: r.YearEndRule, Status: r.Status,
	}
}

type obligationRow struct {
	ID                 string          `db:"id"`
	EntityID           string          `db:"entity_id"`
	ObligationTypeID   string          `db:"obligation_type_id"`
	TaxReferenceNumber *string         `db:"tax_reference_number"`
	Jurisdiction       *string         `db:"jurisdiction"`
	JurisdictionState  *string         `db:"jurisdiction_state"`
	Currency           *string         `db:"currency"`
	Periodicity        string          `db:"periodicity"`
	DeadlineRule       json.RawMessage `db:"deadline_rule"`
	Status             string          `db:"status"`
	UpdatedAt          time.Time       `db:"updated_at"`
}

func (r obligationRow) toDomain() domain.ExistingObligation {
	return domain.ExistingObligation{
		ID: r.ID, UpdatedAt: r.UpdatedAt, EntityID: r.EntityID,
		ObligationTypeID: r.ObligationTypeID, TaxReferenceNumber: r.TaxReferenceNumber,
		Jurisdiction: r.Jurisdiction, JurisdictionState: r.JurisdictionState,
		Currency: r.Currency, Periodicity: r.Periodicity,
		DeadlineRule: r.DeadlineRule, Status: r.Status,
	}
}

// EntitiesByKey folds the tenant's entity index in Go and answers only the keys
// asked for.
//
// A key that matches more than one entity comes back Ambiguous rather than
// resolved to the first: entities carry no uniqueness on name, a tenant may
// already hold two called "Holdings BV", and picking one would attach a file's
// row to whichever the scan happened to reach first. The caller turns ambiguity
// into a row error naming the key, which is a thing a customer can fix.
func (r *ImportRepo) EntitiesByKey(ctx context.Context, keys []string) (map[string]domain.ExistingEntity, error) {
	wanted := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k != "" {
			wanted[k] = struct{}{}
		}
	}
	out := make(map[string]domain.ExistingEntity, len(wanted))
	if len(wanted) == 0 {
		return out, nil
	}

	var rows []entityRow
	if err := r.db.SelectContext(ctx, &rows, selectEntityIndex, maxResolvableRecords+1); err != nil {
		return nil, fmt.Errorf("imports: read entity index: %w", err)
	}
	if len(rows) > maxResolvableRecords {
		return nil, fmt.Errorf("imports: this tenant holds more than %d entities, which is more than an import can resolve names against", maxResolvableRecords)
	}

	// The whole index is in hand, so a parent id costs nothing to name — and a
	// change list that says "parent: Acme Group NV → (empty)" is checkable by a
	// customer in a way that one naming a uuid is not.
	nameOfID := make(map[string]string, len(rows))
	for _, row := range rows {
		nameOfID[row.ID] = row.Name
	}

	for _, row := range rows {
		key := domain.NormalizeKey(row.Name)
		if _, want := wanted[key]; !want {
			continue
		}
		if existing, seen := out[key]; seen {
			existing.Ambiguous = true
			out[key] = existing
			continue
		}
		entity := row.toDomain()
		if row.ParentID != nil {
			if name, ok := nameOfID[*row.ParentID]; ok {
				entity.ParentName = &name
			}
		}
		out[key] = entity
	}
	return out, nil
}

// ObligationTypeIDsByKey folds the tenant's obligation-type catalogue by CODE
// and by NAME, because a spreadsheet writes either ("VAT-RET" or "VAT return").
// A key that two types answer to is omitted entirely — the caller reports it as
// unresolvable, which is the same refusal-to-guess as an ambiguous entity name.
func (r *ImportRepo) ObligationTypeIDsByKey(ctx context.Context, keys []string) (map[string]string, error) {
	wanted := make(map[string]struct{}, len(keys))
	for _, k := range keys {
		if k != "" {
			wanted[k] = struct{}{}
		}
	}
	if len(wanted) == 0 {
		return map[string]string{}, nil
	}

	var rows []struct {
		ID   string `db:"id"`
		Code string `db:"code"`
		Name string `db:"name"`
	}
	if err := r.db.SelectContext(ctx, &rows, selectObligationTypeIndex, maxResolvableRecords+1); err != nil {
		return nil, fmt.Errorf("imports: read obligation type index: %w", err)
	}
	if len(rows) > maxResolvableRecords {
		return nil, fmt.Errorf("imports: this tenant holds more than %d obligation types", maxResolvableRecords)
	}

	found := make(map[string]string, len(wanted))
	ambiguous := make(map[string]bool, len(wanted))
	claim := func(key, id string) {
		if key == "" {
			return
		}
		if _, want := wanted[key]; !want {
			return
		}
		if prior, seen := found[key]; seen && prior != id {
			ambiguous[key] = true
			return
		}
		found[key] = id
	}
	for _, row := range rows {
		claim(domain.NormalizeKey(row.Code), row.ID)
		claim(domain.NormalizeKey(row.Name), row.ID)
	}
	for key := range ambiguous {
		delete(found, key)
	}
	return found, nil
}

// ObligationsByPair resolves (entity, obligation type) pairs. No folding is
// involved — both sides are ids the caller already resolved — so this is an
// exact lookup, over-fetched by entity to keep it one statement.
func (r *ImportRepo) ObligationsByPair(ctx context.Context, pairs []domain.ObligationPair) (map[string]domain.ExistingObligation, error) {
	out := make(map[string]domain.ExistingObligation, len(pairs))
	if len(pairs) == 0 {
		return out, nil
	}
	entityIDs := make([]string, 0, len(pairs))
	seen := make(map[string]bool, len(pairs))
	byPair := make(map[string]string, len(pairs)) // entityID+"/"+typeID -> key
	for _, p := range pairs {
		if p.EntityID == "" || p.ObligationTypeID == "" {
			continue
		}
		if !seen[p.EntityID] {
			seen[p.EntityID] = true
			entityIDs = append(entityIDs, p.EntityID)
		}
		byPair[p.EntityID+"/"+p.ObligationTypeID] = p.Key
	}
	if len(entityIDs) == 0 {
		return out, nil
	}

	var rows []obligationRow
	if err := r.db.SelectContext(ctx, &rows, selectObligationsForEntities, entityIDs); err != nil {
		return nil, fmt.Errorf("imports: read obligations: %w", err)
	}
	for _, row := range rows {
		key, ok := byPair[row.EntityID+"/"+row.ObligationTypeID]
		if !ok {
			continue
		}
		// The product treats (entity, obligation type) as unique. If a tenant
		// somehow holds two, the first by id wins and the second is ignored
		// rather than silently overwriting the first: the caller's plan then
		// names one record and updates exactly that one.
		if _, dup := out[key]; dup {
			continue
		}
		out[key] = row.toDomain()
	}
	return out, nil
}

func (r *ImportRepo) CreateEntity(ctx context.Context, draft *domain.EntityDraft, parentID *string) (*domain.ExistingEntity, error) {
	var row entityRow
	err := r.db.GetContext(ctx, &row, insertEntity,
		parentID, draft.Name, draft.LegalName, draft.Country, draft.TaxResidency,
		draft.FiscalCalendarPattern, draft.FinancialYearEnd,
		draft.FiscalWeekEndDay, draft.FiscalYearEndRule)
	if err != nil {
		return nil, fmt.Errorf("imports: create entity: %w", err)
	}
	created := row.toDomain()
	return &created, nil
}

// UpdateEntity returns (nil, nil) when the record has moved since the plan was
// computed — the caller turns that into a refusal of the whole batch, never
// into a retry, because a retry would write over an edit nobody was shown.
func (r *ImportRepo) UpdateEntity(
	ctx context.Context, id string, expectedVersion time.Time, draft *domain.EntityDraft, parentID *string,
) (*domain.ExistingEntity, error) {
	var row entityRow
	err := r.db.GetContext(ctx, &row, updateEntity,
		id, expectedVersion,
		parentID, draft.Name, draft.LegalName, draft.Country, draft.TaxResidency,
		draft.FiscalCalendarPattern, draft.FinancialYearEnd,
		draft.FiscalWeekEndDay, draft.FiscalYearEndRule)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("imports: update entity: %w", err)
	}
	updated := row.toDomain()
	return &updated, nil
}

func (r *ImportRepo) CreateObligation(
	ctx context.Context, draft *domain.ObligationDraft, entityID, obligationTypeID string,
) (*domain.ExistingObligation, error) {
	rule, err := json.Marshal(draft.DeadlineRule)
	if err != nil {
		return nil, fmt.Errorf("imports: encode deadline rule: %w", err)
	}
	var row obligationRow
	err = r.db.GetContext(ctx, &row, insertObligation,
		entityID, obligationTypeID, draft.TaxReferenceNumber, draft.Jurisdiction,
		draft.JurisdictionState, draft.Currency, draft.Periodicity, rule)
	if err != nil {
		return nil, fmt.Errorf("imports: create entity obligation: %w", err)
	}
	created := row.toDomain()
	return &created, nil
}

func (r *ImportRepo) UpdateObligation(
	ctx context.Context, id string, expectedVersion time.Time, draft *domain.ObligationDraft,
) (*domain.ExistingObligation, error) {
	rule, err := json.Marshal(draft.DeadlineRule)
	if err != nil {
		return nil, fmt.Errorf("imports: encode deadline rule: %w", err)
	}
	var row obligationRow
	err = r.db.GetContext(ctx, &row, updateObligation,
		id, expectedVersion,
		draft.TaxReferenceNumber, draft.Jurisdiction, draft.JurisdictionState,
		draft.Currency, draft.Periodicity, rule)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("imports: update entity obligation: %w", err)
	}
	updated := row.toDomain()
	return &updated, nil
}
