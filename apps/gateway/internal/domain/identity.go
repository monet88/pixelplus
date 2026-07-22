package domain

import "time"

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
