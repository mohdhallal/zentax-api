package worm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mohamadhallal/zentax-api/platform/audit"
)

// The operator procedure this test executes: §6.9 of the ops runbook in the
// sibling docs repo. Override with ZENTAX_OPS_DOC when it lives elsewhere.
const opsDocEnv = "ZENTAX_OPS_DOC"

var defaultOpsDoc = filepath.Join("..", "..", "..", "..", "zentax-ui", "docs", "ops", "environments.md")

// TestTheDocumentedProcedureRunsAsWritten does what a documentation review
// cannot: it follows §6.9 the way an auditor would, from the directory the
// procedure's own steps leave them in, with the commands the page prints and
// the reader the page ships — and then hands both readers three forged
// archives and requires them to agree.
//
// A runbook is a control only if it runs. This one did not: step 1 changed
// directory and step 2a then named a path relative to the directory step 1 had
// left. The failure was a plain "no such file or directory" on the FIRST tool
// an auditor touches, which is exactly where a procedure loses its reader. So
// the paths in the page are extracted from the page — including any `cd` — and
// run, rather than being read and believed.
//
// It is skipped when the docs repo is not checked out beside this one, or when
// python3 is missing; nothing here needs a database or a network.
func TestTheDocumentedProcedureRunsAsWritten(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a binary and shells out to python3")
	}
	doc := opsDoc(t)
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; the auditor's reader cannot be run here")
	}

	section := sectionOf(t, doc, "### 6.9")
	reader := pythonBlock(t, section)
	goCommand := commandLine(t, section, "verify-audit-segment ")
	pyCommand := commandLine(t, section, "python3 verify-segments.py")

	// Step 1 leaves the operator somewhere. Wherever the page says that is, the
	// rest of the page has to run from there.
	work := t.TempDir()
	cwd := work
	for _, dir := range chdirs(section) {
		cwd = filepath.Join(cwd, filepath.FromSlash(dir))
	}
	segments := filepath.Join(work, "segments")
	require.NoError(t, os.MkdirAll(segments, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(work, "verify-segments.py"), []byte(reader), 0o600))

	binary := filepath.Join(work, "verify-audit-segment")
	build := exec.Command("go", "build", "-o", binary, "github.com/mohamadhallal/zentax-api/cmd/verify-audit-segment")
	out, err := build.CombinedOutput()
	require.NoError(t, err, "building the reader the page names: %s", out)

	// A genuine two-segment archive for one tenant, written the way an export
	// writes it — each segment ending with the record of its own export.
	tenantID := uuid.NewString()
	one := sealed(t, BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
		ExportedAt:    base.Add(time.Hour),
		StartPrevHash: audit.GenesisHash,
		Entries:       chain(t, tenantID, audit.GenesisHash, 1, 3),
	}, 0)
	two := sealed(t, BuildInput{
		SegmentID: uuid.NewString(), SegmentSeq: 2, TenantID: tenantID,
		ExportedAt:    base.Add(2 * time.Hour),
		StartPrevHash: one.Document.Chain.EndHash,
		Entries:       chain(t, tenantID, one.Document.Chain.EndHash, one.Document.Range.ToSeq+1, 3),
		Previous:      pointsAt(one),
	}, 0)
	lay := func(built Built) string {
		doc := built.Document
		name := fmt.Sprintf("seg-%012d-%012d.json", doc.Range.FromSeq, doc.Range.ToSeq)
		path := filepath.Join(segments, name)
		require.NoError(t, os.WriteFile(path, built.Bytes, 0o600))
		return path
	}
	lay(one)
	secondPath := lay(two)

	shipped := func() (string, error) { return runIn(cwd, binary, expand(t, cwd, goCommand)...) }
	auditors := func() (string, error) { return runIn(cwd, python, expand(t, cwd, pyCommand)...) }

	t.Run("the page runs", func(t *testing.T) {
		out, err := shipped()
		require.NoError(t, err, "step 2a, verbatim, from where step 1 leaves you:\n%s", out)
		require.Contains(t, out, "starting at the tenant's genesis")

		out, err = auditors()
		require.NoError(t, err, "step 2b, verbatim, from the same place:\n%s", out)
		require.Contains(t, out, "SERIES OK")
		require.Contains(t, out, "starts at genesis")
	})

	// The two readers must not only run: they must agree, and they must agree
	// about the three things this wave fixed. If the page's reader passed an
	// archive the shipped one refused, the page would be worse than nothing —
	// it is the copy handed to somebody who does not trust ours.
	for _, tc := range []struct {
		name string
		edit func(doc Document) Document
	}{
		{
			name: "a leading entry of a later segment rewritten and marked pre-cut-over",
			edit: func(doc Document) Document {
				out := copyEntries(doc)
				out.Entries[0].HashVersion = audit.HashVersionNanosecond
				out.Entries[0].Action = "entity.deleted"
				return out
			},
		},
		{
			name: "the segment re-dated",
			edit: func(doc Document) Document {
				doc.ExportedAt = FormatInstant(base.Add(900 * 24 * time.Hour))
				return doc
			},
		},
		{
			name: "the window it claims to cover widened",
			edit: func(doc Document) Document {
				doc.Range.FirstOccurredAt = FormatInstant(base.Add(-90 * 24 * time.Hour))
				return doc
			},
		},
		{
			name: "the file it follows swapped for another",
			edit: func(doc Document) Document {
				previous := *doc.Previous
				previous.Digest = digestPrefix + strings.Repeat("d", 64)
				doc.Previous = &previous
				return doc
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.NoError(t, os.WriteFile(secondPath, reseal(t, tc.edit(two.Document)), 0o600))
			defer func() { require.NoError(t, os.WriteFile(secondPath, two.Bytes, 0o600)) }()

			out, err := shipped()
			require.Error(t, err, "the shipped reader accepted a forged archive:\n%s", out)
			out, err = auditors()
			require.Error(t, err, "the reader §6.9 hands the auditor accepted a forged archive:\n%s", out)
		})
	}

	// Agreeing on forgeries is half of it. The two readers also have to agree on
	// the archives that are SOUND but cannot be fully bound — the ones where a
	// disagreement sends an auditor to escalate a non-incident, or tells an
	// operator they mistyped a command that in fact ran correctly.

	t.Run("an archive exported before the header was sealed passes both readers", func(t *testing.T) {
		defer swap(t, segments)()

		// Exactly the shape of a real installation's oldest segments: a genesis
		// segment whose export record predates the sealed header, followed by one
		// written after it.
		old := sealedWithRecord(t, BuildInput{
			SegmentID: uuid.NewString(), SegmentSeq: 1, TenantID: tenantID,
			ExportedAt:    base.Add(time.Hour),
			StartPrevHash: audit.GenesisHash,
			Entries:       chain(t, tenantID, audit.GenesisHash, 1, 3),
		}, preWaveRecord(1, tenantID))
		next := sealed(t, BuildInput{
			SegmentID: uuid.NewString(), SegmentSeq: 2, TenantID: tenantID,
			ExportedAt:    base.Add(2 * time.Hour),
			StartPrevHash: old.Document.Chain.EndHash,
			Entries:       chain(t, tenantID, old.Document.Chain.EndHash, old.Document.Range.ToSeq+1, 3),
			Previous:      pointsAt(old),
		}, 0)
		lay(old)
		lay(next)

		out, err := shipped()
		require.NoError(t, err, "the shipped reader called a genuine older archive an edited one — §6.9 tells the operator to escalate that:\n%s", out)
		require.Contains(t, out, "predates the sealed header",
			"it has to SAY which segments' ids and export instants are the file's own words")

		out, err = auditors()
		require.NoError(t, err, "the reader §6.9 hands the auditor refused a genuine older archive:\n%s", out)
		require.Contains(t, out, "SERIES OK")
		require.Contains(t, out, "predates the sealed header")
	})

	t.Run("an empty directory is a statement about the archive, not a syntax error", func(t *testing.T) {
		defer swap(t, segments)()

		out, err := shipped()
		require.Error(t, err, "nothing was checked, which is never intact:\n%s", out)
		require.Equal(t, 2, exitCode(t, err), "2 is 'nothing was checked'; 0 would make the page's `&& echo intact` lie")
		require.Contains(t, out, "NOTHING TO VERIFY")
		require.NotContains(t, out, "usage:",
			"a usage screen reads as 'you typed the command wrong' on a command that ran correctly")

		out, err = auditors()
		require.Error(t, err, "%s", out)
		require.Equal(t, 2, exitCode(t, err))
		require.Contains(t, out, "NOTHING TO VERIFY")
	})
}

// swap empties the evidence directory and restores it afterwards, so a subtest
// can lay a different archive in the place the page's own commands look.
func swap(t *testing.T, dir string) func() {
	t.Helper()
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	saved := make(map[string][]byte, len(entries))
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path) //nolint:gosec // G304: a path this test just wrote
		require.NoError(t, err)
		saved[path] = data
		require.NoError(t, os.Remove(path))
	}
	return func() {
		rest, err := os.ReadDir(dir)
		require.NoError(t, err)
		for _, e := range rest {
			require.NoError(t, os.Remove(filepath.Join(dir, e.Name())))
		}
		for path, data := range saved {
			require.NoError(t, os.WriteFile(path, data, 0o600))
		}
	}
}

// exitCode is the status the reader actually exited with, which is the half of
// a verdict automation reads.
func exitCode(t *testing.T, err error) int {
	t.Helper()
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit)
	return exit.ExitCode()
}

// opsDoc finds the runbook, or skips.
func opsDoc(t *testing.T) string {
	t.Helper()
	path := os.Getenv(opsDocEnv)
	if path == "" {
		path = defaultOpsDoc
	}
	data, err := os.ReadFile(path) //nolint:gosec // G304: a repo-relative doc path, overridable by the operator
	if err != nil {
		t.Skipf("the ops runbook is not here (%s); set %s to run this", path, opsDocEnv)
	}
	return string(data)
}

// sectionOf cuts one "### n.n" section out of the runbook.
func sectionOf(t *testing.T, doc, heading string) string {
	t.Helper()
	_, after, ok := strings.Cut(doc, heading)
	require.True(t, ok, "%s is not in the runbook any more", heading)
	if end := strings.Index(after, "\n### "); end >= 0 {
		after = after[:end]
	}
	if end := strings.Index(after, "\n## "); end >= 0 {
		after = after[:end]
	}
	return after
}

var (
	pythonFence = regexp.MustCompile("(?s)```python\n(.*?)\n```")
	bashFence   = regexp.MustCompile("(?s)```bash\n(.*?)\n```")
	chdir       = regexp.MustCompile(`(?m)(?:^|&&\s*)cd\s+(\S+)`)
)

// pythonBlock is the reader the page hands the auditor, exactly as printed.
func pythonBlock(t *testing.T, section string) string {
	t.Helper()
	m := pythonFence.FindStringSubmatch(section)
	require.NotNil(t, m, "§6.9 no longer carries the python reader it promises")
	return m[1]
}

// commandLine finds a command the page tells the operator to run, and returns
// its arguments — dropping the shell garnish (`&& echo …`) but not the paths,
// which are the point.
func commandLine(t *testing.T, section, prefix string) []string {
	t.Helper()
	for _, block := range bashFence.FindAllStringSubmatch(section, -1) {
		for _, line := range strings.Split(block[1], "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			if cut := strings.Index(line, " && "); cut >= 0 {
				line = line[:cut]
			}
			return strings.Fields(line)[1:]
		}
	}
	t.Fatalf("§6.9 no longer tells the operator to run %q", prefix)
	return nil
}

// chdirs are the directory changes the page's own commands make, in order.
func chdirs(section string) []string {
	var out []string
	for _, block := range bashFence.FindAllStringSubmatch(section, -1) {
		for _, m := range chdir.FindAllStringSubmatch(block[1], -1) {
			out = append(out, m[1])
		}
	}
	return out
}

// expand resolves the shell globs the page writes, since these commands are run
// without a shell.
func expand(t *testing.T, cwd string, args []string) []string {
	t.Helper()
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if !strings.ContainsAny(arg, "*?") {
			out = append(out, arg)
			continue
		}
		matches, err := filepath.Glob(filepath.Join(cwd, filepath.FromSlash(arg)))
		require.NoError(t, err)
		require.NotEmpty(t, matches, "the page's own glob %q matches nothing from %s", arg, cwd)
		for _, match := range matches {
			rel, err := filepath.Rel(cwd, match)
			require.NoError(t, err)
			out = append(out, rel)
		}
	}
	return out
}

func runIn(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...) //nolint:gosec // G204: the command comes from the repo's own runbook
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}
