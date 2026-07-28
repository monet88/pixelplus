package composition_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Domain production surface must not host contract-fixture PNG helpers or a
// storage_cap_later Provider outcome (review findings #3). Scanned from the
// composition package so domain stays free of os/path imports (ADR 0009).
func TestDomainRenderSurfaceOmitsFixturePNGAndStorageCapOutcome(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	renderGo := filepath.Join(filepath.Dir(thisFile), "..", "domain", "render.go")
	src, err := os.ReadFile(renderGo)
	if err != nil {
		t.Fatalf("read domain/render.go: %v", err)
	}
	text := string(src)
	if strings.Contains(text, "func MinimalPNG") {
		t.Fatal("domain must not export MinimalPNG; controlled fixture bytes belong in contract tests")
	}
	if strings.Contains(text, "RenderOutcomeStorageCapLater") || strings.Contains(text, "storage_cap_later") {
		t.Fatal("domain must not define storage_cap_later Provider outcome; storage-cap is placement/delivery")
	}
}
