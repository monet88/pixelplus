// Package ports owns application-facing outbound Gateway contracts.
package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ErrCapabilitySnapshotNotFound reports that a same-Tenant account has no
// Capability Snapshot yet. The application maps it to capability_unverified so
// operators learn a probe is still required (capability semantics section 9.3;
// frozen getCapabilitySnapshot 409).
var ErrCapabilitySnapshotNotFound = errors.New("capability snapshot not found")

// CapabilityStore owns Tenant-scoped Capability Snapshot persistence. Get and
// List return only the authenticated Tenant's snapshots. Put replaces the
// current version-bound snapshot for one account. A missing snapshot returns
// ErrCapabilitySnapshotNotFound; a foreign/unknown account never discloses
// existence beyond the AccountStore visibility gate that runs first.
type CapabilityStore interface {
	Get(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.CapabilitySnapshot, error)
	List(context.Context, domain.SecurityPrincipal) ([]domain.CapabilitySnapshot, error)
	Put(context.Context, domain.SecurityPrincipal, domain.CapabilitySnapshot) error
}

// CapabilityObservationCommand authorizes a controlled capability observation
// after a successful auth-proving probe. It carries only the safe binding; the
// adapter never receives or returns credential material.
type CapabilityObservationCommand struct {
	Principal domain.SecurityPrincipal
	AccountID domain.ProviderAccountID
	AuthMode  domain.AuthMode
	Version   int
}

// CapabilityObservation is the secret-free probe/evidence projection used to
// mint a credential-version-bound Capability Snapshot. It records the five
// primary operations, observed model slugs only, and safe provenance.
type CapabilityObservation struct {
	Operations   map[domain.CapabilityOperation]domain.CapabilityFact
	Models       []domain.ModelCapability
	ProbeSurface string
}

// CapabilityAdapter observes capability evidence for an account after a
// successful probe. A transient backend failure MUST surface as
// ErrDependencyUnavailable. An empty observation is valid and yields an
// all-unverified snapshot rather than inventing models.
type CapabilityAdapter interface {
	Observe(context.Context, CapabilityObservationCommand) (CapabilityObservation, error)
}
