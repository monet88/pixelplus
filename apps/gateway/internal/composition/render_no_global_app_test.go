package composition_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Application must contain no package-global mutable state for render staging
// (queued review: stagedOutputs map is forbidden). Scanned from composition so
// application stays free of os imports (ADR 0009).
func TestApplicationRenderHasNoPackageGlobalStaging(t *testing.T) {
	t.Parallel()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test path")
	}
	srcPath := filepath.Join(filepath.Dir(thisFile), "..", "application", "render.go")
	src, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read application/render.go: %v", err)
	}
	text := string(src)
	for _, forbidden := range []string{"var stagedOutputs", "stagedOutputs.mu", "stagedOutputs.m"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("application/render.go must not contain package-global staging %q; use injected RenderStagingStore", forbidden)
		}
	}
}
