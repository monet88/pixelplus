# Security Policy

PixelPlus is a public repository that hosts a multi-tenant SaaS Provider
Gateway and its Client API keys. This policy states how to report a security
issue privately and what you can expect to happen next. It is written for both
maintainers and external reporters.

## Scope

The security boundary covers the repository and the gateway it produces:

- Provider Credentials, Client API Keys, OAuth tokens, and any other secret
  material (including `.env`, `secrets/`, `credentials/`, `auths/`, and any
  tracked or untracked fixture data).
- Cross-Tenant isolation: tenant ownership, authorization, tenant-scoped
  routing, and the invariants in
  `docs/spec/tenant-ownership-authorization-invariants.md` and
  `docs/spec/tenant-scoped-routing-fallback-affinity-leases.md`.
- Credential lifecycle, vault handling, and sensitive-data handling as defined
  in `docs/spec/credential-vault-and-sensitive-data-lifecycle.md` and
  `docs/spec/provider-account-connection-and-credential-lifecycle.md`.
- The Pure-Go Gateway in `apps/gateway/` and its HTTP surface, including
  `apps/gateway/internal/infrastructure/vault/`,
  `apps/gateway/internal/adapters/`,
  `apps/gateway/internal/application/credential.go`, and
  `apps/gateway/internal/transport/http/`.

## Reporting a vulnerability

Use the private GitHub reporting route at

<https://github.com/monet88/pixelplus/security/advisories/new>

This is the only route that is guaranteed private end to end. Do **not** open a
public GitHub issue, a public pull request, or a public discussion for anything
that looks like a vulnerability, a credential, or a cross-Tenant problem. For a
credential or other secret leak, rotate the secret first if you can, then
report it.

If the GitHub private-advisory form is unavailable (for example, the feature is
disabled on this repository), email the maintainers with a PGP-encrypted
message. The key and contact address are published on the repository owner
profile; do not send secret material in plaintext email.

### What to include

- Repository and branch, or commit SHA, where the issue was found.
- A minimal, reproducible description: the endpoint or file, the affected
  tenant or account scope, and the observable behavior.
- For a credential leak: what was exposed, where, and whether you rotated it.
- For a cross-Tenant issue: the tenant isolation invariant you believe is
  violated and the steps that demonstrate it.
- Severity, if you have one, and any exploitability constraints.

## Disclosure policy

- **Acknowledgement**: maintainers will acknowledge receipt within **72 hours**
  of a report arriving on the private route.
- **Triage and response**: a first triage disposition (accepted, needs more
  information, or out of scope) will be provided within **5 business days**.
  Accepted security issues are prioritized over ordinary repository work.
- **Fix window**: for issues of high or critical severity that are actively
  exploitable, maintainers aim to ship a fix within **14 days** of triage.
  Lower-severity and non-exploitable issues are handled on a regular release
  cadence.
- **Disclosure**: maintainers coordinate disclosure with the reporter and will
  not publish details before a fix is available, unless the issue is already
  public. Findings reported in good faith are not grounds for legal action
  against the reporter.

## Safe harbor

We will not pursue legal action against researchers who report in good faith,
avoid privacy violations and destruction of data, refrain from exploiting an
issue beyond what is needed to demonstrate it, and avoid disruption to
production services. Proof-of-concept work should be confined to the disposable
sandbox in `apps/gateway/deploy/sandbox/`, never against a production or shared
tenant.

## Supply-chain reports

Issues found by dependency or supply-chain tooling (Dependabot, Renovate,
`npm audit`, `govulncheck`) are handled by the same private route when they
affect the gateway runtime, the Docker image, or the release workflow. They are
reviewed by the owners listed in `.github/CODEOWNERS` for the affected surface
before any change merges.
