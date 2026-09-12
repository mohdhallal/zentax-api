// Package pg persists documents + document_versions. Every statement runs on
// the request transaction that carries the app.tenant_id GUC (ADR-0004), so
// RLS scopes reads and the composite FKs reject cross-tenant references;
// users (not RLS'd) is only ever joined pinned to the row's tenant.
package pg

import (
	"context"
	"database/sql"
	"errors"
	baserepo "github.com/mohamadhallal/zentax-api/shared/repositories"
	"strconv"
	"strings"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	authzpg "github.com/mohamadhallal/zentax-api/platform/authz/pg"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.DocumentRepository = (*DocumentRepo)(nil)

type DocumentRepo struct {
	db database.ExecerPg
}

func NewDocumentRepo(db database.ExecerPg) *DocumentRepo {
	return &DocumentRepo{db: db}
}

// readScope resolves the caller's readable entity set for documents
// (ADR-0012 B-3). A document reaches its entity through its workflow; every
// read statement below carries the predicate, so the list, the single get, the
// version list and the download all narrow together — the download especially,
// since that one returns bytes.
func (r *DocumentRepo) readScope(ctx context.Context) (authz.ReadScope, error) {
	return authzpg.ReadScope(ctx, r.db, authz.DocumentRead)
}

// documentScope renders the scope predicate for a table that reaches its
// entity through a DOCUMENT id (the version statements). Empty when the scope
// is unbounded.
func documentScope(scope authz.ReadScope, documentIDCol string, bind func(any) string) string {
	entity := scope.EntityPredicate("sw.entity_id", bind)
	if entity == "" {
		return ""
	}
	return "EXISTS (SELECT 1 FROM documents sd JOIN workflows sw ON sw.id = sd.workflow_id" +
		" WHERE sd.id = " + documentIDCol + " AND " + entity + ")"
}

// WorkflowExists reports whether the workflow is visible to the caller —
// narrowed to their read scope, so the two sub-lists (by workflow, by task
// instance) answer 404 for a workflow outside it rather than an empty page.
// The upload path calls this too, but only AFTER its own authorizer check, so
// a refused upload is still a 403.
func (r *DocumentRepo) WorkflowExists(ctx context.Context, workflowID string) (bool, error) {
	scope, err := r.readScope(ctx)
	if err != nil {
		return false, err
	}
	var b whereBuilder
	b.add("id = " + b.bind(workflowID) + "::uuid")
	if pred := scope.EntityPredicate("entity_id", b.bind); pred != "" {
		b.add(pred)
	}
	where, args := b.where()
	var ok bool
	err = r.db.GetContext(ctx, &ok,
		`SELECT EXISTS (SELECT 1 FROM workflows`+where+`)`, args...)
	return ok, err
}

func (r *DocumentRepo) TaskInstanceRef(ctx context.Context, taskInstanceID string) (*domain.TaskInstanceRef, error) {
	var ref domain.TaskInstanceRef
	err := r.db.GetContext(ctx, &ref,
		`SELECT id, workflow_id, approved_by IS NOT NULL AS approved, status = 'pending_approval' AS pending
		 FROM task_instances WHERE id = $1 LIMIT 1`,
		taskInstanceID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil = not in this tenant
	}
	if err != nil {
		return nil, err
	}
	return &ref, nil
}

func (r *DocumentRepo) Create(ctx context.Context, doc domain.NewDocument, ver domain.NewVersion) error {
	if _, err := r.db.ExecContext(ctx, insertDocument,
		doc.ID, doc.WorkflowID, doc.TaskInstanceID, doc.Category, doc.DocumentType, doc.Label, doc.Notes); err != nil {
		if database.IsForeignKeyViolation(err) {
			// The composite FK is the last line of defence against a
			// cross-tenant workflow / task instance (FK checks bypass RLS).
			return apperrors.NewValidation(domain.MsgWorkflowNotFound)
		}
		return err
	}
	return r.InsertVersion(ctx, ver)
}

func (r *DocumentRepo) InsertVersion(ctx context.Context, ver domain.NewVersion) error {
	_, err := r.db.ExecContext(ctx, insertVersion,
		ver.ID, ver.DocumentID, ver.Version, ver.StorageKey, ver.FileName, ver.FileSize, ver.MimeType, ver.SHA256)
	return err
}

func (r *DocumentRepo) GetByID(ctx context.Context, id domain.DocumentID) (*domain.Document, error) {
	var d domain.Document
	err := r.db.GetContext(ctx, &d, `SELECT `+documentColumns+` FROM documents WHERE id = $1 LIMIT 1`, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil = not found
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func (r *DocumentRepo) BumpVersion(ctx context.Context, id domain.DocumentID, label *string) (int, error) {
	var version int
	err := r.db.GetContext(ctx, &version, bumpVersion, id, label)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return version, err
}

func (r *DocumentRepo) UpdateMetadata(ctx context.Context, id domain.DocumentID, input domain.UpdateDocumentInput) (bool, error) {
	res, err := r.db.ExecContext(ctx, updateMetadata, id, input.Label, input.Notes, input.DocumentType, input.Category)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *DocumentRepo) SoftDelete(ctx context.Context, id domain.DocumentID) (bool, error) {
	res, err := r.db.ExecContext(ctx, softDelete, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (r *DocumentRepo) GetView(ctx context.Context, id domain.DocumentID) (*domain.DocumentView, error) {
	views, _, err := r.list(ctx, domain.ListDocumentsFilter{}, &id)
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, nil //nolint:nilnil // nil,nil = not found (or deleted)
	}
	return &views[0], nil
}

func (r *DocumentRepo) ListViews(ctx context.Context, filter domain.ListDocumentsFilter) ([]domain.DocumentView, int, error) {
	return r.list(ctx, filter, nil)
}

// whereBuilder collects the predicates of the filters that are SET — and only
// those — numbering their binds $1..$n in order of appearance, so every
// statement shape is specific to its request rather than a generic
// `($n IS NULL OR …)` plan. Column names are code-owned constants; caller
// data only ever travels as a bind parameter.
type whereBuilder struct {
	preds []string
	args  []any
}

func (b *whereBuilder) bind(v any) string {
	b.args = append(b.args, v)
	return "$" + strconv.Itoa(len(b.args))
}

func (b *whereBuilder) add(pred string) { b.preds = append(b.preds, pred) }

func (b *whereBuilder) where() (string, []any) {
	return "\nWHERE " + strings.Join(b.preds, "\n  AND "), b.args
}

// documentsWhere renders the WHERE clause of the view: live documents only,
// the caller's read scope, optionally one document by id, then the set filters.
// The year set OR-s the requested financial years of the document's workflow,
// `none` standing for a workflow without one; the search pattern is escaped so
// the term is literal.
func documentsWhere(scope authz.ReadScope, f domain.ListDocumentsFilter, id *domain.DocumentID) (string, []any) {
	var b whereBuilder
	b.add("d.deleted_at IS NULL")
	// The view already joins the workflow, so the scope reads off w.entity_id
	// directly rather than paying for a second lookup.
	if pred := scope.EntityPredicate("w.entity_id", b.bind); pred != "" {
		b.add(pred)
	}
	if id != nil {
		b.add("d.id = " + b.bind(*id) + "::uuid")
	}
	if f.WorkflowID != nil {
		b.add("d.workflow_id = " + b.bind(*f.WorkflowID) + "::uuid")
	}
	if f.TaskInstanceID != nil {
		b.add("d.task_instance_id = " + b.bind(*f.TaskInstanceID) + "::uuid")
	}
	if f.EntityID != nil {
		b.add("w.entity_id = " + b.bind(*f.EntityID) + "::uuid")
	}
	if f.DocumentType != nil {
		b.add("d.document_type = " + b.bind(*f.DocumentType) + "::varchar")
	}
	if p := financialYearPredicate(f.Years, b.bind); p != "" {
		b.add(p)
	}
	if pattern := searchPattern(f.Search); pattern != "" {
		p := b.bind(pattern)
		b.add("(v.file_name ILIKE " + p + "::text ESCAPE '\\'" +
			"\n       OR d.label ILIKE " + p + "::text ESCAPE '\\'" +
			"\n       OR d.notes ILIKE " + p + "::text ESCAPE '\\')")
	}
	return b.where()
}

// financialYearPredicate OR-s the requested years of the document's workflow;
// the FinancialYearNone sentinel stands for a workflow with no financial year.
func financialYearPredicate(years []string, bind func(any) string) string {
	values := make([]string, 0, len(years))
	none := false
	for _, y := range years {
		if y == domain.FinancialYearNone {
			none = true
			continue
		}
		values = append(values, y)
	}
	switch {
	case len(values) == 0 && !none:
		return ""
	case len(values) == 0:
		return "w.financial_year IS NULL"
	}
	var in string
	if len(values) == 1 {
		in = "w.financial_year = " + bind(values[0]) + "::varchar"
	} else {
		in = "w.financial_year = ANY(" + bind(values) + "::varchar[])"
	}
	if none {
		return "(w.financial_year IS NULL OR " + in + ")"
	}
	return in
}

// searchPattern turns the raw search term into the ILIKE pattern: trimmed,
// LIKE metacharacters escaped so they match literally, wrapped in %…%. Empty
// (or whitespace-only) means no search.
func searchPattern(s *string) string {
	if s == nil {
		return ""
	}
	term := strings.TrimSpace(*s)
	if term == "" {
		return ""
	}
	return "%" + baserepo.EscapeLike(term) + "%"
}

// list runs the view statement over the filters that are set (plus, for
// GetView, one document id). The count shares the WHERE so pagination.total
// is exact; an unbounded list (Limit 0, the sub-lists) counts what it returns.
func (r *DocumentRepo) list(
	ctx context.Context, f domain.ListDocumentsFilter, id *domain.DocumentID,
) ([]domain.DocumentView, int, error) {
	scope, err := r.readScope(ctx)
	if err != nil {
		return nil, 0, err
	}
	where, params := documentsWhere(scope, f, id)

	query := viewSelect + where + viewOrder
	queryArgs := params
	if f.Limit > 0 {
		n := len(params)
		query += " LIMIT $" + strconv.Itoa(n+1) + " OFFSET $" + strconv.Itoa(n+2)
		queryArgs = append(append([]any{}, params...), f.Limit, f.Offset)
	}

	views := []domain.DocumentView{}
	if err := r.db.SelectContext(ctx, &views, query, queryArgs...); err != nil {
		return nil, 0, err
	}

	total := len(views)
	if f.Limit > 0 {
		if err := r.db.GetContext(ctx, &total, viewCount+where, params...); err != nil {
			return nil, 0, err
		}
	}
	return views, total, nil
}

// versionWhere renders the WHERE of a version statement: the document, the
// caller's read scope, and whatever else the caller adds.
func (r *DocumentRepo) versionWhere(ctx context.Context, documentID domain.DocumentID) (*whereBuilder, error) {
	scope, err := r.readScope(ctx)
	if err != nil {
		return nil, err
	}
	b := &whereBuilder{}
	b.add("v.document_id = " + b.bind(documentID) + "::uuid")
	if pred := documentScope(scope, "v.document_id", b.bind); pred != "" {
		b.add(pred)
	}
	return b, nil
}

func (r *DocumentRepo) ListVersions(ctx context.Context, documentID domain.DocumentID) ([]domain.DocumentVersion, error) {
	b, err := r.versionWhere(ctx, documentID)
	if err != nil {
		return nil, err
	}
	where, args := b.where()
	versions := []domain.DocumentVersion{}
	err = r.db.SelectContext(ctx, &versions, versionSelect+where+` ORDER BY v.version DESC`, args...)
	return versions, err
}

func (r *DocumentRepo) GetVersion(ctx context.Context, documentID domain.DocumentID, versionID string) (*domain.DocumentVersion, error) {
	b, err := r.versionWhere(ctx, documentID)
	if err != nil {
		return nil, err
	}
	b.add("v.id = " + b.bind(versionID) + "::uuid")
	where, args := b.where()
	var v domain.DocumentVersion
	err = r.db.GetContext(ctx, &v, versionSelect+where+` LIMIT 1`, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil = not found
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *DocumentRepo) GetLatestVersion(ctx context.Context, documentID domain.DocumentID) (*domain.DocumentVersion, error) {
	b, err := r.versionWhere(ctx, documentID)
	if err != nil {
		return nil, err
	}
	where, args := b.where()
	var v domain.DocumentVersion
	err = r.db.GetContext(ctx, &v, versionSelect+`
		JOIN documents d ON d.tenant_id = v.tenant_id AND d.id = v.document_id AND d.current_version = v.version`+
		where+` LIMIT 1`, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil = not found
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}
