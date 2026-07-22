package application

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

const (
	operationGetCapabilitySnapshot domain.OperationToken = "get_capability_snapshot"
	operationListModels            domain.OperationToken = "list_models"
)

// GetCapabilitySnapshotQuery is the typed snapshot-read request.
type GetCapabilitySnapshotQuery struct {
	PresentedKeyMaterial string
	AccountID            domain.ProviderAccountID
	RequestID            domain.Identifier
}

// ListModelsQuery is the typed offerable-models list request.
type ListModelsQuery struct {
	PresentedKeyMaterial string
	RequestID            domain.Identifier
}

// CapabilitySnapshotResult carries one safe snapshot projection plus the
// server-owned request id.
type CapabilitySnapshotResult struct {
	Snapshot  domain.CapabilitySnapshot
	RequestID domain.Identifier
}

// ModelListResult carries the offerable model list for the authenticated Tenant.
type ModelListResult struct {
	Offers    []domain.ModelOffer
	RequestID domain.Identifier
}

// GetCapabilitySnapshot reads one same-Tenant Capability Snapshot, including
// stale or invalid evidence for operator inspection. Missing snapshots map to
// capability_unverified. The read has no Vault decrypt purpose and never
// invents models (capability semantics sections 9.3 and 11).
func (service *ProviderAccountService) GetCapabilitySnapshot(ctx context.Context, query GetCapabilitySnapshotQuery) (CapabilitySnapshotResult, error) {
	sc := spineContext{operation: operationGetCapabilitySnapshot, requestID: service.resolveRequestID(query.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: query.PresentedKeyMaterial})
	if !ok {
		return CapabilitySnapshotResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1: snapshot read accepts accounts.read OR capabilities.read.
	if !principal.Scopes.Has(domain.ScopeAccountsRead) && !principal.Scopes.Has(domain.ScopeCapabilitiesRead) {
		return CapabilitySnapshotResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// Same-Tenant ownership before any capability disclosure.
	account, err := service.accounts.Visible(ctx, principal, query.AccountID)
	if err != nil {
		return CapabilitySnapshotResult{}, service.fail(ctx, sc, service.visibilityCanonical(err))
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationGetCapabilitySnapshot)
	if !ok {
		return CapabilitySnapshotResult{}, service.fail(ctx, sc, canonical)
	}
	service.release(ctx, reservation)

	snapshot, err := service.capabilities.Get(ctx, principal, account.ID)
	if err != nil {
		if errors.Is(err, ports.ErrCapabilitySnapshotNotFound) {
			return CapabilitySnapshotResult{}, service.fail(ctx, sc, domain.NewCapabilityUnverified())
		}
		return CapabilitySnapshotResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	// Management read recomputes freshness at observation time so operators can
	// inspect stale/invalid evidence without authorizing execution.
	snapshot = snapshot.WithDerivedFreshness(sc.start)
	service.observeSuccess(ctx, sc, ports.AuditCapabilitySnapshotRead, principal, account.ID, 200)
	return CapabilitySnapshotResult{Snapshot: snapshot, RequestID: sc.requestID}, nil
}

// ListModels returns only currently offerable model/operation pairs owned by
// the authenticated Tenant. Stale, invalid, unsupported, and unverified
// evidence never appears. Requires capabilities.read.
func (service *ProviderAccountService) ListModels(ctx context.Context, query ListModelsQuery) (ModelListResult, error) {
	sc := spineContext{operation: operationListModels, requestID: service.resolveRequestID(query.RequestID), start: service.clock.Now()}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: query.PresentedKeyMaterial})
	if !ok {
		return ModelListResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	if !principal.Scopes.Has(domain.ScopeCapabilitiesRead) {
		return ModelListResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	reservation, canonical, ok := service.admit(ctx, principal, operationListModels)
	if !ok {
		return ModelListResult{}, service.fail(ctx, sc, canonical)
	}

	accounts, err := service.accounts.List(ctx, principal)
	if err != nil {
		service.release(ctx, reservation)
		return ModelListResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	snapshots, err := service.capabilities.List(ctx, principal)
	if err != nil {
		service.release(ctx, reservation)
		return ModelListResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	service.release(ctx, reservation)

	usable := make(map[domain.ProviderAccountID]domain.ProviderAccount, len(accounts))
	for _, account := range accounts {
		if !service.accountAllowsOffers(account) {
			continue
		}
		usable[account.ID] = account
	}

	offers := make([]domain.ModelOffer, 0)
	for _, snapshot := range snapshots {
		account, ok := usable[snapshot.ProviderAccountID]
		if !ok {
			continue
		}
		// Version binding: a snapshot for an old credential version cannot offer
		// pairs for the current account version (I-CAP-VERSION-BIND).
		if snapshot.CredentialVersion != account.Credential.Version {
			continue
		}
		if snapshot.AuthMode != account.AuthMode {
			continue
		}
		derived := snapshot.WithDerivedFreshness(sc.start)
		if derived.Freshness != domain.SnapshotFresh {
			continue
		}
		for _, model := range derived.Models {
			for _, op := range domain.PrimaryCapabilityOperations() {
				if !derived.IsOfferablePair(op, model, sc.start) {
					continue
				}
				status := model.Operations[op]
				offer := domain.ModelOffer{
					ProviderAccountID: derived.ProviderAccountID,
					Operation:         op,
					OperationStatus:   status,
					ModelSlug:         model.ModelSlug,
					Offerable:         true,
					Freshness:         domain.SnapshotFresh,
					VerifiedAt:        derived.VerifiedAt,
				}
				if op == domain.CapabilityOpChatStreaming {
					if fact, ok := derived.Operations[op]; ok {
						offer.StreamingClass = fact.StreamingClass
					}
				}
				offers = append(offers, offer)
			}
		}
	}

	service.observeSuccess(ctx, sc, ports.AuditModelsListed, principal, "", 200)
	return ModelListResult{Offers: offers, RequestID: sc.requestID}, nil
}

// accountAllowsOffers applies the account usability and risk gates that keep a
// snapshot non-offerable even when its facts are still fresh
// (I-CAP-OFFERABLE / I-SNAPSHOT-NONUSE).
func (service *ProviderAccountService) accountAllowsOffers(account domain.ProviderAccount) bool {
	if account.Lifecycle != domain.LifecycleActive {
		return false
	}
	if account.AuthMode.Prohibited() {
		return false
	}
	if !account.Controls.AuthModeExecutionEnabled {
		return false
	}
	if account.AuthMode.RequiresRiskAck() && !account.RiskAcknowledged {
		return false
	}
	return true
}

// mintCapabilitySnapshot observes and stores a credential-version-bound
// snapshot after a successful probe. Observation failure fails closed and
// prevents activation so the account never becomes active without capability
// evidence for this slice.
func (service *ProviderAccountService) mintCapabilitySnapshot(ctx context.Context, principal domain.SecurityPrincipal, account domain.ProviderAccount) error {
	observation, err := service.capability.Observe(ctx, ports.CapabilityObservationCommand{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Version:   account.Credential.Version,
	})
	if err != nil {
		return err
	}
	snapshot := domain.NewLiveProbeSnapshot(
		account.ID,
		account.AuthMode,
		account.Credential.Version,
		domain.NewTimestamp(service.clock.Now()),
		observation.Operations,
		observation.Models,
		observation.ProbeSurface,
	)
	return service.capabilities.Put(ctx, principal, snapshot)
}
