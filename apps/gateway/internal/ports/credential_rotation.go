package ports

import (
	"context"
	"errors"
)

// ErrCredentialRotationUnsupported reports that the authorized credential
// boundary in force cannot own a rotation. It is returned by a rotation-capable
// injection whose backing Vault has no rotation store wired, and it is the
// fail-closed answer an Adapter must treat as an auth-class failure rather than
// rotating the credential itself.
var ErrCredentialRotationUnsupported = errors.New("provider credential rotation unsupported")

// CredentialRotation is an OPTIONAL capability a CredentialInjection may also
// implement. It exists because a Provider that rotates refresh material on every
// refresh grant makes an Adapter-local refresh actively destructive: the Adapter
// sees rotated material inside its callback, uses the new access_token for one
// re-send, and discards the rotated refresh_token. The Provider has already
// invalidated the previous one, so the material the Vault still holds is dead and
// the NEXT refresh fails — pushing an otherwise healthy account to
// reauthentication.
//
// Rotation therefore belongs to the authorized credential boundary, which is the
// only layer that can persist the rotated set atomically, advance
// credential_version, dedupe concurrent rotations for the same
// (Tenant, Provider Account), and record an audit entry. An Adapter that finds an
// injection WITHOUT this capability MUST NOT rotate on its own: it reports the
// auth-class failure and lets the account move to reauthentication, which loses a
// live session but never silently strands stored credential material.
//
// Material never crosses the callback boundary in either direction as a return
// value: exchange produces the rotated set inside the boundary and use consumes
// it there (ADR 0009, OP-G3).
type CredentialRotation interface {
	// Rotate performs one owned credential rotation.
	//
	// exchange runs the Provider-side rotation grant and returns the COMPLETE
	// rotated material set to persist — not just the short-lived access token —
	// because a partial set would drop the rotated refresh material the next
	// rotation depends on.
	//
	// On success the boundary persists that set under a new credential version,
	// audits the rotation, and invokes use with the rotated material so the caller
	// can re-send the exchange that prompted the rotation. use is never called
	// when persistence fails: an Adapter proceeding on material the Vault does not
	// hold would rotate the Provider's state without rotating the Gateway's.
	//
	// Concurrent rotations for the same (Tenant, Provider Account, Auth Mode) are
	// deduped by the implementation, so two racing requests cannot each spend the
	// same single-use refresh material.
	Rotate(ctx context.Context, exchange func() (string, error), use func(rotated string) error) error
}
