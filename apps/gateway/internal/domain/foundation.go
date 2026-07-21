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
