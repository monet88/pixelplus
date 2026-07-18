# Validation — US-017 Health Spec Review Fixes

## Proof Strategy

Prove the documentation contract is internally consistent, all legacy normative references are either normalized or intentionally classified as admission/error/research vocabulary, and no runtime/public wire implementation is introduced.

## Test Plan

| Layer | Cases |
| --- | --- |
| Unit | Not applicable; specification-only change. |
| Integration | Cross-spec vocabulary and mapping searches. |
| E2E | Not applicable; no runtime surface. |
| Platform | Not applicable. |
| Performance | Not applicable. |
| Logs/Audit | Review redaction/audit semantics remain unchanged. |
| Repository | `git diff --check`; targeted searches; independent review. |

## Fixtures

- Canonical Health State/Reason tables.
- Legacy #9 token mapping cases.
- Each prior-state branch for `recovery_probe_failed`.
- Pre-attempt and post-A6 rate/quota/error mapping examples.

## Commands

```text
git diff --check
git diff --stat
git status --short
Targeted Grep checks for legacy tokens and retry_after_seconds
Independent code-reviewer pass over the final diff
```

## Acceptance Evidence

- `git diff --check` passed; Git emitted only existing CRLF→LF normalization warnings for two edited Markdown files.
- Targeted repository searches found no remaining `retry_after_seconds`, ambiguous `prior state or escalated state`, old “#9 vocabulary remains canonical” claim, or legacy-token C5 comparison.
- Internal `docs/spec/*.md` references resolve across all eight changed contract files.
- `harness-cli story verify US-017` passed using `git diff --check`.
- Independent `code-reviewer` found one missing legacy-scope rule; the mapping now makes every scope-less #9 token account-scoped and focused re-review confirmed the finding fixed.
- Runtime unit/integration/E2E/platform tests remain not applicable because the change is specification-only.
