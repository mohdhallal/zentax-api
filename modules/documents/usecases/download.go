package usecases

import (
	"context"
	"errors"
	"fmt"

	apperrors "github.com/mohamadhallal/zentax-api/errors"
	"github.com/mohamadhallal/zentax-api/modules/documents/domain"
	"github.com/mohamadhallal/zentax-api/platform/storage"
)

// Download opens the latest version's blob. Unknown / other-tenant / deleted /
// outside the caller's entity subtree → 404 before any byte; a blob missing
// from storage is an internal error (logged by the transport as a 500) — the
// metadata says it should exist.
//
// The document is resolved through the read-scoped view, not loadLive: this is
// the path that hands over actual file bytes, so the scope decision is made
// here, in the open, and the version lookup below is narrowed again behind it.
func (uc *UseCases) Download(ctx context.Context, id domain.DocumentID) (*domain.Download, error) {
	if _, err := uc.view(ctx, id); err != nil {
		return nil, err
	}
	ver, err := uc.repo.GetLatestVersion(ctx, id)
	if err != nil {
		return nil, err
	}
	if ver == nil {
		return nil, apperrors.NewNotFound(domain.ErrDocumentNotFound(id))
	}
	return uc.open(ctx, ver)
}

// DownloadVersion opens one specific version of a live document, read-scoped
// exactly as Download is.
func (uc *UseCases) DownloadVersion(ctx context.Context, id domain.DocumentID, versionID string) (*domain.Download, error) {
	if _, err := uc.view(ctx, id); err != nil {
		return nil, err
	}
	ver, err := uc.repo.GetVersion(ctx, id, versionID)
	if err != nil {
		return nil, err
	}
	if ver == nil {
		return nil, apperrors.NewNotFound(domain.ErrVersionNotFound(versionID))
	}
	return uc.open(ctx, ver)
}

func (uc *UseCases) open(ctx context.Context, ver *domain.DocumentVersion) (*domain.Download, error) {
	if uc.store == nil {
		return nil, errors.New("documents: storage is not configured")
	}
	body, _, err := uc.store.Get(ctx, ver.StorageKey)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, fmt.Errorf("%s: document %s version %d (key %s)", domain.MsgBlobMissing, ver.DocumentID, ver.Version, ver.StorageKey)
		}
		return nil, fmt.Errorf("documents: open blob: %w", err)
	}
	// The stored MIME type (validated at upload) is authoritative — not
	// whatever the adapter reports.
	return &domain.Download{Version: *ver, ContentType: ver.MimeType, Body: body}, nil
}
