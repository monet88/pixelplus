// Package ports owns application-facing outbound Gateway contracts.
package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// Typed port errors let the application map an infrastructure outcome to one
// canonical code without parsing driver or provider strings.
var (
	// ErrAuthentication reports that presented Client API Key material did not
	// resolve to a Security Principal. Missing, malformed, unknown,
	// wrong-secret, and revoked cases MUST all use this single error.
	ErrAuthentication = errors.New("client api key authentication failed")
	// ErrAccountNotVisible reports that a Provider Account id is foreign,
	// unknown, or deleted from the principal's perspective. It is the single
	// non-enumerating visibility failure.
	ErrAccountNotVisible = errors.New("provider account not visible")
	// ErrDependencyUnavailable reports that a required backend could not satisfy
	// its fail-closed contract, so admission must not proceed.
	ErrDependencyUnavailable = errors.New("required dependency unavailable")
	// ErrAccountUpdateConflict reports that a conditional account mutation lost
	// its atomic claim (for example a single-flight OAuth marker was already set
	// by a concurrent writer). The application maps this to the matching
	// account_not_usable outcome rather than inventing a second journey.
	ErrAccountUpdateConflict = errors.New("provider account update conflict")
)

// PresentedClientAPIKey is the raw bearer material extracted from a request.
// It is a transient value that never enters durable state, logs, or errors.
type PresentedClientAPIKey struct {
	// Material is the full `sk-pxp_<public_locator>_<secret>` bearer string.
	Material string
}

// PrincipalStore authenticates presented Client API Key material and derives
// the Security Principal server-side. Unknown, malformed, wrong-secret, and
// revoked material MUST fail with ErrAuthentication so the transport surface
// cannot distinguish the cases (#8 section 4.3, #6 I-PRINCIPAL).
type PrincipalStore interface {
	Authenticate(context.Context, PresentedClientAPIKey) (domain.SecurityPrincipal, error)
}

// AdmissionStage names the normative admission step a decision belongs to so a
// controlled fake can prove ordering without exposing internal counters.
type AdmissionStage string

// Admission stages in normative order (#8 section 6).
const (
	AdmissionStageRateLimit   AdmissionStage = "rate_limit"
	AdmissionStageConcurrency AdmissionStage = "concurrency"
	AdmissionStageQuota       AdmissionStage = "quota"
)

// AdmissionRequest carries the authenticated principal and the operation being
// admitted. Request-size is enforced at the transport boundary before this
// port is reached, so it is not part of the admission counter contract.
type AdmissionRequest struct {
	Principal domain.SecurityPrincipal
	Operation domain.OperationToken
}

// AdmissionDecision reports whether the request is admitted for execution. When
// Admitted is false, Stage names the failing normative step so the application
// can emit the matching canonical code without inventing its own mapping.
type AdmissionDecision struct {
	Admitted bool
	Stage    AdmissionStage
}

// AdmissionReservation identifies an accepted admission so its occupancy and
// quota effects can be reconciled after the operation settles.
// SettlementKey, when non-empty, makes Reconcile logically idempotent for that
// durable unit of work (e.g. one Render Job create occupancy). Empty keys keep
// legacy non-keyed settle semantics for request-scoped operations.
type AdmissionReservation struct {
	Principal     domain.SecurityPrincipal
	Operation     domain.OperationToken
	SettlementKey string
}

// AdmissionStore evaluates the A3-A5 admission gates in normative order and
// reserves capacity on accept. Unavailable limit state MUST fail closed rather
// than admit (#8 section 7.6). Reconcile with a non-empty SettlementKey MUST be
// logically idempotent: a second settle for the same key is a no-op.
type AdmissionStore interface {
	Admit(context.Context, AdmissionRequest) (AdmissionDecision, AdmissionReservation, error)
	Reconcile(context.Context, AdmissionReservation) error
}

// ReplayOutcome names the result of an atomic idempotency claim.
type ReplayOutcome string

// Replay outcomes (#20 section 5.5).
const (
	// ReplayClaimed means this request is the sole executor for the scoped key.
	ReplayClaimed ReplayOutcome = "claimed"
	// ReplayInProgress means a matching request already owns the claim.
	ReplayInProgress ReplayOutcome = "in_progress"
	// ReplayTerminal means a matching request already produced a terminal
	// result that must be replayed without new side effects.
	ReplayTerminal ReplayOutcome = "terminal"
	// ReplayConflict means the scoped key is bound to a different fingerprint.
	ReplayConflict ReplayOutcome = "conflict"
	// ReplayUncertain means the prior owner or claim was lost while commit
	// certainty is unavailable; recovery must not steal the claim.
	ReplayUncertain ReplayOutcome = "uncertain"
)

// ReplayDecision is the result of an atomic claim. TerminalAccount carries the
// prior durable Provider Account when Outcome is ReplayTerminal so the original
// result can be replayed without re-persisting.
type ReplayDecision struct {
	Outcome         ReplayOutcome
	TerminalAccount domain.ProviderAccount
	// TerminalOAuth carries the prior durable OAuth journey when Outcome is
	// ReplayTerminal for a startOAuthAuthorization claim.
	TerminalOAuth domain.OAuthAuthorization
}

// ReplayResult is the terminal projection recorded once an owning request
// completes its durable side effect, so later matching replays are stable.
type ReplayResult struct {
	Account domain.ProviderAccount
	// OAuth carries the terminal server-owned OAuth journey projection for a
	// startOAuthAuthorization claim. It is empty for non-OAuth replay results.
	OAuth domain.OAuthAuthorization
}

// ReplayStore performs the atomic idempotency claim, fingerprint match, and
// terminal replay. It enforces the no-steal rule and one accepted owner (#20).
type ReplayStore interface {
	Claim(context.Context, domain.ReplayIdentity) (ReplayDecision, error)
	Complete(context.Context, domain.ReplayIdentity, ReplayResult) error
	// Abandon releases a fresh claim that the owning request will not carry to a
	// durable side effect because a later same-request admission gate rejected
	// it. It only clears an in-progress claim still owned by this request and
	// never removes a terminal record, so a legitimate later retry can re-claim
	// the scoped key without the request ever having debited admission or quota
	// (#20 section 5.5). Abandoning a claim is the owner releasing its own
	// un-acted claim, which is distinct from stealing another owner's claim.
	Abandon(context.Context, domain.ReplayIdentity) error
}

// AccountCreation is the typed command to persist a new Provider Account draft
// for the owning Tenant. The application derives Tenant identity from the
// Security Principal; no client-supplied Tenant authority is trusted.
type AccountCreation struct {
	Principal domain.SecurityPrincipal
	Account   domain.ProviderAccount
}

// AccountStore owns logical Provider Account persistence and same-Tenant,
// non-enumerating visibility. Restore MUST make durable Provider Account state,
// including scoped cooldowns and occupied recovery permits, readable before
// composition reports execution readiness. An unreadable durable state returns
// an error so startup stays fail-closed (health/cooldown spec §7.1-§7.2).
// Visible MUST return ErrAccountNotVisible for foreign, unknown, and deleted
// identifiers so the outcome is indistinguishable (#6 section 5.1).
type AccountStore interface {
	Restore(context.Context) error
	Create(context.Context, AccountCreation) (domain.ProviderAccount, error)
	Visible(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.ProviderAccount, error)
	List(context.Context, domain.SecurityPrincipal) ([]domain.ProviderAccount, error)
	// Update persists a mutated account for the owning Tenant. It is the durable
	// side effect of a lifecycle transition (credential submit, validation, probe
	// activation, or credential rejection). The principal derives Tenant identity
	// server-side; a foreign, unknown, or deleted id MUST return
	// ErrAccountNotVisible so the outcome stays non-enumerating (#6 section 5.1).
	Update(context.Context, AccountUpdate) (domain.ProviderAccount, error)
}

// AccountUpdate is the typed command to persist a mutated Provider Account for
// the owning Tenant. Account carries the already-transitioned safe projection.
// Optional preconditions make single-flight OAuth marker claims durable without
// inventing a revision field on the Public API projection.
type AccountUpdate struct {
	Principal domain.SecurityPrincipal
	Account   domain.ProviderAccount
	// RequireEmptyOAuthMarker rejects the write unless the currently stored
	// ActiveOAuthAuthorizationID is empty. Used to claim a single-flight OAuth
	// journey before the exchange adapter runs.
	RequireEmptyOAuthMarker bool
	// RequireOAuthMarker, when non-empty, rejects the write unless the currently
	// stored ActiveOAuthAuthorizationID equals this value. Used so a terminal
	// poll only clears/settles the journey it owns.
	RequireOAuthMarker domain.OAuthAuthorizationID
	// RequireDraftLifecycle rejects the write unless the currently stored
	// lifecycle is still draft. Combined with RequireEmptyOAuthMarker this
	// prevents a concurrent direct credential submit from being overwritten by a
	// late OAuth start write.
	RequireDraftLifecycle bool
	// RequirePendingVersion fences promotion/settlement to the version this
	// request validated. A stale writer cannot promote another replacement.
	RequirePendingVersion int
	// RequireEmptyPendingVersion rejects the write unless the currently stored
	// PendingCredentialVersion is zero. Used so a management enable/disable or a
	// first-connect probe activation cannot clobber an in-flight replacement that
	// staged after the writer loaded its snapshot.
	RequireEmptyPendingVersion bool
	// RequireLifecycle, when non-empty, rejects the write unless the currently
	// stored lifecycle equals this value. This prevents stale probe/recovery
	// snapshots from resurrecting a disabled account or overwriting a concurrent
	// management transition (health/cooldown spec §7; management contract §4.6).
	RequireLifecycle domain.LifecycleState
	// RequireControls, when non-empty, rejects the write unless the currently
	// stored administrative controls equal this value. This prevents stale health
	// writes from reverting a quarantine, drain, or execution-disabled change.
	RequireControls domain.AdministrativeControls
	// RequireControlsMatch must be true for RequireControls to be enforced. A
	// pointer-free struct cannot distinguish "not set" from "set to zero", so this
	// boolean gates the check.
	RequireControlsMatch bool
	// PatchLastProbedAt mutates only LastProbedAt/UpdatedAt on the existing row
	// so a no-signal active re-probe cannot resurrect concurrent lifecycle fields.
	// Health authority is never stored on AccountStore rows.
	PatchLastProbedAt bool
	LastProbedAt      domain.Timestamp
	// PatchLastAllocatedVersion reserves one monotonic credential version without
	// advancing lifecycle/current credential state before Vault.Put succeeds.
	// RequireLastAllocatedVersionMatch makes zero a valid observed CAS value.
	PatchLastAllocatedVersion        bool
	RequireLastAllocatedVersionMatch bool
	RequireLastAllocatedVersion      int
	LastAllocatedVersion             int
}

// AuditAction names a product/security audit event.
type AuditAction string

// Audit actions emitted by the Provider Account request spine.
const (
	AuditProviderAccountCreated AuditAction = "provider_account.created"
	AuditProviderAccountRead    AuditAction = "provider_account.read"
	AuditProviderAccountListed  AuditAction = "provider_account.listed"
	// AuditProviderCredentialSubmitted records a direct credential submission.
	// It carries the account id and outcome only, never material (connection
	// lifecycle spec §4.4 rule 6).
	AuditProviderCredentialSubmitted AuditAction = "provider_credential.submitted"
	// AuditProviderAccountProbed records a controlled probe attempt and its safe
	// outcome (activated or rejected), never a raw provider payload.
	AuditProviderAccountProbed AuditAction = "provider_account.probed"
	// AuditProviderAccountActivated records the transition into `active` after a
	// required probe succeeds (connection lifecycle spec §4.7).
	AuditProviderAccountActivated AuditAction = "provider_account.activated"
	// AuditProviderAccountDisabled records a management disable. It carries the
	// account id and outcome only; disable preserves credential material and the
	// last truthful health evidence (connection lifecycle spec §4.10 rule 6).
	AuditProviderAccountDisabled AuditAction = "provider_account.disabled"
	// AuditProviderAccountEnabled records a management enable that opens the
	// current-version probe path. It never predicts probe success (management
	// contract §4.5, I-ACCOUNT-ENABLE-PROBED).
	AuditProviderAccountEnabled AuditAction = "provider_account.enabled"
	// AuditProviderAccountDeleted records a management delete. Every current and
	// pending credential version is revoked before the account is removed; the
	// event carries no secrets (connection lifecycle spec §4.12 rule 6).
	AuditProviderAccountDeleted AuditAction = "provider_account.deleted"
	// AuditProviderHintMalformed records that a normalized rate/quota signal
	// carried an unusable relative reset hint. The event retains only safe
	// classification and account ownership, never the raw Provider value.
	AuditProviderHintMalformed AuditAction = "provider_hint.malformed"
	// AuditProviderOAuthStarted records a successful server-owned OAuth start.
	// It carries the account id and outcome only, never codes or tokens.
	AuditProviderOAuthStarted AuditAction = "provider_oauth.started"
	// AuditProviderOAuthPolled records a successful OAuth status poll and its
	// safe terminal or pending outcome, never exchange material.
	AuditProviderOAuthPolled AuditAction = "provider_oauth.polled"
	// AuditCapabilitySnapshotRead records a same-Tenant Capability Snapshot
	// inspection. It never carries credential material.
	AuditCapabilitySnapshotRead AuditAction = "capability_snapshot.read"
	// AuditModelsListed records a Tenant-owned offerable model list projection.
	AuditModelsListed AuditAction = "models.listed"
	// AuditRoutingPolicyRead records a successful Tenant Routing Policy read.
	// It never carries foreign account ids or credential material.
	AuditRoutingPolicyRead AuditAction = "routing_policy.read"
	// AuditRoutingPolicyReplaced records a successful atomic Routing Policy
	// replace. Payload is safe actor/Tenant/outcome fields only.
	AuditRoutingPolicyReplaced AuditAction = "routing_policy.replaced"
	// AuditProviderHealthTransition records a durable health condition transition
	// (cooldown create/renew, recovery success, dependency-failure renewal, hard
	// auth rejection). Payload is safe fields only (health/cooldown spec §19).
	AuditProviderHealthTransition AuditAction = "provider_health.transition"
)

// AuditEvent is a secret-free product/security audit projection. It carries
// safe actor, Tenant, resource, and outcome fields only (#21 observability).
// Health transition fields are optional and never carry raw Provider payloads.
type AuditEvent struct {
	Action            AuditAction
	TenantID          domain.TenantID
	ClientAPIKeyID    domain.ClientAPIKeyID
	ProviderAccountID domain.ProviderAccountID
	RequestID         domain.Identifier
	Outcome           string
	// AuthMode is the account's Auth Mode for health transitions (safe enum).
	AuthMode domain.AuthMode
	// OldState/NewState and OldReason/NewReason are the prior/new health axes.
	OldState  domain.HealthState
	NewState  domain.HealthState
	OldReason domain.HealthReason
	NewReason domain.HealthReason
	// Scope is the affected health scope kind/operation/model (safe ids only).
	Scope domain.HealthScope
	// CredentialVersion is the business credential version for the condition.
	CredentialVersion int
	// SourceClass is the internal evidence class (upstream_attempt, etc.).
	SourceClass domain.HealthSourceClass
	// ConditionRevision is the fenced revision after the transition.
	ConditionRevision int
	// RetryTimingClass is the safe retry class (e.g. provider_cooldown) when a
	// finite wait is authorized; empty otherwise.
	RetryTimingClass string
	// RetryNotBefore is the safe absolute wait bound when authorized.
	RetryNotBefore domain.Timestamp
	// ProbeID correlates the probe/attempt that produced the observation.
	ProbeID domain.Identifier
}

// AuditRecorder writes the secret-free audit projection. A failing recorder is
// a typed dependency outcome for the application to classify.
type AuditRecorder interface {
	Record(context.Context, AuditEvent) error
}

// AuditBatchRecorder accepts one logical audit mutation as an indivisible
// batch. Health transitions that can emit more than one event require this
// capability so a recorder cannot expose event 1 and then fail event 2 while
// the corresponding HealthStore mutation is aborted.
type AuditBatchRecorder interface {
	RecordBatch(context.Context, []AuditEvent) error
}

// TelemetryEvent aggregates by stable safe code, stage, and operation only. It
// never uses prompt, Asset, credential, or bearer values as labels.
type TelemetryEvent struct {
	Operation  domain.OperationToken
	Code       domain.ErrorCode
	StatusCode int
}

// TelemetryRecorder receives safe operational telemetry.
type TelemetryRecorder interface {
	Record(context.Context, TelemetryEvent) error
}

// RequestLog is the single canonical JSON request log line per HTTP request. It
// uses the fixed field set from #21 and is never an authorization proof.
type RequestLog struct {
	RequestID  domain.Identifier
	UserID     domain.ClientAPIKeyID
	Action     string
	DurationMS int64
	StatusCode int
	Message    string
}

// RequestLogRecorder emits exactly one canonical request log per request.
type RequestLogRecorder interface {
	Record(context.Context, RequestLog) error
}
