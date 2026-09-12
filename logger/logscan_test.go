package logger

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The ADR-0015 build gate: "a CI log-scan check so a stray logger.info(user)
// fails the build".
//
// Redaction at the boundary (redact.go) handles the value side — a sensitive
// KEY never emits its value, wherever the call site is. This gate handles the
// shape side, which no runtime handler can see:
//
//  1. the raw request target and the peer address (r.RequestURI, r.URL.RawQuery,
//     r.URL.String(), r.RemoteAddr) reaching a logger. This is the exact
//     regression the audit reproduced — GET /members?search=jane.doe@example.com
//     wrote that address into the log store ADR-0007 places outside the erasure
//     boundary — and a grep would not catch it back, because the offending line
//     read `logger.String("url", r.RequestURI)` and looked innocent.
//  2. logger.Any outside this package: the whole-object constructor. Personal
//     data inside a value sits under keys the redaction handler never sees, so
//     a blob is the one thing it cannot protect. This is how the error handler
//     was dumping AppError.Details.
//  3. a field constructed under a key that names personal data, a secret, or a
//     request target. The handler would redact it at runtime; failing here
//     instead puts the message in front of the author, who can log the
//     reference id they actually meant.
//
// It runs inside `go test ./...`, so it is a gate for every developer and not
// only for CI; ci.yml also runs it as a named step so a failure reads as what
// it is.

// bannedKeys are keys that are not personal data themselves but name a value
// that reliably is: the request target, or a payload blob. Matched whole
// (normalized), so "detailsType" — the shape of a details value, which is
// log-safe — is unaffected.
var bannedKeys = map[string]string{
	"url":         "a URL carries its query string; log route + logger.RedactPath/RedactQuery instead",
	"uri":         "a URI carries its query string; log route + logger.RedactPath/RedactQuery instead",
	"requesturi":  "the raw request target must never be logged; use logger.RedactPath/RedactQuery",
	"details":     "a details blob is a whole object; log its type, or named reference ids",
	"body":        "a request/response body must never be logged (ADR-0015)",
	"payload":     "a payload must never be logged (ADR-0015)",
	"querystring": "log the query keys only: logger.RedactQuery",
	"rawquery":    "log the query keys only: logger.RedactQuery",
}

// bannedSelectors are the request fields whose content is the caller's, in the
// form they reach a handler in.
var bannedSelectors = map[string]string{
	"RequestURI": "the raw target includes the query string (?search=<an e-mail>)",
	"RawQuery":   "a query value is the caller's data",
	"RemoteAddr": "a network address is personal data, and behind a proxy it is not even the caller's",
}

var fieldConstructors = map[string]bool{
	"String": true, "Int": true, "Int64": true, "Bool": true, "Duration": true,
	"Float64": true, "Any": true, "Strings": true, "Time": true, "Stringp": true,
}

var levelMethods = map[string]bool{
	"Debug": true, "Info": true, "Warn": true, "Error": true, "Fatal": true,
}

type violation struct {
	pos  token.Position
	rule string
	why  string
}

func (v violation) String() string {
	return v.pos.String() + ": " + v.rule + " — " + v.why
}

// scanFile reports every ADR-0015 logging violation in one parsed file.
func scanFile(fset *token.FileSet, file *ast.File) []violation {
	pkg := file.Name.Name
	var found []violation

	// A nested call is walked once by its own visit and once by each enclosing
	// logger call (`l.Info("…", logger.String("url", r.RequestURI))`), so the
	// same position is reached more than once. Report it once.
	seen := map[string]bool{}
	add := func(v violation) {
		key := v.pos.String() + v.rule
		if seen[key] {
			return
		}
		seen[key] = true
		found = append(found, v)
	}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		name := sel.Sel.Name
		pkgIdent, isIdent := sel.X.(*ast.Ident)
		onLoggerPkg := isIdent && pkgIdent.Name == "logger"
		isLevelCall := levelMethods[name] && !onLoggerPkg // l.Info(...), logger.Log.Warn(...)

		if !onLoggerPkg && !isLevelCall {
			return true
		}

		// The redaction constructors exist to be handed a raw target; that is
		// the whole point of them.
		redacting := onLoggerPkg && strings.HasPrefix(name, "Redact")

		if !redacting {
			for _, arg := range call.Args {
				ast.Inspect(arg, func(inner ast.Node) bool {
					// logger.RedactQuery(r.URL.RawQuery) is the sanctioned use,
					// wherever it appears: stop before its arguments.
					if isRedactCall(inner) {
						return false
					}
					innerSel, ok := inner.(*ast.SelectorExpr)
					if !ok {
						return true
					}
					if why, banned := bannedSelectors[innerSel.Sel.Name]; banned {
						add(violation{
							pos:  fset.Position(innerSel.Pos()),
							rule: "r." + innerSel.Sel.Name + " reaches a log line",
							why:  why,
						})
					}
					return true
				})
				if urlStringCall(arg) {
					add(violation{
						pos:  fset.Position(arg.Pos()),
						rule: "r.URL.String() reaches a log line",
						why:  "it includes the query string; use logger.RedactPath + logger.RedactQuery",
					})
				}
			}
		}

		if onLoggerPkg && name == "Any" && pkg != "logger" {
			add(violation{
				pos:  fset.Position(call.Pos()),
				rule: "logger.Any is the blind whole-object constructor",
				why:  "key-based redaction cannot see inside a value; log named scalars or the value's type",
			})
		}

		if onLoggerPkg && fieldConstructors[name] && len(call.Args) > 0 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				key, err := strconv.Unquote(lit.Value)
				if err == nil {
					if why, banned := bannedKeys[normalizeKey(key)]; banned {
						add(violation{
							pos:  fset.Position(lit.Pos()),
							rule: "log key " + lit.Value + " is not loggable",
							why:  why,
						})
					} else if IsSensitiveKey(key) {
						add(violation{
							pos:  fset.Position(lit.Pos()),
							rule: "log key " + lit.Value + " names personal data or a secret",
							why:  "ADR-0015: reference IDs only — log the id, not the value (the handler would redact it anyway)",
						})
					}
				}
			}
		}

		return true
	})

	return found
}

// isRedactCall reports whether a node is a call to one of this package's
// redaction helpers — the one place a raw request target belongs.
func isRedactCall(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && pkgIdent.Name == "logger" && strings.HasPrefix(sel.Sel.Name, "Redact")
}

// urlStringCall reports whether an expression is <something>.URL.String().
func urlStringCall(expr ast.Node) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "String" {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	return ok && inner.Sel.Name == "URL"
}

func scanSource(t *testing.T, src string) []violation {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "fixture.go", src, parser.SkipObjectResolution)
	require.NoError(t, err)
	return scanFile(fset, file)
}

func TestLogScan_CatchesWhatTheAuditFound(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		src  string
		want string // substring of the expected rule; "" means "must be clean"
	}{
		{
			name: "the leak as it was written",
			src: `package middlewares
func f(r *http.Request) {
	l.Info("HTTP request", logger.String("url", r.RequestURI))
}`,
			want: "r.RequestURI reaches a log line",
		},
		{
			name: "the peer address",
			src: `package middlewares
func f(r *http.Request) {
	l.Info("HTTP request", logger.String("remoteAddress", r.RemoteAddr))
}`,
			want: "r.RemoteAddr reaches a log line",
		},
		{
			name: "the query string by another name",
			src: `package middlewares
func f(r *http.Request) {
	logger.Log.Info("hit", logger.String("q", r.URL.RawQuery))
}`,
			want: "r.RawQuery reaches a log line",
		},
		{
			name: "the whole URL",
			src: `package middlewares
func f(r *http.Request) {
	logger.Log.Warn("hit", logger.String("target", r.URL.String()))
}`,
			want: "r.URL.String() reaches a log line",
		},
		{
			name: "the details blob",
			src: `package httperr
func f() {
	fields = append(fields, logger.Any("details", appErr.Details))
}`,
			want: "logger.Any is the blind whole-object constructor",
		},
		{
			name: "a sensitive key",
			src: `package usecases
func f() {
	logger.Log.Info("login", logger.String("email", input.Email))
}`,
			want: "names personal data or a secret",
		},
		{
			name: "a banned key even with a safe-looking value",
			src: `package middlewares
func f() {
	logger.Log.Info("hit", logger.String("url", somethingAlreadySafe))
}`,
			want: "is not loggable",
		},
		{
			name: "the fixed request logger",
			src: `package middlewares
func f(r *http.Request) {
	fields := []logger.Field{
		logger.String("route", pattern),
		logger.String("path", logger.RedactPath(r.URL.Path)),
		logger.String("query", logger.RedactQuery(r.URL.RawQuery)),
		logger.String("userAgent", logger.RedactText(r.Header.Get("User-Agent"), 200)),
	}
	l.Info("HTTP request", fields...)
}`,
			want: "",
		},
		{
			name: "the fixed error handler",
			src: `package httperr
func f(r *http.Request) {
	l.Warn("Request error",
		logger.String("path", logger.RedactPath(r.URL.Path)),
		logger.String("code", string(appErr.Code)),
		logger.String("detailsType", fmt.Sprintf("%T", appErr.Details)),
	)
}`,
			want: "",
		},
		{
			name: "not every call that mentions a request is a log call",
			src: `package ratelimit
func f(r *http.Request) {
	addr, ok := parseAddress(r.RemoteAddr)
	_ = store.Put(r.RequestURI)
}`,
			want: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			found := scanSource(t, tc.src)
			if tc.want == "" {
				assert.Empty(t, found, "must be clean: %v", found)
				return
			}
			require.NotEmpty(t, found, "the scan missed it")
			joined := ""
			for _, v := range found {
				joined += v.String() + "\n"
			}
			assert.Contains(t, joined, tc.want)
		})
	}
}

// TestLogHygiene_NoPIICanReachALogLine is the gate itself: it walks the tree
// the way CI does. A failure names the file, the line and the remedy.
func TestLogHygiene_NoPIICanReachALogLine(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs("..")
	require.NoError(t, err)

	skipDirs := map[string]bool{
		".git": true, "infra": true, "node_modules": true, "cdk.out": true,
		"var": true, "coverage": true,
	}

	fset := token.NewFileSet()
	var found []violation
	scanned := 0

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		// Test files are exempt: the fixtures above, and the assertions that
		// prove a redactor works, must be able to write the forbidden shapes.
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if parseErr != nil {
			return parseErr
		}
		scanned++
		found = append(found, scanFile(fset, file)...)
		return nil
	})
	require.NoError(t, err)
	require.Greater(t, scanned, 100, "the walk found almost no Go files — it is looking in the wrong place")

	lines := make([]string, 0, len(found))
	for _, v := range found {
		lines = append(lines, strings.TrimPrefix(v.String(), root+string(filepath.Separator)))
	}
	sort.Strings(lines)

	assert.Empty(t, lines, "ADR-0015: personal data must not be able to reach a log line.\n%s",
		strings.Join(lines, "\n"))
}
