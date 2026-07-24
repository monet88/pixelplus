package domain

import (
	"encoding/json"
	"time"
)

// ProviderAccountID is the stable, safe-to-expose Provider Account id. It
// matches the frozen Public API pattern `^pa_[A-Za-z0-9_]+$`.
type ProviderAccountID string

// Timestamp is a server-owned instant. The zero value means "not set" and is
// omitted from safe projections.
type Timestamp struct {
	value time.Time
}

// OperationToken names a stable Public API operation for scope, admission, and
// telemetry. Values mirror the operation-id vocabulary (#8 section 5.2).
type OperationToken string

// Provider Account operation tokens used by the request spine.
const (
	OperationCreateProviderAccount OperationToken = "create_provider_account"
	OperationListProviderAccounts  OperationToken = "list_provider_accounts"
	OperationGetProviderAccount    OperationToken = "get_provider_account"
	// OperationSubmitProviderCredential names the direct credential submission
	// operation (submitProviderCredential). It requires accounts.manage.
	OperationSubmitProviderCredential OperationToken = "submit_provider_credential"
	// OperationProbeProviderAccount names the controlled probe operation
	// (probeProviderAccount). It requires accounts.manage.
	OperationProbeProviderAccount OperationToken = "probe_provider_account"
	// OperationStartOAuthAuthorization names the server-owned OAuth start
	// operation (startOAuthAuthorization). It requires accounts.manage.
	OperationStartOAuthAuthorization OperationToken = "start_oauth_authorization"
	// OperationGetOAuthAuthorization names the server-owned OAuth poll
	// operation (getOAuthAuthorization). It requires accounts.manage.
	OperationGetOAuthAuthorization OperationToken = "get_oauth_authorization"
	// OperationGetCapabilitySnapshot names the per-account Capability Snapshot
	// read (getCapabilitySnapshot). It accepts accounts.read or capabilities.read.
	OperationGetCapabilitySnapshot OperationToken = "get_capability_snapshot"
	// OperationListModels names the offerable model list (listModels). It
	// requires capabilities.read.
	OperationListModels OperationToken = "list_models"
)

// NewTimestamp wraps a concrete instant.
func NewTimestamp(value time.Time) Timestamp {
	return Timestamp{value: value.UTC()}
}

// IsZero reports whether the timestamp is unset.
func (timestamp Timestamp) IsZero() bool {
	return timestamp.value.IsZero()
}

// Time returns the underlying UTC instant.
func (timestamp Timestamp) Time() time.Time {
	return timestamp.value
}

// MarshalJSON encodes the timestamp as an RFC3339 UTC string. The zero value
// serializes as JSON null so it round-trips cleanly through durable storage.
func (timestamp Timestamp) MarshalJSON() ([]byte, error) {
	if timestamp.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(timestamp.value.UTC().Format(time.RFC3339Nano))
}

// UnmarshalJSON decodes an RFC3339 UTC string or JSON null into a Timestamp.
func (timestamp *Timestamp) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		timestamp.value = time.Time{}
		return nil
	}
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return err
	}
	timestamp.value = value.UTC()
	return nil
}
