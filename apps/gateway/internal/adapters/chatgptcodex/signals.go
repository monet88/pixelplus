package chatgptcodex

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

// signalClass is the Adapter's normalized classification of one upstream
// exchange. It is deliberately small: every value maps onto an outcome the
// spine already understands, so the Adapter never invents an error taxonomy.
type signalClass int

const (
	// signalOK is an ordinary authenticated exchange.
	signalOK signalClass = iota
	// signalAuthFailed is an auth-class failure (HTTP 401). It is reported as an
	// OUTCOME, never as an error, so the account moves to reauth_required rather
	// than surfacing a dependency 503 (ports.ProbeAdapter contract). On the chat
	// surface a 401 is the trigger for an in-boundary refresh attempt before the
	// exchange is reported as auth-failed.
	signalAuthFailed
	// signalChallenged is a Cloudflare / bot-management block on an image path.
	// The Codex executor path carries no sentinel/PoW/Turnstile (evidence §6:
	// all unsupported), so a challenge-class signal here is a transport-layer
	// block rather than an anti-bot interstitial. It is classified and returned;
	// this Adapter never solves one (OP-G6).
	signalChallenged
	// signalRateLimited is a Provider rate-limit/backoff signal (HTTP 429 or a
	// rate_limit_error body).
	signalRateLimited
	// signalQuotaExhausted is a usage_limit_reached body signal: a cooldown-worthy
	// quota exhaustion with a reset hint (evidence §5).
	signalQuotaExhausted
	// signalUnavailable is a transient backend failure. It maps to
	// ErrDependencyUnavailable so admission fails closed.
	signalUnavailable
)

// classifyStatus maps an upstream HTTP status onto a signal class. A body-level
// quota/rate signal is refined separately by parseUsageLimit / parseRateLimit,
// because a 200 Responses body can still carry a usage_limit_reached error event
// (evidence §5 "usage_limit_reached").
func classifyStatus(status int) signalClass {
	switch {
	case status == http.StatusUnauthorized:
		return signalAuthFailed
	case status == http.StatusForbidden:
		// A 403 on this surface is the Cloudflare / bot-management shape rather
		// than an authorization decision the Gateway can act on. Treating it as a
		// challenge keeps it out of the auth-failure path, so a challenged session
		// is never mistaken for a revoked credential and never triggers a
		// pointless reauth prompt (evidence §6 challenge signals).
		return signalChallenged
	case status == http.StatusTooManyRequests:
		return signalRateLimited
	case status >= 500:
		return signalUnavailable
	case status >= 200 && status < 300:
		return signalOK
	default:
		return signalUnavailable
	}
}

// quotaSignal is the normalized usage_limit_reached view taken from a Responses
// error body or a non-streaming exchange.
type quotaSignal struct {
	// ResetsAfterSeconds is the relative reset hint in seconds, zero when the
	// upstream proved no safe hint.
	ResetsAfterSeconds int
	// Present reports whether a usage_limit_reached signal was found at all.
	Present bool
}

// parseUsageLimit extracts a usage_limit_reached error from a Responses body.
// Evidence: CLIProxyAPI codex_executor.go maps error.type usage_limit_reached
// with resets_in_seconds / resets_at to a cooldown-worthy quota exhaustion.
//
// `now` is the reference time the absolute resets_at epoch is measured
// against. It is a parameter rather than an inline time.Now() call so this
// function stays deterministic and testable — the caller owns the clock
// (mirrors CLIProxyAPI's parseCodexRetryAfter(statusCode, errorBody, now)).
//
// Precedence mirrors parseCodexRetryAfter: resets_at is preferred when present
// AND still in the future; resets_in_seconds is the fallback. Both are
// normalized onto the same relative-seconds carrier so callers never see which
// field on the wire produced the hint.
func parseUsageLimit(body string, now time.Time) quotaSignal {
	var payload struct {
		Error struct {
			Type            string  `json:"type"`
			ResetsInSeconds float64 `json:"resets_in_seconds"`
			ResetsAt        int64   `json:"resets_at"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return quotaSignal{}
	}
	if payload.Error.Type != "usage_limit_reached" {
		return quotaSignal{}
	}
	if payload.Error.ResetsAt > 0 {
		resetAt := time.Unix(payload.Error.ResetsAt, 0)
		if resetAt.After(now) {
			return quotaSignal{
				ResetsAfterSeconds: int(resetAt.Sub(now).Seconds()),
				Present:            true,
			}
		}
	}
	return quotaSignal{
		ResetsAfterSeconds: int(payload.Error.ResetsInSeconds),
		Present:            true,
	}
}

// A plan-entitlement parser is deliberately ABSENT here. Earlier drafts
// carried a parsePlanEntitlement helper that nothing called, which read as
// though this Adapter already classified plan entitlement when it did not:
// mapping a free plan onto an entitlement-missing image operation requires an
// account-attributes exchange this story does not make (Observe reads
// /backend-api/models only). Keeping a parser for a body the Adapter never
// fetches asserted coverage that does not exist.
//
// That is a gap to close with the exchange that needs it, not with a helper
// that has no caller. testdata/entitlement_free.json is retained as the
// recorded shape for that future exchange, and is not asserted on by any
// test; validation.md states plainly that the entitlement family is fixture
// shape rather than proved behavior.
//
// Body-level rate/capacity signals, by contrast, ARE live and load-bearing:
// the HTTP 429 in classifyStatus, the in-stream rate_limit_error event, and
// the in-stream model-at-capacity message (decoded in protocol.go's
// decodeResponsesError) are the call sites that actually classify them.

// modelSlugs extracts the session-dependent model slugs from a
// /backend-api/models payload. The list is account and session dependent, so a
// Capability Snapshot must store what was observed rather than a static
// provider catalog (evidence §2.2 "model listing / discovery"). The Codex
// evidence marks discovery `conditionally_supported` and drift-prone, so a
// fixture controls this exchange.
func modelSlugs(body string) []string {
	var payload struct {
		Models []struct {
			Slug string `json:"slug"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return nil
	}
	slugs := make([]string, 0, len(payload.Models))
	for _, model := range payload.Models {
		slug := strings.TrimSpace(model.Slug)
		if slug == "" {
			continue
		}
		slugs = append(slugs, slug)
	}
	return slugs
}
