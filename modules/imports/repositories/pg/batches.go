package pg

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// batchRow is the scan target for import_batches.
type batchRow struct {
	ID             string     `db:"id"`
	Kind           string     `db:"kind"`
	FileName       string     `db:"file_name"`
	ByteSize       int64      `db:"byte_size"`
	Checksum       string     `db:"checksum"`
	RowCount       int        `db:"row_count"`
	CreateCount    int        `db:"create_count"`
	UpdateCount    int        `db:"update_count"`
	UnchangedCount int        `db:"unchanged_count"`
	InvalidCount   int        `db:"invalid_count"`
	Status         string     `db:"status"`
	CreatedBy      *string    `db:"created_by"`
	CreatedAt      time.Time  `db:"created_at"`
	CommittedBy    *string    `db:"committed_by"`
	CommittedAt    *time.Time `db:"committed_at"`
}

func (b batchRow) toDomain() domain.Batch {
	return domain.Batch{
		ID: b.ID, Kind: domain.Kind(b.Kind), FileName: b.FileName, ByteSize: b.ByteSize,
		Checksum: b.Checksum, RowCount: b.RowCount, CreateCount: b.CreateCount,
		UpdateCount: b.UpdateCount, UnchangedCount: b.UnchangedCount, InvalidCount: b.InvalidCount,
		Status: domain.BatchStatus(b.Status), CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt,
		CommittedBy: b.CommittedBy, CommittedAt: b.CommittedAt,
	}
}

// planRow is the scan target for import_rows. changed_fields and issues are
// jsonb and come back as raw bytes.
type planRow struct {
	RowNumber       int             `db:"row_number"`
	Action          string          `db:"action"`
	NaturalKey      string          `db:"natural_key"`
	TargetID        *string         `db:"target_id"`
	TargetUpdatedAt *time.Time      `db:"target_updated_at"`
	Payload         json.RawMessage `db:"payload"`
	ChangedFields   json.RawMessage `db:"changed_fields"`
	Issues          json.RawMessage `db:"issues"`
}

func (p planRow) toDomain() (domain.RowPlan, error) {
	changed, err := decodeChanges(p.ChangedFields)
	if err != nil {
		return domain.RowPlan{}, fmt.Errorf("imports: read changed fields of row %d: %w", p.RowNumber, err)
	}
	return domain.RowPlan{
		RowNumber: p.RowNumber, Action: domain.RowAction(p.Action), NaturalKey: p.NaturalKey,
		TargetID: p.TargetID, TargetUpdatedAt: p.TargetUpdatedAt,
		Payload: p.Payload, Changes: changed, Issues: p.Issues,
	}, nil
}

// decodeChanges reads the changed_fields array, which holds one object per
// changed field — the name, the value stored when the plan was made, and the
// value the file would write.
//
// A plan stored as a bare list of NAMES, which is the shape this column held
// before a dry run could show what an update would do, is still read: it is a
// plan a customer may be halfway through, and refusing to read it would turn
// their commit into a 500 rather than into the honest refusal the version check
// is there to give them.
func decodeChanges(raw json.RawMessage) ([]domain.FieldChange, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	changes := make([]domain.FieldChange, 0, len(entries))
	for _, entry := range entries {
		var name string
		if err := json.Unmarshal(entry, &name); err == nil {
			changes = append(changes, domain.FieldChange{Field: name})
			continue
		}
		var change domain.FieldChange
		if err := json.Unmarshal(entry, &change); err != nil {
			return nil, err
		}
		changes = append(changes, change)
	}
	return changes, nil
}

// CreateBatch writes the batch and its whole plan on one transaction — the
// request's, so a plan is never half-stored. The rows go in one statement per
// row rather than a single multi-row VALUES: the row cap keeps the count
// bounded, and a per-row statement keeps the parameter count far below the
// 65535 the protocol allows, which a 1,000-row multi-row insert of nine columns
// would sit uncomfortably close to.
func (r *ImportRepo) CreateBatch(ctx context.Context, batch *domain.Batch, rows []domain.RowPlan) (*domain.Batch, error) {
	var stored batchRow
	err := r.db.GetContext(ctx, &stored, insertBatch,
		string(batch.Kind), batch.FileName, batch.ByteSize, batch.Checksum, batch.RowCount,
		batch.CreateCount, batch.UpdateCount, batch.UnchangedCount, batch.InvalidCount,
		string(batch.Status))
	if err != nil {
		return nil, fmt.Errorf("imports: create batch: %w", err)
	}

	for i := range rows {
		row := &rows[i]
		changed, err := json.Marshal(emptySliceIfNil(row.Changes))
		if err != nil {
			return nil, fmt.Errorf("imports: encode changed fields of row %d: %w", row.RowNumber, err)
		}
		issues := row.Issues
		if len(issues) == 0 {
			issues = json.RawMessage("[]")
		}
		payload := row.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("{}")
		}
		if _, err := r.db.ExecContext(ctx, insertRow,
			stored.ID, row.RowNumber, string(row.Action), row.NaturalKey,
			row.TargetID, row.TargetUpdatedAt, []byte(payload), changed, []byte(issues)); err != nil {
			return nil, fmt.Errorf("imports: store planned row %d: %w", row.RowNumber, err)
		}
	}

	out := stored.toDomain()
	return &out, nil
}

func (r *ImportRepo) GetBatch(ctx context.Context, id string) (*domain.Batch, error) {
	return r.getBatch(ctx, selectBatch, id)
}

// GetBatchForUpdate is the commit's read: it takes a row lock so two commits of
// one batch cannot both see it as committable.
func (r *ImportRepo) GetBatchForUpdate(ctx context.Context, id string) (*domain.Batch, error) {
	return r.getBatch(ctx, selectBatchForUpdate, id)
}

func (r *ImportRepo) getBatch(ctx context.Context, query, id string) (*domain.Batch, error) {
	var row batchRow
	err := r.db.GetContext(ctx, &row, query, id)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("imports: read batch: %w", err)
	}
	out := row.toDomain()
	return &out, nil
}

func (r *ImportRepo) ListBatches(ctx context.Context, kind domain.Kind, limit, offset int) ([]domain.Batch, int, error) {
	var total int
	if err := r.db.GetContext(ctx, &total, countBatches, string(kind)); err != nil {
		return nil, 0, fmt.Errorf("imports: count batches: %w", err)
	}
	var rows []batchRow
	if err := r.db.SelectContext(ctx, &rows, selectBatches, string(kind), limit, offset); err != nil {
		return nil, 0, fmt.Errorf("imports: list batches: %w", err)
	}
	out := make([]domain.Batch, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.toDomain())
	}
	return out, total, nil
}

func (r *ImportRepo) ListRows(ctx context.Context, batchID string, limit, offset int) ([]domain.RowPlan, int, error) {
	var total int
	if err := r.db.GetContext(ctx, &total, countRows, batchID); err != nil {
		return nil, 0, fmt.Errorf("imports: count planned rows: %w", err)
	}
	var rows []planRow
	if err := r.db.SelectContext(ctx, &rows, selectRows, batchID, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("imports: list planned rows: %w", err)
	}
	out := make([]domain.RowPlan, 0, len(rows))
	for _, row := range rows {
		converted, err := row.toDomain()
		if err != nil {
			return nil, 0, err
		}
		out = append(out, converted)
	}
	return out, total, nil
}

// MarkCommitted moves the batch to its final state. The WHERE repeats the
// status the caller already checked under FOR UPDATE — belt and braces beneath
// the database trigger that refuses every other transition anyway (ZT036).
func (r *ImportRepo) MarkCommitted(ctx context.Context, id string) (*domain.Batch, error) {
	var row batchRow
	err := r.db.GetContext(ctx, &row, markCommitted, id)
	if isNoRows(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("imports: mark batch committed: %w", err)
	}
	out := row.toDomain()
	return &out, nil
}

// emptySliceIfNil keeps a nil slice out of the jsonb column, where it would
// encode as `null` and fail the array CHECK.
func emptySliceIfNil(values []domain.FieldChange) []domain.FieldChange {
	if values == nil {
		return []domain.FieldChange{}
	}
	return values
}
