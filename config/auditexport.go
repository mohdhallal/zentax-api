package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// Audit-export overrides. An EMPTY value never overrides the file (the same
// rule as STORAGE_* — the compose stack passes empty strings for the settings
// the chosen driver does not use).
const (
	EnvAuditExportEnabled              = "AUDIT_EXPORT_ENABLED"
	EnvAuditExportIntervalMinutes      = "AUDIT_EXPORT_INTERVAL_MINUTES"
	EnvAuditExportMaxEntriesPerSegment = "AUDIT_EXPORT_MAX_ENTRIES_PER_SEGMENT"
	EnvAuditExportMaxSegmentsPerRun    = "AUDIT_EXPORT_MAX_SEGMENTS_PER_RUN"
	EnvAuditExportKeyPrefix            = "AUDIT_EXPORT_KEY_PREFIX"

	// The destination. Left unset, segments go to the DOCUMENT store — the
	// right default for the self-hosted edition, which has exactly one place to
	// put bytes. A hosted cell sets these to its own Object-Lock bucket, which
	// is the only configuration in which the export is genuinely write-once.
	EnvAuditExportStorageDriver           = "AUDIT_EXPORT_STORAGE_DRIVER"
	EnvAuditExportStorageFSRoot           = "AUDIT_EXPORT_STORAGE_FS_ROOT"
	EnvAuditExportStorageS3Bucket         = "AUDIT_EXPORT_STORAGE_S3_BUCKET"
	EnvAuditExportStorageS3Region         = "AUDIT_EXPORT_STORAGE_S3_REGION"
	EnvAuditExportStorageS3Endpoint       = "AUDIT_EXPORT_STORAGE_S3_ENDPOINT"
	EnvAuditExportStorageS3ForcePathStyle = "AUDIT_EXPORT_STORAGE_S3_FORCE_PATH_STYLE"
)

// Audit-export defaults. Code, not file — like the rate limiter's and the
// mailer's: a config file that says nothing about the export still boots with
// the evidence being taken. A compliance control that only exists where someone
// remembered to write it down is a control the next cell ships without.
const (
	// DefaultAuditExportIntervalMinutes is hourly. It is the RPO of the audit
	// evidence — how much of the trail a catastrophic loss of the database
	// would leave unproven — and an hour is cheap here: a pass over a tenant
	// with nothing new writes NOTHING (no object, no row, not even a trail
	// entry), so the cadence costs one indexed query per tenant per hour and
	// only an active tenant produces a file.
	DefaultAuditExportIntervalMinutes = 60

	// DefaultAuditExportMaxEntriesPerSegment bounds one file so an auditor can
	// open it; a backlog is exported as a series. See platform/audit/worm.
	DefaultAuditExportMaxEntriesPerSegment = 5000

	// DefaultAuditExportMaxSegmentsPerRun stops one tenant catching up from
	// starving the rest of a pass.
	DefaultAuditExportMaxSegmentsPerRun = 20

	// DefaultAuditExportKeyPrefix roots the object keys, which are then
	// tenants/<tenant>/audit-segments/seg-<from>-<to>.json.
	DefaultAuditExportKeyPrefix = "tenants"

	// MinAuditExportEntriesPerSegment: each cut appends one audit entry of its
	// own (the record of the export), so a segment that could hold a single
	// entry would never catch up with the chain it is exporting.
	MinAuditExportEntriesPerSegment = 2
)

// AuditExportConfig configures the WORM export of the audit trail (ADR-0008
// integrity triad #3, platform/audit/worm): the scheduled job that writes each
// tenant's hash chain out to object storage as independently verifiable
// segments.
type AuditExportConfig struct {
	// Enabled is a POINTER so "absent" and "explicitly false" stay
	// distinguishable: ApplyDefaults turns an absent value ON, while a config
	// that says `"enabled": false` is honoured. A Config assembled in code (the
	// acceptance harness) that never calls ApplyDefaults therefore leaves the
	// export off, which is what a test suite that must not run background work
	// wants.
	Enabled *bool `json:"enabled"`

	IntervalMinutes      int `json:"intervalMinutes"`
	MaxEntriesPerSegment int `json:"maxEntriesPerSegment"`
	MaxSegmentsPerRun    int `json:"maxSegmentsPerRun"`

	// KeyPrefix roots the object keys inside the destination.
	KeyPrefix string `json:"keyPrefix"`

	// Storage is where the segments go. An empty driver means "the document
	// store" — see AuditExportStorageConfig.
	Storage AuditExportStorageConfig `json:"storage"`
}

// AuditExportStorageConfig selects the export destination. It is deliberately
// SEPARATE from StorageConfig rather than a reuse of it:
//
//   - the two stores want opposite lifecycle rules. Documents must be
//     deletable (ADR-0007 erasure); audit segments must not be. A hosted cell
//     therefore points the export at a bucket with S3 Object Lock in compliance
//     mode, which is a bucket documents could never live in.
//   - it carries no upload cap: the size of a segment is the product's own
//     decision (maxEntriesPerSegment), never a caller's.
//
// An empty Driver means "use the document store", which is the self-hosted
// default: one disk, one directory, and an honest statement in
// platform/audit/worm/README.md about what that does and does not give you.
type AuditExportStorageConfig struct {
	Driver string          `json:"driver"` // "" (the document store) | fs | s3
	FS     StorageFSConfig `json:"fs"`
	S3     StorageS3Config `json:"s3"`
}

// Dedicated reports whether the export has a destination of its own.
func (s AuditExportStorageConfig) Dedicated() bool { return s.Driver != "" }

// Active reports whether the export job should be registered.
func (a AuditExportConfig) Active() bool { return a.Enabled != nil && *a.Enabled }

// Interval is the cadence between passes.
func (a AuditExportConfig) Interval() time.Duration {
	return time.Duration(a.IntervalMinutes) * time.Minute
}

// ApplyDefaults fills what an omitted `auditExport` section leaves empty, and
// turns the export ON.
func (a *AuditExportConfig) ApplyDefaults() {
	if a.Enabled == nil {
		on := true
		a.Enabled = &on
	}
	if a.IntervalMinutes <= 0 {
		a.IntervalMinutes = DefaultAuditExportIntervalMinutes
	}
	if a.MaxEntriesPerSegment <= 0 {
		a.MaxEntriesPerSegment = DefaultAuditExportMaxEntriesPerSegment
	}
	if a.MaxSegmentsPerRun <= 0 {
		a.MaxSegmentsPerRun = DefaultAuditExportMaxSegmentsPerRun
	}
	if a.KeyPrefix == "" {
		a.KeyPrefix = DefaultAuditExportKeyPrefix
	}
	if a.Storage.Driver == StorageDriverFS && a.Storage.FS.Root == "" {
		a.Storage.FS.Root = defaultAuditExportFSRoot
	}
}

const defaultAuditExportFSRoot = "./var/audit-segments"

func (a AuditExportConfig) validate() error {
	if !a.Active() {
		return nil
	}
	if a.IntervalMinutes <= 0 {
		return fmt.Errorf("auditExport.intervalMinutes must be positive (or set %s)", EnvAuditExportIntervalMinutes)
	}
	if a.MaxEntriesPerSegment < MinAuditExportEntriesPerSegment {
		return fmt.Errorf("auditExport.maxEntriesPerSegment must be at least %d — every export appends one audit entry of its own, so a smaller segment could never catch up with the chain (or set %s)",
			MinAuditExportEntriesPerSegment, EnvAuditExportMaxEntriesPerSegment)
	}
	if a.MaxSegmentsPerRun <= 0 {
		return fmt.Errorf("auditExport.maxSegmentsPerRun must be positive (or set %s)", EnvAuditExportMaxSegmentsPerRun)
	}
	if err := validateObjectKeyPrefix(a.KeyPrefix); err != nil {
		return fmt.Errorf("auditExport.keyPrefix %q is not a usable object-key prefix: %w (or set %s)",
			a.KeyPrefix, err, EnvAuditExportKeyPrefix)
	}

	switch a.Storage.Driver {
	case "":
		// The document store. Valid, and the self-host default.
	case StorageDriverFS:
		if a.Storage.FS.Root == "" {
			return fmt.Errorf("auditExport.storage.fs.root is required for the fs driver (or set %s)", EnvAuditExportStorageFSRoot)
		}
	case StorageDriverS3:
		if a.Storage.S3.Bucket == "" {
			return fmt.Errorf("auditExport.storage.s3.bucket is required for the s3 driver (or set %s)", EnvAuditExportStorageS3Bucket)
		}
		if a.Storage.S3.Region == "" {
			return fmt.Errorf("auditExport.storage.s3.region is required for the s3 driver (or set %s)", EnvAuditExportStorageS3Region)
		}
	default:
		return fmt.Errorf("auditExport.storage.driver must be empty (the document store), %q or %q, got %q",
			StorageDriverFS, StorageDriverS3, a.Storage.Driver)
	}
	return nil
}

// validateObjectKeyPrefix holds the prefix to the same grammar every storage
// adapter validates a whole key against (platform/storage.ValidateKey), so a
// misconfigured prefix is a boot failure rather than an export that fails
// silently, tenant by tenant, at upload time.
func validateObjectKeyPrefix(prefix string) error {
	if prefix == "" {
		return fmt.Errorf("it is empty")
	}
	for _, r := range prefix {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '/' || r == '_' || r == '.' || r == '-':
		default:
			return fmt.Errorf("%q is not allowed; use [A-Za-z0-9/_.-]", r)
		}
	}
	if prefix[0] == '/' || prefix[len(prefix)-1] == '/' {
		return fmt.Errorf("it must not start or end with \"/\"")
	}
	for _, seg := range splitSegments(prefix) {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%q is not a usable path segment", seg)
		}
	}
	return nil
}

func splitSegments(prefix string) []string {
	var out []string
	start := 0
	for i := 0; i < len(prefix); i++ {
		if prefix[i] == '/' {
			out = append(out, prefix[start:i])
			start = i + 1
		}
	}
	return append(out, prefix[start:])
}

// mergeAuditExportEnvOverrides applies AUDIT_EXPORT_* over the file. Empty
// values are ignored (not "cleared"), the same rule STORAGE_* follows.
func mergeAuditExportEnvOverrides(a *AuditExportConfig) {
	setIf := func(env string, dst *string) {
		if val := os.Getenv(env); val != "" {
			*dst = val
		}
	}
	if val := os.Getenv(EnvAuditExportEnabled); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			a.Enabled = &b
		}
	}
	setPositiveInt(EnvAuditExportIntervalMinutes, &a.IntervalMinutes)
	setPositiveInt(EnvAuditExportMaxEntriesPerSegment, &a.MaxEntriesPerSegment)
	setPositiveInt(EnvAuditExportMaxSegmentsPerRun, &a.MaxSegmentsPerRun)
	setIf(EnvAuditExportKeyPrefix, &a.KeyPrefix)

	setIf(EnvAuditExportStorageDriver, &a.Storage.Driver)
	setIf(EnvAuditExportStorageFSRoot, &a.Storage.FS.Root)
	setIf(EnvAuditExportStorageS3Bucket, &a.Storage.S3.Bucket)
	setIf(EnvAuditExportStorageS3Region, &a.Storage.S3.Region)
	setIf(EnvAuditExportStorageS3Endpoint, &a.Storage.S3.Endpoint)
	if val := os.Getenv(EnvAuditExportStorageS3ForcePathStyle); val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			a.Storage.S3.ForcePathStyle = b
		}
	}
}
