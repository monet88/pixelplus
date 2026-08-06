package chatgptcodex

import (
	"encoding/json"
	"net/http"
	"strings"
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
func parseUsageLimit(body string) quotaSignal {
	var payload struct {
		Error struct {
			Type            string  `json:"type"`
			ResetsInSeconds float64 `json:"resets_in_seconds"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return quotaSignal{}
	}
	if payload.Error.Type != "usage_limit_reached" {
		return quotaSignal{}
	}
	return quotaSignal{
		ResetsAfterSeconds: int(payload.Error.ResetsInSeconds),
		Present:            true,
	}
}

// Body-level rate/capacity and plan-entitlement parsers are deliberately ABSENT
// here. Earlier drafts carried a rateLimitError / modelAtCapacity /
// parsePlanEntitlement trio that nothing called, which read as though this
// Adapter already classified those signals when it did not:
//
//   - Body-level rate signals: the live rate paths are the HTTP 429 in
//     classifyStatus and the in-stream rate_limit_error event in
//     decodeResponsesError. A third, uncalled body parser only obscured which of
//     them is actually load-bearing.
//   - Plan entitlement: mapping a free plan onto an entitlement-missing image
//     operation requires an account-attributes exchange this story does not make
//     (Observe reads /backend-api/models only). Keeping a parser for a body the
//     Adapter never fetches asserted coverage that does not exist.
//
// Both are gaps to close with the exchange that needs them, not with a helper
// that has no caller. testdata/entitlement_free.json is retained as the recorded
// shape for that future exchange; validation.md states plainly that the
// entitlement family is fixture shape rather than proved behavior.

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
