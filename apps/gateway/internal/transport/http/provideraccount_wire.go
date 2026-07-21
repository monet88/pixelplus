package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// The wire DTOs below mirror the frozen Public API v1 schemas exactly. Field
// order and `omitempty` choices match the ProviderAccount, CredentialMetadata,
// HealthSummary, AdministrativeControls, AccountOperationResponse,
// ProviderAccountList, and CanonicalError schemas. Wire representation stays in
// this transport package and never leaks into domain or application types.

type providerAccountWire struct {
	ProviderAccountID string                 `json:"provider_account_id"`
	Provider          string                 `json:"provider"`
	AuthMode          string                 `json:"auth_mode"`
	Label             string                 `json:"label,omitempty"`
	LifecycleState    string                 `json:"lifecycle_state"`
	Credential        credentialMetadataWire `json:"credential"`
	Health            healthSummaryWire      `json:"health"`
	Controls          administrativeControls `json:"administrative_controls"`
	CreatedAt         string                 `json:"created_at"`
	UpdatedAt         string                 `json:"updated_at"`
}

type credentialMetadataWire struct {
	Version          int    `json:"version,omitempty"`
	ExpiresAt        string `json:"expires_at,omitempty"`
	RefreshSupported bool   `json:"refresh_supported"`
	LastValidatedAt  string `json:"last_validated_at,omitempty"`
	LastProbedAt     string `json:"last_probed_at,omitempty"`
}

type healthSummaryWire struct {
	SummaryState string                `json:"summary_state"`
	Conditions   []healthConditionWire `json:"conditions"`
}

type healthConditionWire struct {
	Scope             healthScopeWire `json:"scope"`
	State             string          `json:"state"`
	Reason            string          `json:"reason"`
	CredentialVersion int             `json:"credential_version"`
	ObservedAt        string          `json:"observed_at"`
	Remediation       string          `json:"remediation"`
}

type healthScopeWire struct {
	Kind      string `json:"kind"`
	Operation string `json:"operation,omitempty"`
	ModelSlug string `json:"model_slug,omitempty"`
}

type administrativeControls struct {
	DrainState               string `json:"drain_state"`
	QuarantineState          string `json:"quarantine_state"`
	AuthModeExecutionEnabled bool   `json:"auth_mode_execution_enabled"`
}

type accountOperationResponseWire struct {
	Account   providerAccountWire `json:"account"`
	RequestID string              `json:"request_id"`
}

type providerAccountListWire struct {
	Data []providerAccountWire `json:"data"`
}

// canonicalErrorWire mirrors the frozen CanonicalError schema. Optional fields
// are omitted so foreign/unknown identifiers never carry a resource_reference.
type canonicalErrorWire struct {
	Code             string `json:"code"`
	Category         string `json:"category"`
	StatusClass      string `json:"status_class"`
	Retryability     string `json:"retryability"`
	Remediation      string `json:"remediation"`
	RequestID        string `json:"request_id"`
	FailureStage     string `json:"failure_stage,omitempty"`
	RetryAfterClass  string `json:"retry_after_class,omitempty"`
	IdempotencyState string `json:"idempotency_state,omitempty"`
}

func timestampString(timestamp domain.Timestamp) string {
	if timestamp.IsZero() {
		return ""
	}
	return timestamp.Time().UTC().Format("2006-01-02T15:04:05Z07:00")
}

func toProviderAccountWire(account domain.ProviderAccount) providerAccountWire {
	conditions := make([]healthConditionWire, 0, len(account.Health.Conditions))
	for _, condition := range account.Health.Conditions {
		conditions = append(conditions, healthConditionWire{
			Scope: healthScopeWire{
				Kind:      string(condition.Scope.Kind),
				Operation: condition.Scope.Operation,
				ModelSlug: condition.Scope.ModelSlug,
			},
			State:             string(condition.State),
			Reason:            string(condition.Reason),
			CredentialVersion: condition.CredentialVersion,
			ObservedAt:        timestampString(condition.ObservedAt),
			Remediation:       string(condition.Remediation),
		})
	}
	return providerAccountWire{
		ProviderAccountID: string(account.ID),
		Provider:          string(account.Provider),
		AuthMode:          string(account.AuthMode),
		Label:             account.Label,
		LifecycleState:    string(account.Lifecycle),
		Credential: credentialMetadataWire{
			Version:          account.Credential.Version,
			ExpiresAt:        timestampString(account.Credential.ExpiresAt),
			RefreshSupported: account.Credential.RefreshSupported,
			LastValidatedAt:  timestampString(account.Credential.LastValidatedAt),
			LastProbedAt:     timestampString(account.Credential.LastProbedAt),
		},
		Health: healthSummaryWire{
			SummaryState: string(account.Health.SummaryState),
			Conditions:   conditions,
		},
		Controls: administrativeControls{
			DrainState:               string(account.Controls.Drain),
			QuarantineState:          string(account.Controls.Quarantine),
			AuthModeExecutionEnabled: account.Controls.AuthModeExecutionEnabled,
		},
		CreatedAt: timestampString(account.CreatedAt),
		UpdatedAt: timestampString(account.UpdatedAt),
	}
}

func writeAccountOperation(writer http.ResponseWriter, statusCode int, result application.ProviderAccountResult) {
	writeJSON(writer, statusCode, accountOperationResponseWire{
		Account:   toProviderAccountWire(result.Account),
		RequestID: string(result.RequestID),
	})
}

func writeAccount(writer http.ResponseWriter, statusCode int, account domain.ProviderAccount) {
	writeJSON(writer, statusCode, toProviderAccountWire(account))
}

func writeAccountList(writer http.ResponseWriter, accounts []domain.ProviderAccount) {
	data := make([]providerAccountWire, 0, len(accounts))
	for _, account := range accounts {
		data = append(data, toProviderAccountWire(account))
	}
	writeJSON(writer, http.StatusOK, providerAccountListWire{Data: data})
}

// writeGatewayError serializes a canonical application error. A non-canonical
// error is normalized to internal_error so a raw cause never reaches the wire.
func writeGatewayError(writer http.ResponseWriter, err error) {
	var canonical domain.CanonicalError
	if !errors.As(err, &canonical) {
		canonical = domain.NewInternalError()
	}
	writeCanonical(writer, canonical)
}

// writeCanonical serializes a canonical error with the mapped HTTP status. The
// request id is the server-owned value carried on the canonical error. The
// frozen schema requires a non-empty request_id, so a missing id falls back to
// a safe placeholder rather than emitting an invalid empty value.
func writeCanonical(writer http.ResponseWriter, canonical domain.CanonicalError) {
	requestID := string(canonical.RequestID)
	body := canonicalErrorWire{
		Code:             string(canonical.Code),
		Category:         string(canonical.Category),
		StatusClass:      string(canonical.StatusClass),
		Retryability:     string(canonical.Retryability),
		Remediation:      string(canonical.Remediation),
		RequestID:        requestID,
		FailureStage:     string(canonical.FailureStage),
		RetryAfterClass:  canonical.RetryAfterClass,
		IdempotencyState: string(canonical.IdempotencyState),
	}
	if body.RequestID == "" {
		body.RequestID = "req_unavailable"
	}
	writeJSON(writer, canonical.HTTPStatus(), body)
}

func writeJSON(writer http.ResponseWriter, statusCode int, payload any) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(payload)
}
