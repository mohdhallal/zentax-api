package usecases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	entityobligationsdomain "github.com/mohamadhallal/zentax-api/modules/entityobligations/domain"
	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
)

// planFile turns read rows into the plan a commit will follow. It is the ONE
// place that decides create-vs-update-vs-unchanged, and both halves of the
// feature call it: the dry run to produce the plan, the commit to re-derive it
// and check that it still says the same thing. One function, so the two can
// never drift.
func (uc *UseCases) planFile(ctx context.Context, kind domain.Kind, rows []domain.DraftRow) (*domain.BatchPlan, *resolved, error) {
	if kind == domain.KindEntityObligations {
		return uc.planObligations(ctx, rows)
	}
	return uc.planEntities(ctx, rows)
}

// resolved is the tenant state a file was planned against, kept so the commit
// can write from the very same read it decided from. Reading it twice — once to
// decide, once to write — is how a commit ends up writing something the plan
// never saw.
type resolved struct {
	// entities is keyed by folded entity name, obligations by
	// ObligationDraft.NaturalKey, typeIDs by folded obligation-type reference.
	entities    map[string]domain.ExistingEntity
	typeIDs     map[string]string
	obligations map[string]domain.ExistingObligation
}

// ── entities ────────────────────────────────────────────────────────────────

func (uc *UseCases) planEntities(ctx context.Context, rows []domain.DraftRow) (*domain.BatchPlan, *resolved, error) {
	keys := make([]string, 0, len(rows)*2)
	inFile := make(map[string]int, len(rows)) // natural key → first row number that claims it
	for _, row := range rows {
		if row.Entity == nil {
			continue
		}
		keys = append(keys, row.Entity.NaturalKey(), row.Entity.ParentKey())
		if key := row.Entity.NaturalKey(); key != "" {
			if _, claimed := inFile[key]; !claimed {
				inFile[key] = row.Number
			}
		}
	}

	existing, err := uc.repo.EntitiesByKey(ctx, keys)
	if err != nil {
		return nil, nil, err
	}

	// parentOf is the file's own parent graph, folded key to folded key: the
	// input to both the cycle check and the scope anchor, computed once.
	parentOf := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.Entity == nil {
			continue
		}
		if key := row.Entity.NaturalKey(); key != "" {
			if _, dup := parentOf[key]; !dup {
				parentOf[key] = row.Entity.ParentKey()
			}
		}
	}

	// Rows whose parent is a row of this same file form a forest; a cycle in it
	// is a file that cannot be written in any order, so it is refused whole
	// rather than half-applied.
	cyclic := entityCycles(parentOf, inFile, existing)

	plan := &domain.BatchPlan{}
	for _, row := range rows {
		plan.Add(uc.planEntityRow(ctx, row, existing, inFile, parentOf, cyclic))
	}
	return plan, &resolved{entities: existing}, nil
}

func (uc *UseCases) planEntityRow(
	ctx context.Context,
	row domain.DraftRow,
	existing map[string]domain.ExistingEntity,
	inFile map[string]int,
	parentOf map[string]string,
	cyclic map[int]bool,
) domain.RowPlan {
	issues := append([]domain.Issue(nil), row.Issues...)
	planned := domain.RowPlan{RowNumber: row.Number, NaturalKey: row.NaturalKey()}

	draft := row.Entity
	if draft == nil {
		return invalidRow(planned, issues, "", "this row could not be read")
	}
	planned.Payload = mustJSON(draft)

	key := draft.NaturalKey()
	switch {
	case key == "":
		return invalidRow(planned, issues, domain.FieldName, "this row has no name, so it cannot be matched to a record")
	case inFile[key] != row.Number:
		// The reading half has already refused this row — at the cell, quoting
		// the name and naming the row it repeats. The plan has to agree that it
		// is unwritable; saying it again in weaker words, with no column to click
		// on, would show a customer two errors for one mistake.
		return invalidRow(planned, issues, domain.FieldName,
			duplicateMessage(row, fmt.Sprintf(
				"row %d already imports an entity by this name; a file may name each entity once", inFile[key])))
	case cyclic[row.Number]:
		return invalidRow(planned, issues, domain.FieldParent,
			"this row's parent chain loops back to itself, so there is no order in which the file can be written")
	}

	target, found := existing[key]
	if found && target.Ambiguous {
		return invalidRow(planned, issues, domain.FieldName,
			"this tenant already holds more than one entity with this name, so an import cannot tell which one this row means")
	}

	// The parent, in the three ways a file can name one.
	parentKey := draft.ParentKey()
	parentID := existingParent(existing, parentKey)
	_, parentInFile := inFile[parentKey]
	switch {
	case parentKey == "":
		// A top-level entity. Creating one is a tenant-level write, which only
		// a tenant-wide grant can make — the same rule the create route applies.
	case parentID != nil:
		// The parent is already in the tenant, so its id is known now.
	case parentInFile:
		// The parent is created by this same file, so its id does not exist
		// yet. The commit resolves it after writing the parent; the ORDER is
		// guaranteed by the topological sort, and the cycle check above
		// guarantees such an order exists.
	default:
		return invalidRow(planned, issues, domain.FieldParent,
			fmt.Sprintf("no entity called %q is in this file or in ZenTax", draft.ParentRef))
	}

	if row.HasError() {
		return invalidRow(planned, issues, "", "")
	}

	if !found {
		// A record that does not exist yet cannot be written from a file that
		// does not carry what a new one needs. An update from that same file is
		// fine, and is the whole reason the column is not required of the file.
		if missing := domain.MissingOnCreate(domain.TargetEntities, row.Number, draft.Spoke); len(missing) > 0 {
			return invalidRow(planned, append(issues, missing...), "", "")
		}
		// A create is scoped by where it lands: under the parent, or at the
		// tenant root. When the parent is a row of this same file its id is not
		// known yet, so the check is made against the first ANCESTOR that does
		// exist — which is exactly what the per-row check will conclude once the
		// ancestors are written, because subtree scope is inherited downwards.
		anchor := parentID
		if anchor == nil && parentKey != "" {
			anchor = existingAncestor(parentOf, existing, inFile, parentKey)
		}
		if err := uc.authorizer.EnsureEntityRef(ctx, anchor, authz.EntityWrite); err != nil {
			return forbiddenRow(planned, issues, err)
		}
		return withIssues(actioned(planned, domain.RowCreate), issues)
	}

	planned.TargetID = &target.ID
	version := target.UpdatedAt
	planned.TargetUpdatedAt = &version

	changed := entityChanges(draft, parentID, parentKey, target)
	if len(changed) == 0 {
		// Nothing to write, so nothing to authorize: an unchanged row is how a
		// repeated import proves itself a no-op, and requiring write permission
		// for it would stop a scoped user re-uploading the file they were given.
		return withIssues(actioned(planned, domain.RowUnchanged), issues)
	}
	if err := uc.authorizer.EnsureEntity(ctx, target.ID, authz.EntityWrite); err != nil {
		return forbiddenRow(planned, issues, err)
	}
	// Moving an entity to a new parent moves it in the scope tree, so the
	// DESTINATION is authorized as well as the record. The single-record update
	// route checks only the record today; an import that re-parents a hundred
	// entities is not the place to inherit that gap. A file with no parent
	// column moves nothing, so there is no destination to authorize.
	if draft.Said(domain.FieldParent) && parentID != nil && !samePtr(parentID, target.ParentID) {
		if err := uc.authorizer.EnsureEntityRef(ctx, parentID, authz.EntityWrite); err != nil {
			return forbiddenRow(planned, issues, err)
		}
	}
	planned.Action = domain.RowUpdate
	planned.Changes = changed
	return withIssues(planned, issues)
}

// entityChanges is the field-by-field difference an update would make, with the
// value stored now and the value the file would write for each one.
//
// The comparison is over exactly the columns an import writes AND the file
// spoke about: status and the custom period list are not importable, and a
// field with no column in this file is not the file's business (see changes.go).
// A parent that this file creates counts as a change by construction — the
// entity is about to hang off a record that does not exist yet.
func entityChanges(
	draft *domain.EntityDraft, parentID *string, parentKey string, target domain.ExistingEntity,
) []domain.FieldChange {
	c := changeSet{said: draft.Said}
	c.compare(domain.FieldName, ptrOf(target.Name), ptrOf(draft.Name))
	c.compare(domain.FieldLegalName, target.LegalName, draft.LegalName)
	c.compare(domain.FieldCountry, ptrOf(target.Country), ptrOf(draft.Country))
	c.compare(domain.FieldTaxResidency, target.TaxResidency, draft.TaxResidency)
	c.compare(domain.FieldFiscalCalendarPattern, ptrOf(target.Pattern), ptrOf(draft.FiscalCalendarPattern))
	c.compare(domain.FieldFinancialYearEnd, target.YearEnd, draft.FinancialYearEnd)
	c.compare(domain.FieldFiscalWeekEndDay, ptrOf(target.WeekEndDay), ptrOf(draft.FiscalWeekEndDay))
	c.compare(domain.FieldFiscalYearEndRule, ptrOf(target.YearEndRule), ptrOf(draft.FiscalYearEndRule))

	// The parent is matched by id and shown by NAME: an id in a change list is
	// not something a customer can check.
	if draft.Said(domain.FieldParent) {
		switch {
		case parentKey == "":
			if target.ParentID != nil {
				c.record(domain.FieldParent, target.ParentName, nil)
			}
		case parentID != nil:
			if !samePtr(parentID, target.ParentID) {
				c.record(domain.FieldParent, target.ParentName, textOrNil(draft.ParentRef))
			}
		default:
			// The parent is created by this file, so it is a change whatever the
			// record says today.
			c.record(domain.FieldParent, target.ParentName, textOrNil(draft.ParentRef))
		}
	}
	return c.sorted()
}

// entityCycles finds the rows a parent loop makes unwritable: the rows IN the
// loop, and the rows that merely lead into one, which are equally unwritable
// because the order they need does not exist either.
//
// Only IN-FILE parents can loop. A parent that already exists in the tenant is
// a fixed point — the entities table's own self-FK keeps the stored tree
// acyclic — and a parent that is neither in the file nor in the tenant is a
// different refusal, made by name in planEntityRow.
func entityCycles(parentOf map[string]string, inFile map[string]int, existing map[string]domain.ExistingEntity) map[int]bool {
	// The edges that actually constrain the write order: a parent this file
	// creates.
	edge := make(map[string]string, len(parentOf))
	for key, parent := range parentOf {
		if parent == "" || existingParent(existing, parent) != nil {
			continue
		}
		if _, inThisFile := inFile[parent]; inThisFile {
			edge[key] = parent
		}
	}

	// A key is settled as one of two verdicts, and BOTH are memoized. Memoizing
	// only "safe" would be enough to terminate but not enough to be correct:
	// a row whose walk reaches a key already known to be in a loop has to
	// inherit that verdict, or the row hanging off the loop is reported as
	// writable and the commit deadlocks on an order that cannot exist.
	const (
		unknown = 0
		inLoop  = 1
		safe    = 2
	)
	verdictOf := make(map[string]int, len(edge))
	cyclic := map[int]bool{}

	for start := range parentOf {
		var chain []string
		onPath := make(map[string]bool, len(edge))
		verdict := safe
		for cursor := start; cursor != ""; cursor = edge[cursor] {
			if known := verdictOf[cursor]; known != unknown {
				verdict = known
				break
			}
			if onPath[cursor] {
				verdict = inLoop
				break
			}
			onPath[cursor] = true
			chain = append(chain, cursor)
		}
		for _, key := range chain {
			verdictOf[key] = verdict
			if verdict == inLoop {
				if row, ok := inFile[key]; ok {
					cyclic[row] = true
				}
			}
		}
	}
	return cyclic
}

// existingAncestor walks an in-file parent chain up to the first entity that
// already exists, and returns its id — the scope anchor a create inherits.
//
// It is what lets a file create a whole group under one scoped grant: the
// subtree scope is inherited downwards, so if the first ancestor that ALREADY
// exists is inside the grant's subtree, every descendant this file adds beneath
// it is too. That is exactly what the per-row check will conclude once the
// ancestors have been written, which is why the dry run's verdict and the
// commit's agree.
func existingAncestor(
	parentOf map[string]string, existing map[string]domain.ExistingEntity, inFile map[string]int, parentKey string,
) *string {
	seen := map[string]bool{}
	for cursor := parentKey; cursor != "" && !seen[cursor]; {
		seen[cursor] = true
		if id := existingParent(existing, cursor); id != nil {
			return id
		}
		if _, inThisFile := inFile[cursor]; !inThisFile {
			return nil
		}
		cursor = parentOf[cursor]
	}
	return nil
}

func existingParent(existing map[string]domain.ExistingEntity, key string) *string {
	parent, ok := existing[key]
	if !ok || parent.Ambiguous {
		return nil
	}
	id := parent.ID
	return &id
}

// ── entity obligations ──────────────────────────────────────────────────────

func (uc *UseCases) planObligations(ctx context.Context, rows []domain.DraftRow) (*domain.BatchPlan, *resolved, error) {
	entityKeys := make([]string, 0, len(rows))
	typeKeys := make([]string, 0, len(rows))
	inFile := make(map[string]int, len(rows))
	for _, row := range rows {
		if row.Obligation == nil {
			continue
		}
		entityKeys = append(entityKeys, row.Obligation.EntityKey())
		typeKeys = append(typeKeys, row.Obligation.ObligationTypeKey())
		if key := row.Obligation.NaturalKey(); key != "" {
			if _, claimed := inFile[key]; !claimed {
				inFile[key] = row.Number
			}
		}
	}

	entities, err := uc.repo.EntitiesByKey(ctx, entityKeys)
	if err != nil {
		return nil, nil, err
	}
	typeIDs, err := uc.repo.ObligationTypeIDsByKey(ctx, typeKeys)
	if err != nil {
		return nil, nil, err
	}

	pairs := make([]domain.ObligationPair, 0, len(rows))
	for _, row := range rows {
		if row.Obligation == nil {
			continue
		}
		entity, okEntity := entities[row.Obligation.EntityKey()]
		typeID, okType := typeIDs[row.Obligation.ObligationTypeKey()]
		if !okEntity || entity.Ambiguous || !okType {
			continue
		}
		pairs = append(pairs, domain.ObligationPair{
			Key: row.Obligation.NaturalKey(), EntityID: entity.ID, ObligationTypeID: typeID,
		})
	}
	existing, err := uc.repo.ObligationsByPair(ctx, pairs)
	if err != nil {
		return nil, nil, err
	}

	plan := &domain.BatchPlan{}
	for _, row := range rows {
		plan.Add(uc.planObligationRow(ctx, row, entities, typeIDs, existing, inFile))
	}
	return plan, &resolved{entities: entities, typeIDs: typeIDs, obligations: existing}, nil
}

func (uc *UseCases) planObligationRow(
	ctx context.Context,
	row domain.DraftRow,
	entities map[string]domain.ExistingEntity,
	typeIDs map[string]string,
	existing map[string]domain.ExistingObligation,
	inFile map[string]int,
) domain.RowPlan {
	issues := append([]domain.Issue(nil), row.Issues...)
	planned := domain.RowPlan{RowNumber: row.Number, NaturalKey: row.NaturalKey()}

	draft := row.Obligation
	if draft == nil {
		return invalidRow(planned, issues, "", "this row could not be read")
	}
	planned.Payload = mustJSON(draft)

	if key := draft.NaturalKey(); inFile[key] != row.Number {
		// As for entities: the reader's refusal is the one with the cell
		// reference and the remedy, and this row is refused whatever it says.
		return invalidRow(planned, issues, domain.FieldObligationType,
			duplicateMessage(row, fmt.Sprintf(
				"row %d already imports this entity's %s; an entity has one obligation of each type",
				inFile[key], draft.ObligationTypeRef)))
	}

	entity, okEntity := entities[draft.EntityKey()]
	switch {
	case !okEntity:
		// Obligations hang off entities, and this import does not create them:
		// the entity file comes first. Saying so by name is the difference
		// between a fixable file and a mystery.
		return invalidRow(planned, issues, domain.FieldEntity,
			fmt.Sprintf("no entity called %q is in ZenTax; import the entities first", draft.EntityRef))
	case entity.Ambiguous:
		return invalidRow(planned, issues, domain.FieldEntity,
			fmt.Sprintf("more than one entity is called %q, so an import cannot tell which one this row means", draft.EntityRef))
	}

	if _, okType := typeIDs[draft.ObligationTypeKey()]; !okType {
		// Obligation TYPES are deliberately not created by an import: doing so
		// would write a third kind of record under a capability the caller was
		// never checked for.
		return invalidRow(planned, issues, domain.FieldObligationType,
			fmt.Sprintf("no obligation type matches %q; add it to ZenTax first, or correct the code", draft.ObligationTypeRef))
	}

	if row.HasError() {
		return invalidRow(planned, issues, "", "")
	}

	target, found := existing[draft.NaturalKey()]
	issues = append(issues, deadlineRemarks(row.Number, draft, target, found)...)
	if !found {
		// As for entities: a new obligation needs its filing frequency, and a
		// file that does not mention one can still correct the obligations that
		// already have it.
		if missing := domain.MissingOnCreate(domain.TargetObligations, row.Number, draft.Spoke); len(missing) > 0 {
			return invalidRow(planned, append(issues, missing...), "", "")
		}
		if err := uc.authorizer.EnsureEntity(ctx, entity.ID, authz.EntityObligationWrite); err != nil {
			return forbiddenRow(planned, issues, err)
		}
		return withIssues(actioned(planned, domain.RowCreate), issues)
	}

	planned.TargetID = &target.ID
	version := target.UpdatedAt
	planned.TargetUpdatedAt = &version

	changed := obligationChanges(draft, target)
	if len(changed) == 0 {
		return withIssues(actioned(planned, domain.RowUnchanged), issues)
	}
	// The obligation's OWN scope, resolved from the record rather than from the
	// file — the same call the single-record update makes. A row cannot move an
	// obligation between entities (the pair is its key), so there is no second
	// destination to authorize.
	if err := uc.authorizer.EnsureEntityObligation(ctx, target.ID, authz.EntityObligationWrite); err != nil {
		return forbiddenRow(planned, issues, err)
	}
	planned.Action = domain.RowUpdate
	planned.Changes = changed
	return withIssues(planned, issues)
}

// deadlineRemarks is what there is to say about a row that describes no
// deadline rule, which depends entirely on what it is about to land on: a new
// obligation with no schedule, an existing one whose schedule this row leaves
// exactly as it is, or an existing one that never had a schedule.
//
// The reading half cannot tell these apart — it has never seen the tenant — and
// a remark written for one of them is wrong for the others. The old text said
// "the obligation will be recorded" while a commit was erasing a live quarterly
// filing calendar underneath it.
func deadlineRemarks(
	rowNumber int, draft *domain.ObligationDraft, target domain.ExistingObligation, found bool,
) []domain.Issue {
	if describesRule(draft) {
		return nil
	}
	warn := func(message string) []domain.Issue {
		return []domain.Issue{{
			Severity: domain.SeverityWarning, Row: rowNumber,
			Field: domain.FieldDeadlineType, Message: message,
		}}
	}
	stored := describeStoredRule(target.DeadlineRule)
	switch {
	case !found:
		return warn(domain.ErrNoDeadlineRule())
	case !storedSchedules(target.DeadlineRule):
		return warn(domain.ErrStillNoDeadlineRule())
	case partialRule(draft) && stored != nil:
		return warn(domain.ErrDeadlineRuleKept(*stored))
	default:
		// An existing rule, and a file that says nothing about deadlines. That
		// is the ordinary correction — a new tax reference number, a jurisdiction
		// — and there is nothing to warn about: the rule stands.
		return nil
	}
}

// obligationChanges is the difference an update would make, over the columns
// the file spoke about — with the deadline rule treated as ONE field, because
// half a rule is not a rule and a row that does not describe a complete one
// leaves the stored rule alone.
func obligationChanges(draft *domain.ObligationDraft, target domain.ExistingObligation) []domain.FieldChange {
	c := changeSet{said: draft.Said}
	c.compare(domain.FieldTaxReferenceNumber, target.TaxReferenceNumber, draft.TaxReferenceNumber)
	c.compare(domain.FieldJurisdiction, target.Jurisdiction, draft.Jurisdiction)
	c.compare(domain.FieldJurisdictionState, target.JurisdictionState, draft.JurisdictionState)
	c.compare(domain.FieldCurrency, target.Currency, draft.Currency)
	c.compare(domain.FieldPeriodicity, ptrOf(target.Periodicity), ptrOf(draft.Periodicity))
	if merged := mergedRule(draft, decodeRule(target.DeadlineRule)); !sameRule(merged, target.DeadlineRule) {
		c.record(domain.FieldDeadlineType, describeStoredRule(target.DeadlineRule), describeRule(merged))
	}
	return c.sorted()
}

// sameRule compares a rule against the one an obligation stores, through their
// canonical JSON, because that is how the column stores one: encoding/json
// sorts keys, so equal rules compare equal whatever order Postgres hands the
// jsonb back in.
func sameRule(rule entityobligationsdomain.DeadlineRule, storedRaw json.RawMessage) bool {
	want, err := json.Marshal(rule)
	if err != nil {
		return false
	}
	var stored map[string]any
	if len(storedRaw) > 0 {
		if err := json.Unmarshal(storedRaw, &stored); err != nil {
			return false
		}
	}
	got, err := json.Marshal(stored)
	if err != nil {
		return false
	}
	var wantMap map[string]any
	if err := json.Unmarshal(want, &wantMap); err != nil {
		return false
	}
	normalized, err := json.Marshal(wantMap)
	if err != nil {
		return false
	}
	return string(normalized) == string(got)
}

// ── shared ──────────────────────────────────────────────────────────────────

// withIssues attaches a row's issues to its plan.
//
// EVERY row carries them, not only the refused ones. A warning is the dry run
// saying "this is legal, and probably not what you meant" — a new obligation
// with no deadline rule, a count of fixed dates that does not match the
// periodicity, a column that will not be imported — and a customer who never
// sees it finds out in a filing instead.
func withIssues(planned domain.RowPlan, issues []domain.Issue) domain.RowPlan {
	if len(issues) == 0 {
		planned.Issues = json.RawMessage("[]")
		return planned
	}
	planned.Issues = mustJSON(issues)
	return planned
}

// actioned sets a plan's verdict, so each success return stays one line.
func actioned(r domain.RowPlan, a domain.RowAction) domain.RowPlan {
	r.Action = a
	return r
}

// duplicateMessage is what the PLAN has to add about a row that repeats an
// earlier row's natural key — which is usually nothing.
//
// The reading half refuses such a row where it can address the cell, quote the
// name and say what to do about it (ErrDuplicateEntity, ErrDuplicateObligation);
// repeating that here produces two errors for one mistake, the second weaker
// than the first and with no column to click on. A row that is already refused
// for another reason gets nothing added either: two rows with no obligation
// type collide on a key made of two empty halves, and "row 2 already imports
// this entity's " is not a sentence to show anybody.
//
// The guard itself stays whatever this returns — the row is still unwritable,
// and the plan still says so.
func duplicateMessage(row domain.DraftRow, message string) string {
	if row.DuplicateOfRow != 0 || row.HasError() {
		return ""
	}
	return message
}

// invalidRow marks a row unwritable, adding a reason unless the reading half
// already supplied one.
func invalidRow(planned domain.RowPlan, issues []domain.Issue, field, message string) domain.RowPlan {
	planned.Action = domain.RowInvalid
	planned.TargetID = nil
	planned.TargetUpdatedAt = nil
	planned.Changes = nil
	if message != "" {
		issues = append(issues, domain.Issue{
			Severity: domain.SeverityError, Row: planned.RowNumber,
			Field: field, Message: message,
		})
	}
	planned.Issues = mustJSON(issues)
	return planned
}

// forbiddenRow is an authorization refusal rendered as a row error rather than
// as a 403 for the whole request.
//
// That is deliberate, and it is what makes the dry run useful to a scoped user:
// a file that mostly fits their subtree tells them exactly which rows do not,
// instead of failing whole with no indication of where. The file is still
// refused — an invalid row makes the batch 'rejected', which can never be
// committed — so nothing is weakened by reporting it this way.
func forbiddenRow(planned domain.RowPlan, issues []domain.Issue, err error) domain.RowPlan {
	if isForbidden(err) {
		return invalidRow(planned, issues, "", "you do not have permission to write this record")
	}
	// Not a permission refusal at all — a resolver failure, say. It still
	// refuses the row, and it still names what went wrong.
	return invalidRow(planned, issues, "", err.Error())
}

// isForbidden reads the domain error classification the rest of the tree uses
// (errors.ForbiddenError and anything else that answers IsForbidden), rather
// than comparing types: httperr.Classify makes the same interface check.
func isForbidden(err error) bool {
	var forbidden interface{ IsForbidden() bool }
	return errors.As(err, &forbidden) && forbidden.IsForbidden()
}

func samePtr(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}

// mustJSON encodes a value that is about to be stored. A failure here is a
// programming error — every one of these shapes is a plain struct of strings —
// and the honest fallback is an empty object, which the caller's own CHECK
// constraints will then refuse rather than silently storing nonsense.
func mustJSON(v any) json.RawMessage {
	out, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage("{}")
	}
	return out
}
