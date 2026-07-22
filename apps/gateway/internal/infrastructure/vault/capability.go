package vault

import (
	"context"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// FailClosedCapabilityStore is the production foundation Capability Snapshot
// store. No durable capability ledger is wired yet, so reads report not-found
// and writes fail closed with ErrDependencyUnavailable rather than inventing
// evidence or admitting unbacked state (capability semantics section 9).
type FailClosedCapabilityStore struct{}

// NewFailClosedCapabilityStore builds the fail-closed foundation store.
func NewFailClosedCapabilityStore() *FailClosedCapabilityStore {
	return &FailClosedCapabilityStore{}
}

// Get reports that no snapshot exists because no durable ledger is configured.
func (*FailClosedCapabilityStore) Get(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (domain.CapabilitySnapshot, error) {
	return domain.CapabilitySnapshot{}, ports.ErrCapabilitySnapshotNotFound
}

// List returns an empty Tenant projection because no durable ledger exists.
func (*FailClosedCapabilityStore) List(context.Context, domain.SecurityPrincipal) ([]domain.CapabilitySnapshot, error) {
	return nil, nil
}

// Put fails closed because no durable capability ledger is configured.
func (*FailClosedCapabilityStore) Put(context.Context, domain.SecurityPrincipal, domain.CapabilitySnapshot) error {
	return ports.ErrDependencyUnavailable
}

// FailClosedCapabilityAdapter is the production foundation Capability Adapter.
// No real Provider capability surface is wired yet, so observation fails closed
// with ErrDependencyUnavailable rather than inventing model/operation facts.
type FailClosedCapabilityAdapter struct{}

// NewFailClosedCapabilityAdapter builds the fail-closed foundation adapter.
func NewFailClosedCapabilityAdapter() *FailClosedCapabilityAdapter {
	return &FailClosedCapabilityAdapter{}
}

// Observe fails closed because no Provider capability surface is configured.
func (*FailClosedCapabilityAdapter) Observe(context.Context, ports.CapabilityObservationCommand) (ports.CapabilityObservation, error) {
	return ports.CapabilityObservation{}, ports.ErrDependencyUnavailable
}

var (
	_ ports.CapabilityStore   = (*FailClosedCapabilityStore)(nil)
	_ ports.CapabilityAdapter = (*FailClosedCapabilityAdapter)(nil)
)
