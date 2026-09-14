package handlers

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/mohamadhallal/zentax-api/config"
)

// The bounds a customer has to build a file against: what the upload enforces,
// what the refusal says when it does, and what the product tells a customer
// BEFORE they build the file.
//
// These three used to be three different numbers. The screen promised 25 MB
// (copied from the documents module's cap), the server refused at 2 MiB, and
// the refusal named no size at all — so a tax manager with a 4 MB register was
// told their file was too big, was not told what would fit, and was told it
// under the headline "That file could not be read".

// A refusal that does not name its limit is not a reason, it is a wall.
func TestTheRefusalsNameTheirLimit(t *testing.T) {
	maxBytes := config.DefaultImportMaxFileBytes

	// The common case: the part declared its size, so both numbers are known.
	known := tooLarge(maxBytes, 4_400_000)
	for _, want := range []string{"4.2 MB", "2 MB", "import it in parts"} {
		if !strings.Contains(known.Message, want) {
			t.Errorf("a refusal naming a 4.2 MB file against the shipped cap does not say %q: %s", want, known.Message)
		}
	}

	// The reader stopped the body early, so the file's own size was never
	// established. The limit is still named; the size is not invented.
	unknown := tooLarge(maxBytes, sizeUnknown)
	if !strings.Contains(unknown.Message, "the limit is 2 MB per file") {
		t.Errorf("a refusal with no measured size still has to name the cap: %s", unknown.Message)
	}
	if regexp.MustCompile(`this file is [\d,]`).MatchString(unknown.Message) {
		t.Errorf("a refusal claimed a size the reader never measured: %s", unknown.Message)
	}

	// The row cap refuses in the unit the customer's sheet is measured in.
	rows := tooManyRows(config.DefaultImportMaxRows)
	for _, want := range []string{"1,000 data rows", "not counting the header", "nothing was read past that row"} {
		if !strings.Contains(rows.Message, want) {
			t.Errorf("the row-cap refusal does not say %q: %s", want, rows.Message)
		}
	}

	// The numbers stay in Details as numbers, for anything that wants to
	// compare rather than read.
	if details, ok := known.Details.(map[string]any); !ok ||
		details["maxBytes"] != maxBytes || details["fileBytes"] != int64(4_400_000) {
		t.Errorf("the refusal dropped its machine-readable numbers: %#v", known.Details)
	}
}

// Two sizes that round to the same words read as a contradiction ("this file is
// 2 MB and an import may carry up to 2 MB"), so a collision is spelled out in
// bytes instead. A refused file is never told it is exactly the size that fits.
func TestASizeThatRoundsToTheCapIsGivenExactly(t *testing.T) {
	err := tooLarge(2*1024*1024, 2*1024*1024+48)
	if strings.Contains(err.Message, "2 MB and an import may carry up to 2 MB") {
		t.Fatalf("the refusal contradicts itself: %s", err.Message)
	}
	for _, want := range []string{"2,097,200 bytes", "2,097,152 bytes"} {
		if !strings.Contains(err.Message, want) {
			t.Errorf("a rounding collision has to name both sizes exactly; missing %q: %s", want, err.Message)
		}
	}
}

// formatFileSize is the API's half of one agreement: the cap named in a refusal
// and the cap stated on the import page are the same number in the same words.
// The client's own suite (client/src/api/imports.test.ts) pins these same
// strings against its formatBytes.
func TestFileSizesAreSpelledAsTheClientSpellsThem(t *testing.T) {
	for _, c := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{999, "999 B"},
		{1024, "1 KB"},
		{1536, "1.5 KB"},
		{2 * 1024 * 1024, "2 MB"},
		{4_400_000, "4.2 MB"},
		{25 * 1024 * 1024, "25 MB"},
		{64 * 1024 * 1024, "64 MB"},
	} {
		if got := formatFileSize(c.bytes); got != c.want {
			t.Errorf("formatFileSize(%d) = %q, want %q", c.bytes, got, c.want)
		}
	}
}

// The template route is where the wizard learns what will fit. It carries the
// bounds this installation enforces — not a constant compiled into a screen —
// so an operator who raises the cap raises what the page promises with it.
func TestTheTemplateRoutePublishesTheBoundsTheUploadEnforces(t *testing.T) {
	bounds := Bounds{MaxFileBytes: 8 * 1024 * 1024, MaxRows: 2500}

	body, err := json.Marshal(templateResponse{Limits: limitsJSON{
		MaxFileBytes: bounds.MaxFileBytes,
		MaxRows:      bounds.MaxRows,
	}})
	if err != nil {
		t.Fatalf("the template response does not marshal: %v", err)
	}

	var got struct {
		Kind   string `json:"kind"`
		Fields []any  `json:"fields"`
		Limits struct {
			MaxFileBytes int64 `json:"maxFileBytes"`
			MaxRows      int   `json:"maxRows"`
		} `json:"limits"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("the template response does not read back: %v", err)
	}
	if got.Limits.MaxFileBytes != bounds.MaxFileBytes || got.Limits.MaxRows != bounds.MaxRows {
		t.Errorf("limits came back as %+v, want %+v", got.Limits, bounds)
	}
	// The columns are where they have always been: `limits` is additive, and a
	// client reading `fields` off this route keeps working.
	if !strings.Contains(string(body), `"fields"`) || !strings.Contains(string(body), `"kind"`) {
		t.Errorf("the embedded column set stopped being inlined: %s", body)
	}
}
