# PR CI foundation and canonical verify entrypoint (ENF-01 #123, ENF-04 #126)

Date: 2026-08-06
Scope: `.github/workflows/ci.yml`, `scripts/verify-repository.mjs`, toolchain pinning,
and the reconciliation of the phantom `docker.yml` workflow.

This record exists because both tickets demand proof rather than assertion. A gate that
was never observed failing is indistinguishable from a gate that inspects nothing, so the
negative controls below are the substance of the work, not a formality.

## 1. Baseline correction

Both issues were written against a snapshot that has since moved. Measured on the local
`main` at `fac6533` before any change here:

| Claim in the ticket | Measured reality |
| --- | --- |
| `gofmt -l apps/gateway` → 10+ files unformatted | 1 file: `internal/contracttest/chat_settlement_detached_test.go` (a single trailing blank line) |
| `validate-provider-gateway-implementation-spec.mjs` → FAIL fingerprint mismatch | PASS — already fixed by `fac6533` (ENF-02, #124) |
| GitHub API reports a `Docker` workflow not in the tree | Confirmed |

The argument in #123 still holds on the corrected numbers: one unformatted tracked file is
still a file no gate was catching. The claims are recorded as corrected rather than
restated, because carrying a stale "10+ files" figure forward would make later readers
distrust the rest of the record.

## 2. The `gofmt` trap, and why it is a separate commit

`gofmt -l` exits **0** while listing unformatted files. A naive `run: gofmt -l apps/gateway`
step therefore passes forever. Both the workflow and the verify entrypoint treat the
*listing* as the failure signal.

The one pre-existing violation was fixed in its own commit (`3688e24`) ahead of the
workflow, so the workflow's first run is a real signal rather than a red build inherited
from earlier drift, per the #123 acceptance criteria.

## 3. Phantom `docker.yml` reconciliation

The GitHub API reported an active workflow at `.github/workflows/docker.yml`. Findings:

- `.github/` has **never** existed in the tracked history (`git log --all -- .github` is empty).
- The workflow had exactly 2 runs, both `pull_request` events on
  `feature/issue-51-scoped-health-controls` (2026-07-23), head `2c41f41`.
- That branch was deleted and never merged; the commit is not reachable from `main`.

So the entry was residual registration from a deleted PR branch, not a tracked workflow —
it never protected anything on `main`. **Resolution: removed.** Both runs were deleted via
the API, which retired the stale workflow entry; `actions/workflows` now reports
`total_count: 0`, and the only workflow that will register from here is the tracked
`ci.yml`.

## 4. One implementation, sliced by CI job

#126 requires a single verification implementation. A workflow that lists `gofmt`, `go vet`
and each validator itself would satisfy the letter of #123 while making that claim false:
the PR gate and a local run would be two definitions of "verified", free to drift apart
silently.

So the workflow does not restate anything. Every step in the entrypoint declares which CI
job owns it, and each job in `ci.yml` runs exactly one command:

```yaml
- name: verify
  run: node scripts/verify-repository.mjs --fast --job=repository-hygiene
```

The `--job` filter narrows a mode's plan without changing which mode a step belongs to, so
the four PR-gate jobs partition `--fast` exactly — 2 + 2 + 1 + 1 = the 6 checks a developer
gets from a bare `--fast`. Three consequences are enforced by
`scripts/test-verify-repository.mjs` rather than by convention:

- Every step must name a declared job. A step owned by none would run locally and silently
  never run in CI.
- The slices must cover each mode exactly, with nothing counted twice.
- `ci.yml` must invoke the entrypoint per job and must not contain an inlined `gofmt -l`,
  `go -C apps/gateway test`, `node scripts/validate-…` or `python scripts/…` command.

A `--job` that matches no step exits 2 rather than 0, because a workflow typo that produced
an empty green required check is the precise failure this entrypoint exists to prevent.

Preflight is narrowed the same way: `requiredToolsFor(plan)` resolves only the tools a slice
actually invokes, so the Go job does not demand a Python interpreter and CI is not pushed to
install one just to satisfy a check. Indirect dependencies are declared explicitly —
`validate-public-api-contract.mjs` shells out to `python jsonschema` even though its own
command is a Node one, so its steps carry `needs: ["python", "python jsonschema"]`.

## 5. Negative controls

Each gate was proved to observe real failure. Every mutation was reverted and confirmed
byte-identical to `HEAD` afterwards; none was committed.

### 5.1 Formatting mutation

Appended `func  badlyFormatted( ) {}` to `apps/gateway/internal/ports/capability.go` and ran the
hygiene slice (`--fast --job=repository-hygiene`):

```text
FAIL     gofmt                                     66ms  exit=0
           artifact: apps/gateway
           remedy:   run `gofmt -w apps/gateway` and commit the result
FAIL: gofmt failed after 66ms (1 later check(s) never ran)
```

Note `exit=0` in that line: the underlying command *succeeded*, and the gate failed anyway.
That is precisely the trap being defended against.

### 5.2 Breaking OpenAPI mutation

Removed the `/assets` path from `contracts/openapi/pixelplus-public-api-v1.yaml`:

```text
FAIL: stable Public API contract has 4 violation(s)
- POST /assets must document 403 insufficient assets.write via ErrorForbidden
- POST /assets must document 413 request_too_large for uploads over L-ASSET-UPLOAD-MAX
- baseline compatibility: POST /assets cannot be removed
- missing required operation POST /assets
```

The first attempt at this mutation silently matched nothing (the artifact is JSON despite
its `.yaml` extension) and the validator passed. That pass was **vacuous** — it proved the
regex was wrong, not that the artifact was sound. It is recorded here because a vacuous
green is the exact failure mode this report is meant to catch.

### 5.3 Drift mutation

The claim that this entrypoint is the *only* implementation is only worth as much as the
test that observes it breaking. `ci.yml`'s hygiene job was mutated to call the underlying
command directly (`run: gofmt -l apps/gateway`) instead of the entrypoint:

```text
ci.yml must run the verify entrypoint for repository-hygiene rather than restating its commands
EXIT=1
```

Restored from backup and re-run: PASS. Without this control the anti-drift assertion in
`scripts/test-verify-repository.mjs` would be indistinguishable from an assertion that
inspects nothing.

## 6. Two real defects found by building the entrypoint

Both were found by running the orchestrator, not by review:

1. **`ENOBUFS` false failure.** The management contract validator emits more than
   `spawnSync`'s 1 MB default buffer, so the harness reported a passing gate as failed.
   Steps whose output is not itself the signal now stream through with `stdio: "inherit"`.
   A harness that manufactures failures is worse than no harness.
2. **Docker probed via the wrong command.** `docker --version` reports the CLI and succeeds
   with the daemon down, so it cannot decide whether a Docker gate can run. The probe is
   now `docker info`, which requires a reachable daemon.

## 7. Scope deviation: the architecture check

#126 lists "architecture and dependency-direction checks" under `--full`. No such rule is
documented anywhere in this repository — there is no ADR, and `CONTEXT.md` states no import
matrix. Implementing a full layer-by-layer gate would have meant inventing architectural
rules the repository never agreed to, and failing builds on that authority.

`scripts/check-dependency-direction.mjs` therefore encodes only the invariant that is both
uncontroversial for a ports-and-adapters design and **empirically true of the tracked tree
today**, verified before it was written:

```text
internal/domain  imports no other internal package
internal/ports   imports at most internal/domain
```

The outer layers are deliberately not gated. Widening this should follow a documented
decision, not a script.

## 8. Toolchain pinning

| File | Pins |
| --- | --- |
| `.tool-versions`, `mise.toml` | go 1.25.5, node 24.14.0, python 3.13.12 |
| `package.json` | `packageManager: npm@11.17.0`, `engines` |
| `requirements-validation.txt` | `jsonschema==4.26.0` |

`@ai-hero/sandcastle` was removed from `package.json` and the lockfile; no tracked script
or source file referenced it.

A missing tool is a **failure with a stated remedy**, never a skip. Docker-dependent checks
are the only permitted conditional and announce themselves:

```text
SKIPPED  docker sandbox smoke                       0ms  exit=-
           the Docker daemon is unavailable, so the sandbox smoke did NOT run.
           This mode's result is weaker than a full pass.

NOTE: 1 check(s) were skipped. This run is not a full pass.
```

## 9. Verification

```text
node scripts/test-verify-repository.mjs     PASS
node scripts/verify-repository.mjs --fast   PASS (6 checks)
node scripts/verify-repository.mjs --full   PASS (15 checks, docker smoke skipped)
```

Each PR-gate slice was also run on its own, and the counts add up to the whole gate rather
than to some subset of it:

```text
--fast --job=repository-hygiene          PASS (2 checks)
--fast --job=gateway-unit-and-contract   PASS (2 checks)
--fast --job=public-api-contract         PASS (1 check)
--fast --job=authority-consistency       PASS (1 check)
                                         ----------------
                                         6 = the --fast gate
```

`--release` was not run to completion locally: it requires a reachable Docker daemon for
the container build, and `govulncheck` downloads a module at run time. Its steps are
declared but are **unproven locally** and should be treated as such until a release run
exercises them in CI.

## 10. Outstanding — branch protection

The branch-protection criteria in #123 (required checks on the latest head, dismiss stale
approvals, no force-push or deletion, conversation resolution, audited admin bypass) are
repository *settings*, not tracked files. They cannot be committed and are not yet applied.
Required checks should be registered only after `ci.yml` has run once on a pull request, so
the check names exist to select. This remains open.
