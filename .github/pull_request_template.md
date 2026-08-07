---
name: Pull request
about: Propose a change to PixelPlus
title: "[feat|fix|docs|chore|security] Short imperative summary"
labels: []
assignees: []
---

<!--
Every pull request must satisfy the four requirements below. Do not delete the
sections; fill them in. A PR that does not link an issue and state its
authority will not be reviewed.
-->

## Linked issue

<!-- REQUIRED: the GitHub issue number this PR resolves or is part of. -->
Closes #<issue-number>

## Normative authority

<!-- REQUIRED: the authoritative document or contract the change implements
     or amends. PixelPlus changes are validated against authority, not against
     a mental model. Cite the canonical path, for example:
       - docs/spec/provider-gateway-implementation-ready-specification.md
       - contracts/openapi/pixelplus-public-api-v1.yaml
       - docs/decisions/NNNN-*.md
     If no authority exists for this change yet, say so and explain why the
     gap is acceptable. -->
Authority: <path or "none — explain why">

## Proof

<!-- REQUIRED: the evidence that this change is verified. CI runs the canonical
     gate `scripts/verify-repository.mjs`; do not paste a re-stated command
     list. State what ran and the real result, for example:
       - `node scripts/verify-repository.mjs --fast` — PASS (n checks)
       - `go test -race ./...` — PASS
       - mutation/contract suite — PASS
       - Docker sandbox smoke — run / skipped (reason)
     If a check was skipped or failed, say so plainly. -->
Proof:

## Risk flags

<!-- REQUIRED: mark each flag that applies to this change. -->
- [ ] Auth / Authorization / Tenancy
- [ ] Data model or migration
- [ ] Audit / security
- [ ] Public contracts (`contracts/`)
- [ ] External systems (Provider adapters, OAuth, webhooks)
- [ ] Security-sensitive dependency update (contracts, adapters, Docker
      digest, Actions pin)
- [ ] Cross-platform
- [ ] Existing behavior change
- [ ] Weak proof (unclear or missing tests)

## Scope checklist

- [ ] Ownership in `.github/CODEOWNERS` covers every touched path.
- [ ] This PR is not a bot merge (see `.github/dependabot.yml`).

## Description

<!-- Short summary of what changed and why. Keep the diff reviewable. -->
