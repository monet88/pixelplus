# 0010 Grok xAI OAuth Operation Surface Policy

Date: 2026-07-20

## Status

Accepted

## Context

The Grok xAI OAuth evidence identifies two upstream surface families with
different behavior: `cli-chat-proxy.grok.com` for OAuth chat and `api.x.ai` for
image operations. The research source intentionally left the selection as a
future PixelPlus policy decision. The Provider Account and Capability Snapshot
specifications required a surface binding to be recorded, but did not say who
selects it, whether clients can configure it, or whether execution may switch
surfaces after a failure.

That ambiguity would force the Gateway implementation issue to make a product
and execution decision during code work.

## Decision

PixelPlus owns one server-side, operation-specific surface policy for the
`grok_xai_oauth` Auth Mode:

| Operation | Required surface |
| --- | --- |
| `chat` | `cli_chat_proxy` (`https://cli-chat-proxy.grok.com/v1`) |
| `chat_streaming` | `cli_chat_proxy` (`https://cli-chat-proxy.grok.com/v1`) |
| `image_generation` | `api_x_ai` (`https://api.x.ai/v1`) |
| `image_edit` | `api_x_ai` (`https://api.x.ai/v1`) |
| `inpaint` | `unsupported` |

Clients do not submit, select, or override the surface family. Provider Account
creation continues to accept only the stable public fields; the server records
the applicable surface-policy version as non-secret account metadata.

Activation uses a cost-minimal authenticated `cli_chat_proxy` chat probe. That
probe proves account authentication only for the chat surface. Image generation
and image edit remain non-offerable until a live, current-credential probe proves
the exact operation and model on `api_x_ai`, because current evidence does not
establish OAuth-token acceptance on media endpoints.

Capability facts record the exact surface on which they were observed. A fact
from one surface never authorizes an operation on the other surface. Adapter,
routing, retry, and recovery code must not switch between these surface families
automatically. Failure on the required surface produces the canonical
capability, authentication, health, or Provider outcome for that operation and
may invalidate its capability fact; it does not trigger a cross-surface attempt.

Changing the mapping, exposing client selection, or adding cross-surface
fallback requires a new product decision and refreshed capability/risk evidence.

## Alternatives Considered

1. Bind each Provider Account to one client-selected base URL. Rejected because
   the stable create-account contract has no such field and one account needs
   chat and image operations on different surface families.
2. Probe both surfaces during activation and choose whichever succeeds.
   Rejected because that silently turns probing into policy selection and may
   call an unverified media surface without an admitted image operation.
3. Try the second surface after a failure. Rejected because it creates silent
   cross-surface fallback, can change commit/retry semantics, and can overstate
   capability evidence.

## Consequences

Positive:

- Issue #42 has a deterministic operation-to-surface mapping.
- Clients cannot widen Provider access by selecting an upstream base URL.
- Chat proof cannot accidentally authorize image execution, and vice versa.
- Retry ownership and capability provenance remain surface-specific.

Tradeoffs:

- A Grok xAI OAuth account may be active for chat while image operations remain
  non-offerable pending separate live probes.
- An outage or entitlement failure on one surface does not automatically fall
  back to the other surface.

## Follow-Up

- The Grok xAI OAuth Adapter implementation must encode this mapping as typed
  Auth Mode policy, not process environment or client input.
- Public-HTTP contract tests must prove the selected Adapter surface per
  operation and zero calls to the alternate surface.
