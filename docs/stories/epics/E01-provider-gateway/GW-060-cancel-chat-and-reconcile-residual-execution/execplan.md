# Exec Plan

## Goal

Deliver issue #60 (Gateway T17): explicit cancel route, same-Tenant idempotent
cancel, and the X5/X6 residual reconciliation split, proven through the public
HTTP seam over real composition.

## Scope

In scope:

- `ports.ChatResidualStore`, `ports.ChatResidualDrain`,
  `ports.ErrChatResidualCapacityFull`, and `ports.ChatResidualHold`.
- `application.chatExecutionRegistry` for in-flight execution tracking.
- `application.CancelChatExecution` and `ChatCancelResult`.
- `application.settleStream` X5/X6 split in `chat_stream.go`.
- Transport: `POST /v1/chat/executions/{execution_id}/cancel` route and
  `chatCancelResponseWire`.
- Composition wiring with nil-safe fail-closed defaults.
- `domain.RemediationExecutionRecovery` and the `upstream_timeout`/
  `upstream_unavailable` remediation fix.
- Contract tests for §10.2 items 7, 8, 11, 12, 13 (non-cancelable settlement,
  retained occupancy rejects a new A6, missing final usage settles
  conservatively, cancel idempotent, Tenant A cancel leaves Tenant B unchanged)
  and §10.5 items 26, 27 (foreign-Tenant non-enumeration / unknown 404, cancel
  scope + auth).

Out of scope:

- Numeric timeout classes, drain windows, `L-TENANT-CHAT-RESIDUAL` value (#17).
- Real Provider cancel/abort integration (T18–T23).
- Disconnect incremental-read test (requires blocked-stream SSE parsing).
- Timeout contract test (requires a timeout-enforcing Adapter).

## Risk Classification

Risk flags:

- Authorization (same-Tenant cancel, non-enumeration).
- Audit/security (cancel audit, accounting fault).
- Public contracts (new route, `ChatCancelResponse`).
- Existing behavior (X5/X6 split changes settlement timing for canceled
  streams).
- Multi-domain (domain, ports, application, transport, composition).

Hard gates: Authorization, Audit/security.

Lane: high-risk.

## Work Phases

1. Domain: `RemediationExecutionRecovery`, fix `upstream_timeout`/
   `upstream_unavailable` remediation.
2. Ports: `ChatResidualStore`, `ChatResidualDrain`, audit actions.
3. Application: registry, `CancelChatExecution`, `settleStream` X5/X6 split.
4. Transport: cancel route + wire type.
5. Composition: wire residual ports, fixture Options.
6. Contract tests at the public seam.
7. Full validation matrix, then commit.
