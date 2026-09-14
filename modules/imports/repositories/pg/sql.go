package pg

// The statements the committing half runs. Every one of them runs on the
// request transaction, so tenant isolation is the RLS predicate bound at the Tx
// seam (ADR-0004) and never a WHERE clause here; tenant_id likewise defaults
// from the app.tenant_id GUC on every INSERT.

// entityIndexColumns is the projection the natural-key resolution reads. It is
// the entity's whole importable surface plus the version the plan is computed
// from, because resolving a key and deciding create-vs-update-vs-unchanged are
// one question and must be answered from one read.
const entityIndexColumns = `id, parent_entity_id, name, legal_name, country, tax_residency,
	fiscal_calendar_pattern, financial_year_end, fiscal_week_end_day, fiscal_year_end_rule,
	status, updated_at`

const obligationColumns = `id, entity_id, obligation_type_id, tax_reference_number, jurisdiction,
	jurisdiction_state, currency, periodicity, deadline_rule, status, updated_at`

const (
	// selectEntityIndex reads the tenant's whole entity index.
	//
	// The whole index, and not a WHERE on the folded names, because the fold IS
	// the natural key: NormalizeKey collapses inner whitespace, strips the
	// zero-width characters a copy-and-paste drags in and lower-cases the rest,
	// and a SQL expression that reproduced it approximately would be a SECOND,
	// subtly different implementation of the one thing a natural key may not
	// have two of. A name carrying a non-breaking space would then match in Go
	// and miss in SQL, and the import would answer "create" for a record that
	// already exists — a duplicated tax entity, silently.
	//
	// So the fold happens in Go, once, over an indexed single-tenant scan. The
	// cost is bounded by the tenant's entity count (a tax book, not a ledger)
	// and paid once per import, which is a rare operation; the LIMIT above the
	// bound is there so that a tenant far outside that assumption is told it
	// rather than served a wrong answer.
	selectEntityIndex = `SELECT ` + entityIndexColumns + ` FROM entities ORDER BY id LIMIT $1`

	// selectObligationTypeIndex reads the tenant's obligation-type catalogue —
	// tenant-level reference data, a handful of rows — folded in Go for the
	// same reason, and by CODE or NAME because a spreadsheet writes either.
	selectObligationTypeIndex = `SELECT id, code, name FROM obligation_types ORDER BY id LIMIT $1`

	// selectObligationsForEntities reads every obligation of the entities a file
	// touches. The pair (entity, obligation type) is resolved from two ids, so
	// this needs no folding at all; over-fetching by entity and pairing in Go
	// keeps it one statement instead of one per row.
	selectObligationsForEntities = `SELECT ` + obligationColumns + `
		FROM entity_obligations WHERE entity_id = ANY($1::uuid[]) ORDER BY id`

	// insertEntity is modules/entities' own Create, minus custom_periods (a flat
	// file cannot carry an ordered list of named periods) and minus status (an
	// import records an entity, it does not retire one — the column defaults to
	// active exactly as the create route leaves it).
	insertEntity = `
		INSERT INTO entities (parent_entity_id, name, legal_name, country, tax_residency,
		                      fiscal_calendar_pattern, financial_year_end,
		                      fiscal_week_end_day, fiscal_year_end_rule)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING ` + entityIndexColumns

	// updateEntity is modules/entities' own Update with two differences.
	//
	// The first is the WHERE: updated_at must still be the version the dry run
	// computed the plan from. That is what turns "the plan said update" into a
	// promise — a record somebody edited in between fails to match, the use case
	// sees zero rows, and the whole batch is refused rather than reverted to the
	// state the plan assumed.
	//
	// The second is what it does NOT set: status and custom_periods. Neither is
	// importable, and a full-replace update that wrote them would clear, from
	// every row of every file, two values the file never mentioned.
	//
	// The columns it DOES set are not the file's values alone. An import update
	// merges — a column the file has no header for is left exactly as it is —
	// and because this statement writes whole rows, "leave it alone" arrives
	// here as the stored value handed back to it (usecases.mergeEntity). The
	// version pin is what makes that safe: the value came from the committing
	// transaction's own read of this very row.
	updateEntity = `
		UPDATE entities
		SET parent_entity_id = $3,
		    name = $4,
		    legal_name = $5,
		    country = $6,
		    tax_residency = $7,
		    fiscal_calendar_pattern = $8,
		    financial_year_end = $9,
		    fiscal_week_end_day = $10,
		    fiscal_year_end_rule = $11,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1 AND updated_at = $2
		RETURNING ` + entityIndexColumns

	insertObligation = `
		INSERT INTO entity_obligations (entity_id, obligation_type_id, tax_reference_number,
		                                jurisdiction, jurisdiction_state, currency,
		                                periodicity, deadline_rule)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING ` + obligationColumns

	// updateObligation leaves entity_id and obligation_type_id alone: they ARE
	// the natural key, so a row that named a different pair is a different
	// obligation, never a move. Status is left alone for the same reason as an
	// entity's, and the columns it does set arrive merged, as an entity's do —
	// deadline_rule above all, because a file with no deadline columns that
	// wrote this one would delete the calendar a statutory filing is dated by.
	updateObligation = `
		UPDATE entity_obligations
		SET tax_reference_number = $3,
		    jurisdiction = $4,
		    jurisdiction_state = $5,
		    currency = $6,
		    periodicity = $7,
		    deadline_rule = $8,
		    updated_by = NULLIF(current_setting('app.user_id', true), '')::uuid,
		    updated_at = NOW()
		WHERE id = $1 AND updated_at = $2
		RETURNING ` + obligationColumns

	batchColumns = `id, kind, file_name, byte_size, checksum, row_count, create_count,
		update_count, unchanged_count, invalid_count, status, created_by, created_at,
		committed_by, committed_at`

	insertBatch = `
		INSERT INTO import_batches (kind, file_name, byte_size, checksum, row_count,
		                            create_count, update_count, unchanged_count, invalid_count, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING ` + batchColumns

	// insertRow stores one planned row. changed_fields holds one OBJECT per
	// changed field — {field, from, to} — rather than the bare name the column
	// was first written with: a preview that cannot say what a field changes
	// FROM is not a preview. The column's CHECK asks only that it be a non-empty
	// array on an update, which both shapes satisfy, and the reader still
	// accepts the older one (see decodeChanges) so a plan made before this
	// change is still committable.
	insertRow = `
		INSERT INTO import_rows (batch_id, row_number, action, natural_key, target_id,
		                         target_updated_at, payload, changed_fields, issues)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	selectBatch = `SELECT ` + batchColumns + ` FROM import_batches WHERE id = $1 LIMIT 1`

	// selectBatchForUpdate is the commit's own read. FOR UPDATE serializes two
	// commits of the same batch: the second waits, then finds it 'committed' and
	// is refused. Without it both could read 'validated' and both would write.
	selectBatchForUpdate = `SELECT ` + batchColumns + ` FROM import_batches WHERE id = $1 FOR UPDATE`

	selectBatches = `SELECT ` + batchColumns + ` FROM import_batches
		WHERE kind = $1 ORDER BY created_at DESC, id DESC LIMIT $2 OFFSET $3`

	countBatches = `SELECT COUNT(*)::int FROM import_batches WHERE kind = $1`

	rowColumns = `row_number, action, natural_key, target_id, target_updated_at,
		payload, changed_fields, issues`

	selectRows = `SELECT ` + rowColumns + ` FROM import_rows
		WHERE batch_id = $1 ORDER BY row_number LIMIT $2 OFFSET $3`

	countRows = `SELECT COUNT(*)::int FROM import_rows WHERE batch_id = $1`

	markCommitted = `
		UPDATE import_batches
		SET status = 'committed',
		    committed_at = NOW(),
		    committed_by = NULLIF(current_setting('app.user_id', true), '')::uuid
		WHERE id = $1 AND status = 'validated'
		RETURNING ` + batchColumns
)
