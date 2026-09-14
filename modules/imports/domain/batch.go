package domain

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// This file is the COMMITTING half's vocabulary: what a dry run promises, and
// the seams through which it is read, stored and applied. The reading half
// (doc.go and its neighbours) turns a file into drafts and issues and knows
// nothing about the tenant; everything here needs the database, the requester's
// grants, or both.
//
// The two halves meet at exactly one place, FileReader, so the reading half can
// grow a new source format — a workbook, a delimiter it had to guess — without
// the writing half learning what a zip is.

// BatchStatus is the lifecycle of an uploaded file.
//
// Both terminal states are terminal in the DATABASE (a row trigger, SQLSTATE
// ZT036), not merely here: 'rejected' because a file with one bad row must
// never become committable by any path, and 'committed' because a plan that has
// been applied must never be applied twice.
type BatchStatus string

const (
	BatchValidated BatchStatus = "validated"
	BatchRejected  BatchStatus = "rejected"
	BatchCommitted BatchStatus = "committed"
)

// RowAction is what the commit will do with one file row. It is decided at the
// dry run and frozen there; the commit re-derives it and refuses the WHOLE
// batch if any row now resolves differently.
type RowAction string

const (
	// RowCreate — the natural key names no record yet.
	RowCreate RowAction = "create"
	// RowUpdate — the key names a record whose values this row changes.
	RowUpdate RowAction = "update"
	// RowUnchanged — the key names a record this row agrees with entirely. No
	// statement is issued for it and no audit entry is written, because nothing
	// happened. This action is what makes a repeated import provably a no-op
	// rather than a hopefully-idempotent one.
	RowUnchanged RowAction = "unchanged"
	// RowInvalid — the row cannot be written, and therefore neither can the file.
	RowInvalid RowAction = "invalid"
)

// Kind is a Target as it is stored and as the audit trail names it. The URL and
// the reading half use Target ("entity-obligations"); the database vocabulary
// doubles as a SQL identifier vocabulary and uses underscores.
type Kind string

const (
	KindEntities          Kind = "entities"
	KindEntityObligations Kind = "entity_obligations"
)

// KindOf is the stored kind of a target.
func KindOf(t Target) Kind {
	if t == TargetObligations {
		return KindEntityObligations
	}
	return KindEntities
}

// Target is the kind as the URL and the reading half write it.
func (k Kind) Target() Target {
	if k == KindEntityObligations {
		return TargetObligations
	}
	return TargetEntities
}

// Batch is one uploaded file: what it was, what the dry run promised, and
// whether it has been applied.
type Batch struct {
	ID             string      `db:"id"`
	Kind           Kind        `db:"kind"`
	FileName       string      `db:"file_name"`
	ByteSize       int64       `db:"byte_size"`
	Checksum       string      `db:"checksum"`
	RowCount       int         `db:"row_count"`
	CreateCount    int         `db:"create_count"`
	UpdateCount    int         `db:"update_count"`
	UnchangedCount int         `db:"unchanged_count"`
	InvalidCount   int         `db:"invalid_count"`
	Status         BatchStatus `db:"status"`
	CreatedBy      *string     `db:"created_by"`
	CreatedAt      time.Time   `db:"created_at"`
	CommittedBy    *string     `db:"committed_by"`
	CommittedAt    *time.Time  `db:"committed_at"`
}

// Committable reports whether this batch may still be applied.
func (b *Batch) Committable() bool { return b != nil && b.Status == BatchValidated }

// RowPlan is one file row's entry in the plan.
//
// Payload is the validated record the file asks for — the body the single-record
// create route could have been given by hand. Its references are still written
// by name; they are resolved when the row is written, because that is the moment
// the answer has to be true.
//
// TargetID and TargetUpdatedAt are the record the key resolved to and the
// VERSION the plan was computed from. The commit writes WHERE updated_at =
// TargetUpdatedAt, so a record edited between the dry run and the commit is
// refused rather than quietly reverted to the state the plan assumed.
type RowPlan struct {
	RowNumber       int             `db:"row_number"`
	Action          RowAction       `db:"action"`
	NaturalKey      string          `db:"natural_key"`
	TargetID        *string         `db:"target_id"`
	TargetUpdatedAt *time.Time      `db:"target_updated_at"`
	Payload         json.RawMessage `db:"payload"`
	Changes         []FieldChange   `db:"changed_fields"`
	Issues          json.RawMessage `db:"issues"`
}

// FieldChange is one field an update moves: its name, the value stored NOW, and
// the value the file would write.
//
// Both values, not only the name, because a preview that says "3 updated" and
// names three fields is a promise rather than a preview: "legalName will be
// corrected" and "legalName will be permanently erased" are the same sentence
// without the old value. They are carried on the plan — not recomputed when the
// report is read — because by then the file is gone and the record may have
// moved.
//
// From and To are the values as a customer would read them, not as the database
// holds them: a parent is its NAME, never its id, and a deadline rule is the
// sentence it computes ("1 month 7 days after period end"). nil is "empty".
type FieldChange struct {
	Field string  `json:"field"`
	From  *string `json:"from"`
	To    *string `json:"to"`
}

// ChangedFieldNames is the bare list of field names an update moves, for the
// part of the report that only has room for the names.
func ChangedFieldNames(changes []FieldChange) []string {
	names := make([]string, 0, len(changes))
	for _, c := range changes {
		names = append(names, c.Field)
	}
	return names
}

// RowIssues decodes the stored issues. A payload that cannot be read back is
// reported as exactly that rather than as "no issues": an invalid row must never
// be able to present itself as a clean one because its evidence failed to parse.
func (r *RowPlan) RowIssues() []Issue {
	if len(r.Issues) == 0 {
		return nil
	}
	var out []Issue
	if err := json.Unmarshal(r.Issues, &out); err != nil {
		return []Issue{{Severity: SeverityError, Row: r.RowNumber,
			Message: "this row's issues could not be read back"}}
	}
	return out
}

// EntityDraft decodes the payload of an entity row.
func (r *RowPlan) EntityDraft() (*EntityDraft, error) {
	var d EntityDraft
	if err := json.Unmarshal(r.Payload, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// ObligationDraft decodes the payload of an entity-obligation row.
func (r *RowPlan) ObligationDraft() (*ObligationDraft, error) {
	var d ObligationDraft
	if err := json.Unmarshal(r.Payload, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

// BatchPlan is a whole validated file: the rows in file order, the counts, the
// issues that belong to the file rather than to any one row, and how the file
// was read.
//
// HeaderRow / Columns / Ignored travel with the plan because "how did you read
// my file" is part of the dry run's answer, not a detail of it: a column bound
// to the wrong field is the one import mistake a customer cannot see by looking
// at the data afterwards.
type BatchPlan struct {
	Rows           []RowPlan
	FileIssues     []Issue
	HeaderRow      int
	Columns        map[string]string
	Ignored        []IgnoredColumn
	CreateCount    int
	UpdateCount    int
	UnchangedCount int
	InvalidCount   int
}

// Status is 'rejected' the moment one row is invalid. The database holds the
// same rule as a constraint, so a batch's status can never disagree with the
// plan it summarizes.
func (p *BatchPlan) Status() BatchStatus {
	if p.InvalidCount > 0 {
		return BatchRejected
	}
	return BatchValidated
}

// Add appends a planned row and keeps the counts in step with it, so a summary
// can never drift from the rows it summarizes.
func (p *BatchPlan) Add(row RowPlan) {
	p.Rows = append(p.Rows, row)
	switch row.Action {
	case RowCreate:
		p.CreateCount++
	case RowUpdate:
		p.UpdateCount++
	case RowUnchanged:
		p.UnchangedCount++
	case RowInvalid:
		p.InvalidCount++
	}
}

// Upload is the file as it reached the use case: already bounded by byte size,
// already hashed, not yet parsed.
type Upload struct {
	Target   Target
	FileName string
	Content  []byte
	Checksum string
	// MaxRows is the row cap this upload is read under. It travels with the
	// upload rather than sitting in the reader so that the bound is a property
	// of the deployment's configuration, not of whichever reader happens to be
	// wired in.
	MaxRows int
}

// ReadOutcome is what the reading half produces from one file: a draft per row
// it could read, the issues it raised, and the header binding it settled on.
//
// Drafts and Issues are BOTH returned even when there are errors: a customer
// fixing a file needs the whole list, and a row that failed still has a place in
// the report. Exactly one of EntityDraft / ObligationDraft is set on each draft,
// according to the target.
type ReadOutcome struct {
	// HeaderRow is the source row the header was found on, and Columns the
	// field→header map the reader bound — "this is how I read your file",
	// which is the first thing to check when a report looks wrong.
	HeaderRow int
	Columns   map[string]string
	// Ignored are the columns this import does not know. Reported, never
	// guessed at, and never a refusal.
	Ignored []IgnoredColumn
	// FileIssues belong to the file rather than to any row (no header row, a
	// workbook with two candidate sheets, a column claimed twice).
	FileIssues []Issue
	Rows       []DraftRow
}

// HasFileError reports whether the file itself is unreadable, in which case
// there is no row-by-row report to give.
func (o *ReadOutcome) HasFileError() bool {
	for _, issue := range o.FileIssues {
		if issue.IsError() {
			return true
		}
	}
	return false
}

// DraftRow is one row of the file after reading: the draft it asks for and
// everything wrong with it. A row with an error still appears — it is the row
// the customer has to go and fix.
type DraftRow struct {
	// Number is the row number the CUSTOMER sees in their own spreadsheet.
	Number     int
	Entity     *EntityDraft
	Obligation *ObligationDraft
	Issues     []Issue
	// DuplicateOfRow is the earlier row of this same file that already claimed
	// this row's natural key, and 0 when none did. The refusal itself is already
	// in Issues, addressed at the cell and naming what to do about it; this says
	// so to the PLANNER, which has to mark the row unwritable a second time and
	// must not describe it a second time. One mistake is one error.
	DuplicateOfRow int
}

// HasError reports whether this row blocks the commit.
func (r *DraftRow) HasError() bool {
	for _, issue := range r.Issues {
		if issue.IsError() {
			return true
		}
	}
	return false
}

// NaturalKey is the key this row is matched on, or "" when the row is too
// broken to have one.
func (r *DraftRow) NaturalKey() string {
	switch {
	case r.Entity != nil:
		return r.Entity.NaturalKey()
	case r.Obligation != nil:
		return r.Obligation.NaturalKey()
	default:
		return ""
	}
}

// Bounds the reading half must honour. They are declared here, with the port,
// because they are part of its contract rather than of any one reader: whatever
// format a reader learns to read, it stops at Upload.MaxRows and says so with
// this error, and the handler stops the request body at the configured byte cap
// long before a reader is reached.
var (
	// ErrTooManyRows is what a reader returns the moment a file's
	// (MaxRows+1)-th data row is reached. Nothing past that row is parsed, so
	// the memory an oversized file can cost is bounded by the row cap as well as
	// by the byte cap. The handler answers it with 413.
	ErrTooManyRows = errors.New("this file has more rows than an import may carry")
)

// FileReader is the reading half, behind an interface: it turns uploaded bytes
// into drafts and issues and reads nothing from the database.
//
// It is a port rather than a direct call for one reason that matters: the
// formats a customer sends will grow (a workbook, a semicolon-delimited export
// from a German Excel, a UTF-16 file) and the committing half must never have
// an opinion about any of them.
type FileReader interface {
	Read(ctx context.Context, upload Upload) (*ReadOutcome, error)
}

// ExistingEntity is an entity the natural key already resolves to, with the
// version the plan is computed from.
//
// Ambiguous is the honest answer to the one weakness of a name-shaped key: if a
// tenant already holds two entities whose folded names are equal, the importer
// cannot tell which one a row means, and says so rather than picking. It is not
// a database constraint because entities carry no uniqueness on name today and
// a tenant may legitimately already be in that state — it is the importer that
// refuses to guess, per row, and only for the rows affected.
type ExistingEntity struct {
	ID        string
	UpdatedAt time.Time
	ParentID  *string
	// ParentName is the parent's name, resolved alongside the id because a
	// change list has to show a customer what their file would do, and an id is
	// not something they can check. nil when the entity is top-level.
	ParentName   *string
	Name         string
	LegalName    *string
	Country      string
	TaxResidency *string
	Pattern      string
	YearEnd      *string
	WeekEndDay   string
	YearEndRule  string
	Status       string
	Ambiguous    bool
}

// ExistingObligation is an entity obligation the natural key already resolves
// to. Obligations carry no ambiguity: the pair (entity, obligation type) is
// resolved from two ids, and the repository refuses to answer with more than
// one row.
type ExistingObligation struct {
	ID                 string
	UpdatedAt          time.Time
	EntityID           string
	ObligationTypeID   string
	TaxReferenceNumber *string
	Jurisdiction       *string
	JurisdictionState  *string
	Currency           *string
	Periodicity        string
	DeadlineRule       json.RawMessage
	Status             string
}

// ImportRepository is everything the committing half needs from Postgres. Every
// method runs on the request transaction, so tenant isolation is the RLS
// predicate bound at the Tx seam (ADR-0004) and never a WHERE clause here.
//
// The lookups are deliberately NOT narrowed by the RBAC read scope, unlike the
// list repositories next door, and the reason is the opposite of carelessness:
// a scoped principal whose key lookup came back empty because the record is
// outside their subtree would be told their row is a CREATE, and would then be
// refused — or worse, would create a duplicate. Resolving the key blind and
// letting the authorizer refuse the write is the only ordering in which a
// scoped user gets a true answer ("you may not write this record") instead of a
// false one ("this record does not exist"). The cost is that such a user can
// learn a name is taken, which is the lesser of the two.
type ImportRepository interface {
	// EntitiesByKey resolves folded entity names to the entities they name.
	// A key matching more than one entity comes back Ambiguous.
	EntitiesByKey(ctx context.Context, keys []string) (map[string]ExistingEntity, error)
	// ObligationTypeIDsByKey resolves folded obligation-type codes OR names to
	// ids; a key matching more than one type is omitted, and the caller reports
	// it as unresolvable rather than picking.
	ObligationTypeIDsByKey(ctx context.Context, keys []string) (map[string]string, error)
	// ObligationsByEntityAndType resolves (entity id, obligation type id) pairs
	// to the obligations they name, keyed by ObligationDraft.NaturalKey's shape
	// built from the two FOLDED references the caller resolved them from.
	ObligationsByPair(ctx context.Context, pairs []ObligationPair) (map[string]ExistingObligation, error)

	CreateEntity(ctx context.Context, draft *EntityDraft, parentID *string) (*ExistingEntity, error)
	// UpdateEntity rewrites the entity only if it is still at expectedVersion.
	// A nil result with a nil error means it has moved, which refuses the batch.
	UpdateEntity(ctx context.Context, id string, expectedVersion time.Time, draft *EntityDraft, parentID *string) (*ExistingEntity, error)

	CreateObligation(ctx context.Context, draft *ObligationDraft, entityID, obligationTypeID string) (*ExistingObligation, error)
	UpdateObligation(ctx context.Context, id string, expectedVersion time.Time, draft *ObligationDraft) (*ExistingObligation, error)

	CreateBatch(ctx context.Context, batch *Batch, rows []RowPlan) (*Batch, error)
	GetBatch(ctx context.Context, id string) (*Batch, error)
	// GetBatchForUpdate is the commit's read, which takes a row lock: two
	// commits of one batch must not both see it as committable. The second
	// waits, then finds it committed and is refused.
	GetBatchForUpdate(ctx context.Context, id string) (*Batch, error)
	ListBatches(ctx context.Context, kind Kind, limit, offset int) ([]Batch, int, error)
	ListRows(ctx context.Context, batchID string, limit, offset int) ([]RowPlan, int, error)
	MarkCommitted(ctx context.Context, id string) (*Batch, error)
}

// ObligationPair is one resolved (entity, obligation type) lookup: the two ids
// to match on, and the folded key the answer is filed under.
type ObligationPair struct {
	Key              string
	EntityID         string
	ObligationTypeID string
}

// ImportUseCases is the module's own seam, as every module here has one.
type ImportUseCases interface {
	// Validate reads, validates and plans an uploaded file, and stores the plan.
	// It is the dry run: nothing of the customer's tax book is written.
	Validate(ctx context.Context, upload Upload) (*Batch, *BatchPlan, error)
	// Commit applies a validated plan, all of it or none of it.
	Commit(ctx context.Context, kind Kind, batchID string) (*Batch, error)
	Get(ctx context.Context, kind Kind, batchID string) (*Batch, error)
	Rows(ctx context.Context, kind Kind, batchID string, limit, offset int) ([]RowPlan, int, error)
	List(ctx context.Context, kind Kind, limit, offset int) ([]Batch, int, error)
	// Template is the column set a customer should send, so the product can hand
	// out the header row rather than leaving it to be guessed from a manual.
	Template(ctx context.Context, kind Kind) []Field
}
