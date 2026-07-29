# Design

## Domain Model

`RenderJob.ValidateDurable` rejects restored states the domain state machine cannot produce, especially `queued` jobs carrying worker or Provider-attempt evidence. Replay rows retain their distinct lifecycle semantics but must preserve valid scope and Tenant binding.

## Application Flow

1. Composition restores durable Render Job and replay stores.
2. Before readiness and queue recovery, the Render service enumerates durable jobs and reconciles each stable replay identity.
3. Matching missing/in-progress replay records become terminal references to the existing job; fingerprint conflicts fail startup closed.
4. Queue recovery republishes non-terminal safe job references.
5. A recovered post-payload job receives a recovery-only claim and cannot enter the Provider render path.

## Interface Contract

No wire shape changes. Existing create retries return the same job instead of remaining permanently `idempotency_in_progress` after a crash between job creation and replay completion.

## Data Model

Both stores remain append-only JSONL. Each process performs one full validated restore, then tracks the applied byte offset and file identity. Under the cross-process advisory lock it applies only complete appended rows. Truncation, replacement, malformed tails, or impossible rows fail closed or trigger a fresh validated rebuild before state is exposed.

The `.lock` file is a stable anchor only. Ownership is the open OS lock/handle, so process death releases exclusion even though the anchor remains on disk.

## UI / Platform Impact

No UI change. Lock implementations are split by Go build tags for Windows and Unix.

## Observability

Startup reconciliation errors participate in readiness failure. Durable ledgers, errors, audit, and status continue to exclude prompt plaintext, credentials, and content bytes.

## Alternatives Considered

1. Delete-on-close `O_EXCL` marker files. Rejected because abrupt process death leaves a permanent blocker.
2. Add a new database transaction. Rejected because issue #56 must preserve the accepted zero-dependency file-backed implementation.
3. Blindly overwrite replay records from jobs. Rejected because a fingerprint conflict could steal another idempotency owner.
