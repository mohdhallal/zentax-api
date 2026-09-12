package usecases

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/app"
	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/platform/storage"
)

const (
	tenantA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	wfID    = "44444444-4444-4444-4444-444444444444"
	tiID    = "66666666-6666-6666-6666-666666666666"
	docID   = "77777777-7777-7777-7777-777777777777"
	verID   = "88888888-8888-8888-8888-888888888888"
)

var pdfBytes = []byte("%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\n")

func tenantCtx(t *testing.T) context.Context {
	return app.WithTenantID(t.Context(), tenantA)
}

func pdfUpload(size int64) domain.FileUpload {
	return domain.FileUpload{FileName: "return.pdf", ContentType: "application/pdf", Size: size, Body: bytes.NewReader(pdfBytes)}
}

func sampleDoc(taskInstance *string) *domain.Document {
	return &domain.Document{
		ID: docID, WorkflowID: wfID, TaskInstanceID: taskInstance, Category: "compliance",
		DocumentType: "draft_return", CurrentVersion: 1, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
}

func sampleView() *domain.DocumentView {
	return &domain.DocumentView{ID: docID, WorkflowID: wfID, Version: 1, VersionID: verID, FileName: "return.pdf"}
}

func newUC(repo *domain.DocumentRepositoryMock, store *storage.Mock, max int64) *UseCases {
	return NewUseCases(repo, store, max)
}

// --- Create --------------------------------------------------------------

func TestCreate_HappyPath_StreamsAndPersists(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 1<<20)

	repo.On("WorkflowExists", ctx, wfID).Return(true, nil).Once()
	store.On("Put", ctx, mock.MatchedBy(func(key string) bool {
		return strings.HasPrefix(key, "tenants/"+tenantA+"/documents/")
	}), int64(len(pdfBytes)), "application/pdf").Return(nil).Once()
	repo.On("Create", ctx, mock.MatchedBy(func(d domain.NewDocument) bool {
		return d.WorkflowID == wfID && d.Category == "compliance" && d.DocumentType == "draft_return" && d.TaskInstanceID == nil
	}), mock.MatchedBy(func(v domain.NewVersion) bool {
		return v.Version == 1 && v.FileName == "return.pdf" && v.FileSize == int64(len(pdfBytes)) &&
			v.MimeType == "application/pdf" && len(v.SHA256) == 64 &&
			v.StorageKey == StorageKey(tenantA, v.DocumentID, v.ID)
	})).Return(nil).Once()
	repo.On("GetView", ctx, mock.Anything).Return(sampleView(), nil).Once()

	view, err := uc.Create(ctx, domain.CreateDocumentInput{
		WorkflowID: wfID, DocumentType: "draft_return", File: pdfUpload(int64(len(pdfBytes))),
	})
	require.NoError(t, err)
	assert.Equal(t, docID, view.ID)
	repo.AssertExpectations(t)
	store.AssertExpectations(t)
}

func TestCreate_FileNameIsPathStripped(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 1<<20)

	repo.On("WorkflowExists", ctx, wfID).Return(true, nil).Once()
	store.On("Put", ctx, mock.Anything, mock.Anything, "text/plain").Return(nil).Once()
	repo.On("Create", ctx, mock.Anything, mock.MatchedBy(func(v domain.NewVersion) bool {
		return v.FileName == "notes.txt"
	})).Return(nil).Once()
	repo.On("GetView", ctx, mock.Anything).Return(sampleView(), nil).Once()

	body := []byte("hello")
	_, err := uc.Create(ctx, domain.CreateDocumentInput{
		WorkflowID: wfID, DocumentType: "other",
		File: domain.FileUpload{FileName: `C:\Users\me\..\notes.txt`, ContentType: "text/plain; charset=utf-8", Size: 5, Body: bytes.NewReader(body)},
	})
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestCreate_InvalidVocabulary(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	uc := newUC(new(domain.DocumentRepositoryMock), new(storage.Mock), 1<<20)

	_, err := uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "invoice", File: pdfUpload(10)})
	assert.IsType(t, &apperrors.ValidationError{}, err)

	_, err = uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "other", Category: "misc", File: pdfUpload(10)})
	assert.IsType(t, &apperrors.ValidationError{}, err)

	_, err = uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "other", File: domain.FileUpload{FileName: "  ", Body: bytes.NewReader(nil)}})
	assert.IsType(t, &apperrors.ValidationError{}, err)
}

func TestCreate_WorkflowNotFound(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	uc := newUC(repo, new(storage.Mock), 1<<20)
	repo.On("WorkflowExists", ctx, wfID).Return(false, nil).Once()

	_, err := uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "other", File: pdfUpload(10)})
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertExpectations(t)
}

func TestCreate_TaskInstanceMustBelongToWorkflow(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 1<<20)
	ti := tiID

	// Another workflow's instance → 400.
	repo.On("WorkflowExists", ctx, wfID).Return(true, nil)
	repo.On("TaskInstanceRef", ctx, tiID).Return(&domain.TaskInstanceRef{ID: tiID, WorkflowID: "55555555-5555-5555-5555-555555555555"}, nil).Once()
	_, err := uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, TaskInstanceID: &ti, DocumentType: "other", File: pdfUpload(10)})
	assert.IsType(t, &apperrors.ValidationError{}, err)

	// Unknown instance (other tenant → invisible) → 400.
	repo.On("TaskInstanceRef", ctx, tiID).Return(nil, nil).Once()
	_, err = uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, TaskInstanceID: &ti, DocumentType: "other", File: pdfUpload(10)})
	assert.IsType(t, &apperrors.ValidationError{}, err)

	// Approved instance → 409 (ADR-0018).
	repo.On("TaskInstanceRef", ctx, tiID).Return(&domain.TaskInstanceRef{ID: tiID, WorkflowID: wfID, Approved: true}, nil).Once()
	_, err = uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, TaskInstanceID: &ti, DocumentType: "other", File: pdfUpload(10)})
	assert.IsType(t, &apperrors.ConflictError{}, err)

	store.AssertNotCalled(t, "Put", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestCreate_FileTooLarge_DeclaredSize(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 10)
	repo.On("WorkflowExists", ctx, wfID).Return(true, nil).Once()

	_, err := uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "other", File: pdfUpload(11)})
	var tl *domain.FileTooLargeError
	require.True(t, errors.As(err, &tl))
	assert.Equal(t, int64(10), tl.Max)
	store.AssertNotCalled(t, "Put", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestCreate_FileTooLarge_ActualStreamExceedsCap(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 10)
	repo.On("WorkflowExists", ctx, wfID).Return(true, nil).Once()
	// The mock drains the stream: the capped reader fails it.
	store.On("Put", ctx, mock.Anything, int64(-1), "application/pdf").Return(errors.New("short")).Once()
	store.On("Delete", mock.Anything, mock.Anything).Return(nil).Once()

	// Size unknown (0) so the declared-size gate does not fire; the bytes do.
	up := domain.FileUpload{FileName: "x.pdf", ContentType: "application/pdf", Size: 0, Body: bytes.NewReader(pdfBytes)}
	_, err := uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "other", File: up})
	var tl *domain.FileTooLargeError
	require.True(t, errors.As(err, &tl), "got %v", err)
	store.AssertExpectations(t)
}

func TestCreate_MimeRejected(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 1<<20)
	repo.On("WorkflowExists", ctx, wfID).Return(true, nil)

	// Not allowlisted.
	up := domain.FileUpload{FileName: "x.exe", ContentType: "application/x-msdownload", Size: 4, Body: bytes.NewReader([]byte("MZ\x00\x00"))}
	_, err := uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "other", File: up})
	var umt *domain.UnsupportedMediaTypeError
	require.True(t, errors.As(err, &umt))

	// Declared PDF, actually PNG → sniff mismatch.
	png := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, make([]byte, 16)...)
	up = domain.FileUpload{FileName: "x.pdf", ContentType: "application/pdf", Size: int64(len(png)), Body: bytes.NewReader(png)}
	_, err = uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "other", File: up})
	require.True(t, errors.As(err, &umt))
	assert.Equal(t, "image/png", umt.Sniffed)

	store.AssertNotCalled(t, "Put", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestCreate_DBFailureAfterPutDeletesBlob(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 1<<20)
	repo.On("WorkflowExists", ctx, wfID).Return(true, nil).Once()
	store.On("Put", ctx, mock.Anything, mock.Anything, "application/pdf").Return(nil).Once()
	repo.On("Create", ctx, mock.Anything, mock.Anything).Return(assert.AnError).Once()
	store.On("Delete", mock.Anything, mock.MatchedBy(func(k string) bool { return strings.HasPrefix(k, "tenants/") })).Return(nil).Once()

	_, err := uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "other", File: pdfUpload(int64(len(pdfBytes)))})
	assert.ErrorIs(t, err, assert.AnError)
	store.AssertExpectations(t)
}

func TestCreate_StoragePutFails(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 1<<20)
	repo.On("WorkflowExists", ctx, wfID).Return(true, nil).Once()
	store.On("Put", ctx, mock.Anything, mock.Anything, "application/pdf").Return(errors.New("disk full")).Once()
	store.On("Delete", mock.Anything, mock.Anything).Return(nil).Once()

	_, err := uc.Create(ctx, domain.CreateDocumentInput{WorkflowID: wfID, DocumentType: "other", File: pdfUpload(int64(len(pdfBytes)))})
	require.Error(t, err)
	repo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything, mock.Anything)
}

// --- AddVersion ------------------------------------------------------------

func TestAddVersion_NumbersFromBump(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 1<<20)

	repo.On("GetByID", ctx, docID).Return(sampleDoc(nil), nil).Once()
	repo.On("BumpVersion", ctx, docID, (*string)(nil)).Return(3, nil).Once()
	store.On("Put", ctx, mock.MatchedBy(func(k string) bool {
		return strings.HasPrefix(k, "tenants/"+tenantA+"/documents/"+docID+"/")
	}), int64(len(pdfBytes)), "application/pdf").Return(nil).Once()
	repo.On("InsertVersion", ctx, mock.MatchedBy(func(v domain.NewVersion) bool {
		return v.DocumentID == docID && v.Version == 3
	})).Return(nil).Once()
	repo.On("GetView", ctx, docID).Return(sampleView(), nil).Once()

	_, err := uc.AddVersion(ctx, docID, domain.AddVersionInput{File: pdfUpload(int64(len(pdfBytes)))})
	require.NoError(t, err)
	repo.AssertExpectations(t)
	store.AssertExpectations(t)
}

func TestAddVersion_ApprovedInstanceIsImmutable(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 1<<20)
	ti := tiID

	repo.On("GetByID", ctx, docID).Return(sampleDoc(&ti), nil).Once()
	repo.On("TaskInstanceRef", ctx, tiID).Return(&domain.TaskInstanceRef{ID: tiID, WorkflowID: wfID, Approved: true}, nil).Once()

	_, err := uc.AddVersion(ctx, docID, domain.AddVersionInput{File: pdfUpload(10)})
	require.IsType(t, &apperrors.ConflictError{}, err)
	assert.Equal(t, domain.MsgApprovedImmutable, err.Error())
	repo.AssertNotCalled(t, "BumpVersion", mock.Anything, mock.Anything, mock.Anything)
	store.AssertNotCalled(t, "Put", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestAddVersion_DeletedIs404(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	uc := newUC(repo, new(storage.Mock), 1<<20)
	gone := sampleDoc(nil)
	now := time.Now()
	gone.DeletedAt = &now
	repo.On("GetByID", ctx, docID).Return(gone, nil).Once()

	_, err := uc.AddVersion(ctx, docID, domain.AddVersionInput{File: pdfUpload(10)})
	assert.IsType(t, &apperrors.NotFoundError{}, err)
}

// --- Update / Delete ---------------------------------------------------------

func TestUpdate_ApprovedIs409_And_PartialFieldsPassThrough(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	uc := newUC(repo, new(storage.Mock), 1<<20)
	ti := tiID

	repo.On("GetByID", ctx, docID).Return(sampleDoc(&ti), nil).Once()
	repo.On("TaskInstanceRef", ctx, tiID).Return(&domain.TaskInstanceRef{ID: tiID, WorkflowID: wfID, Approved: true}, nil).Once()
	label := "x"
	_, err := uc.Update(ctx, docID, domain.UpdateDocumentInput{Label: &label})
	assert.IsType(t, &apperrors.ConflictError{}, err)

	// Not approved: only the given fields flow to the repo; the rest stay nil.
	repo.On("GetByID", ctx, docID).Return(sampleDoc(&ti), nil).Once()
	repo.On("TaskInstanceRef", ctx, tiID).Return(&domain.TaskInstanceRef{ID: tiID, WorkflowID: wfID}, nil).Once()
	repo.On("UpdateMetadata", ctx, docID, domain.UpdateDocumentInput{Label: &label}).Return(true, nil).Once()
	repo.On("GetView", ctx, docID).Return(sampleView(), nil).Once()
	_, err = uc.Update(ctx, docID, domain.UpdateDocumentInput{Label: &label})
	require.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestUpdate_InvalidVocabulary(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	uc := newUC(new(domain.DocumentRepositoryMock), new(storage.Mock), 1<<20)
	bad := "invoice"
	_, err := uc.Update(ctx, docID, domain.UpdateDocumentInput{DocumentType: &bad})
	assert.IsType(t, &apperrors.ValidationError{}, err)
}

func TestDelete_SoftDeleteThen404(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	uc := newUC(repo, new(storage.Mock), 1<<20)

	repo.On("GetByID", ctx, docID).Return(sampleDoc(nil), nil).Once()
	repo.On("SoftDelete", ctx, docID).Return(true, nil).Once()
	require.NoError(t, uc.Delete(ctx, docID))

	gone := sampleDoc(nil)
	now := time.Now()
	gone.DeletedAt = &now
	repo.On("GetByID", ctx, docID).Return(gone, nil).Once()
	assert.IsType(t, &apperrors.NotFoundError{}, uc.Delete(ctx, docID))

	repo.On("GetByID", ctx, "missing").Return(nil, nil).Once()
	assert.IsType(t, &apperrors.NotFoundError{}, uc.Delete(ctx, "missing"))
	repo.AssertExpectations(t)
}

func TestDelete_ApprovedIs409(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	uc := newUC(repo, new(storage.Mock), 1<<20)
	ti := tiID
	repo.On("GetByID", ctx, docID).Return(sampleDoc(&ti), nil).Once()
	repo.On("TaskInstanceRef", ctx, tiID).Return(&domain.TaskInstanceRef{ID: tiID, WorkflowID: wfID, Approved: true}, nil).Once()
	assert.IsType(t, &apperrors.ConflictError{}, uc.Delete(ctx, docID))
	repo.AssertNotCalled(t, "SoftDelete", mock.Anything, mock.Anything)
}

// --- Reads / download --------------------------------------------------------

func TestGet_DeletedIs404(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	uc := newUC(repo, new(storage.Mock), 1<<20)
	repo.On("GetView", ctx, docID).Return(nil, nil).Once()
	_, err := uc.Get(ctx, docID)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
}

func TestListByWorkflow_And_ByTaskInstance_404(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	uc := newUC(repo, new(storage.Mock), 1<<20)
	repo.On("WorkflowExists", ctx, wfID).Return(false, nil).Once()
	_, err := uc.ListByWorkflow(ctx, wfID)
	assert.IsType(t, &apperrors.NotFoundError{}, err)

	repo.On("TaskInstanceRef", ctx, tiID).Return(nil, nil).Once()
	_, err = uc.ListByTaskInstance(ctx, tiID)
	assert.IsType(t, &apperrors.NotFoundError{}, err)

	// An instance whose workflow the caller cannot READ — outside their entity
	// subtree — is a 404, not an empty list. WorkflowExists is read-scoped, so
	// false is exactly the answer a scoped caller gets.
	repo.On("TaskInstanceRef", ctx, tiID).Return(&domain.TaskInstanceRef{ID: tiID, WorkflowID: wfID}, nil).Once()
	repo.On("WorkflowExists", ctx, wfID).Return(false, nil).Once()
	_, err = uc.ListByTaskInstance(ctx, tiID)
	assert.IsType(t, &apperrors.NotFoundError{}, err)

	repo.On("TaskInstanceRef", ctx, tiID).Return(&domain.TaskInstanceRef{ID: tiID, WorkflowID: wfID}, nil).Once()
	repo.On("WorkflowExists", ctx, wfID).Return(true, nil).Once()
	ti := tiID
	repo.On("ListViews", ctx, domain.ListDocumentsFilter{TaskInstanceID: &ti}).Return([]domain.DocumentView{*sampleView()}, 1, nil).Once()
	views, err := uc.ListByTaskInstance(ctx, tiID)
	require.NoError(t, err)
	assert.Len(t, views, 1)
	repo.AssertExpectations(t)
}

func TestDownload_LatestAndMissingBlob(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	store := new(storage.Mock)
	uc := newUC(repo, store, 1<<20)
	ver := &domain.DocumentVersion{ID: verID, DocumentID: docID, Version: 2, StorageKey: "tenants/a/documents/d/v", FileName: "r.pdf", FileSize: 3, MimeType: "application/pdf"}

	// Download resolves the document through the read-scoped VIEW, so a
	// document outside the caller's subtree never reaches storage.
	repo.On("GetView", ctx, docID).Return(sampleView(), nil)
	repo.On("GetLatestVersion", ctx, docID).Return(ver, nil).Once()
	store.On("Get", ctx, ver.StorageKey).Return(io.NopCloser(strings.NewReader("abc")), storage.ObjectInfo{Size: 3, ContentType: "application/octet-stream"}, nil).Once()

	dl, err := uc.Download(ctx, docID)
	require.NoError(t, err)
	got, _ := io.ReadAll(dl.Body)
	dl.Body.Close()
	assert.Equal(t, "abc", string(got))
	assert.Equal(t, "application/pdf", dl.ContentType, "the stored MIME type wins over the adapter's")
	assert.Equal(t, 2, dl.Version.Version)

	// Blob gone from storage → internal error (500), not a 404.
	repo.On("GetLatestVersion", ctx, docID).Return(ver, nil).Once()
	store.On("Get", ctx, ver.StorageKey).Return(nil, storage.ObjectInfo{}, storage.ErrNotFound).Once()
	_, err = uc.Download(ctx, docID)
	require.Error(t, err)
	assert.NotErrorAs(t, err, new(*apperrors.NotFoundError))
	assert.Contains(t, err.Error(), domain.MsgBlobMissing)
}

func TestDownloadVersion_UnknownVersion404(t *testing.T) {
	t.Parallel()
	ctx := tenantCtx(t)
	repo := new(domain.DocumentRepositoryMock)
	uc := newUC(repo, new(storage.Mock), 1<<20)
	repo.On("GetView", ctx, docID).Return(sampleView(), nil).Once()
	repo.On("GetVersion", ctx, docID, verID).Return(nil, nil).Once()
	_, err := uc.DownloadVersion(ctx, docID, verID)
	assert.IsType(t, &apperrors.NotFoundError{}, err)

	// A document outside the caller's read scope is 404 before any version
	// lookup at all — the view is where the narrowing happens.
	repo.On("GetView", ctx, docID).Return(nil, nil).Once()
	_, err = uc.DownloadVersion(ctx, docID, verID)
	assert.IsType(t, &apperrors.NotFoundError{}, err)
	repo.AssertNumberOfCalls(t, "GetVersion", 1)
}

func TestSanitizeFileName(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"a.pdf":               "a.pdf",
		"/etc/passwd":         "passwd",
		`..\..\x.txt`:         "x.txt",
		"  spaced .pdf  ":     "spaced .pdf",
		"ctl\x00\x1fname":     "ctlname",
		"ünïcödé rapport.pdf": "ünïcödé rapport.pdf",
	} {
		got, err := sanitizeFileName(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"", "   ", "/", ".", "..", "dir/"} {
		_, err := sanitizeFileName(bad)
		assert.Error(t, err, "%q", bad)
	}
	long, err := sanitizeFileName(strings.Repeat("é", 300))
	require.NoError(t, err)
	assert.Equal(t, MaxFileNameLen, len([]rune(long)))
}

func TestStorageKey(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "tenants/t/documents/d/v", StorageKey("t", "d", "v"))
	assert.NoError(t, storage.ValidateKey(StorageKey(tenantA, docID, verID)))
}
