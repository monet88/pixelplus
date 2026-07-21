# OPS-001 Gateway Runtime Ticket Execution Playbook

## Status

implemented

## Lane

normal

## Product Contract

`TODOS.md` must describe the fastest dependency-safe way to execute Gateway
runtime issues #44-#67 plus support issues #68-#70 while preserving independent
review, public or operational seam proof, Harness evidence, supply-chain proof, and
merge-before-close discipline.

## Relevant Product Docs

- `TODOS.md`
- `docs/spec/provider-gateway-implementation-ready-specification.md`
- `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`
- `docs/spec/api-versioning-compatibility-idempotency-contract-testing-policy.md`

## Acceptance Criteria

- The completed specification-era plan is replaced by the #44-#67 runtime
  dependency graph.
- The playbook identifies the critical path and all useful parallel frontiers.
- Every ticket uses a bounded context, public HTTP or exported worker proof,
  independent review, fresh verification, Harness trace, and merge-before-close
  gate.
- The playbook distinguishes fast feedback, final review proof, pre-merge
  verification, and release-quality conformance.
- Upstream revision drift starts alongside #44, reports changes without running
  or synchronizing upstream code, and gates Adapter/conformance work through
  human-reviewed evidence; inputs are strictly validated and the scheduled
  workflow uses pinned Actions with least-privilege permissions.
- The Docker sandbox starts after #44, uses the production composition root,
  remains disposable and localhost-only, and never becomes the direct-Go inner
  loop or a production topology decision; it has no host network/privilege,
  drops capabilities, and accepts no credential side channel.
- Both early support tickets are complete before #61-#66; Provider Credentials
  enter live probes only through Public API/Vault, and Grok Web SSO remains
  prohibited.
- After #67 and #68, #70 establishes changelog fragments, protected SemVer
  GitHub Releases, and scanned/attested multi-arch GHCR images before #42 closes;
  stable publication remains maintainer-approved and deployment stays deferred.
- Deferred deployment, launch, and migration work remains closed until #67.

## Design Notes

- Use a default capacity model of three builders plus one independent reviewer.
- Keep one branch/worktree and one PR per ticket.
- Merge in dependency order and rebase parallel work after shared changes land.
- Treat GitHub native dependencies as authoritative over the static schedule.
- Start #69 with #44; start #68 after #44 while #45 continues the runtime path.
- Start #70 only after #67 and #68; it blocks closing #42 but does not authorize
  a stable production release or production topology.
- Use `.ref/` only as local research input. Automation reports upstream drift
  but never copies, vendors, merges, or executes upstream code.
- Reuse CLIProxyAPI's useful tag-driven/multi-arch principles only. PixelPlus
  uses change fragments, protected SemVer tags, GHCR-first publication, pinned
  Actions, least-privilege jobs, SBOM/provenance, and image scanning.
- Use `Refs #N`, then close manually only after post-merge proof and Harness
  completion evidence are recorded.

## Validation

| Layer | Expected proof |
| --- | --- |
| Unit | A deterministic content check requires #44-#70, Docker sandbox rules, upstream drift/review rules, and changelog/release supply-chain gates. |
| Integration | GitHub issue graph #44-#70 is compared against the documented support gates, runtime schedule, and #67/#68 -> #70 -> #42 closure chain. |
| E2E | Not applicable; this task changes operating documentation only. |
| Platform | `git diff --check` and Markdown structure checks pass on Windows. |
| Release | Independent documentation review finds no missing blocker, review, verification, or closure gate. |

## Harness Delta

The obsolete specification issue plan is replaced by an implementation
execution playbook that future agents can follow without reconstructing the
critical path, Docker live-probe boundary, upstream drift loop, changelog/release
foundation, or completion gates.

## Evidence

- `scripts/bin/harness-cli.exe story verify OPS-001` passed with #44-#70,
  Docker, upstream drift, changelog, GHCR, SBOM/provenance, review/verify, and
  `git diff --check` assertions.
- A live GraphQL comparison passed for the exact native `blockedBy` set of every
  #44-#70 issue, their #42 parent links, the complete sub-issue set, and the
  #67/#68 -> #70 -> #42 closure chain.
- A repository audit found zero tracked release files, tags, GitHub Releases, or
  workflows before #70. CLIProxyAPI was used only as reference evidence for
  tag-driven notes/assets and multi-arch images; PixelPlus adds stronger
  fragment, protected-tag, GHCR, pinning, scan, SBOM, and provenance gates.
- A metadata-only `git ls-remote` audit found `chatgpt2api` and
  `gemini-web-to-api` unchanged, while `CLIProxyAPI` and `grok2api` had advanced.
  The reviewed SHAs stayed unchanged and the drift was recorded on #69 for
  human watched-path/license/protocol/risk review.
- A content check found the complete #44-#70 range, all required execution/review/
  verify/close sections, Docker security boundaries, the no-auto-sync upstream
  loop, changelog fragments, protected GHCR supply-chain gates, and no remaining
  specification-frontier headings.
- Final review corrected PR auto-close ordering, scoped Go commands to the
  `apps/gateway` module, kept local Harness completion in the ticket worktree,
  separated pre-merge completion from post-merge smoke/closure, and documented
  the normal/high-risk lane defaults.
