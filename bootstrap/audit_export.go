package bootstrap

import (
	"context"
	"fmt"
	"time"

	"github.com/mohamadhallal/zentax-api/config"
	"github.com/mohamadhallal/zentax-api/logger"
	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/audit/worm"
	"github.com/mohamadhallal/zentax-api/platform/database"
	"github.com/mohamadhallal/zentax-api/platform/scheduler"
	"github.com/mohamadhallal/zentax-api/platform/storage"
)

// auditExportMaxTimeout bounds one pass. The pass is resumable at every step —
// an interrupted upload is retried, an interrupted cut never happened — so
// cutting it off is cheap, and a job that outlives its budget must not hold up
// the reminder scan or the outbox behind it.
const auditExportMaxTimeout = 10 * time.Minute

// newAuditExportJob assembles the WORM export (ADR-0008 integrity triad #3),
// or returns nil when the deployment has switched it off.
//
// The DESTINATION is the one decision the composition root makes here. Left
// unconfigured it is the document store — correct for the self-hosted edition,
// which has one place to put bytes, and honest about what that gives you
// (platform/audit/worm/README.md: append-only by convention, not by the medium).
// A hosted cell names its own bucket, which is the configuration in which the
// export is genuinely write-once: S3 Object Lock in compliance mode, on a
// bucket the API's own role cannot delete from.
func newAuditExportJob(cfg *config.Config, db *database.Exec, docStore storage.Storage) (*scheduler.Job, error) {
	if !cfg.AuditExport.Active() {
		logger.Log.Info("Audit WORM export is disabled; the hash chain will not be written off-box")
		return nil, nil
	}

	dest, dedicated, err := auditExportDestination(cfg, docStore)
	if err != nil {
		return nil, err
	}

	exporter := worm.NewExporter(
		db,
		worm.NewStore(db),
		dest,
		audit.NewRecorder(db),
		worm.Settings{
			MaxEntriesPerSegment: cfg.AuditExport.MaxEntriesPerSegment,
			MaxSegmentsPerRun:    cfg.AuditExport.MaxSegmentsPerRun,
			KeyPrefix:            cfg.AuditExport.KeyPrefix,
		},
	)

	interval := cfg.AuditExport.Interval()
	timeout := auditExportMaxTimeout
	if interval < timeout {
		timeout = interval
	}

	logger.Log.Info("Audit WORM export configured",
		logger.String("destination", auditExportDestinationName(cfg, dedicated)),
		logger.String("keyPrefix", cfg.AuditExport.KeyPrefix),
		logger.String("interval", interval.String()),
		logger.Int("maxEntriesPerSegment", cfg.AuditExport.MaxEntriesPerSegment),
	)

	return &scheduler.Job{
		Name:     exporter.Name(),
		Interval: interval,
		Timeout:  timeout,
		Run: func(ctx context.Context) error {
			_, err := exporter.Run(ctx)
			return err
		},
	}, nil
}

// auditExportDestination builds the export's object store: its own, when the
// deployment named one, otherwise the document store.
func auditExportDestination(cfg *config.Config, docStore storage.Storage) (storage.Storage, bool, error) {
	if !cfg.AuditExport.Storage.Dedicated() {
		if docStore == nil {
			return nil, false, fmt.Errorf("audit export has no destination: no dedicated store is configured (%s) and the document store is absent",
				config.EnvAuditExportStorageDriver)
		}
		return docStore, false, nil
	}

	// The adapter factory is shared with the document store; MaxUploadBytes is
	// a request-side cap and plays no part here (the size of a segment is the
	// product's own decision), so it is set to a non-zero placeholder to satisfy
	// the shared struct.
	dest, err := NewStorage(context.Background(), config.StorageConfig{
		Driver:         cfg.AuditExport.Storage.Driver,
		MaxUploadBytes: 1,
		FS:             cfg.AuditExport.Storage.FS,
		S3:             cfg.AuditExport.Storage.S3,
	})
	if err != nil {
		return nil, false, fmt.Errorf("audit export destination: %w", err)
	}
	return dest, true, nil
}

// auditExportDestinationName is the log line's description of where evidence
// goes — never a credential, and never the whole S3 endpoint URL.
func auditExportDestinationName(cfg *config.Config, dedicated bool) string {
	if !dedicated {
		return "the document store (shared)"
	}
	switch cfg.AuditExport.Storage.Driver {
	case config.StorageDriverS3:
		return "s3://" + cfg.AuditExport.Storage.S3.Bucket
	default:
		return cfg.AuditExport.Storage.Driver + ":" + cfg.AuditExport.Storage.FS.Root
	}
}
