// Package domain owns Provider-independent Gateway values and invariants.
package domain

// IdentifierKind names a server-owned identifier namespace.
type IdentifierKind string

const (
	IdentifierKindRequest IdentifierKind = "request"
	IdentifierKindJob     IdentifierKind = "job"
	// IdentifierKindProviderAccount namespaces server-owned Provider Account
	// identities. The generated value is prefixed with `pa_` by the port.
	IdentifierKindProviderAccount IdentifierKind = "pa"
	// IdentifierKindAsset namespaces server-owned Asset identities. The
	// generated value is prefixed with `asset_` by the port so the wire id
	// matches the frozen Public API example shape.
	IdentifierKindAsset IdentifierKind = "asset"
	// IdentifierKindOAuth namespaces server-owned OAuth journey identities.
	// The generated value is prefixed with `oauth_` so it matches the frozen
	// Public API authorization_id pattern.
	IdentifierKindOAuth IdentifierKind = "oauth"
	// IdentifierKindWorker namespaces worker process identities used for
	// Render Job fencing leases (#14 §5.1).
	IdentifierKindWorker IdentifierKind = "worker"
	// IdentifierKindAttempt namespaces upstream attempt identities.
	IdentifierKindAttempt IdentifierKind = "attempt"
)

// Identifier is a server-owned opaque identifier.
type Identifier string

// JobRef is the durable identity shared by job stores and workers.
type JobRef struct {
	TenantID Identifier
	JobID    Identifier
}

// Valid reports whether both ownership and job identities are present.
func (ref JobRef) Valid() bool {
	return ref.TenantID != "" && ref.JobID != ""
}
