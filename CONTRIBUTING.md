# Contributing to PixelPlus

PixelPlus is a proprietary, closed-source repository (see `LICENSE`). External
contributions are not accepted at this time. This file is primarily for
maintainers and for collaborators who have been granted access.

## Licensing

This repository is **not open source**. `LICENSE` records an explicit
"All rights reserved" notice. Do not fork, copy, redistribute, or incorporate
PixelPlus source into another project. If you need a license to use or modify
PixelPlus, contact the maintainers first.

## Verification

There is one canonical verification entrypoint and it is the only place the
command list is defined:

```text
scripts/verify-repository.mjs
```

Run it with `--help` for the modes (`--fast`, `--full`, `--release`) and the
CI job slices (`--job=`). Do not restate the verify commands in PR
descriptions, in other docs, or in CI. The entrypoint owns the list;
`.github/workflows/ci.yml` invokes it with `--job=` slices so a local run and
the PR gate cannot drift into two definitions of "verified".

Before a local run, pin the toolchain with `mise install` and install the
pinned dependencies (`npm ci`, `python -m pip install -r
requirements-validation.txt`). A missing tool is a failure with a stated
remedy, never a silent skip. `--full` requires a reachable Docker daemon for the
sandbox smoke and reports the skip loudly if it is not available.

## Source hierarchy

PixelPlus is a monorepo. The stable public surface and the gateway are the
normative pieces; the rest is coordination and packaging.

- `contracts/` — the stable OpenAPI contracts and compatibility baselines.
  `contracts/openapi/pixelplus-public-api-v1.yaml` is the stable unified Public
  API; `contracts/openapi/baselines/` holds frozen v1.0.0 compatibility
  baselines. See `contracts/README.md`.
- `apps/gateway/` — the Pure-Go SaaS Provider Gateway.
  - `internal/domain/` — domain entities, value objects, invariants.
  - `internal/application/` — application use cases, commands, and queries.
  - `internal/infrastructure/` — infrastructure: `vault/` (credential vault and
    sensitive data), `persistence/`, `observability/`, `jobs/`.
  - `internal/adapters/` — Provider adapters (ChatGPT Codex, ChatGPT Web).
  - `internal/ports/` — the ports those adapters implement.
  - `internal/transport/http/` — the HTTP seam over the public contract.
  - `internal/composition/` — production composition root and runtime.
  - `internal/contracttest/` — contract-level behavior suites.
  - `cmd/` — the production `gateway` entrypoint.
- `docs/spec/` — the normative specification and lifecycle documents the
  gateway implements, validated by `scripts/validate-provider-gateway-implementation-spec.mjs`.
- `docs/decisions/` — durable architecture and policy decisions.
- `scripts/` — the canonical verify entrypoint, validators, and harness
  automation. See `scripts/README.md`.
- `apps/photoshop-plugin/` — the Adobe Photoshop UXP plugin (built against the
  locked Public API contract).

The dependency direction rule (from `docs/ARCHITECTURE.md`) is enforced by
`scripts/check-dependency-direction.mjs`: inner layers (`domain`, `application`)
must not depend on outer layers (`infrastructure`, `transport`, adapters).

## Pull requests

Every pull request must use the template in
`.github/pull_request_template.md`, which requires the linked issue, the
normative authority the change cites, the proof, and any risk flags.

Ownership and required review live in `.github/CODEOWNERS`. The four surfaces
where a wrong merge is expensive — contracts, security and vault code, Provider
adapters, and the release workflow — each have a designated owner whose review
is required. Dependency bumps that touch those surfaces are explicitly excluded
from automatic merging (see `.github/dependabot.yml`).

## Security

To report a vulnerability, a credential leak, or a cross-Tenant issue, use the
private route described in `SECURITY.md`. Do not open a public issue for
security problems.
