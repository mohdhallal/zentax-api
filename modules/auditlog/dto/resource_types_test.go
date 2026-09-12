package dto

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// retiredResourceTypes are values the trail once wrote and no longer does. The
// ledger is append-only, so their historical rows must stay filterable and the
// value stays in dto.ResourceTypes even though no call site produces it any
// more. Empty today — every listed type is still written. Add a value here
// ONLY together with the commit that removes its last Record call.
var retiredResourceTypes = map[string]string{}

// recordSite is one `…audit.Record(ctx, action, resourceType, …)` call found in
// the tree.
type recordSite struct {
	pos          string // file:line, relative to the repository root
	pkgDir       string
	action       string
	resourceType string
}

// TestResourceTypeTagIsTheOneDefinition pins the struct tag — which Go forces
// to be a literal — to ResourceTypes, so the published enum, the 400 message
// and the list every other test in this file checks can never drift apart.
func TestResourceTypeTagIsTheOneDefinition(t *testing.T) {
	field, ok := reflect.TypeOf(ListAuditLogQuery{}).FieldByName("ResourceType")
	if !ok {
		t.Fatal("ListAuditLogQuery has no ResourceType field")
	}
	want := "omitempty," + resourceTypeRule()
	if got := field.Tag.Get("validate"); got != want {
		t.Fatalf("the resourceType tag has drifted from dto.ResourceTypes.\n got: %s\nwant: %s", got, want)
	}
}

// TestEveryRecordedResourceTypeIsFilterable is the guard the widening exists
// for: it re-derives the vocabulary from every platform/audit Record call in
// the tree and fails when the trail writes a resource type GET /audit-log
// cannot be asked for. A new audited resource family therefore cannot ship
// half-visible — the auditor's filter is part of recording it.
func TestEveryRecordedResourceTypeIsFilterable(t *testing.T) {
	sites := recordCallSites(t)

	filterable := make(map[string]bool, len(ResourceTypes))
	for _, rt := range ResourceTypes {
		filterable[rt] = true
	}

	missing := map[string][]string{}
	for _, s := range sites {
		if !filterable[s.resourceType] {
			missing[s.resourceType] = append(missing[s.resourceType], s.pos+" ("+s.action+")")
		}
	}
	if len(missing) > 0 {
		var lines []string
		for rt, at := range missing {
			lines = append(lines, fmt.Sprintf("  %q recorded at %s", rt, strings.Join(at, ", ")))
		}
		sort.Strings(lines)
		t.Fatalf("the trail writes resource types GET /audit-log cannot filter on;\n"+
			"add them to dto.ResourceTypes AND to the oneof= tag beside it:\n%s",
			strings.Join(lines, "\n"))
	}
}

// TestEveryFilterableResourceTypeIsRecorded is the other direction: the enum
// may not advertise a filter that can never match. A value that is genuinely
// retired keeps its historical rows filterable by moving into
// retiredResourceTypes rather than out of ResourceTypes.
func TestEveryFilterableResourceTypeIsRecorded(t *testing.T) {
	recorded := map[string]bool{}
	for _, s := range recordCallSites(t) {
		recorded[s.resourceType] = true
	}

	var orphans []string
	for _, rt := range ResourceTypes {
		if recorded[rt] || retiredResourceTypes[rt] != "" {
			continue
		}
		orphans = append(orphans, rt)
	}
	if len(orphans) > 0 {
		t.Fatalf("dto.ResourceTypes advertises %v, which nothing records. If the trail really "+
			"stopped writing one, leave it listed (old rows must stay filterable) and record why "+
			"in retiredResourceTypes; if it was a typo, fix the list and the oneof= tag.", orphans)
	}
}

// TestResourceTypeScanSeesEveryRecordingPackage keeps the two tests above from
// passing vacuously. The scan matches on the SHAPE of the call
// (`<something audit>.Record(ctx, action, resourceType, id, details)`), so a
// renamed recorder field would silently make it blind; every package that
// holds a *audit.Recorder must therefore yield at least one call site.
func TestResourceTypeScanSeesEveryRecordingPackage(t *testing.T) {
	sites := recordCallSites(t)
	holders := recorderHolderPackages(t)

	found := map[string]bool{}
	for _, s := range sites {
		found[s.pkgDir] = true
	}

	var blind []string
	for _, dir := range holders {
		if !found[dir] {
			blind = append(blind, dir)
		}
	}
	sort.Strings(blind)
	if len(blind) > 0 {
		t.Fatalf("no audit.Record call was recognised in %v, although each holds a *audit.Recorder — "+
			"the source scan in this file has gone blind (recorder field renamed? Record wrapped?) "+
			"and the two vocabulary tests would pass on an incomplete set", blind)
	}
	if len(holders) == 0 {
		t.Fatal("no package in the tree holds a *audit.Recorder — the scan is not reaching the source")
	}
}

// --- the scan ---------------------------------------------------------------

// repoRoot is three levels up from modules/auditlog/dto; `go test` runs with
// the package directory as the working directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the repository root: %v", root, err)
	}
	return root
}

// scannedTrees are the directories that hold production recorders. `infra/`
// (CDK templates that are not valid Go) and every `_test.go` file are out:
// tests record deliberately synthetic types.
var scannedTrees = []string{"modules", "platform"}

func goFiles(t *testing.T) (root string, files []string) {
	t.Helper()
	root = repoRoot(t)
	for _, tree := range scannedTrees {
		err := filepath.WalkDir(filepath.Join(root, tree), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "node_modules" || d.Name() == "testdata" {
					return fs.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walking %s: %v", tree, err)
		}
	}
	if len(files) == 0 {
		t.Fatalf("no Go source found under %v in %s", scannedTrees, root)
	}
	return root, files
}

func recordCallSites(t *testing.T) []recordSite {
	t.Helper()
	root, files := goFiles(t)
	fset := token.NewFileSet()

	var sites []recordSite
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		pkgDir := filepath.Dir(rel)

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			// Recorder.Record(ctx, action, resourceType, resourceID, details).
			if !ok || sel.Sel.Name != "Record" || len(call.Args) != 5 {
				return true
			}
			if !strings.Contains(strings.ToLower(exprText(sel.X)), "audit") {
				return true
			}
			pos := fmt.Sprintf("%s:%d", rel, fset.Position(call.Pos()).Line)
			action, okA := stringLit(call.Args[1])
			resource, okR := stringLit(call.Args[2])
			if !okR {
				// A computed resource type would leave the vocabulary
				// unknowable from the source; the trail's filter would then
				// be a guess. Refuse rather than skip.
				t.Errorf("%s: audit.Record is called with a computed resource type (%s) — "+
					"GET /audit-log's filter vocabulary can no longer be derived from the source",
					pos, exprText(call.Args[2]))
				return true
			}
			if !okA {
				action = exprText(call.Args[1])
			}
			sites = append(sites, recordSite{pos: pos, pkgDir: pkgDir, action: action, resourceType: resource})
			return true
		})
	}
	if len(sites) == 0 {
		t.Fatal("the scan found no audit.Record call at all — it is not reaching the source")
	}
	return sites
}

// recorderHolderPackages lists the package directories that declare a
// *audit.Recorder (a use-case struct field or a WithAudit parameter).
func recorderHolderPackages(t *testing.T) []string {
	t.Helper()
	root, files := goFiles(t)
	fset := token.NewFileSet()

	seen := map[string]bool{}
	var dirs []string
	for _, path := range files {
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parsing %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		dir := filepath.Dir(rel)
		ast.Inspect(file, func(n ast.Node) bool {
			star, ok := n.(*ast.StarExpr)
			if !ok || exprText(star.X) != "audit.Recorder" {
				return true
			}
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
			return true
		})
	}
	sort.Strings(dirs)
	return dirs
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

// exprText renders the identifier chains the scan cares about (`uc.audit`,
// `audit.Recorder`); anything else renders as its Go type name, which is
// enough for a diagnostic and never matches.
func exprText(e ast.Expr) string {
	switch v := e.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return exprText(v.X) + "." + v.Sel.Name
	case *ast.CallExpr:
		return exprText(v.Fun) + "(…)"
	case *ast.StarExpr:
		return "*" + exprText(v.X)
	default:
		return fmt.Sprintf("%T", e)
	}
}
