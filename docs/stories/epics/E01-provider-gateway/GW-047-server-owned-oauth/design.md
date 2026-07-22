# Design

## Domain Model

- `OAuthPurpose`: `connect` | `reauthenticate`
- `OAuthFlow`: `browser` | `device`
- `OAuthStatus`: `authorization_pending` | `succeeded` | `failed`
- `OAuthAuthorization`: safe server-owned projection (`authorization_id`,
  account id, purpose, flow, status, optional verification_uri/user_code,
  expires_at, remediation)
- Account-private `ActiveOAuthAuthorizationID` journey marker (never on wire)
- Remediation token `complete_oauth`

## Application Flow

### startOAuthAuthorization

1. A0 authenticate
2. A1 `accounts.manage`
3. A2 size + request validation (`purpose`, `flow_preference`, Idempotency-Key)
4. Same-Tenant ownership (non-enumerating 404)
5. Auth Mode / risk / lifecycle gates before any exchange start
6. Replay claim (fingerprint: account + purpose + flow)
7. A3-A5 admission
8. `OAuthExchangeAdapter.Start` creates one server-owned authorization identity
9. Persist account journey marker; account stays non-usable (`draft` for connect)
10. Replay complete with terminal OAuth projection
11. Safe audit/telemetry/request-log

### getOAuthAuthorization

1. A0 authenticate + A1 `accounts.manage`
2. Ownership on account id
3. Load authorization (non-enumerating foreign/unknown)
4. Admission
5. If pending: `OAuthExchangeAdapter.Poll` may advance to succeeded/failed/expired
6. On first succeeded exchange for connect: Vault put of exchanged material under
   next version, account -> `pending_validation`, clear journey marker
7. On failed/expired connect: store no credential, restore `draft`, clear marker
8. Return only safe OAuthAuthorization fields

Activation is intentionally NOT performed here; the existing probe operation
remains the only transition into `active`.

## Interface Contract

- `POST /v1/provider-accounts/{id}/oauth-authorizations` -> 202 OAuthAuthorization
- `GET /v1/provider-accounts/{id}/oauth-authorizations/{authorization_id}` -> 200
- Required scope: `accounts.manage`
- Start requires `Idempotency-Key`
- 400 invalid purpose/flow/Auth Mode class; 409 lifecycle/single-flight/replay;
  404 non-enumerating

## Data Model

In-memory foundation OAuth journey store + fail-closed production adapter.
No schema migration in this slice.

## Observability

Audit actions: `provider_oauth.started`, `provider_oauth.polled`.
No secret, code, token, or PKCE material in audit/telemetry/request-log.

## Alternatives Considered

1. Poll auto-activates after exchange - rejected; violates I-NO-ACTIVE-ON-FAIL /
   probe-required activation already proven in #46.
2. Application-held exchange secrets across requests - rejected; server-owned
   adapter retains exchange material until Vault put.
