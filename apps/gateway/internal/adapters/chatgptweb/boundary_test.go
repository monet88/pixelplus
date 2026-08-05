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

func TestAdapterPerformsNoHTTPEgressOfItsOwn(t *testing.T) {
	t.Parallel()

	// The Adapter reads net/http for its status-code constants, which is inert.
	// What it must not do is build a client or a request: every byte leaving this
	// package goes through the injected Transport, so a lab deployment that
	// enables the mode without supplying transport still cannot reach a Provider.
	for name, file := range packageFiles(t) {
		ast.Inspect(file, func(node ast.Node) bool {
			selector, ok := node.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			if !ok || pkg.Name != "http" {
				return true
			}
			for _, forbidden := range forbiddenEgress {
				if selector.Sel.Name == forbidden {
					t.Errorf("%s uses http.%s; egress must go through the Transport seam", name, forbidden)
				}
			}
			return true
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

func TestAdapterPackageDeclaresNoMutableGlobals(t *testing.T) {
	t.Parallel()

	// A package-level var is process-wide state shared across every Tenant and
	// account the Adapter serves. Only the sentinel error values and interface
	// assertions are acceptable, and both are immutable.
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
				for index, ident := range value.Names {
					if ident.Name == "_" {
						continue
					}
					if strings.HasPrefix(ident.Name, "Err") || strings.HasPrefix(ident.Name, "err") {
						continue
					}
					_ = index
					t.Errorf("%s: package-level var %q is mutable cross-call state", name, ident.Name)
				}
			}
		}
	}
}
