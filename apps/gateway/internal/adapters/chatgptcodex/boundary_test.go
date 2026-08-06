package chatgptcodex_test

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/adapters/chatgptcodex"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// forbiddenImports are the packages an Adapter that "translates protocol only"
// must never reach for. Each entry names a responsibility the Adapter would be
// taking over if it imported it.
var forbiddenImports = map[string]string{
	"internal/application":                "Tenant selection and full-operation retry belong to the spine",
	"internal/composition":                "an Adapter never wires its own dependencies",
	"internal/infrastructure/persistence": "durable state belongs to a store, not an Adapter",
	"internal/infrastructure/vault":       "credential material arrives through ports.CredentialInjection only",
	"internal/transport/http":             "an Adapter is not a transport surface",
}

// forbiddenEgress names the net/http identifiers that would perform a real
// request. Importing net/http for its status constants is fine; constructing a
// client or a request is not, because this story ships no HTTP egress — the
// Transport seam is the only way out (non-goal).
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

// requiredFixtureFamilies enumerates the fixture coverage AC3 names. Keeping the
// list here rather than implicit in test names means dropping a family fails the
// suite instead of silently shrinking coverage.
var requiredFixtureFamilies = map[string][]string{
	"refresh":           {"token_refresh.json"},
	"chat and stream":   {"chat_stream.sse"},
	"image operations":  {"image_generate.sse", "image_edit.sse"},
	"entitlement":       {"entitlement_free.json"},
	"quota and rate":    {"quota_rate.json"},
	"challenge":         {"challenge.json"},
	"protocol drift":    {"protocol_drift.sse"},
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
// path, plus whether that import was made with `.` (a dot-import). Matching a
// selector's base against the literal "http" is a spelling check, not an import
// check: `import httpx "net/http"` plus `httpx.NewRequest(...)` is the exact
// same egress with a different first token. Resolving the file's ACTUAL local
// name closes that hole.
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

	for name, file := range packageFiles(t) {
		local, dotImported, imported := resolveImportName(t, name, file, "net/http")
		if !imported {
			continue
		}
		if dotImported {
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

func TestFixturesCarryNoRealSecrets(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no fixtures found")
	}

	// Shapes that would indicate a real credential was pasted into testdata.
	// OP-G3 forbids credential material anywhere it can be read back out.
	forbidden := []string{
		"sk-",
		"eyJhbGciOi", // a real JWT header
		"Bearer ey",
		"__Secure-1PSID",
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		body := loadFixture(t, entry.Name())
		for _, needle := range forbidden {
			if strings.Contains(body, needle) {
				t.Errorf("fixture %s contains %q, which looks like real credential material", entry.Name(), needle)
			}
		}
		// Every credential-shaped fixture value must be the single placeholder.
		if strings.Contains(body, "accessToken") {
			t.Errorf("fixture %s contains camelCase accessToken; use the snake_case bundle fields", entry.Name())
		}
	}

	// Guard the guard: the placeholder itself must exist in at least one fixture,
	// otherwise the check above passes vacuously.
	found := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if strings.Contains(loadFixture(t, entry.Name()), fixturePlaceholder) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("no fixture uses the %q placeholder; the secret guard would pass vacuously", fixturePlaceholder)
	}
}

// TestTheAdapterReleasesTheUpstreamStreamExactlyOnce asserts the close signal
// the sseStream fixture records. Zero closes leaks the upstream body; two closes
// means two owners think they hold it. The streaming and non-streaming surfaces
// are checked separately because they are different call paths into the same
// decoder.
func TestTheAdapterReleasesTheUpstreamStreamExactlyOnce(t *testing.T) {
	t.Parallel()

	newTransportWithStream := func(t *testing.T) (*fixtureTransport, *sseStream) {
		t.Helper()
		stream := newSSEStream(loadFixture(t, "chat_stream.sse"))
		transport := newFixtureTransport().on(chatgptcodex.PathCodexResponses, chatgptcodex.Response{
			Status: http.StatusOK,
			Stream: stream,
		})
		return transport, stream
	}

	t.Run("non-streaming Run", func(t *testing.T) {
		t.Parallel()
		transport, stream := newTransportWithStream(t)
		outcome, err := chatgptcodex.New(transport).
			Run(t.Context(), chatCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()})
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
		if outcome.Commit != domain.CommitCommitted {
			t.Fatalf("commit = %s, want committed (the stream must have been consumed for the close count to mean anything)", outcome.Commit)
		}
		if got := stream.closeCount(); got != 1 {
			t.Errorf("Close() called %d times, want exactly 1 (0 leaks the upstream body, >1 means two owners released it)", got)
		}
	})

	t.Run("streaming Stream", func(t *testing.T) {
		t.Parallel()
		transport, stream := newTransportWithStream(t)
		sink := &recordingSink{}
		outcome, err := chatgptcodex.New(transport).
			Stream(t.Context(), streamCommand("gpt-fixture-codex-1"), &staticCredential{material: codexBundleMaterial()}, sink)
		if err != nil {
			t.Fatalf("Stream() error = %v", err)
		}
		if outcome.Commit != domain.CommitCommitted {
			t.Fatalf("commit = %s, want committed", outcome.Commit)
		}
		if got := stream.closeCount(); got != 1 {
			t.Errorf("Close() called %d times, want exactly 1", got)
		}
	})

	t.Run("a read after close is refused", func(t *testing.T) {
		t.Parallel()
		stream := newSSEStream("first\nsecond\n")
		if _, ok, err := stream.Next(); !ok || err != nil {
			t.Fatalf("first Next() = (ok=%v, err=%v), want a payload", ok, err)
		}
		if err := stream.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if _, ok, err := stream.Next(); ok || !errors.Is(err, errStreamClosed) {
			t.Errorf("Next() after Close = (ok=%v, err=%v), want (false, read after close)", ok, err)
		}
	})
}

// TestNilTransportFailsClosed asserts the shipped production posture: a
// registered Adapter with no transport fails every surface closed rather than
// reaching a Provider.
func TestNilTransportFailsClosed(t *testing.T) {
	t.Parallel()

	adapter := chatgptcodex.New(nil)

	if _, err := adapter.Probe(context.Background(), probeCommand()); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Errorf("Probe() error = %v, want ErrDependencyUnavailable on nil transport", err)
	}
	if _, err := adapter.Observe(context.Background(), capabilityCommand()); !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Errorf("Observe() error = %v, want ErrDependencyUnavailable on nil transport", err)
	}
}
