package chatgptcodex

import (
	"strconv"
	"testing"
	"time"
)

// TestParseUsageLimitResetsAtOnly asserts an absolute resets_at epoch (with no
// resets_in_seconds) is converted to a normalized relative-seconds hint.
func TestParseUsageLimitResetsAtOnly(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetAt := now.Add(2 * time.Hour)
	body := `{"error":{"type":"usage_limit_reached","resets_at":` + strconv.FormatInt(resetAt.Unix(), 10) + `}}`

	signal := parseUsageLimit(body, now)
	if !signal.Present {
		t.Fatal("Present = false, want true")
	}
	if signal.ResetsAfterSeconds != 7200 {
		t.Errorf("ResetsAfterSeconds = %d, want 7200", signal.ResetsAfterSeconds)
	}
}

// TestParseUsageLimitResetsInSecondsOnly asserts the relative hint alone still
// works exactly as before resets_at support was added.
func TestParseUsageLimitResetsInSecondsOnly(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body := `{"error":{"type":"usage_limit_reached","resets_in_seconds":3600}}`

	signal := parseUsageLimit(body, now)
	if !signal.Present {
		t.Fatal("Present = false, want true")
	}
	if signal.ResetsAfterSeconds != 3600 {
		t.Errorf("ResetsAfterSeconds = %d, want 3600", signal.ResetsAfterSeconds)
	}
}

// TestParseUsageLimitPrefersResetsAtWhenBothPresent asserts resets_at wins
// over resets_in_seconds when both are present and resets_at is in the future,
// mirroring CLIProxyAPI codex_executor.go parseCodexRetryAfter precedence.
func TestParseUsageLimitPrefersResetsAtWhenBothPresent(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetAt := now.Add(30 * time.Minute)
	body := `{"error":{"type":"usage_limit_reached","resets_at":` + strconv.FormatInt(resetAt.Unix(), 10) + `,"resets_in_seconds":3600}}`

	signal := parseUsageLimit(body, now)
	if !signal.Present {
		t.Fatal("Present = false, want true")
	}
	if signal.ResetsAfterSeconds != 1800 {
		t.Errorf("ResetsAfterSeconds = %d, want 1800 (resets_at must win over resets_in_seconds)", signal.ResetsAfterSeconds)
	}
}

// TestParseUsageLimitFallsBackWhenResetsAtIsInThePast asserts a past resets_at
// is NOT trusted — the reset already elapsed, so it falls back to
// resets_in_seconds, mirroring parseCodexRetryAfter's resetAtTime.After(now)
// guard.
func TestParseUsageLimitFallsBackWhenResetsAtIsInThePast(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	resetAt := now.Add(-1 * time.Hour)
	body := `{"error":{"type":"usage_limit_reached","resets_at":` + strconv.FormatInt(resetAt.Unix(), 10) + `,"resets_in_seconds":900}}`

	signal := parseUsageLimit(body, now)
	if !signal.Present {
		t.Fatal("Present = false, want true")
	}
	if signal.ResetsAfterSeconds != 900 {
		t.Errorf("ResetsAfterSeconds = %d, want 900 (a past resets_at must fall back to resets_in_seconds)", signal.ResetsAfterSeconds)
	}
}
