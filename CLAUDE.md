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
- **`/triage`**: Move _incoming_ raw issues/PRs through triage labels. Do not triage tickets produced by `/to-tickets`.
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

### Default Router

- **Clear and small:** Implement directly, or use `/tdd` when behavior can be tested through a clear seam.
- **Unclear and small:** Use `/grill-me`, or `/grill-with-docs` for an existing codebase. Add `/research` or `/prototype` when external facts or design assumptions must be validated, then use `/to-spec` → `/implement`.
- **Clear and large:** Use `/to-spec` → `/to-tickets` → `/implement`, handling one unblocked ticket per fresh session.
- **Unclear and large:** Use `/wayfinder` to resolve decision tickets, then `/to-spec` → `/to-tickets` → `/implement`.

### Special Cases

- **Known bug with a clear reproduction:** `/tdd` → regression test → smallest safe fix.
- **Unknown, flaky, or performance bug:** `/diagnosing-bugs`.
- **UI or state-model uncertainty:** `/prototype`.
- **External API, library, or documentation uncertainty:** `/research`.
- **Repeated architectural friction:** `/improve-codebase-architecture`, then spec and implement the selected improvement.
- **Incoming issues or external PRs:** `/triage`.
- **Reviewing completed work:** `/code-review` against a fixed point and originating spec.
- **Merge or rebase conflicts:** `/resolving-merge-conflicts`.
- **Moving work to another session or agent:** `/handoff`.
- **Unsure which workflow applies:** `/ask-matt`.

### Rules

- Skip steps that do not reduce meaningful uncertainty or risk.
- Record the base branch or commit before implementation.
- Prefer existing test seams over creating new ones.
- Treat tickets as vertical, independently verifiable slices.
- `/wayfinder` resolves decisions; `/to-tickets` creates implementation work.
- Keep prototypes and temporary debugging instrumentation out of production.
- Record unrelated discoveries as separate issues instead of expanding scope.
- `/implement` should use TDD at agreed seams, run relevant checks, and finish with `/code-review`.
