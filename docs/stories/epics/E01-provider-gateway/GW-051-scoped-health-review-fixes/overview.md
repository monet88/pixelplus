# Overview

## Current Behavior

PR #81 implements scoped health and cooldown controls, but review found that
health authority remains embedded in AccountStore, durable single-flight is
process-local, dependency failures can occupy recovery forever, audit records
are incomplete, container state is temporary, and malformed restored state can
open readiness or panic later.

## Target Behavior

Health conditions and recovery permits have an independent durable store with
CAS/revision fencing. Two store instances cannot both claim one revision.
Dependency failures renew the condition with bounded backoff, clear the old
owner, and allow a later single recovery attempt. Every persisted health
transition has a safe complete audit record. Restart restores state before
readiness, and invalid state fails closed.

## Affected Users

- Tenant operators managing Provider Accounts.
- Gateway operators investigating Provider health and recovery.
- Platform engineers deploying the Gateway in containers.

## Affected Product Docs

- `docs/spec/provider-account-health-cooldown-and-operator-controls.md`
- `docs/spec/auth-mode-risk-envelope-and-kill-criteria.md`
- `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`

## Non-Goals

- Implement Provider Surface Circuit corroboration.
- Add production database infrastructure.
- Change credential storage, projection, or logging.
- Add a general container release pipeline.
