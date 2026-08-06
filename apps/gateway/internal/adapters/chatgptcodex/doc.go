// Package chatgptcodex translates the ChatGPT Codex OAuth protocol and nothing
// else.
//
// Auth Mode status is `gated` (auth-mode-risk-envelope-and-kill-criteria.md
// §5.2): an official Codex OAuth/CLI surface with residual credential-share,
// resale/lease, and plan-contract tension. Composition registers this Adapter
// only when an operator deliberately enables the mode in the deployment's gated
// profile (decision 0014) AND the Tenant has acknowledged the residual risk
// (§6.2). It is never ordinary self-serve and never lab-only: it is the gated
// twin of the experimental chatgptweb Adapter.
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
// This Adapter never solves a challenge. The Codex executor path carries no
// sentinel, proof-of-work, Turnstile, or Arkose (capability evidence §6: all
// `unsupported` on the Codex path), so a challenge-class signal here is a
// Cloudflare/bot block on an image path rather than an anti-bot interstitial.
// It is classified as challenged and returned regardless; OP-G6 refuses
// challenge solving as a product capability.
//
// Credential material — including the OAuth refresh_token — is used only inside
// a ports.CredentialInjection callback and never reaches a struct field, a log,
// an error string, or a returned outcome (OP-G3). OAuth refresh is performed
// inside that same callback when a 401 surfaces, so rotated token material
// stays inside the Adapter/Vault boundary (§5.2 security impact, #62 AC2).
//
// # Evidence
//
// Wire shapes are reference-learned from `.ref/CLIProxyAPI`
// (internal/runtime/executor/codex_executor.go, codex_openai_images.go) and
// `.ref/chatgpt2api`, recorded in
// docs/spec/research/chatgpt-auth-mode-capability-evidence.md §2.2 and §3.2.
// Official Codex auth docs (developers.openai.com/codex/auth) verify the
// product auth model and refresh/caching expectations; they do not verify the
// reverse responses protocol details. A reference repository is a research
// seam, not production permission (risk envelope §3.1 rule 4). No behavior here
// has been verified against a live account: §10.1 gap 1 is explicit that no
// live probe was run, and running one requires authorization for the exact
// account and the per-account credential binding that #111 will provide.
package chatgptcodex
