package vault

import (
	"context"

	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// FailClosedCredentialVault is the production foundation Credential Vault. No
// real envelope-encryption backend is wired yet (KMS/HSM topology is deferred
// to a later ticket), so every protected operation fails closed with
// ErrDependencyUnavailable rather than storing or validating material with a
// weaker guarantee. This keeps the real production composition constructor safe
// by default: a credential submission returns a 503-class dependency error and
// no account can advance toward `active` until a real Vault lands (credential
// vault spec §1.2, I-FAIL-CLOSED-SENSITIVE).
type FailClosedCredentialVault struct{}

// NewFailClosedCredentialVault builds the fail-closed foundation Vault.
func NewFailClosedCredentialVault() *FailClosedCredentialVault {
	return &FailClosedCredentialVault{}
}

// Put fails closed because no encryption backend is configured. The transient
// material is discarded without being stored, logged, or echoed.
func (*FailClosedCredentialVault) Put(context.Context, ports.CredentialIntake) error {
	return ports.ErrDependencyUnavailable
}

// Validate fails closed because no stored material can be read.
func (*FailClosedCredentialVault) Validate(context.Context, ports.CredentialValidation) (ports.CredentialValidationResult, error) {
	return ports.CredentialValidationResult{}, ports.ErrDependencyUnavailable
}

// Revoke fails closed because no credential store is configured.
func (*FailClosedCredentialVault) Revoke(context.Context, ports.CredentialValidation) error {
	return ports.ErrDependencyUnavailable
}

// FailClosedProbeAdapter is the production foundation Probe Adapter. No real
// Provider probe surface is wired yet, so every probe fails closed with
// ErrDependencyUnavailable. It never reports Authenticated=false (which would
// wrongly classify the account as credential-rejected); an absent adapter is a
// dependency outage, not a credential verdict (connection lifecycle spec §4.6,
// I-PROBE-MINIMAL).
type FailClosedProbeAdapter struct{}

// NewFailClosedProbeAdapter builds the fail-closed foundation Probe Adapter.
func NewFailClosedProbeAdapter() *FailClosedProbeAdapter {
	return &FailClosedProbeAdapter{}
}

// Probe fails closed because no Provider probe surface is configured.
func (*FailClosedProbeAdapter) Probe(context.Context, ports.ProbeCommand) (ports.ProbeOutcome, error) {
	return ports.ProbeOutcome{}, ports.ErrDependencyUnavailable
}

var (
	_ ports.CredentialVault = (*FailClosedCredentialVault)(nil)
	_ ports.ProbeAdapter    = (*FailClosedProbeAdapter)(nil)
)
