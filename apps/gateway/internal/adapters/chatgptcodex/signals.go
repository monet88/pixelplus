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
			Type           string  `json:"type"`
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

// rateLimitError reports whether a Responses body carries a rate_limit_error /
// rate_limit_exceeded signal, which is a transient per-minute style limit the
// evidence treats as retry-worthy rather than a long cooldown.
func rateLimitError(body string) bool {
	var payload struct {
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return false
	}
	switch payload.Error.Type {
	case "rate_limit_error", "rate_limit_exceeded":
		return true
	default:
		return false
	}
}

// modelAtCapacity reports whether a Responses body carries a "selected model is
// at capacity" message, which the evidence maps toward 429-class handling.
func modelAtCapacity(body string) bool {
	var payload struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return false
	}
	return containsFold(payload.Error.Message, "at capacity")
}

// planEntitlement is the normalized plan/entitlement view taken from a Codex
// account-attributes or id_token-claims projection.
type planEntitlement struct {
	// PlanType is the observed plan class (free, plus, team, pro, business,
	// enterprise). Empty when nothing was observed.
	PlanType string
	// ImageToolInjected reports whether the Codex image_generation tool is
	// exposed for this plan. The evidence restricts Codex image exposure to
	// Plus/Team/Pro; a free plan skips image tool injection
	// (isCodexFreePlanAuth), so image operations are entitlement-missing on a
	// free account rather than unsupported by the surface.
	ImageToolInjected bool
}

// parsePlanEntitlement extracts the plan class from an account-attributes body.
// Evidence: CLIProxyAPI isCodexFreePlanAuth; chatgpt2api plan filters.
func parsePlanEntitlement(body string) planEntitlement {
	var payload struct {
		PlanType string `json:"plan_type"`
		Attributes struct {
			PlanType string `json:"plan_type"`
		} `json:"attributes"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return planEntitlement{}
	}
	plan := payload.PlanType
	if plan == "" {
		plan = payload.Attributes.PlanType
	}
	return planEntitlement{
		PlanType:          plan,
		ImageToolInjected: codexPlanInjectsImageTool(plan),
	}
}

// codexPlanInjectsImageTool reports whether a plan exposes the Codex
// image_generation tool. The evidence gates Codex image to Plus/Team/Pro
// (chatgpt2api model exposure, CLIProxy isCodexFreePlanAuth); a free plan skips
// image tool injection, so image operations are entitlement-missing there.
func codexPlanInjectsImageTool(plan string) bool {
	switch plan {
	case "plus", "team", "pro", "business", "enterprise":
		return true
	default:
		return false
	}
}

// containsFold is a case-insensitive substring check used only for stable,
// non-secret upstream message fragments (never for credential material).
func containsFold(haystack, needle string) bool {
	if len(needle) == 0 {
		return true
	}
	if len(haystack) < len(needle) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if equalFold(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

// equalFold is an ASCII case-insensitive compare sufficient for the fixed
// upstream message fragments this package inspects.
func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
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
