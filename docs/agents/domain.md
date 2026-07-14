# Domain Docs

How the engineering skills should consume this repo's domain documentation when exploring the codebase.

## Before exploring, read these

- `CONTEXT.md` at the repo root.
- `docs/adr/` — read ADRs that touch the area being explored.

If either location doesn't exist, proceed silently. Don't flag its absence or suggest creating it upfront. The `/domain-modeling` skill creates domain artifacts lazily when terms or decisions are actually resolved.

## File structure

PixelPlus uses a single-context layout:

```text
/
├── CONTEXT.md
├── docs/adr/
└── apps/
    ├── gateway/
    └── photoshop-plugin/
```

Both applications share the domain language in `CONTEXT.md`. System-wide architectural decisions live under `docs/adr/`.

## Use the glossary's vocabulary

When output names a domain concept in an issue title, proposal, hypothesis, or test name, use the term as defined in `CONTEXT.md`. Don't drift to synonyms the glossary explicitly avoids.

If a needed concept isn't in the glossary yet, reconsider whether the term belongs to the project or note the genuine gap for `/domain-modeling`.

## Flag ADR conflicts

If output contradicts an existing ADR, surface the conflict explicitly instead of silently overriding it:

> _Contradicts ADR-0001 (Product monorepo) — but worth reopening because…_
