## Agent skills

### Issue tracker

Issues and PRDs are tracked in GitHub Issues for `monet88/pixelplus`. See `docs/agents/issue-tracker.md`.

### Triage labels

Triage uses the default labels: `needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, and `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

This repository uses a single-context layout with `CONTEXT.md` at the root and ADRs under `docs/adr/`. See `docs/agents/domain.md`.

## Engineering Skill Routing

Invoke a skill only when it fits the task. Its `SKILL.md` is the authoritative workflow and takes precedence over this file.

### User-invoked orchestrators

Type these. The agent must not invent a substitute pipeline.

- **`/ask-matt`**: Route when unsure which skill or flow fits.
- **`/setup-matt-pocock-skills`**: One-time repo setup for issue tracker, triage labels, and domain doc layout.
- **`/grill-with-docs`**: Default align for in-repo work. Sharpens the idea and updates `CONTEXT.md` / ADRs.
- **`/grill-me`**: Align when there is little or no codebase. Stateless; does not write domain docs.
- **`/to-spec`**: Synthesize the current conversation into a spec and publish it. No re-interview.
- **`/to-tickets`**: Split a plan or spec into tracer-bullet tickets with blocking edges.
- **`/implement`**: Build from a ticket or spec. Drives `/tdd` at agreed seams and closes with `/code-review` before commit.
- **`/wayfinder`**: Map a foggy multi-session effort as decision tickets until the path is clear, then merge onto `/to-spec`.
- **`/triage`**: Move *incoming* raw issues/PRs through triage labels. Do not triage tickets produced by `/to-tickets`.
- **`/improve-codebase-architecture`**: Scan for deepening opportunities; run on friction or periodically after agent-heavy shipping.
- **`/handoff`**: Bridge context windows — fork to a fresh session with a handoff file (prototype detours, smart-zone limits).

### Model-invoked disciplines

The agent may reach for these when the task fits; user may also invoke them.

- **`/grilling`**: Interview primitive behind `/grill-me` and `/grill-with-docs`. Prefer the wrappers for code work so domain docs update.
- **`/research`**: Verify docs/APIs/specs against primary sources; save cited findings.
- **`/prototype`**: Throwaway code to answer a design question. Bridge with `/handoff` both ways when it needs a fresh session.
- **`/codebase-design`**: Design deep modules, seams, and testability.
- **`/domain-modeling`**: Sharpen glossary terms and ADRs in `CONTEXT.md`.
- **`/tdd`**: Red → Green vertical slices. Prefer bare `/tdd` for one concrete behaviour without a full spec.
- **`/code-review`**: Parallel Standards + Spec review since a fixed point.
- **`/diagnosing-bugs`**: Reproduce → minimise → instrument → fix → regression test.
- **`/resolving-merge-conflicts`**: Resolve merge/rebase by both sides' intent; do not `--abort` merely to avoid the conflict.

## Choose by clarity and size

### Clear and small

`/tdd` or direct implementation  
→ targeted checks  
→ `/code-review`  
→ commit  

Prefer `/implement` when work already comes from a ticket or spec. Prefer bare `/tdd` for a single concrete behaviour (do not invent a spec for a tiny fix).

### Unclear but small (inside existing repo)

`/grill-with-docs`  
→ optional `/research` or `/prototype` (bridge with `/handoff` both ways if a fresh session is needed)  
→ multi-session build?  
  **NO** → `/implement` in the same context  
  **YES** → `/to-spec` → `/to-tickets` → `/implement` one unblocked ticket per fresh session  

### Unclear, little/no codebase, multi-session

`/grill-me`  
→ multi-session build?  
  **NO** → `/implement` in the same context  
  **YES** → `/to-spec` → `/to-tickets` → `/implement` one unblocked ticket per fresh session  

If the fog spans many open decisions beyond one grill → `/wayfinder` instead.

### Clear but large

`/to-spec`  
→ `/to-tickets`  
→ `/implement` one unblocked ticket per fresh session  

### Unclear and large

`/wayfinder`  
→ resolve decision tickets  
→ `/to-spec`  
→ `/to-tickets`  
→ `/implement` one unblocked ticket per fresh session  

## Special situations

- Unknown, flaky, or performance bug: `/diagnosing-bugs`
- Known bug with a clear regression test: `/tdd`
- UI or state-model uncertainty: `/prototype` (handoff out/back if fresh session)
- External documentation or API uncertainty: `/research`
- Repeated architectural friction: `/improve-codebase-architecture`
- Incoming raw issues or external PRs: `/triage` (never triage `/to-tickets` output)
- Merge or rebase conflicts: `/resolving-merge-conflicts`
- Moving work to a fresh session or agent: `/handoff`
- Unsure which workflow fits: `/ask-matt`

## General rules

- Skip any workflow step that does not reduce meaningful uncertainty or implementation risk.
- `/to-spec` / `/to-tickets` only when the build spans sessions (or after `/wayfinder` clears) — not after every grill.
- Keep grill → to-spec → to-tickets in one unbroken context; never compact mid-phase. Start each `/implement` fresh from the ticket. If the window approaches the smart zone (~120k), `/handoff` and continue. Prefer `/handoff` over mid-phase `/compact`; compact only at intentional phase breaks.
- Prefer `/grill-with-docs` over bare `/grilling` when a codebase and `CONTEXT.md` exist.
- Do not `/triage` tickets produced by `/to-tickets`; triage only incoming raw issues/PRs.
- Prefer `/implement` when work comes from a ticket or spec; prefer bare `/tdd` for one concrete behaviour.
- Record a branch base or commit fixed point before implementation.
- Prefer existing test seams over creating new ones.
- Treat tickets as vertical, independently verifiable slices.
- Work one implementation ticket per fresh context; pick unblocked (blockers done) tickets, not foggy wayfinder frontier tickets, once on the main ship flow.
- Keep discovery, decisions, planning, implementation, and review as separate phases.
- Do not let prototypes, debugging instrumentation, or speculative abstractions enter production code.
- Do not AFK-implement issues labelled `needs-triage`, `needs-info`, or `ready-for-human`.
