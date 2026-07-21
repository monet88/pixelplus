package composition_test

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

const modulePath = "github.com/monet88/pixelplus/apps/gateway"

func TestGatewayImportsRespectDependencyDirection(t *testing.T) {
	t.Parallel()

	root := gatewayRoot(t)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			return nil
		}

		relativeFile, err := filepath.Rel(root, path)
		if err != nil {
			t.Fatalf("relative path for %s: %v", path, err)
		}
		importer := filepath.ToSlash(filepath.Dir(relativeFile))
		if strings.HasSuffix(relativeFile, "_test.go") {
			importer += "_test"
		}
		if packageLayer(importer) == "" {
			t.Errorf("%s: package is outside the accepted Gateway layout", relativeFile)
		}

		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", relativeFile, err)
		}
		for _, imported := range file.Imports {
			pathValue, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				t.Fatalf("unquote import in %s: %v", relativeFile, err)
			}
			if violation := importViolation(importer, pathValue); violation != "" {
				t.Errorf("%s imports %s: %s", relativeFile, pathValue, violation)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk Gateway source: %v", err)
	}
}

func TestDependencyPolicyRejectsForbiddenEdges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		importer string
		imported string
	}{
		{name: "domain to application", importer: "internal/domain", imported: modulePath + "/internal/application"},
		{name: "nested domain to transport", importer: "internal/domain/model", imported: modulePath + "/internal/transport/http"},
		{name: "application to transport", importer: "internal/application", imported: modulePath + "/internal/transport/http"},
		{name: "ports to infrastructure", importer: "internal/ports", imported: modulePath + "/internal/infrastructure/jobs"},
		{name: "transport to persistence", importer: "internal/transport/http", imported: modulePath + "/internal/infrastructure/persistence"},
		{name: "adapter to composition", importer: "internal/adapters", imported: modulePath + "/internal/composition"},
		{name: "domain to HTTP", importer: "internal/domain", imported: "net/http"},
		{name: "application to SQL", importer: "internal/application", imported: "database/sql"},
		{name: "ports to process environment", importer: "internal/ports", imported: "os"},
		{name: "transport to SQL", importer: "internal/transport/http", imported: "database/sql"},
		{name: "lookalike module path", importer: "internal/application", imported: modulePath + "evil/internal/domain"},
		{name: "infrastructure third party", importer: "internal/infrastructure/jobs", imported: "example.com/queue"},
		{name: "command third party", importer: "cmd/gateway", imported: "example.com/cli"},
		{name: "composition to cgo runtime", importer: "internal/composition", imported: "runtime/cgo"},
		{name: "cgo from composition", importer: "internal/composition", imported: "C"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if violation := importViolation(test.importer, test.imported); violation == "" {
				t.Fatalf("import %s -> %s was allowed", test.importer, test.imported)
			}
		})
	}
}

func gatewayRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve architecture test path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func importViolation(importer, imported string) string {
	if imported == "C" || imported == "runtime/cgo" || strings.HasPrefix(imported, "runtime/cgo/") {
		return "cgo is forbidden by the Pure-Go dependency budget"
	}
	if isBudgetedPackage(importer) && isThirdParty(imported) {
		return "third-party dependency exceeds the zero-dependency budget"
	}
	if !strings.HasPrefix(imported, modulePath+"/") {
		return standardLibraryViolation(packageLayer(importer), imported)
	}

	imported = strings.TrimPrefix(imported, modulePath+"/")
	if allowedInternalImport(importer, imported) {
		return ""
	}
	return "forbidden Gateway dependency direction"
}

func standardLibraryViolation(layer, imported string) string {
	forbidden := map[string][]string{
		"domain":      {"database/sql", "net/http", "os", "plugin", "syscall"},
		"application": {"database/sql", "net/http", "os", "plugin", "syscall"},
		"ports":       {"database/sql", "net/http", "os", "plugin", "syscall"},
		"transport":   {"database/sql", "os", "plugin", "syscall"},
	}
	for _, root := range forbidden[layer] {
		if imported == root || strings.HasPrefix(imported, root+"/") {
			return "standard-library import violates the accepted layer boundary"
		}
	}
	return ""
}

func isThirdParty(imported string) bool {
	if imported == modulePath || strings.HasPrefix(imported, modulePath+"/") {
		return false
	}
	first, _, _ := strings.Cut(imported, "/")
	return strings.Contains(first, ".")
}

func isBudgetedPackage(importer string) bool {
	switch packageLayer(importer) {
	case "domain", "application", "ports", "transport", "adapters", "infrastructure", "composition", "contracttest", "cmd":
		return true
	default:
		return false
	}
}

func allowedInternalImport(importer, imported string) bool {
	importer = strings.TrimSuffix(importer, "_test")
	if importer == imported {
		return true
	}

	importerLayer := packageLayer(importer)
	importedLayer := packageLayer(imported)
	switch importerLayer {
	case "domain":
		return importedLayer == "domain"
	case "ports":
		return importedLayer == "ports" || importedLayer == "domain"
	case "application":
		return importedLayer == "application" || importedLayer == "domain" || importedLayer == "ports"
	case "transport":
		return importedLayer == "transport" || importedLayer == "application" || importedLayer == "domain"
	case "adapters":
		return importedLayer == "adapters" || importedLayer == "domain" || importedLayer == "ports"
	case "infrastructure":
		return sameInfrastructurePackage(importer, imported) || importedLayer == "domain" || importedLayer == "ports"
	case "composition":
		return importedLayer == "composition" ||
			importedLayer == "domain" ||
			importedLayer == "application" ||
			importedLayer == "ports" ||
			importedLayer == "transport" ||
			importedLayer == "adapters" ||
			importedLayer == "infrastructure"
	case "contracttest":
		return importedLayer == "contracttest" ||
			importedLayer == "domain" ||
			importedLayer == "ports" ||
			importedLayer == "composition"
	case "cmd":
		return importedLayer == "composition"
	default:
		return false
	}
}

func packageLayer(path string) string {
	path = strings.TrimSuffix(path, "_test")
	switch {
	case path == "cmd/gateway":
		return "cmd"
	case inPackageTree(path, "internal/domain"):
		return "domain"
	case inPackageTree(path, "internal/application"):
		return "application"
	case inPackageTree(path, "internal/ports"):
		return "ports"
	case inPackageTree(path, "internal/transport/http"):
		return "transport"
	case inPackageTree(path, "internal/adapters"):
		return "adapters"
	case inPackageTree(path, "internal/infrastructure/jobs"),
		inPackageTree(path, "internal/infrastructure/persistence"),
		inPackageTree(path, "internal/infrastructure/vault"),
		inPackageTree(path, "internal/infrastructure/observability"):
		return "infrastructure"
	case inPackageTree(path, "internal/composition"):
		return "composition"
	case inPackageTree(path, "internal/contracttest"):
		return "contracttest"
	default:
		return ""
	}
}

func inPackageTree(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+"/")
}

func sameInfrastructurePackage(left, right string) bool {
	for _, root := range []string{
		"internal/infrastructure/jobs",
		"internal/infrastructure/persistence",
		"internal/infrastructure/vault",
		"internal/infrastructure/observability",
	} {
		if inPackageTree(left, root) && inPackageTree(right, root) {
			return true
		}
	}
	return false
}
