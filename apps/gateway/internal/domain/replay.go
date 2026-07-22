package domain

import "strconv"

// ReplayScope is the scope that isolates an idempotency record. It binds a
// record to exactly one Tenant and Client API Key so a replayed key from
// another Tenant can never reach this record (#6 A7, #20 section 5.2).
type ReplayScope struct {
	TenantID       TenantID
	ClientAPIKeyID ClientAPIKeyID
	Key            string
}

// Valid reports whether the scope carries a usable idempotency key.
func (scope ReplayScope) Valid() bool {
	return scope.TenantID != "" && scope.ClientAPIKeyID != "" && scope.Key != ""
}

// Fingerprint is a stable digest of the operation identity plus every request
// input that can change the durable side effect. Two requests with the same
// scope but different fingerprints conflict rather than replay.
type Fingerprint string

// ReplayIdentity binds a scoped idempotency key to one operation fingerprint.
type ReplayIdentity struct {
	Scope       ReplayScope
	Fingerprint Fingerprint
}

// NewCreateProviderAccountFingerprint builds a stable fingerprint over the
// create-draft inputs that determine the durable side effect: the operation
// identity, Provider, Auth Mode, and label. The same scoped idempotency key
// with any of these changed produces a different fingerprint and therefore an
// idempotency conflict rather than a replay (#20 section 5.2). The value is a
// bounded, non-secret projection; the create command carries no secret input.
func NewCreateProviderAccountFingerprint(provider Provider, mode AuthMode, label string) Fingerprint {
	// A record separator that cannot appear in the enum values keeps distinct
	// field tuples from colliding after concatenation.
	const separator = "\x1f"
	return Fingerprint("create_provider_account" + separator +
		string(provider) + separator +
		string(mode) + separator +
		label)
}

// NewSubmitCredentialFingerprint builds a stable fingerprint over direct
// credential submission inputs that determine the durable side effect: the
// operation identity, target account id, and credential class. Direct first
// submission and reauthentication deliberately have different operation
// identities even when they target the same account and class. Secret material
// is excluded so retries replay the terminal result without storing a secret in
// the replay record (#20 section 5.2, connection lifecycle spec §9.1).
func NewSubmitCredentialFingerprint(accountID ProviderAccountID, class CredentialClass, replacement bool) Fingerprint {
	const separator = "\x1f"
	operation := "submit_provider_credential"
	if replacement {
		operation = "reauthenticate_provider_account"
	}
	return Fingerprint(operation + separator +
		string(accountID) + separator +
		string(class))
}

// NewCreateAssetFingerprint builds a stable fingerprint over the upload inputs
// that determine the durable side effect: the operation identity, Asset kind,
// content checksum, and byte size. The same scoped idempotency key with any of
// these changed produces a different fingerprint and therefore an idempotency
// conflict rather than a replay (#20 section 5.2). The value is a bounded,
// non-secret projection; the upload command carries no secret input and the
// checksum is an explicitly non-secret content digest (#13 section 3.1).
func NewCreateAssetFingerprint(kind AssetKind, checksum string, byteSize int64) Fingerprint {
	const separator = "\x1f"
	return Fingerprint("create_asset" + separator +
		string(kind) + separator +
		checksum + separator +
		strconv.FormatInt(byteSize, 10))
}

// NewStartOAuthAuthorizationFingerprint builds a stable fingerprint over the
// OAuth start inputs that determine the durable side effect: the operation
// identity, the target account id, purpose, and flow preference. Secret exchange
// material is never part of the fingerprint because it is server-owned and never
// client-supplied (#20 section 5.2, management contract §4.3).
func NewStartOAuthAuthorizationFingerprint(accountID ProviderAccountID, purpose OAuthPurpose, flow OAuthFlow) Fingerprint {
	const separator = "\x1f"
	return Fingerprint("start_oauth_authorization" + separator +
		string(accountID) + separator +
		string(purpose) + separator +
		string(flow))
}
