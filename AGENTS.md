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

## Context selection

`AGENTS.md` selects the request class (read-only vs change). Read
`docs/CONTEXT_RULES.md` to choose the bounded retrieval set for that class and
risk lane; do not preload the whole documentation tree. `CONTEXT.md` at the
repo root is the domain glossary and normative spec index.

## Repository layout

```text
/
├── CONTEXT.md                   domain glossary and normative spec index
├── docs/
│   ├── adr/                     repository and product decisions
│   ├── agents/                  issue-tracker, triage-label, domain guidance
│   ├── ARCHITECTURE.md          PixelPlus system architecture
│   ├── decisions/               Harness and product decisions
│   ├── spec/                    normative Provider Gateway specifications
│   ├── stories/                 feature packets and backlog
│   └── harness/                 reusable Harness template documentation
├── apps/
│   ├── gateway/                 Pure-Go Provider Gateway (Go module)
│   └── photoshop-plugin/        Photoshop UXP Plugin
├── contracts/openapi/           Public API OpenAPI artifacts
└── scripts/                     verification, intake, and Harness tooling
```

## Issue and triage

Issues and PRDs are tracked in GitHub Issues for `monet88/pixelplus`; use the
`gh` CLI. Triage uses the default labels. See `docs/agents/issue-tracker.md`,
`docs/agents/triage-labels.md`, and `docs/agents/domain.md` for conventions.

## Code intelligence

This repository is indexed by GitNexus as **pixelplus**. Use the GitNexus MCP
tools to understand code, assess impact, and navigate safely:

- Run impact analysis before editing any symbol and report the blast radius.
- Run `detect_changes()` before committing to verify the change only affects
  expected symbols and execution flows.
- Prefer `query()` over grepping when exploring unfamiliar code, and warn on
  HIGH or CRITICAL impact risk before editing.

Index stale? Run `node .gitnexus/run.cjs analyze` from the project root.

Machine-specific and tool-specific overrides belong in `AGENTS.local.md` and
`CLAUDE.local.md`, which are not tracked.
