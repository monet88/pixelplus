package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ErrCredentialAbsent reports that a probe was requested for an account whose
// current credential version was never stored in the Vault. It is a fail-closed
// usability outcome, not a dependency failure.
var ErrCredentialAbsent = errors.New("provider credential absent for current version")

// CredentialIntake is the transient direct-submission command carried from the
// transport to the Credential Vault. Material is the raw secret set the Tenant
// submitted once over TLS; it is bound to exactly one Tenant, account, Auth
// Mode, credential class, and version. The application forwards it to the Vault
// without inspecting or retaining it: application code never receives plaintext
// or ciphertext as authority (credential vault spec §3.3, I-CREDENTIAL-VAULT-ONLY,
// connection lifecycle spec §9.3).
type CredentialIntake struct {
	Principal domain.SecurityPrincipal
	AccountID domain.ProviderAccountID
	AuthMode  domain.AuthMode
	Class     domain.CredentialClass
	Version   int
	// Material is the transient secret set. It never enters durable Gateway
	// state, logs, errors, audit, or any response; the Vault encrypts it under
	// the Tenant/account/version binding and returns nothing secret.
	Material string
}

// CredentialValidation authorizes required validation of a stored credential
// version. It carries only the safe binding; the Vault reads the material it
// stored internally and never returns it.
type CredentialValidation struct {
	Principal domain.SecurityPrincipal
	AccountID domain.ProviderAccountID
	AuthMode  domain.AuthMode
	Version   int
}

// CredentialValidationResult is the safe pass/fail projection of required
// validation. A false Valid classifies to a credential-rejected outcome; the
// result never carries the material or a raw provider payload.
type CredentialValidationResult struct {
	Valid bool
}

// CredentialVault is the protected boundary for Provider Credential material.
// Put encrypts and persists a new version under an immutable Tenant/account/
// Auth Mode/version binding; Validate evaluates required shape/issuer checks on
// the stored version without releasing material. Neither operation returns
// plaintext or ciphertext to the application (credential vault spec §3.3,
// §4, I-PURPOSE-BOUND-DECRYPT). Unavailable Vault state MUST fail closed.
type CredentialVault interface {
	Put(context.Context, CredentialIntake) error
	Validate(context.Context, CredentialValidation) (CredentialValidationResult, error)
	// Revoke makes a credential version unavailable for new decrypts. Revoke is
	// idempotent so recovery can safely repeat after a failed cutover.
	Revoke(context.Context, CredentialValidation) error
}

// ProbeCommand authorizes a controlled, cost-minimal, auth-proving probe of a
// stored credential version. Scope names the requested breadth (account,
// operation, or model); the adapter proves authentication only and never runs a
// billable render (connection lifecycle spec §4.6, I-PROBE-MINIMAL).
type ProbeCommand struct {
	Principal domain.SecurityPrincipal
	AccountID domain.ProviderAccountID
	AuthMode  domain.AuthMode
	Version   int
	Scope     domain.HealthScope
}

// ProbeSignalClass is the normalized post-authentication runtime signal a probe
// may surface alongside a proven credential. It is the safe classification of a
// Provider rate/quota response the adapter observed; it never carries a raw
// provider header, body, or bucket identifier (health/cooldown spec §6, §17.8).
// The empty value means no runtime signal — an ordinary authenticated probe.
type ProbeSignalClass string

// Probe runtime signal classes. Only the time-waitable rate/quota classes create
// a scoped cooldown; other outcomes remain the credential-rejected / dependency
// paths already owned by the probe spine.
const (
	// ProbeSignalNone is the absence of a runtime rate/quota signal.
	ProbeSignalNone ProbeSignalClass = ""
	// ProbeSignalRateLimited is a validated Provider rate-limit/backoff signal. It
	// creates or renews a cooling_down cooldown at the evidenced scope (§6 rule 1).
	ProbeSignalRateLimited ProbeSignalClass = "rate_limited"
	// ProbeSignalQuotaExhausted is a validated Provider entitlement/quota-reset
	// signal. It creates or renews a cooling_down cooldown at the evidenced scope.
	ProbeSignalQuotaExhausted ProbeSignalClass = "quota_exhausted"
)

// ProbeOutcome is the safe classification of a probe attempt. Authenticated
// true means the credential proved usable for the current version; false means
// an auth-class failure that maps to a credential-rejected, non-activating
// outcome. It never carries a raw provider payload or secret (connection
// lifecycle spec §4.6 rule 3).
//
// Signal, SignalScope, and RetryAfterSeconds carry an OPTIONAL normalized
// runtime rate/quota signal observed during an otherwise authenticated probe.
// When Signal is a rate/quota class, the account activates (auth proven) but the
// Gateway overlays a durable scoped cooldown at the narrowest proven scope; an
// empty/unknown SignalScope normalizes to account scope (§6.2-§6.3). These
// fields never carry a raw provider payload; RetryAfterSeconds is the validated
// relative Retry-After hint in seconds (zero when no safe hint was proven).
type ProbeOutcome struct {
	Authenticated     bool
	Signal            ProbeSignalClass
	SignalScope       domain.HealthScope
	RetryAfterSeconds int
}

// ProbeAdapter runs the required probe for an Auth Mode. A transient backend
// failure MUST surface as ErrDependencyUnavailable so admission fails closed;
// an auth-class failure is reported as Authenticated=false, never as an error,
// so the account moves to reauth_required rather than a dependency 503.
type ProbeAdapter interface {
	Probe(context.Context, ProbeCommand) (ProbeOutcome, error)
}
