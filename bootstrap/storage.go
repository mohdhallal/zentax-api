package bootstrap

import (
	"context"
	"fmt"

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/platform/storage"
	storagefs "github.com/mohamadhallal/zentax-api/platform/storage/fs"
	storages3 "github.com/mohamadhallal/zentax-api/platform/storage/s3"
)

// NewStorage builds the document blob store selected by config (ADR-0022):
// the filesystem adapter (self-host default, tests) or S3 / an S3-compatible
// endpoint. Config validation already guaranteed the driver's required fields.
func NewStorage(ctx context.Context, cfg config.StorageConfig) (storage.Storage, error) {
	switch cfg.Driver {
	case config.StorageDriverFS:
		return storagefs.New(cfg.FS.Root)
	case config.StorageDriverS3:
		return storages3.New(ctx, storages3.Options{
			Bucket:         cfg.S3.Bucket,
			Region:         cfg.S3.Region,
			Endpoint:       cfg.S3.Endpoint,
			ForcePathStyle: cfg.S3.ForcePathStyle,
		})
	default:
		return nil, fmt.Errorf("unknown storage driver %q", cfg.Driver)
	}
}
