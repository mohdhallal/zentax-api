// Package pg persists documents + document_versions. Every statement runs on
// the request transaction that carries the app.tenant_id GUC (ADR-0004), so
// RLS scopes reads and the composite FKs reject cross-tenant references;
// users (not RLS'd) is only ever joined pinned to the row's tenant.
package pg

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/platform/database"
)

var _ domain.DocumentRepository = (*DocumentRepo)(nil)

type DocumentRepo struct {
	db database.ExecerPg
}

func NewDocumentRepo(db database.ExecerPg) *DocumentRepo {
	return &DocumentRepo{db: db}
}

func (r *DocumentRepo) WorkflowExists(ctx context.Context, workflowID string) (bool, error) {
	var ok bool
	err := r.db.GetContext(ctx, &ok, `SELECT EXISTS (SELECT 1 FROM workflows WHERE id = $1)`, workflowID)
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
	views, _, err := r.list(ctx, `d.id = $7::uuid`, domain.ListDocumentsFilter{}, id)
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, nil //nolint:nilnil // nil,nil = not found (or deleted)
	}
	return &views[0], nil
}

func (r *DocumentRepo) ListViews(ctx context.Context, filter domain.ListDocumentsFilter) ([]domain.DocumentView, int, error) {
	return r.list(ctx, "", filter)
}

// list runs the view statement with the NULL-tolerant filters; extraWhere (with
// its own positional arg) narrows it further. The count shares the WHERE so
// pagination.total is exact.
func (r *DocumentRepo) list(
	ctx context.Context, extraWhere string, f domain.ListDocumentsFilter, extraArgs ...any,
) ([]domain.DocumentView, int, error) {
	var search *string
	if f.Search != nil && strings.TrimSpace(*f.Search) != "" {
		p := "%" + escapeLike(strings.TrimSpace(*f.Search)) + "%"
		search = &p
	}
	args := []any{f.WorkflowID, f.TaskInstanceID, f.EntityID, f.DocumentType, f.Year, search}
	args = append(args, extraArgs...)

	where := ""
	if extraWhere != "" {
		where = " AND " + extraWhere
	}

	query := viewSelect + where + viewOrder
	queryArgs := args
	if f.Limit > 0 {
		n := len(args)
		query += " LIMIT $" + itoa(n+1) + " OFFSET $" + itoa(n+2)
		queryArgs = append(append([]any{}, args...), f.Limit, f.Offset)
	}

	views := []domain.DocumentView{}
	if err := r.db.SelectContext(ctx, &views, query, queryArgs...); err != nil {
		return nil, 0, err
	}

	total := len(views)
	if f.Limit > 0 {
		if err := r.db.GetContext(ctx, &total, viewCount+where, args...); err != nil {
			return nil, 0, err
		}
	}
	return views, total, nil
}

func (r *DocumentRepo) ListVersions(ctx context.Context, documentID domain.DocumentID) ([]domain.DocumentVersion, error) {
	versions := []domain.DocumentVersion{}
	err := r.db.SelectContext(ctx, &versions, versionSelect+` WHERE v.document_id = $1 ORDER BY v.version DESC`, documentID)
	return versions, err
}

func (r *DocumentRepo) GetVersion(ctx context.Context, documentID domain.DocumentID, versionID string) (*domain.DocumentVersion, error) {
	var v domain.DocumentVersion
	err := r.db.GetContext(ctx, &v, versionSelect+` WHERE v.document_id = $1 AND v.id = $2 LIMIT 1`, documentID, versionID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil = not found
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func (r *DocumentRepo) GetLatestVersion(ctx context.Context, documentID domain.DocumentID) (*domain.DocumentVersion, error) {
	var v domain.DocumentVersion
	err := r.db.GetContext(ctx, &v, versionSelect+`
		JOIN documents d ON d.tenant_id = v.tenant_id AND d.id = v.document_id AND d.current_version = v.version
		WHERE v.document_id = $1 LIMIT 1`, documentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // nil,nil = not found
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// escapeLike escapes the LIKE metacharacters (and the escape char itself) so a
// search term is matched literally.
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
