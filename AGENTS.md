# Agent Instructions

<!-- HARNESS:BEGIN -->
## Harness

Choose the request class before any Harness operation.

- When the requested outcome is only an answer, explanation, review, diagnosis,
  plan, or status report: inspect only the material needed to respond. Keep the
  task read-only. Do not bootstrap, initialize or migrate a database, record
  intake, or record a trace.
- When the user explicitly asks to change, build, fix, or write repository
  artifacts: first run `scripts/bootstrap-harness.sh`
  on macOS/Linux or `.\scripts\bootstrap-harness.ps1` on Windows. Then use
  `docs/FEATURE_INTAKE.md` to classify and record the request, query
  `scripts/bin/harness-cli query matrix --active --summary` on macOS/Linux or
  `.\scripts\bin\harness-cli.exe query matrix --active --summary` on Windows,
  and retrieve only the lane- and task-specific context described in
  `docs/CONTEXT_RULES.md`.
<!-- HARNESS:END -->

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
- Use `/to-spec` and `/to-tickets` for work that spans sessions (or after `/wayfinder` clears decisions), not after every grill.
- Prefer `/grill-with-docs` over bare `/grilling` when the repository has `CONTEXT.md`.
- Keep discovery, decisions, planning, implementation, and review as distinct phases; do not compact or break context mid-phase. Use `/handoff` when a fresh context is needed.
- Record the base branch or commit before implementation.
- Work one unblocked implementation ticket per fresh context; do not implement a foggy `/wayfinder` frontier ticket.
- Do not AFK-implement issues labeled `needs-triage`, `needs-info`, or `ready-for-human`.
- Do not run `/triage` on tickets produced by `/to-tickets`; triage only incoming raw issues/PRs.
- Prefer existing test seams over creating new ones.
- Treat tickets as vertical, independently verifiable slices.
- `/wayfinder` resolves decisions; `/to-tickets` creates implementation work.
- Keep prototypes and temporary debugging instrumentation out of production.
- Record unrelated discoveries as separate issues instead of expanding scope.
- `/implement` should use TDD at agreed seams, run relevant checks, and finish with `/code-review`.
<!-- CODEGRAPH_START -->
## CodeGraph

In repositories indexed by CodeGraph (a `.codegraph/` directory exists at the repo root), reach for it BEFORE grep/find or reading files when you need to understand or locate code:

- **MCP tool** (when available): `codegraph_explore` answers most code questions in one call — the relevant symbols' verbatim source plus the call paths between them, including dynamic-dispatch hops grep can't follow. Name a file or symbol in the query to read its current line-numbered source. If it's listed but deferred, load it by name via tool search.
- **Shell** (always works): `codegraph explore "<symbol names or question>"` prints the same output.

If there is no `.codegraph/` directory, skip CodeGraph entirely — indexing is the user's decision.
<!-- CODEGRAPH_END -->

<!-- gitnexus:start -->
# GitNexus — Code Intelligence

This project is indexed by GitNexus as **pixelplus** (4061 symbols, 8754 relationships, 218 execution flows). Use the GitNexus MCP tools to understand code, assess impact, and navigate safely.

> Index stale? Run `node .gitnexus/run.cjs analyze` from the project root — it auto-selects an available runner. No `.gitnexus/run.cjs` yet? `npx gitnexus analyze` (npm 11 crash → `npm i -g gitnexus`; #1939).

## Always Do

- **MUST run impact analysis before editing any symbol.** Before modifying a function, class, or method, run `impact({target: "symbolName", direction: "upstream"})` and report the blast radius (direct callers, affected processes, risk level) to the user.
- **MUST run `detect_changes()` before committing** to verify your changes only affect expected symbols and execution flows. For regression review, compare against the default branch: `detect_changes({scope: "compare", base_ref: "main"})`.
- **MUST warn the user** if impact analysis returns HIGH or CRITICAL risk before proceeding with edits.
- When exploring unfamiliar code, use `query({search_query: "concept"})` to find execution flows instead of grepping. It returns process-grouped results ranked by relevance.
- When you need full context on a specific symbol — callers, callees, which execution flows it participates in — use `context({name: "symbolName"})`.
- For security review, `explain({target: "fileOrSymbol"})` lists taint findings (source→sink flows; needs `analyze --pdg`).

## Never Do

- NEVER edit a function, class, or method without first running `impact` on it.
- NEVER ignore HIGH or CRITICAL risk warnings from impact analysis.
- NEVER rename symbols with find-and-replace — use `rename` which understands the call graph.
- NEVER commit changes without running `detect_changes()` to check affected scope.

## Resources

| Resource | Use for |
|----------|---------|
| `gitnexus://repo/pixelplus/context` | Codebase overview, check index freshness |
| `gitnexus://repo/pixelplus/clusters` | All functional areas |
| `gitnexus://repo/pixelplus/processes` | All execution flows |
| `gitnexus://repo/pixelplus/process/{name}` | Step-by-step execution trace |

## CLI

| Task | Read this skill file |
|------|---------------------|
| Understand architecture / "How does X work?" | `.claude/skills/gitnexus/gitnexus-exploring/SKILL.md` |
| Blast radius / "What breaks if I change X?" | `.claude/skills/gitnexus/gitnexus-impact-analysis/SKILL.md` |
| Trace bugs / "Why is X failing?" | `.claude/skills/gitnexus/gitnexus-debugging/SKILL.md` |
| Rename / extract / split / refactor | `.claude/skills/gitnexus/gitnexus-refactoring/SKILL.md` |
| Tools, resources, schema reference | `.claude/skills/gitnexus/gitnexus-guide/SKILL.md` |
| Index, status, clean, wiki CLI commands | `.claude/skills/gitnexus/gitnexus-cli/SKILL.md` |

<!-- gitnexus:end -->
