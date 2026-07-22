package domain

// ErrorCategory is the canonical boundary category of a failure. Values mirror
// the frozen Public API `CanonicalError.category` enum.
type ErrorCategory string

// Canonical error categories.
const (
	CategoryAuthentication ErrorCategory = "authentication"
	CategoryAuthorization  ErrorCategory = "authorization"
	CategoryAdmission      ErrorCategory = "admission"
	CategoryValidation     ErrorCategory = "validation"
	CategoryRouting        ErrorCategory = "routing"
	CategoryCapability     ErrorCategory = "capability"
	CategoryExecution      ErrorCategory = "execution"
	CategoryDelivery       ErrorCategory = "delivery"
	CategoryDependency     ErrorCategory = "dependency"
	CategoryInternal       ErrorCategory = "internal"
	CategoryCredential     ErrorCategory = "credential"
)

// StatusClass is the HTTP-oriented outcome class. Values mirror the frozen
// Public API `CanonicalError.status_class` enum.
type StatusClass string

// Canonical status classes used by the request spine.
const (
	StatusUnauthorized     StatusClass = "unauthorized"
	StatusNotFound         StatusClass = "not_found"
	StatusForbidden        StatusClass = "forbidden"
	StatusInvalidRequest   StatusClass = "invalid_request"
	StatusRequestSize      StatusClass = "request_size"
	StatusRateLimit        StatusClass = "rate_limit"
	StatusConcurrencyLimit StatusClass = "concurrency_limit"
	StatusQuota            StatusClass = "quota"
	StatusAccountPolicy    StatusClass = "account_policy"
	StatusConflict         StatusClass = "conflict"
	StatusDependency       StatusClass = "dependency"
	StatusInternal         StatusClass = "internal"
)

// HTTPStatus maps a canonical status class to its HTTP status code. The mapping
// is a pure value projection owned by the canonical error definition so no
// stage re-derives it; transport still owns emitting the status to the wire and
// the application uses it only for the request-log status_code field required by
// ADR 0009. It never imports net/http so the domain layer stays transport-free.
func (class StatusClass) HTTPStatus() int {
	switch class {
	case StatusUnauthorized:
		return 401
	case StatusNotFound:
		return 404
	case StatusForbidden:
		return 403
	case StatusInvalidRequest:
		return 400
	case StatusRequestSize:
		return 413
	case StatusRateLimit, StatusConcurrencyLimit, StatusQuota:
		return 429
	case StatusAccountPolicy, StatusConflict:
		return 409
	case StatusDependency:
		return 503
	default:
		return 500
	}
}

// Retryability is one stable retryability class. Values mirror the frozen
// Public API `CanonicalError.retryability` enum.
type Retryability string

// Retryability classes.
const (
	RetryNotRetryable           Retryability = "not_retryable"
	RetryAfter                  Retryability = "retry_after"
	RetrySafeInternal           Retryability = "safe_internal_retry"
	RetryIdempotentReplay       Retryability = "idempotent_replay"
	RetryNewRequestOnly         Retryability = "new_request_only"
	RetryOperatorActionRequired Retryability = "operator_action_required"
)

// Remediation is one closed-vocabulary remediation token. Values mirror the
// frozen Public API `Remediation` enum.
type Remediation string

// Remediation tokens used by the request spine.
const (
	RemediationAuthenticate        Remediation = "authenticate"
	RemediationRequestPermission   Remediation = "request_permission"
	RemediationFixRequest          Remediation = "fix_request"
	RemediationReducePayload       Remediation = "reduce_payload"
	RemediationWaitAdmission       Remediation = "wait_admission"
	RemediationRetrySameKey        Remediation = "retry_same_idempotency_key"
	RemediationContactOperator     Remediation = "contact_operator"
	RemediationSubmitCredential    Remediation = "submit_credential"
	RemediationAuthModeUnavailable Remediation = "auth_mode_unavailable"
	// RemediationReauthenticate asks the Tenant to submit replacement credential
	// material for an existing account after a validation or probe auth failure
	// (connection lifecycle spec §5.3 B, §4.6).
	RemediationReauthenticate Remediation = "reauthenticate"
	// RemediationAckRisk asks the Tenant to record the required residual-risk
	// acknowledgement before a `gated`/`experimental` Auth Mode credential may
	// become usable (risk envelope §6.1, connection lifecycle spec §5.3 D).
	RemediationAckRisk Remediation = "ack_risk"
	// RemediationEnableAccount asks the Tenant to re-enable a disabled account
	// before it can serve work (connection lifecycle spec §4.10).
	RemediationEnableAccount Remediation = "enable_account"
	// RemediationAccountRemediation is the account-policy remediation returned
	// when a Provider Account is not usable for the requested operation
	// (frozen ErrorAccountNotUsable example).
	RemediationAccountRemediation Remediation = "account_remediation"
	RemediationNone               Remediation = "none"
)

// FailureStage is the bounded stage where a failure was classified. Values
// mirror the frozen Public API `CanonicalError.failure_stage` enum.
type FailureStage string

// Failure stages used by the request spine.
const (
	StageAuthentication    FailureStage = "authentication"
	StageAuthorization     FailureStage = "authorization"
	StageAdmission         FailureStage = "admission"
	StageRequestValidation FailureStage = "request_validation"
	StageRouting           FailureStage = "routing"
	StageRecovery          FailureStage = "recovery"
	StageDependency        FailureStage = "dependency"
	StageInternal          FailureStage = "internal"
)

// IdempotencyState is the bounded replay state. Values mirror the frozen
// Public API `CanonicalError.idempotency_state` enum.
type IdempotencyState string

// Idempotency states.
const (
	IdempotencyNotUsed    IdempotencyState = "not_used"
	IdempotencyInProgress IdempotencyState = "in_progress"
	IdempotencyTerminal   IdempotencyState = "terminal"
	IdempotencyConflict   IdempotencyState = "conflict"
	IdempotencyUncertain  IdempotencyState = "uncertain"
)

// CanonicalError is the Provider-independent Public API failure value. It never
// carries tenant_id, Provider Credential material, prompt/content, raw Provider
// data, or foreign-resource existence. Foreign and unknown identifiers all use
// the same ErrCodeResourceNotFound value with no ResourceReference so they are
// observationally indistinguishable (#6 section 5.1, #16 I-ERROR-NON-ENUM).
type CanonicalError struct {
	Code             ErrorCode
	Category         ErrorCategory
	StatusClass      StatusClass
	Retryability     Retryability
	Remediation      Remediation
	FailureStage     FailureStage
	RetryAfterClass  string
	IdempotencyState IdempotencyState
	// RequestID is the server-owned correlation id for this Public API error.
	// It is set at the boundary that emits the error and is never a client
	// value that authorizes access (#16 section 3.1, 3.3).
	RequestID Identifier
	// Reference is a same-Tenant local id, included only when already authorized
	// on this path. It is omitted for foreign or unknown identifiers.
	Reference ResourceReference
}

// WithRequestID returns a copy of the canonical error carrying the server-owned
// request id. It never mutates the shared constructor value.
func (canonical CanonicalError) WithRequestID(requestID Identifier) CanonicalError {
	canonical.RequestID = requestID
	return canonical
}

// HTTPStatus returns the HTTP status code for this canonical error's status
// class. It is a pure projection over StatusClass.HTTPStatus.
func (canonical CanonicalError) HTTPStatus() int {
	return canonical.StatusClass.HTTPStatus()
}

// ResourceReference is a same-Tenant local id projection. It is only populated
// once ownership is established and is always omitted for foreign/unknown ids.
type ResourceReference struct {
	ProviderAccountID ProviderAccountID
}

// Empty reports whether the reference has no populated id.
func (reference ResourceReference) Empty() bool {
	return reference.ProviderAccountID == ""
}

// ErrorCode is a stable semantic error token matching `^[a-z][a-z0-9_]*$`.
type ErrorCode string

// Canonical error codes emitted by the request spine.
const (
	ErrCodeAuthenticationFailed  ErrorCode = "authentication_failed"
	ErrCodeResourceNotFound      ErrorCode = "resource_not_found"
	ErrCodeForbidden             ErrorCode = "forbidden"
	ErrCodeInvalidRequest        ErrorCode = "invalid_request"
	ErrCodeRequestTooLarge       ErrorCode = "request_too_large"
	ErrCodeRateLimit             ErrorCode = "rate_limit"
	ErrCodeConcurrencyLimit      ErrorCode = "concurrency_limit"
	ErrCodeQuotaExhausted        ErrorCode = "quota_exhausted"
	ErrCodeIdempotencyConflict   ErrorCode = "idempotency_conflict"
	ErrCodeIdempotencyInProgress ErrorCode = "idempotency_in_progress"
	ErrCodeIdempotencyUncertain  ErrorCode = "idempotency_uncertain"
	ErrCodeAuthModeUnavailable   ErrorCode = "auth_mode_unavailable"
	ErrCodeAccountNotUsable      ErrorCode = "account_not_usable"
	ErrCodeDependencyUnavailable ErrorCode = "dependency_unavailable"
	ErrCodeInternal              ErrorCode = "internal_error"
)

// Error satisfies the error interface so a CanonicalError can flow through Go
// error returns while preserving its stable semantic code.
func (canonical CanonicalError) Error() string {
	return string(canonical.Code)
}

// The constructors below are the single source of truth for the canonical
// (code, category, status_class, retryability, remediation) tuple. Keeping the
// tuple assembled in one place prevents a stage from inventing an inconsistent
// mapping (#16 I-ERROR-CANONICAL).

// NewAuthenticationFailed builds the indistinguishable authentication failure
// used for missing, malformed, unknown, wrong-secret, and revoked keys.
func NewAuthenticationFailed() CanonicalError {
	return CanonicalError{
		Code:         ErrCodeAuthenticationFailed,
		Category:     CategoryAuthentication,
		StatusClass:  StatusUnauthorized,
		Retryability: RetryNotRetryable,
		Remediation:  RemediationAuthenticate,
		FailureStage: StageAuthentication,
	}
}

// NewResourceNotFound builds the non-enumerating not-found outcome shared by
// foreign and unknown Tenant-scoped identifiers. It never carries a reference.
func NewResourceNotFound() CanonicalError {
	return CanonicalError{
		Code:         ErrCodeResourceNotFound,
		Category:     CategoryAuthorization,
		StatusClass:  StatusNotFound,
		Retryability: RetryNotRetryable,
		Remediation:  RemediationNone,
		FailureStage: StageAuthorization,
	}
}

// NewForbidden builds the same-Tenant scope/policy denial. It must never be
// used to confirm that a foreign resource exists.
func NewForbidden() CanonicalError {
	return CanonicalError{
		Code:         ErrCodeForbidden,
		Category:     CategoryAuthorization,
		StatusClass:  StatusForbidden,
		Retryability: RetryNotRetryable,
		Remediation:  RemediationRequestPermission,
		FailureStage: StageAuthorization,
	}
}

// NewInvalidRequest builds a request-validation failure that is not a raw size
// violation.
func NewInvalidRequest() CanonicalError {
	return CanonicalError{
		Code:         ErrCodeInvalidRequest,
		Category:     CategoryValidation,
		StatusClass:  StatusInvalidRequest,
		Retryability: RetryNotRetryable,
		Remediation:  RemediationFixRequest,
		FailureStage: StageRequestValidation,
	}
}

// NewRequestTooLarge builds the request-size admission failure.
func NewRequestTooLarge() CanonicalError {
	return CanonicalError{
		Code:         ErrCodeRequestTooLarge,
		Category:     CategoryAdmission,
		StatusClass:  StatusRequestSize,
		Retryability: RetryNotRetryable,
		Remediation:  RemediationReducePayload,
		FailureStage: StageAdmission,
	}
}

// NewRateLimit builds the A3 admission rate-limit failure.
func NewRateLimit() CanonicalError {
	return CanonicalError{
		Code:            ErrCodeRateLimit,
		Category:        CategoryAdmission,
		StatusClass:     StatusRateLimit,
		Retryability:    RetryAfter,
		Remediation:     RemediationWaitAdmission,
		FailureStage:    StageAdmission,
		RetryAfterClass: "admission_rate_window",
	}
}

// NewConcurrencyLimit builds the A4 admission concurrency failure.
func NewConcurrencyLimit() CanonicalError {
	return CanonicalError{
		Code:            ErrCodeConcurrencyLimit,
		Category:        CategoryAdmission,
		StatusClass:     StatusConcurrencyLimit,
		Retryability:    RetryAfter,
		Remediation:     RemediationWaitAdmission,
		FailureStage:    StageAdmission,
		RetryAfterClass: "concurrency_release",
	}
}

// NewQuotaExhausted builds the A5 admission quota failure.
func NewQuotaExhausted() CanonicalError {
	return CanonicalError{
		Code:            ErrCodeQuotaExhausted,
		Category:        CategoryAdmission,
		StatusClass:     StatusQuota,
		Retryability:    RetryAfter,
		Remediation:     RemediationWaitAdmission,
		FailureStage:    StageAdmission,
		RetryAfterClass: "admission_quota_reset",
	}
}

// NewIdempotencyConflict builds the conflict returned when a scoped key is
// bound to a different request fingerprint.
func NewIdempotencyConflict() CanonicalError {
	return CanonicalError{
		Code:             ErrCodeIdempotencyConflict,
		Category:         CategoryAuthorization,
		StatusClass:      StatusConflict,
		Retryability:     RetryNotRetryable,
		Remediation:      RemediationFixRequest,
		FailureStage:     StageAuthorization,
		IdempotencyState: IdempotencyConflict,
	}
}

// NewIdempotencyInProgress builds the outcome returned to a concurrent matching
// request that is not the executor.
func NewIdempotencyInProgress() CanonicalError {
	return CanonicalError{
		Code:             ErrCodeIdempotencyInProgress,
		Category:         CategoryExecution,
		StatusClass:      StatusConflict,
		Retryability:     RetryAfter,
		Remediation:      RemediationRetrySameKey,
		FailureStage:     StageAdmission,
		RetryAfterClass:  "idempotency_recovery",
		IdempotencyState: IdempotencyInProgress,
	}
}

// NewIdempotencyUncertain builds the outcome returned when the prior owner or
// durable claim was lost while commit certainty is unavailable. The claim is
// never stolen into a second execution (#20 section 5.5).
func NewIdempotencyUncertain() CanonicalError {
	return CanonicalError{
		Code:             ErrCodeIdempotencyUncertain,
		Category:         CategoryExecution,
		StatusClass:      StatusConflict,
		Retryability:     RetryOperatorActionRequired,
		Remediation:      RemediationContactOperator,
		FailureStage:     StageRecovery,
		IdempotencyState: IdempotencyUncertain,
	}
}

// NewAuthModeUnavailable builds the fail-closed routing failure returned when a
// requested Auth Mode is outside the product risk envelope. The MVP spine emits
// it for a prohibited Auth Mode (auth-mode-risk-envelope-and-kill-criteria.md
// §5.5: Grok Web SSO); gated/experimental gating (operator flag + Tenant
// acknowledgement) is owned by #7/#9 and out of scope here.
func NewAuthModeUnavailable() CanonicalError {
	return CanonicalError{
		Code:         ErrCodeAuthModeUnavailable,
		Category:     CategoryRouting,
		StatusClass:  StatusAccountPolicy,
		Retryability: RetryOperatorActionRequired,
		Remediation:  RemediationAuthModeUnavailable,
		FailureStage: StageRouting,
	}
}

// NewAccountNotUsable builds the account-policy failure returned when a
// Provider Account exists and is visible to the owning Tenant but cannot serve
// the requested credential/probe operation because a durable usability gate is
// unmet (wrong lifecycle state for the transition, Auth Mode execution
// disabled, or a `gated`/`experimental` mode without the required Tenant risk
// acknowledgement). It fails closed before any Vault use or Probe Adapter call
// (connection lifecycle spec §5.2, §4.2). The caller supplies the closed-set
// remediation token the Tenant needs (submit_credential, ack_risk,
// reauthenticate, enable_account, or account_remediation); the frozen 409
// ErrorAccountNotUsable example uses account_remediation as the generic form.
func NewAccountNotUsable(remediation Remediation) CanonicalError {
	return CanonicalError{
		Code:         ErrCodeAccountNotUsable,
		Category:     CategoryRouting,
		StatusClass:  StatusAccountPolicy,
		Retryability: RetryNotRetryable,
		Remediation:  remediation,
		FailureStage: StageRouting,
	}
}

// NewDependencyUnavailable builds the fail-closed dependency failure used when
// a required backend cannot satisfy its contract.
func NewDependencyUnavailable() CanonicalError {
	return CanonicalError{
		Code:            ErrCodeDependencyUnavailable,
		Category:        CategoryDependency,
		StatusClass:     StatusDependency,
		Retryability:    RetryAfter,
		Remediation:     RemediationWaitAdmission,
		FailureStage:    StageDependency,
		RetryAfterClass: "dependency_recovery",
	}
}

// NewInternalError builds the fallback for an unclassified internal outcome. It
// never carries a raw exception string.
func NewInternalError() CanonicalError {
	return CanonicalError{
		Code:         ErrCodeInternal,
		Category:     CategoryInternal,
		StatusClass:  StatusInternal,
		Retryability: RetryNotRetryable,
		Remediation:  RemediationNone,
		FailureStage: StageInternal,
	}
}
