package dto

import (
	"encoding/json"
	"time"

	"github.com/mohamadhallal/zentax-api/modules/imports/domain"
)

// BatchJSON is one uploaded file as the API returns it.
//
// `committable` is computed rather than left to the client to infer from
// `status`, because the rule it encodes — a rejected file can never be
// committed, a committed one never re-run — is server authority (ADR-0001), and
// a client that derived it would be a second place the rule could be wrong.
type BatchJSON struct {
	ID          string  `json:"id"`
	Kind        string  `json:"kind"`
	FileName    string  `json:"fileName"`
	ByteSize    int64   `json:"byteSize"`
	Checksum    string  `json:"checksum"`
	Status      string  `json:"status"`
	Committable bool    `json:"committable"`
	Summary     Summary `json:"summary"`

	CreatedBy   *string    `json:"createdBy"`
	CreatedAt   time.Time  `json:"createdAt"`
	CommittedBy *string    `json:"committedBy"`
	CommittedAt *time.Time `json:"committedAt"`
}

// Summary is what the dry run promises, in one line: this many records will be
// created, this many changed, this many are already as the file says, and this
// many cannot be imported at all.
type Summary struct {
	Rows      int `json:"rows"`
	Creates   int `json:"creates"`
	Updates   int `json:"updates"`
	Unchanged int `json:"unchanged"`
	Invalid   int `json:"invalid"`
}

func BatchToJSON(b *domain.Batch) BatchJSON {
	if b == nil {
		return BatchJSON{}
	}
	return BatchJSON{
		ID: b.ID, Kind: string(b.Kind), FileName: b.FileName, ByteSize: b.ByteSize,
		Checksum: b.Checksum, Status: string(b.Status), Committable: b.Committable(),
		Summary: Summary{
			Rows: b.RowCount, Creates: b.CreateCount, Updates: b.UpdateCount,
			Unchanged: b.UnchangedCount, Invalid: b.InvalidCount,
		},
		CreatedBy: b.CreatedBy, CreatedAt: b.CreatedAt,
		CommittedBy: b.CommittedBy, CommittedAt: b.CommittedAt,
	}
}

func BatchesToJSON(batches []domain.Batch) []BatchJSON {
	out := make([]BatchJSON, 0, len(batches))
	for i := range batches {
		out = append(out, BatchToJSON(&batches[i]))
	}
	return out
}

// RowJSON is one row of the dry run's report.
//
// `record` is the row as the server READ it — every value it took from the
// file, and the defaults a create would take — echoed back so the customer
// checks what was understood rather than what they think they typed. It is the
// single most useful thing a report can show about a create, because a column
// bound to the wrong field is invisible any other way. On an UPDATE it is not
// the record-to-be: an update writes only the fields the file spoke about, and
// `changes` below is the account of those.
//
// `changes` is the other half of that, and the half an UPDATE lives or dies by:
// each field the row would move, the value stored now, and the value the file
// would write. A report that named the fields and stopped there could not tell
// a customer whether "legalName" was about to be corrected or permanently
// erased, which is not a preview — it is a promise they are asked to take on
// trust. `changedFields` is the same list with the values dropped, kept because
// a summary line has room for names and nothing else.
type RowJSON struct {
	Row           int                  `json:"row"`
	Action        string               `json:"action"`
	Key           string               `json:"key"`
	TargetID      *string              `json:"targetId"`
	ChangedFields []string             `json:"changedFields"`
	Changes       []domain.FieldChange `json:"changes"`
	Record        json.RawMessage      `json:"record"`
	Issues        []domain.Issue       `json:"issues"`
}

func RowToJSON(r *domain.RowPlan) RowJSON {
	if r == nil {
		return RowJSON{}
	}
	changes := r.Changes
	if changes == nil {
		changes = []domain.FieldChange{}
	}
	record := stripPlanBookkeeping(r.Payload)
	if len(record) == 0 {
		record = json.RawMessage("{}")
	}
	issues := r.RowIssues()
	if issues == nil {
		issues = []domain.Issue{}
	}
	return RowJSON{
		Row: r.RowNumber, Action: string(r.Action), Key: r.NaturalKey,
		TargetID: r.TargetID, ChangedFields: domain.ChangedFieldNames(changes),
		Changes: changes, Record: record, Issues: issues,
	}
}

// stripPlanBookkeeping drops `spoke` — the set of fields the file made a
// statement about — from the record echoed back.
//
// It is stored with the payload because the planner needs it at commit time,
// when the file is long gone, and it is what makes an update leave alone every
// column the file has no header for. It is not part of the record, though: what
// the file said about a field is reported as a change, and which columns it
// carried is reported as `columns`.
func stripPlanBookkeeping(payload json.RawMessage) json.RawMessage {
	if len(payload) == 0 {
		return payload
	}
	var record map[string]json.RawMessage
	if err := json.Unmarshal(payload, &record); err != nil {
		return payload
	}
	if _, present := record["spoke"]; !present {
		return payload
	}
	delete(record, "spoke")
	out, err := json.Marshal(record)
	if err != nil {
		return payload
	}
	return out
}

func RowsToJSON(rows []domain.RowPlan) []RowJSON {
	out := make([]RowJSON, 0, len(rows))
	for i := range rows {
		out = append(out, RowToJSON(&rows[i]))
	}
	return out
}

// DryRunJSON is the upload's own answer: the batch, the rows it planned, and
// what the reader had to decide in order to read the file at all.
//
// `columns` and `ignored` are the answer to "how did you read my file", and
// they are on the response rather than buried in a log because a column bound
// to the wrong field, or a column silently not imported, is the one import
// mistake a customer cannot see in the data.
type DryRunJSON struct {
	Batch   BatchJSON      `json:"batch"`
	Rows    []RowJSON      `json:"rows"`
	Issues  []domain.Issue `json:"fileIssues"`
	Columns ColumnsJSON    `json:"columns"`
}

// ColumnsJSON is the header binding, reported back.
type ColumnsJSON struct {
	HeaderRow int                    `json:"headerRow"`
	Bound     map[string]string      `json:"bound"`
	Ignored   []domain.IgnoredColumn `json:"notImported"`
}

// TemplateJSON is the column set a customer should send. `aliases` are the
// other spellings accepted, so a customer with an existing export can check
// whether their headers already match rather than renaming anything.
type TemplateJSON struct {
	Kind   string         `json:"kind"`
	Fields []domain.Field `json:"fields"`
}
