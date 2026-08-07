# 0015 Repository License and Governance Policy

Date: 2026-08-07

## Status

Accepted

## Context

PixelPlus is a public GitHub repository hosting a multi-tenant SaaS Provider
Gateway and its Client API keys. Issue #129 surfaced that the repository had no
governance files: no license or rights notice, no private security reporting
route, no contributor/verify guidance, no ownership map, and no dependency-update
automation. The repository enforcement audit (section 12.14) recorded this as
ENF-07.

The license question was deliberately left open: the ticket states it is a
commercial-strategy decision, not a task, and the repository must not imply it
is open source before that strategy is chosen. Picking a permissive or copyleft
license now would pre-empt the strategy and is hard to unwind.

## Decision

1. **License**: PixelPlus is proprietary. A `LICENSE` file records an explicit
   "All rights reserved" notice and states that no open-source license is
   granted. No MIT/Apache/BSD/GPL or other license text is added, and nothing
   in the repository implies PixelPlus is open source. A future commercial
   decision may replace this notice.
2. **Security**: `SECURITY.md` publishes a private reporting route (GitHub
   private advisory, email fallback) for credential leaks and cross-Tenant
   issues, a 72-hour acknowledgement / 5-business-day triage expectation, and a
   safe-harbor statement.
3. **Contributing**: `CONTRIBUTING.md` points at the canonical verify
   entrypoint `scripts/verify-repository.mjs` rather than restating a command
   list, and documents the source hierarchy.
4. **Ownership**: `.github/CODEOWNERS` assigns separate, review-required
   ownership to contracts, security and vault code, Provider adapters, and the
   release workflow.
5. **Pull requests**: `.github/pull_request_template.md` requires the linked
   issue, the normative authority, the proof, and risk flags.
6. **Dependency updates**: Dependabot opens reviewed pull requests and
   auto-merges nothing. Security-sensitive updates (contracts, Provider
   adapters, vault code, Docker base-image/digest, GitHub Actions pins) are
   explicitly excluded from bot merge by `.github/workflows/dependabot-guard.yml`,
   which fails and disables auto-merge until a maintainer reviews and adds the
   `security-reviewed` label. Every dependabot PR routes to its code owner.

## Alternatives Considered

1. Adopt an open-source license (MIT or Apache-2.0). Rejected: it pre-empts the
   commercial strategy and is hard to revoke once the repository implies
   openness.
2. Use Renovate instead of Dependabot. Rejected for now: Dependabot is
   zero-config on GitHub and covers npm, Docker, and GitHub Actions in one
   manifest. Renovate remains a fallback if Dependabot's coverage or
   scheduling becomes insufficient.
3. Rely on CODEOWNERS alone to protect the sensitive surfaces. Rejected: a
   digest or Actions-pin bump can read as routine, so an explicit bot-merge
   blocker is required in addition to human review.

## Consequences

Positive:

- Credential leaks and cross-Tenant issues have a stated private route and a
  response expectation.
- The repository states unambiguously that no open-source license is granted.
- Contributors and CI share one definition of "verified" through the canonical
  entrypoint.
- The four expensive-merge surfaces each have an accountable owner, and
  security-sensitive dependency bumps cannot be bot-merged.

Tradeoffs:

- `CODEOWNERS` review is only enforced once branch protection requires
  "Require review from Code Owners"; that repository-settings step is tracked
  as a follow-up.
- Dependabot opening only reviewed PRs is slower than auto-merge; that is
  intentional for a public repository.
- All owners resolve to the sole maintainer today; ownership lines must be
  expanded before contributors are added so nothing lands unowned.

## Follow-Up

- Enable "Require review from Code Owners" and required status checks on `main`
  once the release workflow (#70) is ready, so CODEOWNERS and the guard are
  enforced at the branch level.
- Add private vulnerability reporting enablement and confirm the email
  fallback route in `SECURITY.md`.
- Revisit the license notice when the commercial strategy is decided.
