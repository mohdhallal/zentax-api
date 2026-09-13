// Command verify-audit-segment checks exported audit-chain segments
// (platform/audit/worm) WITHOUT a database, a session, or a running ZenTax.
//
// It is the reference reader for the format, and the answer to "how would an
// auditor check what you handed them?": point it at a file, a list of files, or
// a directory of them, and it recomputes the digest, every entry hash, every
// link, each segment's header against the record the chain carries for it,
// and — for a series — the joins between consecutive segments, including the
// digest by which each one names the file before it.
//
//	verify-audit-segment seg-000000000001-000000000042.json
//	verify-audit-segment /evidence/tenants/<tenant>/audit-segments
//	verify-audit-segment -series -quiet /evidence/.../audit-segments && echo ok
//
// Flags:
//
//	-series   also require the files to form ONE unbroken series: ordinals and
//	          sequence numbers contiguous, each segment starting from the
//	          previous one's end hash AND naming it by digest. With -genesis,
//	          additionally require the series to start at the beginning of the
//	          tenant's chain, which is what makes it the WHOLE trail rather than
//	          a window of it.
//	-genesis  require the first segment to be the tenant's genesis segment.
//	-strict   refuse the cut-over exemption entirely: every entry must be
//	          recomputable. A deployment with no entries older than the
//	          2026-09-12 hash-format cut-over — which is every deployment
//	          created since — can and should run with this.
//	-sealed   refuse an archive in which any segment's header is not bound by
//	          the chain — the newest segment's, whose record has not been
//	          written yet (see below), or an older one whose record predates the
//	          sealed header. Both leave segmentId and exportedAt covered by
//	          nothing, which is the statement -sealed exists to refuse.
//	-quiet    print nothing on success. It never suppresses the notes about
//	          fields the chain does not bind — the unsealed header below, and a
//	          record written before the header was sealed. Those notes are the
//	          difference between "everything here is bound" and "everything here
//	          is bound except these two fields of these segments", and silence in
//	          automation reads as the former.
//
// AN EXPORT RECORD WRITTEN BEFORE THE HEADER WAS SEALED. The four header fields
// a record restates — segmentId, exportedAt, previousDigest, preCutoverThroughSeq
// — were added on 2026-09-13. A record written before that states the range and
// nothing else, and since it lives in a write-once segment it will never say
// more. Such a segment is reported, not refused: its range is still held against
// the chain, and the rest is named as the file's own words. A record stating
// SOME of the four is refused — no writer has produced that shape, and the
// fields are hash-covered, so it is an edit that also rebuilt the entry hash.
//
// THE NEWEST SEGMENT'S HEADER, and why it is not a failure by default. A
// segment's header — its id and the moment it was exported — is bound by the
// `audit.exported` ENTRY the chain carries for it, which is written after the
// cut. When a cut is capped at MaxEntriesPerSegment that entry falls outside
// the range and lands in the next segment, so the newest file in an archive can
// legitimately be one whose header nothing covers yet: its entries, its range,
// its links and its end hash are all bound, but `segmentId` and `exportedAt`
// are not, and an edit to those two alone is not detected until the next export
// lands. That is a property of an append-only format being read mid-stream, not
// damage, and the reader says so on every run rather than exiting non-zero —
// otherwise every archive copied between exports would look tampered with. An
// automated check that wants the stronger statement asks for -sealed.
//
// Exit status: 0 when everything asked for holds, 1 when a check failed, 2 when
// nothing was checked — no argument, or a path that held no segment files. 2 is
// deliberately not 0: a tenant that has never been exported must not satisfy
// `verify-audit-segment … && echo "intact"`. These are the same three statuses,
// with the same meanings, as the independent reader §6.9 hands the auditor;
// the two are run against each other in platform/audit/worm/procedure_test.go.
//
// Anyone reimplementing this in another language should read the "verification"
// block inside any segment: it carries the recipe.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mohamadhallal/zentax-api/platform/audit"
	"github.com/mohamadhallal/zentax-api/platform/audit/worm"
)

func main() {
	series := flag.Bool("series", false, "require the segments to form one unbroken series")
	genesis := flag.Bool("genesis", false, "require the first segment to start at the tenant's genesis")
	strict := flag.Bool("strict", false, "refuse any entry that predates the hash-format cut-over")
	sealed := flag.Bool("sealed", false, "refuse an archive in which any segment's header is not bound by the chain")
	quiet := flag.Bool("quiet", false, "print nothing on success (the unsealed-header note is still printed)")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		os.Exit(2)
	}
	paths, err := collect(args)
	if err != nil {
		fail(err)
	}
	if len(paths) == 0 {
		// A path WAS given and held no segments. That is a statement about the
		// archive — "I checked nothing" — and not the syntax error the usage
		// screen used to imply. It must stay non-zero: the documented form is
		// `verify-audit-segment … && echo "whole trail, intact"`.
		nothing(args)
	}

	checked := make([]checkedFile, 0, len(paths))
	for _, path := range paths {
		data, err := os.ReadFile(path) //nolint:gosec // G304: the path is the operator's own argument
		if err != nil {
			fail(fmt.Errorf("%s: %w", path, err))
		}
		report, err := worm.VerifyFile(data)
		if err != nil {
			fail(fmt.Errorf("%s: %w", path, err))
		}
		if *strict && report.PreCutoverEntries > 0 {
			fail(fmt.Errorf("%s: %d entries claim the pre-cut-over exemption and -strict was asked for: seq %d..%d cannot be recomputed",
				path, report.PreCutoverEntries, report.FromSeq, report.PreCutoverThroughSeq))
		}
		checked = append(checked, checkedFile{path: path, report: report})
		if !*quiet {
			fmt.Printf("OK  %s\n    %s\n    digest sha256:%s\n", path, report, report.Digest)
			for _, note := range notes(report) {
				fmt.Printf("    note: %s\n", note)
			}
		}
	}

	if *series || *genesis {
		sort.SliceStable(checked, func(i, j int) bool { return checked[i].report.SegmentSeq < checked[j].report.SegmentSeq })
	}
	if *genesis && !checked[0].report.Genesis {
		fail(errors.New("the first segment is not the tenant's genesis segment (-genesis)"))
	}

	reports := make([]worm.Report, 0, len(checked))
	for _, c := range checked {
		reports = append(reports, c.report)
	}

	if *series {
		result, err := worm.VerifySeries(reports)
		if err != nil {
			fail(err)
		}
		if !*quiet {
			fmt.Printf("OK  series of %d segments: tenant %s, seq %d..%d, %s\n",
				result.Segments, result.TenantID, result.FromSeq, result.ToSeq, genesisNote(result.Genesis))
			if result.PreCutoverThroughSeq > 0 {
				// "bounded by the sealed export record" is only true when a
				// record that STATES the bound covers the segment carrying the
				// prefix. It was printed unconditionally, which overstated what
				// had been checked on exactly the archives where it mattered
				// most — and there are two of them: a capped genesis segment
				// that is also the newest file (no record yet), and a genesis
				// segment exported before 2026-09-13 (a record that never
				// stated the bound). In both, how far the prefix reaches is the
				// file's own word.
				bound := "and bounded by the sealed export record"
				if unboundPreCutover(checked, result) {
					bound = "and NOT bounded by an export record — see the note below"
				}
				fmt.Printf("    the tenant's pre-cut-over prefix is seq %d..%d: link-checked only, %s\n",
					result.FromSeq, result.PreCutoverThroughSeq, bound)
			}
		}
		// Printed whether or not -quiet was asked for: these are the things
		// about a PASSING archive that are not bound by the chain, and the
		// documented automation form is `-series -quiet … && echo ok`, where
		// silence means "all of it is bound".
		if result.UnsealedFrom > 0 {
			unsealed(result.UnsealedFrom, *sealed)
		}
		if len(result.PreSealHeaders) > 0 {
			preSeal(result.PreSealHeaders, *sealed)
		}
	} else {
		// Without the series, a pre-cut-over run in a file that does not begin
		// the tenant's chain is a claim about entries nobody can check. It must
		// not pass as "the earliest entries, link-checked".
		for _, c := range checked {
			if !c.report.PreCutoverProven {
				fail(fmt.Errorf("%s: %d entries from seq %d claim the pre-cut-over exemption, and this segment does not start the tenant's chain, so nothing here shows they are its historical prefix — supply the earlier segments and pass -series",
					c.path, c.report.PreCutoverEntries, c.report.FromSeq))
			}
			// One file at a time, a segment whose header no record binds is a
			// segment whose id and export time are the file's own words, and
			// -sealed is a refusal to accept that. The two ways it happens want
			// different advice: a capped range's record is elsewhere and can be
			// supplied, a pre-seal record is here and will never say more.
			// (Without -sealed the per-file note already says which it is.)
			switch {
			case *sealed && c.report.HeaderRecordPreSeal:
				fail(fmt.Errorf("%s: this segment's export record predates the sealed header, so its id and export time are bound by nothing — and no later export will bind them — and -sealed was asked for",
					c.path))
			case *sealed && !c.report.HeaderSealed:
				fail(fmt.Errorf("%s: this segment does not carry the export record covering its own header, so its id and export time are bound by nothing here and -sealed was asked for — supply the segment that carries the record and pass -series",
					c.path))
			}
		}
	}

	if !*quiet {
		fmt.Printf("verified %d segment(s)\n", len(checked))
	}
}

// checkedFile keeps a report with the path it came from, so a message about a
// file names that file even after the reports have been put in chain order.
type checkedFile struct {
	path   string
	report worm.Report
}

// notes are the things about one file that are true, worth saying, and not
// failures.
func notes(report worm.Report) []string {
	var out []string
	if report.PreCutoverEntries > 0 {
		out = append(out, fmt.Sprintf("%d entries predate the hash-chain cut-over (hash format v%d); their links are intact but their hashes cannot be recomputed from any stored form",
			report.PreCutoverEntries, audit.HashVersionNanosecond))
	}
	if !report.PreCutoverProven {
		out = append(out, "those entries are NOT at the beginning of the tenant's chain as far as this file can show; only the whole series from genesis can settle that (-series -genesis)")
	}
	if report.HeaderRecordPreSeal {
		out = append(out, "this segment's export record predates the sealed header (it was exported before 2026-09-13): its range is held against the chain, its id and export time are its own words")
	} else if !report.HeaderSealed {
		out = append(out, "this segment does not carry the export record that covers its own header (its range was capped); the record is in a later segment, so check the series")
	}
	return out
}

// unsealed says what the newest segment's unbound header means, on stderr so
// that -quiet cannot swallow it, and turns it into a refusal when -sealed was
// asked for.
//
// WHY IT IS SAID AT ALL. Everything else in a passing archive is bound by the
// chain: change an entry, a range, a link or an end hash and the check fails.
// The header of a trailing segment whose export record has not landed yet is
// the exception — `segmentId` and `exportedAt` are covered by nothing until the
// next export writes the record — so an archive that verifies today can differ
// in those two fields from the same archive verified tomorrow, and nothing
// would have said so. Disclosing it in the tool's own output is the difference
// between a known property of reading an append-only format mid-stream and a
// hole. Anyone who needs the stronger statement in automation passes -sealed.
func unsealed(fromSegment int64, refuse bool) {
	detail := fmt.Sprintf("the header of segment %d onwards is not covered by the chain yet: its export record lands in a later segment, which has not been exported. Its entries, range, links and end hash ARE bound; its segment id and export time are not, until the next export",
		fromSegment)
	if refuse {
		fail(fmt.Errorf("%s (-sealed)", detail))
	}
	fmt.Fprintf(os.Stderr, "note: %s\n", detail)
}

// preSeal says what a record written before the header was sealed leaves
// unbound, on stderr for the same reason as unsealed: it is a caveat on an
// archive that PASSED, and silence under -quiet would read as "all of it is
// bound".
//
// It is not a milder unsealed header, it is a permanent one. A capped segment's
// record lands with the next export and the note goes away; these records are in
// write-once segments and will never say more than they said. There is nothing
// to re-run for, which is why the note says to put it in the report.
func preSeal(segments []int64, refuse bool) {
	detail := fmt.Sprintf("the export record for segment %s predates the sealed header (exported before 2026-09-13, when the record began restating it). The range IS checked against the chain; the segment id, the export instant, the predecessor digest and the pre-cut-over bound are the file's own words, bound by nothing, and no later export will bind them. Say exactly that about %s in the report",
		list(segments), plural(segments, "that segment", "those segments"))
	if refuse {
		fail(fmt.Errorf("%s (-sealed)", detail))
	}
	fmt.Fprintf(os.Stderr, "note: %s\n", detail)
}

// nothing reports that the operator's own path held no segments — which is
// never "intact" — and exits 2, the code that means nothing was checked.
func nothing(args []string) {
	fmt.Fprintf(os.Stderr, "NOTHING TO VERIFY: no segment files under %s — either this tenant has never had a segment exported, or you fetched nothing. Settle which at the source before reporting either\n",
		strings.Join(args, " "))
	os.Exit(2)
}

func list(segments []int64) string {
	parts := make([]string, 0, len(segments))
	for _, s := range segments {
		parts = append(parts, strconv.FormatInt(s, 10))
	}
	return strings.Join(parts, ", ")
}

func plural(segments []int64, one, many string) string {
	if len(segments) == 1 {
		return one
	}
	return many
}

// unboundPreCutover reports whether any segment carrying part of the tenant's
// pre-cut-over prefix has no record STATING how far that prefix reaches —
// either because its record has not been written yet (a trailing capped
// segment) or because its record predates the sealed header and never stated
// it. Either way the bound is the file's own word and must not be reported as
// sealed.
func unboundPreCutover(checked []checkedFile, result worm.SeriesReport) bool {
	for _, c := range checked {
		if c.report.PreCutoverEntries == 0 {
			continue
		}
		if result.UnsealedFrom > 0 && c.report.SegmentSeq >= result.UnsealedFrom {
			return true
		}
		if c.report.HeaderRecordPreSeal {
			return true
		}
		for _, seq := range result.PreSealHeaders {
			if seq == c.report.SegmentSeq {
				return true
			}
		}
	}
	return false
}

func genesisNote(genesis bool) string {
	if genesis {
		return "starting at the tenant's genesis"
	}
	return "starting mid-chain (earlier segments were not supplied)"
}

// collect expands directories into the segment files inside them, sorted — the
// keys are zero-padded, so lexical order is chain order.
func collect(args []string) ([]string, error) {
	var paths []string
	for _, arg := range args {
		info, err := os.Stat(arg)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			paths = append(paths, arg)
			continue
		}
		entries, err := os.ReadDir(arg)
		if err != nil {
			return nil, err
		}
		var found []string
		for _, e := range entries {
			name := e.Name()
			// .meta sidecars belong to the filesystem storage adapter, not to
			// the format.
			if e.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".meta") {
				continue
			}
			found = append(found, filepath.Join(arg, name))
		}
		sort.Strings(found)
		paths = append(paths, found...)
	}
	return paths, nil
}

func usage() {
	fmt.Fprintf(os.Stderr, `verify-audit-segment — check exported ZenTax audit-chain segments offline.

usage: verify-audit-segment [-series] [-genesis] [-strict] [-sealed] [-quiet] <file|directory>...

`)
	flag.PrintDefaults()
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "FAILED: %v\n", err)
	os.Exit(1)
}
