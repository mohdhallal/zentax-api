package domain

import (
	"context"

	"github.com/stretchr/testify/mock"
)

// DocumentRepositoryMock is a testify mock of DocumentRepository.
type DocumentRepositoryMock struct {
	mock.Mock
}

var _ DocumentRepository = (*DocumentRepositoryMock)(nil)

func (m *DocumentRepositoryMock) WorkflowExists(ctx context.Context, workflowID string) (bool, error) {
	a := m.Called(ctx, workflowID)
	return a.Bool(0), a.Error(1)
}

func (m *DocumentRepositoryMock) TaskInstanceRef(ctx context.Context, taskInstanceID string) (*TaskInstanceRef, error) {
	a := m.Called(ctx, taskInstanceID)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).(*TaskInstanceRef), a.Error(1)
}

func (m *DocumentRepositoryMock) Create(ctx context.Context, doc NewDocument, ver NewVersion) error {
	a := m.Called(ctx, doc, ver)
	return a.Error(0)
}

func (m *DocumentRepositoryMock) GetByID(ctx context.Context, id DocumentID) (*Document, error) {
	a := m.Called(ctx, id)
	return documentOrNil(a.Get(0)), a.Error(1)
}

func (m *DocumentRepositoryMock) BumpVersion(ctx context.Context, id DocumentID, label *string) (int, error) {
	a := m.Called(ctx, id, label)
	return a.Int(0), a.Error(1)
}

func (m *DocumentRepositoryMock) InsertVersion(ctx context.Context, ver NewVersion) error {
	a := m.Called(ctx, ver)
	return a.Error(0)
}

func (m *DocumentRepositoryMock) UpdateMetadata(ctx context.Context, id DocumentID, input UpdateDocumentInput) (bool, error) {
	a := m.Called(ctx, id, input)
	return a.Bool(0), a.Error(1)
}

func (m *DocumentRepositoryMock) SoftDelete(ctx context.Context, id DocumentID) (bool, error) {
	a := m.Called(ctx, id)
	return a.Bool(0), a.Error(1)
}

func (m *DocumentRepositoryMock) GetView(ctx context.Context, id DocumentID) (*DocumentView, error) {
	a := m.Called(ctx, id)
	return viewOrNil(a.Get(0)), a.Error(1)
}

func (m *DocumentRepositoryMock) ListViews(ctx context.Context, filter ListDocumentsFilter) ([]DocumentView, int, error) {
	a := m.Called(ctx, filter)
	if a.Get(0) == nil {
		return nil, a.Int(1), a.Error(2)
	}
	return a.Get(0).([]DocumentView), a.Int(1), a.Error(2)
}

func (m *DocumentRepositoryMock) ListVersions(ctx context.Context, documentID DocumentID) ([]DocumentVersion, error) {
	a := m.Called(ctx, documentID)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).([]DocumentVersion), a.Error(1)
}

func (m *DocumentRepositoryMock) GetVersion(ctx context.Context, documentID DocumentID, versionID string) (*DocumentVersion, error) {
	a := m.Called(ctx, documentID, versionID)
	return versionOrNil(a.Get(0)), a.Error(1)
}

func (m *DocumentRepositoryMock) GetLatestVersion(ctx context.Context, documentID DocumentID) (*DocumentVersion, error) {
	a := m.Called(ctx, documentID)
	return versionOrNil(a.Get(0)), a.Error(1)
}

// DocumentUseCasesMock is a testify mock of DocumentUseCases.
type DocumentUseCasesMock struct {
	mock.Mock
}

var _ DocumentUseCases = (*DocumentUseCasesMock)(nil)

func (m *DocumentUseCasesMock) Create(ctx context.Context, input CreateDocumentInput) (*DocumentView, error) {
	a := m.Called(ctx, input)
	return viewOrNil(a.Get(0)), a.Error(1)
}

func (m *DocumentUseCasesMock) AddVersion(ctx context.Context, id DocumentID, input AddVersionInput) (*DocumentView, error) {
	a := m.Called(ctx, id, input)
	return viewOrNil(a.Get(0)), a.Error(1)
}

func (m *DocumentUseCasesMock) Get(ctx context.Context, id DocumentID) (*DocumentView, error) {
	a := m.Called(ctx, id)
	return viewOrNil(a.Get(0)), a.Error(1)
}

func (m *DocumentUseCasesMock) List(ctx context.Context, filter ListDocumentsFilter) ([]DocumentView, int, error) {
	a := m.Called(ctx, filter)
	if a.Get(0) == nil {
		return nil, a.Int(1), a.Error(2)
	}
	return a.Get(0).([]DocumentView), a.Int(1), a.Error(2)
}

func (m *DocumentUseCasesMock) ListByWorkflow(ctx context.Context, workflowID string) ([]DocumentView, error) {
	a := m.Called(ctx, workflowID)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).([]DocumentView), a.Error(1)
}

func (m *DocumentUseCasesMock) ListByTaskInstance(ctx context.Context, taskInstanceID string) ([]DocumentView, error) {
	a := m.Called(ctx, taskInstanceID)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).([]DocumentView), a.Error(1)
}

func (m *DocumentUseCasesMock) ListVersions(ctx context.Context, id DocumentID) ([]DocumentVersion, error) {
	a := m.Called(ctx, id)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).([]DocumentVersion), a.Error(1)
}

func (m *DocumentUseCasesMock) Update(ctx context.Context, id DocumentID, input UpdateDocumentInput) (*DocumentView, error) {
	a := m.Called(ctx, id, input)
	return viewOrNil(a.Get(0)), a.Error(1)
}

func (m *DocumentUseCasesMock) Delete(ctx context.Context, id DocumentID) error {
	a := m.Called(ctx, id)
	return a.Error(0)
}

func (m *DocumentUseCasesMock) Download(ctx context.Context, id DocumentID) (*Download, error) {
	a := m.Called(ctx, id)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).(*Download), a.Error(1)
}

func (m *DocumentUseCasesMock) DownloadVersion(ctx context.Context, id DocumentID, versionID string) (*Download, error) {
	a := m.Called(ctx, id, versionID)
	if a.Get(0) == nil {
		return nil, a.Error(1)
	}
	return a.Get(0).(*Download), a.Error(1)
}

func documentOrNil(v any) *Document {
	if v == nil {
		return nil
	}
	return v.(*Document)
}

func viewOrNil(v any) *DocumentView {
	if v == nil {
		return nil
	}
	return v.(*DocumentView)
}

func versionOrNil(v any) *DocumentVersion {
	if v == nil {
		return nil
	}
	return v.(*DocumentVersion)
}
