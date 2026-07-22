package domain

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

// NewSubmitCredentialFingerprint builds a stable fingerprint over the direct
// credential submission inputs that determine the durable side effect: the
// operation identity, the target account id, and the credential class. The
// submitted secret material is deliberately excluded so a legitimate retry with
// the same Idempotency-Key replays the terminal result rather than conflicting,
// while a class change (a different credential lifecycle) conflicts. The value
// is a bounded, non-secret projection and never carries the material
// (#20 section 5.2, connection lifecycle spec §9.1).
func NewSubmitCredentialFingerprint(accountID ProviderAccountID, class CredentialClass) Fingerprint {
	const separator = "\x1f"
	return Fingerprint("submit_provider_credential" + separator +
		string(accountID) + separator +
		string(class))
}
