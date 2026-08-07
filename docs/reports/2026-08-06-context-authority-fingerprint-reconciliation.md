# CONTEXT.md authority fingerprint reconciliation (ENF-02, #124)

Date: 2026-08-06
Scope: `scripts/validate-provider-gateway-implementation-spec.mjs` failing on a clean
checkout with `FAIL: authority content does not match validator-owned fingerprint: CONTEXT.md`.

This record exists because the failure is authority-sensitive. A refresh alone would have
turned the gate green while proving nothing, so the diff was classified *before* the
refresh script was run. This document is the written classification the ticket requires.

## 1. The fingerprinted revision

The stored fingerprint was `cad8f6513c4fcc985c70313fc5135848ee9faafac9895bb9bbfba7db36f4bb30`.

It was resolved to a concrete revision by replaying the validator's own hash
(`normalizedText` then SHA-256) over every historical version of `CONTEXT.md`:

| Field | Value |
|---|---|
| Fingerprinted commit | `1bbb9165e4a76378eb7df99edb97135b10f38ba3` |
| Date / subject | 2026-07-20 — `docs(spec): assemble implementation-ready Provider Gateway specification (#43)` |
| Drifting commit | `4ae1284905d0a50b353ea55aabc8499b317a9831` (2026-08-06) |
| Drifting subject | `docs(context): add domain glossary sections for the gateway and plugin` |
| Diff | `+65 / -0`, one commit, additive only |

Exactly one commit touched `CONTEXT.md` between the fingerprint and `HEAD`, and it makes
no deletions or edits to pre-existing text. Every pre-existing glossary entry is
byte-identical, so no previously accepted authority was rewritten.

## 2. Change classification

Every added block, classified per the ticket's three categories.

| # | Added section | Class | Shifts implementation-ready handoff? |
|---|---|---|---|
| 1 | Lab Profile | **Gateway authority** | No — already decided, see §3 |
| 2 | Capability Baseline | **Gateway authority** | No — already decided, see §3 |
| 3 | Execution Path | Plugin vocabulary | No |
| 4 | Product License | Plugin vocabulary | No |
| 5 | Connection | Plugin vocabulary | No |
| 6 | Target Rect | Plugin vocabulary | No |
| 7 | Context Rect | Plugin vocabulary | No |
| 8 | Mask Convention | Plugin vocabulary | No |
| 9 | Image Engine | Plugin vocabulary | No |

No block is editorial-only; all nine are new domain definitions.

### Gateway authority (1–2)

`Lab Profile` and `Capability Baseline` are the two domain concepts introduced by Gateway
T18 (#61) and are Gateway-authoritative: `Lab Profile` is referenced by 23 Go files under
`apps/gateway`. They therefore need a recorded decision rather than silent absorption.

That decision already exists and predates this reconciliation:
`docs/decisions/0013-experimental-lab-profile-and-capability-baseline.md` (Status:
**Accepted**, 2026-08-05), reviewed and merged as part of PR 94. The glossary text here
restates that accepted decision; it does not introduce a new one.

### Plugin vocabulary (3–9)

These are Plugin-side terms. None appears in
`docs/spec/provider-gateway-implementation-ready-specification.md`, and none appears in
`apps/gateway` Go sources. They are backed by their own accepted, tracked records:

- `docs/adr/0002-two-execution-paths-and-uxp-network-allowlist.md` (Accepted, 2026-08-05)
  — Execution Path, Product License, Connection, Image Engine.
- `docs/adr/0003-canonical-mask-convention.md` (Accepted, 2026-08-05)
  — Target Rect, Context Rect, Mask Convention.

`CONTEXT.md` is a shared glossary spanning both the Gateway and the Plugin. Plugin
vocabulary landing in it changes the fingerprint without changing Gateway authority,
which is precisely why the diff had to be read rather than regenerated.

## 3. Decision

No change in this diff shifts the implementation-ready Gateway handoff:

- The two Gateway-authoritative additions are restatements of an already-Accepted decision
  record (ADR 0013), not new authority.
- The seven Plugin additions are outside the Gateway handoff surface and carry their own
  Accepted ADRs.
- Nothing pre-existing was modified or deleted.

The fingerprint refresh is therefore an accurate re-baseline of reviewed content, and no
new decision record is required by this reconciliation.

## 4. Refresh scope

`refresh-provider-gateway-implementation-spec-contract.mjs` rewrites *all* semantic hashes
and *all* 27 authority fingerprints, so its blast radius was verified rather than assumed.
The contract was snapshotted before the run and diffed after. Exactly one line changed:

```text
-  "CONTEXT.md": "cad8f6513c4fcc985c70313fc5135848ee9faafac9895bb9bbfba7db36f4bb30"
+  "CONTEXT.md": "f1dff83d3049fcdb120d43d41c94315d227aac208f0c9d0cb508274f1965f6eb"
```

The other 26 authority fingerprints and all four semantic hashes were unchanged,
confirming no second authority had drifted unnoticed behind the CONTEXT.md failure.

The script was run once.

## 5. Verification

| Check | Result |
|---|---|
| `validate-provider-gateway-implementation-spec.mjs` after refresh, no second refresh | PASS (27 authority files) |
| `test-provider-gateway-implementation-spec-validator.mjs` | PASS, 0 failures |
| Refresh script referenced by any CI workflow | None — no workflow references it |

## 6. Negative control

To prove the gate detects drift rather than absorbing it, an intentional authority drift
was appended to `CONTEXT.md`:

1. Run 1 → `FAIL: authority content does not match validator-owned fingerprint: CONTEXT.md`
2. Run 2, unchanged → **still FAIL**. The validator does not self-heal; repeated runs
   never regenerate the fingerprint.
3. Drift reverted → `PASS`, and `git diff --stat CONTEXT.md` reports no residual change.

The proof was reverted, as required.

## 7. Residual risk

The acceptance criterion "CI validates and never refreshes" is only half-satisfiable here.
The refresh script is absent from every workflow, but at the time of this reconciliation
(2026-08-06, before #123 landed) the repository had no `.github/workflows` at all — CI did
not yet exist. The guarantee that CI validates and never refreshes had to be enforced when
the PR CI foundation landed under #123; this reconciliation could not close that half by
itself.
