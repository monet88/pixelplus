package chatgptweb

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
	// than surfacing a dependency 503 (ports.ProbeAdapter contract).
	signalAuthFailed
	// signalChallenged is a bot-interstitial / sentinel challenge requirement.
	// It is classified and returned; this Adapter never solves one (OP-G6, KS-5).
	signalChallenged
	// signalRateLimited is a Provider rate-limit/backoff signal (HTTP 429).
	signalRateLimited
	// signalUnavailable is a transient backend failure. It maps to
	// ErrDependencyUnavailable so admission fails closed.
	signalUnavailable
)

// classifyStatus maps an upstream HTTP status onto a signal class.
func classifyStatus(status int) signalClass {
	switch {
	case status == http.StatusUnauthorized:
		return signalAuthFailed
	case status == http.StatusForbidden:
		// A 403 on this surface is the Cloudflare / bot-management shape rather
		// than an authorization decision the Gateway can act on. Treating it as a
		// challenge keeps it out of the auth-failure path, so a challenged session
		// is never mistaken for a revoked credential and never triggers a pointless
		// reauth prompt (evidence §6 challenge signals).
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

// challengeRequired reports whether a sentinel chat-requirements payload demands
// a challenge this Adapter refuses to solve.
//
// The reference implementation calls a Turnstile solver here. This Adapter does
// not and must not: OP-G6 refuses challenge solving as a product capability, and
// KS-5 makes any new anti-bot reverse engineering a kill trigger. Classifying
// the requirement honestly is what lets the FG-5/KS-2 challenge-rate counters
// mean anything.
func challengeRequired(body string) bool {
	var payload struct {
		Arkose struct {
			Required bool `json:"required"`
		} `json:"arkose"`
		ProofOfWork struct {
			Required bool `json:"required"`
		} `json:"proofofwork"`
		Turnstile struct {
			Required bool `json:"required"`
		} `json:"turnstile"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return false
	}
	return payload.Arkose.Required || payload.ProofOfWork.Required || payload.Turnstile.Required
}

// imageQuota is the normalized image_gen entitlement view taken from
// conversation/init limits_progress.
type imageQuota struct {
	// Remaining is the reported image_gen allowance left.
	Remaining int
	// ResetAfterSeconds is the relative reset hint in seconds, zero when the
	// upstream proved no safe hint.
	ResetAfterSeconds int
	// Present reports whether an image_gen row was found at all. A missing row is
	// not a zero allowance: it means nothing was observed, so it must not create
	// a cooldown.
	Present bool
}

// parseImageQuota extracts the image_gen row from a conversation/init payload.
// Evidence: openai_backend_api.py `_extract_quota_and_restore_at`.
func parseImageQuota(body string) imageQuota {
	var payload struct {
		LimitsProgress []struct {
			FeatureName string  `json:"feature_name"`
			Remaining   float64 `json:"remaining"`
			ResetAfter  float64 `json:"reset_after"`
		} `json:"limits_progress"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		return imageQuota{}
	}
	for _, row := range payload.LimitsProgress {
		if row.FeatureName != "image_gen" {
			continue
		}
		return imageQuota{
			Remaining:         int(row.Remaining),
			ResetAfterSeconds: int(row.ResetAfter),
			Present:           true,
		}
	}
	return imageQuota{}
}

// modelSlugs extracts the session-dependent model slugs from a /backend-api/models
// payload. The list is account and session dependent, so a Capability Snapshot
// must store what was observed rather than a static provider catalog
// (evidence §2.1 "model listing / discovery").
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
