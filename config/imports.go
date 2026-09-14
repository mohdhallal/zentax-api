package config

import (
	"fmt"
	"os"
	"strconv"
)

// Spreadsheet-ingest environment overrides. As everywhere else in this package,
// an EMPTY value never overrides the file — deployment manifests routinely pass
// "" for a knob they are not setting.
const (
	EnvImportMaxFileBytes = "IMPORT_MAX_FILE_BYTES"
	EnvImportMaxRows      = "IMPORT_MAX_ROWS"
)

// Import bounds. They are code, not file, so a config file that says nothing
// about imports still boots with the limits on — an unbounded import is a
// denial of service with a spreadsheet, and a control nobody remembered to
// write down is a control nobody has.
const (
	// DefaultImportMaxFileBytes caps an uploaded file at 2 MiB, and the number
	// is measured against a file at the ROW cap rather than picked.
	//
	// MEASURED on 2026-09-14, generated from Fields() itself: a full sheet at
	// 1,000 data rows, with a 52-character German legal name in every name
	// column and realistic values elsewhere, is 234 KB for entities (9 columns)
	// and 207 KB for obligations (16 columns) — about a tenth of the cap. The
	// pathological version of the same file, every one of the 16 obligation
	// columns carrying that long name, is 890 KB, still comfortably inside it.
	// As a workbook the same register is 61 KB. So a file that trips this cap is
	// not carrying rows: it is carrying formatting, images or other sheets.
	//
	// It is checked against the part's DECLARED size and enforced again by a
	// reader that stops at the cap, so neither a lying Content-Length nor a
	// chunked body can get past it.
	//
	// Both bounds are PUBLISHED, not private: GET /imports/{kind}/template
	// carries them, the wizard states them and checks a chosen file against them
	// before uploading, and the refusals name them. Changing either number here
	// changes what the import page says, with nothing to keep in step — which is
	// the point, because the page and the server last disagreed by a factor of
	// twelve. Raising the file cap means revisiting parsing.DefaultLimits'
	// MaxUnzipBytes with it: a workbook is a zip, and what it unpacks to is
	// bounded separately (see parsing/reader.go).
	DefaultImportMaxFileBytes int64 = 2 * 1024 * 1024

	// DefaultImportMaxRows caps a file at 1,000 data rows, and the number is
	// derived rather than guessed.
	//
	// A commit writes one audit entry per record, each of which takes the
	// tenant's chain lock and reads the chain head, and the whole commit — every
	// record and every entry — is one transaction inside one HTTP request. The
	// binding constraint is therefore server.writeTimeoutMs, 30 seconds in the
	// shipped configuration. MEASURED against local Postgres on 2026-09-13, a
	// 1,000-row entity file validates in 435 ms and commits in 1.26 s — 1.26 ms
	// per (INSERT + chained audit entry) — which leaves more than an order of
	// magnitude of headroom, while ten thousand rows would sit close enough to
	// the timeout that a slow day would cut the customer off mid-commit.
	//
	// Raising it means raising the write timeout with it, and re-measuring the
	// chain append, which is why the two numbers are documented together.
	DefaultImportMaxRows = 1000

	// maxImportMaxFileBytes is the ceiling an operator may raise the file cap
	// to. The whole file is held in memory while it is parsed — a spreadsheet
	// cannot be validated as a stream, because the header may be on row twelve
	// and a duplicate key on row nine hundred — so this bound is what keeps a
	// handful of concurrent imports from being an out-of-memory kill.
	maxImportMaxFileBytes int64 = 64 * 1024 * 1024
	maxImportMaxRows            = 100000
)

// ImportConfig bounds spreadsheet ingest (PB-C5).
//
// Both bounds are enforced BEFORE the file is parsed, which is the point of
// having them: the byte cap is applied to the request body as it is read, so an
// oversized upload is refused without ever being held; the row cap is applied
// while the rows are counted out, so the first row past it ends the parse and
// nothing beyond it is built. An import that is refused costs a response, not a
// gigabyte.
type ImportConfig struct {
	MaxFileBytes int64 `json:"maxFileBytes"` // per-file cap; 0 → DefaultImportMaxFileBytes
	MaxRows      int   `json:"maxRows"`      // per-file data-row cap; 0 → DefaultImportMaxRows
}

// FileBytes is the effective byte cap, defaulted.
//
// It is an accessor rather than a field read, and that is deliberate: a Config
// assembled in code rather than loaded from disk (the acceptance harness does
// exactly that) has never called ApplyDefaults, and a bound that only exists
// when someone remembered to call a setup function is a bound the next
// composition root ships without.
func (c ImportConfig) FileBytes() int64 {
	if c.MaxFileBytes <= 0 {
		return DefaultImportMaxFileBytes
	}
	return c.MaxFileBytes
}

// Rows is the effective row cap, defaulted, by the same rule.
func (c ImportConfig) Rows() int {
	if c.MaxRows <= 0 {
		return DefaultImportMaxRows
	}
	return c.MaxRows
}

// ApplyDefaults fills what an omitted `imports` section leaves empty.
func (c *ImportConfig) ApplyDefaults() {
	c.MaxFileBytes = c.FileBytes()
	c.MaxRows = c.Rows()
}

// validate refuses a configuration that removes the bound rather than sets it.
// Negative and zero are already read as "use the default"; what has to be
// refused is a value large enough to be no bound at all, because an operator
// who raises this to make one awkward import work has changed the memory
// ceiling of every API task in the cell.
func (c ImportConfig) validate() error {
	if c.MaxFileBytes > maxImportMaxFileBytes {
		return fmt.Errorf("imports.maxFileBytes must be at most %d bytes (the whole file is held in memory while it is parsed)", maxImportMaxFileBytes)
	}
	if c.MaxRows > maxImportMaxRows {
		return fmt.Errorf("imports.maxRows must be at most %d (a commit is one transaction inside one request; see DefaultImportMaxRows)", maxImportMaxRows)
	}
	return nil
}

func mergeImportEnvOverrides(c *ImportConfig) {
	if val := os.Getenv(EnvImportMaxFileBytes); val != "" {
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			c.MaxFileBytes = n
		}
	}
	if val := os.Getenv(EnvImportMaxRows); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			c.MaxRows = n
		}
	}
}
