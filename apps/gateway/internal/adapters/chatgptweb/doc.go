// Package chatgptweb translates the ChatGPT Web Access protocol and nothing
// else.
//
// Auth Mode status is `experimental` (auth-mode-risk-envelope-and-kill-criteria.md
// §5.1): lab-only, default off everywhere, never ordinary production
// self-service. Composition registers this Adapter only when an operator
// deliberately enables the mode in the deployment's lab profile.
//
// # Scope
//
// The Adapter owns protocol translation. It does NOT own:
//
//   - Tenant or account selection — routing (P0-P5) owns that, and every command
//     the Adapter receives already names the account the spine chose.
//   - Durable state — the Adapter holds no mutable field across calls, so there
//     is no ledger, cache, or session to lose or leak between requests.
//   - Full-operation retry — a failed exchange is classified and returned; the
//     spine decides whether another account is attempted (P5 fallback), which is
//     the only place that can honor the authoritative-no-commit rule.
//
// # Refusals
//
// This Adapter never solves a challenge. When the upstream sentinel demands
// Arkose, proof-of-work, or Turnstile, the exchange is classified as challenged
// and returned. OP-G6 refuses challenge solving as a product capability, and
// KS-5 makes new anti-bot reverse engineering a kill trigger rather than a
// feature. The honest classification is what feeds the FG-5/KS-2 counters.
//
// Credential material is used only inside a ports.CredentialInjection callback
// and never reaches a struct field, a log, an error string, or a returned
// outcome (OP-G3).
//
// # Evidence
//
// Wire shapes are reference-learned from `.ref/chatgpt2api`
// (docs/upstream-sse-conversation.md and services/openai_backend_api.py) and
// recorded in docs/spec/research/chatgpt-auth-mode-capability-evidence.md §2.1
// and §3.1. A reference repository is a research seam, not production
// permission (risk envelope §3.1 rule 4). No behavior here has been verified
// against a live account: §10.1 gap 1 is explicit that no live probe was run,
// and running one requires authorization for the exact account.
package chatgptweb
