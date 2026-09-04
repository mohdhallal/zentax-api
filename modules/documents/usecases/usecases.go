// Package usecases implements the documents application logic (ADR-0022):
// uploads validated server-side (size cap, MIME allowlist + content sniff),
// streamed to the Storage seam under an opaque tenant-prefixed key while the
// SHA-256 is computed, with the metadata rows written on the request
// transaction; versioning; soft delete; the ADR-0018 freeze for documents of
// approved task instances; audit on every mutation.
package usecases

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/authz"
	"github.com/mohamadhallal/zentax-api/platform/storage"
)

// MaxFileNameLen caps the stored file name (runes).
const MaxFileNameLen = 255

var _ domain.DocumentUseCases = (*UseCases)(nil)

type UseCases struct {
	repo       domain.DocumentRepository
	store      storage.Storage
	maxUpload  int64
	authorizer *authz.Authorizer
	audit      *audit.Recorder
}

// NewUseCases builds the documents use cases. maxUpload is the per-file byte
// cap; the authorizer is optional (variadic) so unit tests skip scope checks
// while the app always wires one.
func NewUseCases(repo domain.DocumentRepository, store storage.Storage, maxUpload int64, authorizer ...*authz.Authorizer) *UseCases {
	uc := &UseCases{repo: repo, store: store, maxUpload: maxUpload}
	if len(authorizer) > 0 {
		uc.authorizer = authorizer[0]
	}
	return uc
}

// WithAudit injects the audit recorder (ADR-0008). Optional and nil-safe.
func (uc *UseCases) WithAudit(r *audit.Recorder) *UseCases {
	uc.audit = r
	return uc
}

// StorageKey is the opaque, tenant-prefixed object key (ADR-0022 decision 3).
// Never derived from user input.
func StorageKey(tenantID, documentID, versionID string) string {
	return "tenants/" + tenantID + "/documents/" + documentID + "/" + versionID
}

// loadLive returns the document or a 404 for a missing / soft-deleted one.
func (uc *UseCases) loadLive(ctx context.Context, id domain.DocumentID) (*domain.Document, error) {
	doc, err := uc.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if doc == nil || doc.IsDeleted() {
		return nil, apperrors.NewNotFound(domain.ErrDocumentNotFound(id))
	}
	return doc, nil
}

// ensureMutable applies ADR-0018: a document attached to an approved task
// instance is frozen, and one attached to an instance awaiting approval is
// locked while the reviewer looks at it (409 either way). A dangling instance
// pointer (instance since deleted → SET NULL, or otherwise invisible) does not
// lock anything.
func (uc *UseCases) ensureMutable(ctx context.Context, doc *domain.Document) error {
	if doc.TaskInstanceID == nil {
		return nil
	}
	ref, err := uc.repo.TaskInstanceRef(ctx, *doc.TaskInstanceID)
	if err != nil {
		return err
	}
	if ref.Locked() {
		return apperrors.NewConflict(ref.LockMessage())
	}
	return nil
}

func (uc *UseCases) view(ctx context.Context, id domain.DocumentID) (*domain.DocumentView, error) {
	v, err := uc.repo.GetView(ctx, id)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, apperrors.NewNotFound(domain.ErrDocumentNotFound(id))
	}
	return v, nil
}

// stored is what storeFile learned while streaming.
type stored struct {
	ContentType string
	Size        int64
	SHA256      string
}

var errUploadTooLarge = errors.New("upload exceeds the size cap")

// cappedReader fails the stream once more than max bytes flow through it —
// defence in depth behind the transport's MaxBytesReader and the declared size.
type cappedReader struct {
	r    io.Reader
	max  int64
	seen int64
}

func (c *cappedReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.seen += int64(n)
	if c.seen > c.max {
		return n, errUploadTooLarge
	}
	return n, err
}

// storeFile validates the upload (size cap, declared-type allowlist, content
// sniff on the first 512 bytes) and streams it to storage under key, computing
// the SHA-256 and byte count on the way (io.TeeReader). Nothing is written
// before the validation passes; a failed or short write is deleted again.
func (uc *UseCases) storeFile(ctx context.Context, key string, file domain.FileUpload) (*stored, error) {
	if uc.store == nil {
		return nil, errors.New("documents: storage is not configured")
	}
	if file.Body == nil {
		return nil, apperrors.NewValidation(domain.MsgFileRequired)
	}
	if file.Size > uc.maxUpload {
		return nil, &domain.FileTooLargeError{Size: file.Size, Max: uc.maxUpload}
	}

	head := make([]byte, domain.SniffLen)
	n, err := io.ReadFull(file.Body, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, fmt.Errorf("documents: read upload: %w", err)
	}
	head = head[:n]

	contentType, err := domain.CheckContentType(file.ContentType, head)
	if err != nil {
		return nil, err
	}

	hash := sha256.New()
	counter := &countingWriter{}
	body := io.MultiReader(bytes.NewReader(head), file.Body)
	capped := &cappedReader{r: body, max: uc.maxUpload}
	tee := io.TeeReader(capped, io.MultiWriter(hash, counter))

	size := file.Size
	if size <= 0 {
		size = -1 // unknown to the adapter; fs verifies nothing, s3 buffers
	}
	if err := uc.store.Put(ctx, key, tee, size, contentType); err != nil {
		uc.deleteBlob(ctx, key)
		if errors.Is(err, errUploadTooLarge) || counter.n > uc.maxUpload {
			return nil, &domain.FileTooLargeError{Size: counter.n, Max: uc.maxUpload}
		}
		return nil, fmt.Errorf("documents: store blob: %w", err)
	}
	if file.Size > 0 && counter.n != file.Size {
		uc.deleteBlob(ctx, key)
		return nil, apperrors.NewValidation(fmt.Sprintf("upload was %d bytes, expected %d", counter.n, file.Size))
	}

	return &stored{
		ContentType: contentType,
		Size:        counter.n,
		SHA256:      hex.EncodeToString(hash.Sum(nil)),
	}, nil
}

// deleteBlob is the best-effort compensation when the metadata write fails
// after the bytes landed (the tx rolls back; the blob must not linger).
func (uc *UseCases) deleteBlob(ctx context.Context, key string) {
	if uc.store == nil {
		return
	}
	if err := uc.store.Delete(context.WithoutCancel(ctx), key); err != nil {
		logger.Log.WithContext(ctx).Warn("documents: orphan blob cleanup failed",
			logger.String("key", key), logger.Error(err))
	}
}

type countingWriter struct{ n int64 }

func (c *countingWriter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// sanitizeFileName strips any path (both separators), control characters and
// surrounding whitespace, and caps the length; empty → validation error.
func sanitizeFileName(name string) (string, error) {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return "", apperrors.NewValidation(domain.MsgFileNameRequired)
	}
	if utf8.RuneCountInString(name) > MaxFileNameLen {
		runes := []rune(name)
		name = string(runes[:MaxFileNameLen])
	}
	return name, nil
}

func tenantFrom(ctx context.Context) (string, error) {
	tenantID := app.GetTenantID(ctx)
	if tenantID == "" {
		return "", errors.New("documents: no tenant in context")
	}
	return tenantID, nil
}

func newID() string { return uuid.NewString() }
