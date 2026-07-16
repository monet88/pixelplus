# DOC-001 Backfill Canonical Product Documentation

## Status

implemented

## Lane

normal

## Product Contract

The repository must retain the canonical product glossary and specification
documents that define the Provider Gateway domains, while the Harness documents
must remain available as the agent workflow layer. The backfill restores the
deleted canonical documents from the verified local backup and does not change
their normative content or product code.

## Relevant Product Docs

- `CONTEXT.md`
- `docs/adr/0001-product-monorepo.md`
- `docs/agents/`
- `docs/spec/`
- `docs/spec/research/`
- `apps/gateway/README.md`
- `apps/photoshop-plugin/README.md`
- `contracts/README.md`

## Acceptance Criteria

- All canonical files deleted from `docs/` are restored from
  `.harness-backup/20260716162759/`.
- Restored files are byte-for-byte identical to the current `HEAD` versions.
- `CONTEXT.md` links resolve to the restored specification files.
- The documentation map explains the coexistence of Harness docs and the
  existing `docs/spec/` product contract.
- No application source or public contract implementation is changed.

## Design Notes

- Source snapshot: `.harness-backup/20260716162759/`.
- Keep the existing `docs/spec/` layout because `CONTEXT.md` and the current
  branch already use it as the canonical product specification location.
- Do not migrate specs into `docs/product/` in this task.

## Validation

| Layer | Expected proof |
| --- | --- |
| Unit | Not applicable; docs-only change. |
| Integration | Not applicable; docs-only change. |
| E2E | Not applicable; docs-only change. |
| Platform | Not applicable; docs-only change. |
| Release | Not applicable; docs-only change. |
| Repository | Backup hashes match `HEAD`; links and whitespace checks pass. |

## Harness Delta

Clarify the documentation map so future agents can distinguish the generic
Harness workflow docs from the existing Provider Gateway product specs.

## Evidence

- `scripts/bootstrap-harness.ps1` — Harness initialized successfully.
- `harness-cli intake` — Intake #1 recorded as a normal maintenance request.
- `harness-cli story verify DOC-001` — `git diff --check` passed.
- Backup-to-worktree raw-byte hash check passed for all 19 restored canonical
  files; each also matched the corresponding `HEAD` blob hash before restore.
- `CONTEXT.md` contains 20 `docs/spec/*.md` references and every reference
  resolves to an existing file.
- `apps/`, `contracts/`, and public implementation paths have no diff.
- Fresh-context review completed on plan/spec fidelity and standards/correctness;
  the reported raw-byte mismatch was not reproducible with direct
  `git hash-object` checks.
