# Architecture — PixelPlus Provider Gateway

PixelPlus is a Photoshop UXP Plugin (InpaintKit) served by a Pure-Go Provider
Gateway. The gateway brokers AI image and chat operations between the plugin
and upstream AI providers (ChatGPT, Gemini, Grok), owning tenant identity,
credential custody, durable execution, routing, and capability enforcement.
The domain glossary and normative spec index is `CONTEXT.md` at the repo root;
this document is the structural map of the running system.

## Gateway Layers

The gateway is a ports-and-adapters (hexagonal) Go module under
`apps/gateway/` with four inner layers plus the composition root:

| Package | Role | Dependency rule |
| --- | --- | --- |
| `internal/domain` | Pure value objects, state machines, invariants, policies (Auth Mode, Lab/Gated profile, errors, capability, routing, Render Job). No I/O. | Imports nothing internal. |
| `internal/ports` | Interfaces the application layer needs from the outside world (Credential Vault, persistence, job runtime, clock, IDs, Adapter, capability, routing, chat). | Imports at most `internal/domain`. |
| `internal/application` | Use cases and service objects: Provider Account lifecycle, OAuth application layer, capability, routing, Asset, Render, Chat execution. Depends on `domain` and `ports`. | No Adapter, transport, or concrete persistence. |
| `internal/infrastructure` | Standard-library implementations behind the ports: SQLite/file persistence, Credential Vault, job runtime, observability. | May import `domain`, `application`, `ports`. |
| `internal/transport/http` | The Public API seam: `net/http` handlers and wire DTOs under the `/v1` server base. | Imports the port-facing gateways. |
| `internal/adapters` | Provider/Auth-Mode protocol translation for ChatGPT Web, ChatGPT Codex OAuth (plus Gemini/Grok surfaces). | Imports `ports` and provider wire formats. |
| `internal/composition` | The composition root. The only package allowed to wire dependencies. | May import everything; nothing imports it. |

Dependency direction is enforced by `scripts/check-dependency-direction.mjs`:
`domain` imports nothing internal, and `ports` imports at most `domain`.

## Dependency Direction

Inner layers never depend on outer ones:

```text
domain
  <- ports
      <- application
          <- infrastructure / transport / adapters
              <- composition
```

`composition.New` (in `internal/composition/runtime.go`) is the single place
concrete implementations are selected and injected. Every service observes the
same instances — e.g. one shared Asset metadata/content pair across the Asset
and Render services so an upload followed by an edit/inpaint sees the same
same-Tenant Assets — so nothing can bypass tenant ownership by constructing a
second store.

## Composition Root

`cmd/gateway/main.go` reads environment configuration
(`PIXELPLUS_GATEWAY_ADDR`, startup/shutdown timeouts, `PROVIDER_ACCOUNT_STORE_PATH`)
and calls `composition.New(composition.Config{...}, composition.ProductionDependencies())`.
`ProductionDependencies()` provides the standard-library foundation: a
process-local job `Runtime`, a real `Clock`, and a random ID generator.

`composition.New` then:

- Restores durable state (Provider Account, health, routing policy, Render Job
  and replay ledgers, queue publications) under a startup timeout.
- Builds the services (Provider Account, Asset, Render, Chat) and the HTTP
  handler.
- Advertises readiness only after every recovery step succeeds and the render
  durability gate passes. Any restore failure substitutes a fail-closed store
  (`UnavailableAccountStore`, `UnavailableHealthStore`,
  `UnavailableRoutingPolicyStore`, `UnavailableRenderJobStore`,
  `FailClosedChatDigester`, `FailClosedOAuthExchangeAdapter`), so a partially
  restored or empty view is never exposed to product traffic.

The process serves two concurrent concerns: the HTTP server
(`runtime.Handler()`) and the worker loop (`runtime.RunWorkers`) for durable
job execution, drained together on shutdown.

## Public API Seam

The Public API seam is `internal/transport/http`. `NewHandler` builds one
`http.ServeMux` and registers the frozen `/v1` surface:

- Provider Account lifecycle, credential, OAuth authorization, disable, and
  controls (`/v1/provider-accounts`).
- Capability Snapshot and model listing (`/v1/provider-accounts/{id}/capability-snapshot`, `/v1/models`).
- Assets (`/v1/assets`, `/v1/assets/{id}/content`).
- Routing Policy (`GET`/`PUT /v1/routing-policy`).
- Durable Render Jobs and image operations (`/v1/images/generations`, `/v1/images/edits`, `/v1/images/inpaints`, `/v1/render-jobs/{id}`, cancel, output retry).
- Chat and streaming (`/v1/chat/completions`, cancel), plus `/healthz` and `/readyz` status routes.

The single stable artifact is `contracts/openapi/pixelplus-public-api-v1.yaml`
(OpenAPI 3.1.1, 26 operations). Every operation authenticates with a
Client API Key (`sk-pxp_...`) that derives the `SecurityPrincipal`
(`tenant_id` + `client_api_key_id`); clients never send `tenant_id`.

## The Two Execution Paths

There are exactly two execution paths, branched by credential kind, not by user
type (ADR 0002):

- **Direct Path** — the user's own API key lives on the device; the Plugin calls
  the Provider directly. No Tenant, Asset, Render Job, or Client API Key exists
  on this path. The Photoshop Plugin's `manifest.json` allowlists the
  Provider hosts for this path.
- **Gateway Path** — the user uses an OAuth/Web account; the Plugin calls the
  Public API with a Client API Key and the Gateway executes on the server.
  Credentials live in the Credential Vault.

The two paths never mix within one request. Path selection is an explicit user
decision, not an automatic fallback.

## Credential Boundaries

- **Provider Credential** — secret material proving the gateway may act through
  a Provider Account. Never used cross-tenant. Material is vault-encrypted and
  separated from account metadata; it is decrypted only on a same-Tenant purpose
  allowlist (`provider_execution`, `provider_probe`, `provider_refresh`) after
  ownership, lifecycle, version, and audit gates pass.
- **Credential Vault** — the logical boundary holding encrypted Provider
  Credential envelopes and sensitive-data objects, bound to Tenant/resource.
  Ciphertext, wrapped keys, or handles alone grant nothing; purpose-bound
  authorization and audit intent are required to decrypt.
- **Client API Key** — the gateway-issued bearer credential for software calling
  the Public API on behalf of a Tenant. Only a secret hash is stored
  (HMAC-SHA-256 with a server pepper); scope can only narrow rights within the
  owning Tenant.
- **Product License** (`INPK-...`) — activates the Plugin on a device; it is
  neither a Client API Key nor a Provider Credential and grants no Public API
  access.

No plaintext credential, envelope, or bearer material ever reaches a Public API
response, log, metric label, trace, or audit record.

## Durable Jobs and Persistence Status

The gateway keeps operational state in SQLite/file ledgers beside a configured
`PROVIDER_ACCOUNT_STORE_PATH`:

- Provider Account state (durable cooldowns and recovery permits survive
  restarts).
- Health ledger, routing policy ledger.
- Render Job ledger, render replay ledger, queue publication recovery.

Render Jobs are the durable unit of image work with the state machine
`queued -> running -> cancel_requested -> canceled | failed | completed`.
Workers claim jobs with a lease + fencing token; an accepted job has at most one
committed upstream attempt, `unknown` commit is never treated as a non-commit,
and `completed` is announced only after an immutable result manifest is durable.
Retrieval/staging/output Asset placement retries use a stable placement key and
never re-run generation/edit/inpaint. Chat executions and streaming carry their
own lifecycle (one terminal, heartbeat in synthetic streams, residual tracking
after disconnect).

Persistence status today: the file-ledger stores cover Provider Account, health,
routing, Render Jobs, replay, and queue recovery. Chat replay/stream-lease and
the Credential Vault are behind their ports; the job runtime is process-local
(`infrastructure/jobs`), with durable delivery owned by a later slice.

## Plugin Boundary

The Plugin (`apps/photoshop-plugin/`, a Photoshop UXP application) is the client
of the Public API. Its responsibilities are bounded to Photoshop host
interaction and pure image helpers:

- Port the verified `src/photoshop/*` host interactions and image helpers from
  the legacy `layerflow` repo; reimplement generation service to be async
  (submit a Render Job, then poll/place output).
- Keep Direct Path provider clients (which must exist for Direct Path).
- Mask convention: the Plugin always produces canonical **luminance**
  masks (white = edit, alpha=255); any inversion belongs to the component that
  talks directly to upstream (the Plugin's provider client on Direct Path, the
  Gateway's Provider Adapter on Gateway Path). No other component inverts.

The Gateway side of the boundary is the Public API seam: the Plugin never
touches domain internals, persistence, or the Vault.

## Harness Template

The reusable Harness template content (consumer layering guidance, parse-first
and command/query rules) moved to `docs/harness/ARCHITECTURE.md` when this
document became the PixelPlus system architecture.

## Related Documents

- `CONTEXT.md` — domain glossary and normative spec index.
- `docs/adr/0002-two-execution-paths-and-uxp-network-allowlist.md`
- `docs/adr/0003-canonical-mask-convention.md`
- `docs/decisions/0009-pure-go-module-seams-and-dependency-budget.md`
- `docs/decisions/0013-experimental-lab-profile-and-capability-baseline.md`
- `docs/decisions/0014-gated-auth-mode-operator-feature-flag.md`
- `docs/spec/provider-gateway-implementation-ready-specification.md`
- `contracts/openapi/pixelplus-public-api-v1.yaml`
