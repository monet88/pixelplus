package chatgptweb_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// forbiddenImports are the packages an Adapter that "translates protocol only"
// must never reach for. Each entry names a responsibility the Adapter would be
// taking over if it imported it (AC1).
var forbiddenImports = map[string]string{
	"internal/application":                "Tenant selection and full-operation retry belong to the spine",
	"internal/composition":                "an Adapter never wires its own dependencies",
	"internal/infrastructure/persistence": "durable state belongs to a store, not an Adapter",
	"internal/infrastructure/vault":       "credential material arrives through ports.CredentialInjection only",
	"internal/transport/http":             "an Adapter is not a transport surface",
}

// forbiddenEgress names the net/http identifiers that would perform a real
// request. Importing net/http for its status constants is fine and is what the
// Adapter does; constructing a client or a request is not, because this story
// ships no HTTP egress — the Transport seam is the only way out (non-goal).
var forbiddenEgress = []string{
	"Client",
	"DefaultClient",
	"NewRequest",
	"NewRequestWithContext",
	"Get",
	"Post",
	"PostForm",
	"Head",
	"Do",
}

// packageFiles parses every non-test source file in the Adapter package.
func packageFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package directory: %v", err)
	}
	fileSet := token.NewFileSet()
	parsed := map[string]*ast.File{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fileSet, filepath.Join(".", name), nil, parser.AllErrors)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		parsed[name] = file
	}
	if len(parsed) == 0 {
		t.Fatal("no Adapter source files found; this guard would pass vacuously")
	}
	return parsed
}

// requiredFixtureFamilies enumerates the fixture coverage AC4 names. Keeping the
// list here rather than implicit in test names means dropping a family fails the
// suite instead of silently shrinking coverage.
var requiredFixtureFamilies = map[string][]string{
	"credential preparation": {"credential_prepare.json"},
	"chat and stream":        {"chat_stream.sse"},
	"image operations":       {"image_generate.sse", "image_edit.sse"},
	"challenge":              {"challenge.json"},
	"quota and rate":         {"quota_rate.json"},
	"protocol drift":         {"protocol_drift.sse"},
	"moderation":             {"moderation_blocked.sse"},
}

func TestEveryRequiredFixtureFamilyIsPresent(t *testing.T) {
	t.Parallel()

	for family, files := range requiredFixtureFamilies {
		for _, file := range files {
			info, err := os.Stat(filepath.Join("testdata", file))
			if err != nil {
				t.Errorf("fixture family %q is missing %s: %v", family, file, err)
				continue
			}
			if info.Size() == 0 {
				t.Errorf("fixture family %q file %s is empty", family, file)
			}
		}
	}
}

func TestAdapterOwnsNoSpineResponsibility(t *testing.T) {
	t.Parallel()

	for name, file := range packageFiles(t) {
		for _, imported := range file.Imports {
			path, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", name, err)
			}
			for forbidden, reason := range forbiddenImports {
				if path == forbidden || strings.HasSuffix(path, "/"+forbidden) {
					t.Errorf("%s imports %s: %s", name, path, reason)
				}
			}
		}
	}
}

// resolveImportName reports the local identifier a file uses for one import
// path, plus whether that import was made with `.` (a dot-import).
//
// Why this exists: matching a selector's base identifier against the literal
// string "http" is a spelling check, not an import check. Go lets a file write
// `import httpx "net/http"` and then `httpx.NewRequest(...)`, which is the exact
// same egress with a different first token — a name-only guard reports clean
// while the Adapter dials out. Resolving the file's ACTUAL local name closes
// that hole in the direction that matters: renaming the import can no longer
// disable the check.
//
// The three cases the AST forces us to handle:
//
//   - spec.Name == nil — the ordinary `import "net/http"`. The local name is the
//     package's own name, which for net/http is the last path segment, "http".
//   - spec.Name.Name == "_" — a blank import. It binds no identifier, so no
//     selector can reference it and it cannot produce egress. Skipped.
//   - spec.Name.Name == "." — a dot-import. Every exported name lands in file
//     scope unqualified, so egress becomes a bare `Client{}` or `NewRequest(...)`
//     with no base identifier at all. There is nothing for a selector-based
//     guard to match, so the caller must refuse it rather than pretend to have
//     checked.
//
// Using the last path segment as the default name is exact for net/http; it is
// an approximation only for packages whose declared name differs from their
// directory, which is not the case for anything this guard inspects.
func resolveImportName(t *testing.T, fileName string, file *ast.File, importPath string) (local string, dotImported bool, imported bool) {
	t.Helper()
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			t.Fatalf("unquote import in %s: %v", fileName, err)
		}
		if path != importPath {
			continue
		}
		if spec.Name == nil {
			segments := strings.Split(path, "/")
			return segments[len(segments)-1], false, true
		}
		switch spec.Name.Name {
		case "_":
			// Bound to nothing; it cannot be the base of a selector.
			continue
		case ".":
			return "", true, true
		default:
			return spec.Name.Name, false, true
		}
	}
	return "", false, false
}

func TestAdapterPerformsNoHTTPEgressOfItsOwn(t *testing.T) {
	t.Parallel()

	// The Adapter reads net/http for its status-code constants, which is inert.
	// What it must not do is build a client or a request: every byte leaving this
	// package goes through the injected Transport, so a lab deployment that
	// enables the mode without supplying transport still cannot reach a Provider.
	//
	// The check resolves each file's own local name for net/http instead of
	// assuming the identifier is spelled "http". Cause and effect: under a literal
	// "http" comparison, adding `import httpx "net/http"` plus
	// `httpx.NewRequest(ctx, ...)` is real egress the guard scores as clean,
	// because no selector base is spelled "http" any more. That rename is a
	// one-token edit, and the property it silently deletes is the entire non-goal
	// ("this story ships no HTTP egress").
	for name, file := range packageFiles(t) {
		local, dotImported, imported := resolveImportName(t, name, file, "net/http")
		if !imported {
			continue
		}
		if dotImported {
			// A dot-import makes `Client{}` and `NewRequest(...)` legal with no
			// qualifier, so no base identifier survives for this guard to inspect.
			// Refusing the import is the only honest outcome: a guard that cannot
			// observe the property must fail, not pass.
			t.Errorf("%s dot-imports net/http; egress identifiers become unqualified and unverifiable, "+
				"so the Transport-seam guard cannot hold — import it with a qualifier", name)
			continue
		}
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != local {
				return true
			}
			for _, forbidden := range forbiddenEgress {
				if selector.Sel.Name == forbidden {
					t.Errorf("%s uses %s.%s (net/http imported as %q); egress must go through the Transport seam",
						name, local, forbidden, local)
				}
			}
			return true
		})
	}
}

// TestResolveImportNameHandlesEveryImportForm pins the resolver the egress guard
// depends on.
//
// This exists because one of the forms below cannot be exercised by mutating the
// real package. A dot-import of net/http is a COMPILE error here: the package
// declares its own Request, Response, and Transport types, and a dot-import
// drops net/http's identically-named types into the same file scope
// ("Request already declared through dot-import of package http"). So the
// dot-import branch of the guard can only be proved against a synthetic file,
// and without this test that branch would itself be unverified — the same
// false-confidence defect the guard was fixed for.
func TestResolveImportNameHandlesEveryImportForm(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name        string
		source      string
		wantLocal   string
		wantDot     bool
		wantPresent bool
	}{
		{
			// No Name node at all: the local name is the package name, "http".
			name:        "default name",
			source:      "package p\nimport \"net/http\"\n",
			wantLocal:   "http",
			wantPresent: true,
		},
		{
			// The hole the old literal "http" comparison left open.
			name:        "alias",
			source:      "package p\nimport httpx \"net/http\"\n",
			wantLocal:   "httpx",
			wantPresent: true,
		},
		{
			// Binds no identifier, so it can never be a selector base.
			name:        "blank",
			source:      "package p\nimport _ \"net/http\"\n",
			wantPresent: false,
		},
		{
			// Reported as dot-imported so the caller can refuse it.
			name:        "dot",
			source:      "package p\nimport . \"net/http\"\n",
			wantDot:     true,
			wantPresent: true,
		},
		{
			// A same-suffix path must not be mistaken for net/http.
			name:        "absent with a decoy suffix",
			source:      "package p\nimport \"example.com/fake/net/http\"\n",
			wantPresent: false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			file, err := parser.ParseFile(token.NewFileSet(), "synthetic.go", testCase.source, parser.AllErrors)
			if err != nil {
				t.Fatalf("parse synthetic source: %v", err)
			}
			local, dotImported, imported := resolveImportName(t, "synthetic.go", file, "net/http")
			if imported != testCase.wantPresent {
				t.Errorf("imported = %v, want %v", imported, testCase.wantPresent)
			}
			if dotImported != testCase.wantDot {
				t.Errorf("dotImported = %v, want %v", dotImported, testCase.wantDot)
			}
			if local != testCase.wantLocal {
				t.Errorf("local = %q, want %q", local, testCase.wantLocal)
			}
		})
	}
}

func TestAdapterHoldsNoStateAcrossCalls(t *testing.T) {
	t.Parallel()

	// The Adapter struct may hold exactly the Transport seam. Any other field
	// would be state surviving between calls, which is what AC1 forbids: a
	// cached conversation id, a retry counter, or a memoized credential would
	// each make the Adapter a decision-maker rather than a translator.
	found := false
	for name, file := range packageFiles(t) {
		ast.Inspect(file, func(node ast.Node) bool {
			spec, ok := node.(*ast.TypeSpec)
			if !ok || spec.Name.Name != "Adapter" {
				return true
			}
			structType, ok := spec.Type.(*ast.StructType)
			if !ok {
				t.Fatalf("%s: Adapter is not a struct", name)
			}
			found = true
			for _, field := range structType.Fields.List {
				for _, fieldName := range field.Names {
					if fieldName.Name != "transport" {
						t.Errorf("%s: Adapter holds field %q; only the Transport seam is permitted", name, fieldName.Name)
					}
				}
				if len(field.Names) == 0 {
					t.Errorf("%s: Adapter embeds a type; embedding can smuggle in state", name)
				}
			}
			return false
		})
	}
	if !found {
		t.Fatal("Adapter type not found; this guard would pass vacuously")
	}
}

// isErrorSentinel reports whether one package-level ValueSpec declares genuine
// immutable error sentinels rather than mutable state.
//
// The property being enforced is "the Adapter holds no cross-call state", and a
// name prefix cannot enforce it. `var errCount int` and
// `var errAuthFailed = errors.New(...)` both start with "err"; only the second
// is a sentinel. The first is a process-wide counter shared by every Tenant and
// account the Adapter serves — exactly the accumulator AC1 forbids — and a
// prefix check waves it through.
//
// So the value is inspected instead of the name. A sentinel must:
//
//  1. have an initializer for every declared name (a bare `var errCount int` has
//     none, which is itself proof it is zero-valued mutable state), and
//  2. initialize each name with a call to errors.New or fmt.Errorf, resolved
//     through the file's own local import names so an alias cannot spoof it.
//
// Note this is deliberately stricter than "is of error type": a var holding an
// error returned by some helper could still be reassigned meaningfully, whereas
// an errors.New/fmt.Errorf value created once at init is the idiomatic
// write-once sentinel the package actually declares.
func isErrorSentinel(t *testing.T, fileName string, file *ast.File, value *ast.ValueSpec) bool {
	t.Helper()
	// No initializer at all: `var errCount int`. Zero-valued, therefore mutable.
	if len(value.Values) != len(value.Names) {
		return false
	}
	errorsLocal, errorsDot, errorsImported := resolveImportName(t, fileName, file, "errors")
	fmtLocal, fmtDot, fmtImported := resolveImportName(t, fileName, file, "fmt")
	for _, expr := range value.Values {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			return false
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return false
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return false
		}
		isErrorsNew := errorsImported && !errorsDot && pkg.Name == errorsLocal && selector.Sel.Name == "New"
		isFmtErrorf := fmtImported && !fmtDot && pkg.Name == fmtLocal && selector.Sel.Name == "Errorf"
		if !isErrorsNew && !isFmtErrorf {
			return false
		}
	}
	return true
}

func TestAdapterPackageDeclaresNoMutableGlobals(t *testing.T) {
	t.Parallel()

	// A package-level var is process-wide state shared across every Tenant and
	// account the Adapter serves. Only the sentinel error values and interface
	// assertions are acceptable, and both are immutable.
	//
	// The exemption is proved from the declaration's VALUE, not its name. Concrete
	// cause and effect: a future `var errCount int` incremented on each failure
	// would make consecutive requests observe each other — two Tenants sharing one
	// counter — and under the old `strings.HasPrefix(name, "err")` rule it was
	// exempt on the strength of three characters. Requiring an errors.New /
	// fmt.Errorf initializer keeps every real sentinel in chat.go and transport.go
	// passing while that counter fails.
	for name, file := range packageFiles(t) {
		for _, decl := range file.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok || general.Tok != token.VAR {
				continue
			}
			for _, spec := range general.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				// `_ = ports.ChatAdapter((*Adapter)(nil))` and friends bind no name and
				// allocate no storage; they are compile-time interface assertions.
				sentinel := isErrorSentinel(t, name, file, value)
				for _, ident := range value.Names {
					if ident.Name == "_" {
						continue
					}
					if sentinel {
						continue
					}
					t.Errorf("%s: package-level var %q is mutable cross-call state "+
						"(only errors.New/fmt.Errorf sentinels and blank interface assertions are permitted)",
						name, ident.Name)
				}
			}
		}
	}
}
