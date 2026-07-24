// Package persistence owns physical durable state and atomic transitions.
package persistence

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// healthRecord is one durable health projection keyed by Tenant and account.
type healthRecord struct {
	TenantID       domain.TenantID          `json:"tenant_id"`
	AccountID      domain.ProviderAccountID `json:"account_id"`
	Health         domain.HealthSummary     `json:"health"`
	RecoveryPermit domain.RecoveryPermit    `json:"recovery_permit"`
}

// MemoryHealthStore is the in-process foundation HealthStore.
type MemoryHealthStore struct {
	mu       sync.Mutex
	byTenant map[domain.TenantID]map[domain.ProviderAccountID]healthRecord
}

// NewMemoryHealthStore builds an empty foundation health store.
func NewMemoryHealthStore() *MemoryHealthStore {
	return &MemoryHealthStore{byTenant: make(map[domain.TenantID]map[domain.ProviderAccountID]healthRecord)}
}

func (*MemoryHealthStore) Restore(context.Context) error { return nil }

func (store *MemoryHealthStore) Read(_ context.Context, principal domain.SecurityPrincipal, id domain.ProviderAccountID) (ports.AccountHealth, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.readLocked(principal.TenantID, id)
}

func (store *MemoryHealthStore) readLocked(tenant domain.TenantID, id domain.ProviderAccountID) (ports.AccountHealth, error) {
	accounts, ok := store.byTenant[tenant]
	if !ok {
		return ports.AccountHealth{}, ports.ErrHealthNotFound
	}
	record, ok := accounts[id]
	if !ok {
		return ports.AccountHealth{}, ports.ErrHealthNotFound
	}
	return ports.AccountHealth{Health: record.Health, RecoveryPermit: record.RecoveryPermit}, nil
}

func (store *MemoryHealthStore) putLocked(record healthRecord) {
	accounts, ok := store.byTenant[record.TenantID]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]healthRecord)
		store.byTenant[record.TenantID] = accounts
	}
	accounts[record.AccountID] = record
}

func (store *MemoryHealthStore) getLocked(tenant domain.TenantID, id domain.ProviderAccountID) (healthRecord, bool) {
	accounts, ok := store.byTenant[tenant]
	if !ok {
		return healthRecord{}, false
	}
	record, ok := accounts[id]
	return record, ok
}

// Seed installs a health projection for independently controlled fixtures.
func (store *MemoryHealthStore) Seed(tenant domain.TenantID, id domain.ProviderAccountID, health domain.HealthSummary, permit domain.RecoveryPermit) {
	store.mu.Lock()
	defer store.mu.Unlock()
	store.putLocked(healthRecord{TenantID: tenant, AccountID: id, Health: health, RecoveryPermit: permit})
}

func (store *MemoryHealthStore) Initialize(ctx context.Context, init ports.HealthInitialize) (ports.AccountHealth, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.getLocked(init.Principal.TenantID, init.AccountID); ok {
		return ports.AccountHealth{}, ports.ErrAccountUpdateConflict
	}
	record := healthRecord{TenantID: init.Principal.TenantID, AccountID: init.AccountID, Health: init.Health}
	tr, err := commitMutation(ctx, init.Audit, true, []ports.HealthTransition{{
		NewCondition: conditionAt(init.Health, domain.HealthScope{Kind: domain.HealthScopeAccount}),
		Scope:        domain.HealthScope{Kind: domain.HealthScopeAccount},
		Outcome:      "health_initialize",
	}}, store.memPut, record)
	if err != nil {
		return ports.AccountHealth{}, err
	}
	return tr.Result, nil
}

func (store *MemoryHealthStore) memPut(record healthRecord) error {
	record.Health.SummaryState = worstSummary(record.Health.Conditions)
	if err := validateHealthRecord(record); err != nil {
		return err
	}
	store.putLocked(record)
	return nil
}

func (store *MemoryHealthStore) ObserveCooldown(ctx context.Context, obs ports.CooldownObservation) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return applyObserveCooldown(ctx, store.getLocked, store.memPut, obs)
}

func (store *MemoryHealthStore) ClaimRecoveryPermit(ctx context.Context, claim ports.RecoveryPermitClaim) (ports.ClaimResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return applyClaim(ctx, store.getLocked, store.memPut, claim)
}

func (store *MemoryHealthStore) RenewAfterDependencyFailure(ctx context.Context, renew ports.DependencyFailureRenewal) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return applyDependencyRenewal(ctx, store.getLocked, store.memPut, renew)
}

func (store *MemoryHealthStore) ResolveRecovery(ctx context.Context, resolution ports.RecoveryResolution) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return applyResolve(ctx, store.getLocked, store.memPut, resolution)
}

func (store *MemoryHealthStore) RecordHardFailure(ctx context.Context, obs ports.HardFailureObservation) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return applyHardFailure(ctx, store.getLocked, store.memPut, obs)
}

func (store *MemoryHealthStore) ResetForEnableProbe(ctx context.Context, reset ports.EnableProbeReset) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return applyEnableReset(ctx, store.getLocked, store.memPut, reset)
}

func (store *MemoryHealthStore) ClearPermit(ctx context.Context, clear ports.PermitClear) (ports.AccountHealth, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return applyClearPermit(ctx, store.getLocked, store.memPut, clear)
}

func (store *MemoryHealthStore) RecordActivation(ctx context.Context, act ports.ActivationHealth) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return applyActivation(ctx, store.getLocked, store.memPut, act)
}

func (store *MemoryHealthStore) ResetForCredentialEpoch(ctx context.Context, reset ports.CredentialEpochReset) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return applyCredentialEpoch(ctx, store.getLocked, store.memPut, reset)
}

// FileHealthStore is a durable HealthStore with append-only ledger and exclusive lock.
type FileHealthStore struct {
	mu    sync.Mutex
	path  string
	lock  string
	byKey map[healthKey]healthRecord
}

type healthKey struct {
	tenant  domain.TenantID
	account domain.ProviderAccountID
}

func NewFileHealthStore(path string) *FileHealthStore {
	return &FileHealthStore{path: path, lock: path + ".lock", byKey: make(map[healthKey]healthRecord)}
}

func (store *FileHealthStore) Restore(ctx context.Context) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	// Same O_EXCL + reload path as Read/writes so startup cannot observe a
	// partial append while another process holds the exclusive lock.
	unlock, err := store.acquireLock()
	if err != nil {
		return err
	}
	defer unlock()
	return store.reloadLocked(ctx)
}

func (store *FileHealthStore) Read(ctx context.Context, principal domain.SecurityPrincipal, id domain.ProviderAccountID) (ports.AccountHealth, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	// Exclusive lock + reload: cross-process writers serialize; readers never
	// observe a partial append or a stale process-local cache.
	unlock, err := store.acquireLock()
	if err != nil {
		return ports.AccountHealth{}, err
	}
	defer unlock()
	if err := store.reloadLocked(ctx); err != nil {
		return ports.AccountHealth{}, err
	}
	record, ok := store.byKey[healthKey{tenant: principal.TenantID, account: id}]
	if !ok {
		return ports.AccountHealth{}, ports.ErrHealthNotFound
	}
	return ports.AccountHealth{Health: record.Health, RecoveryPermit: record.RecoveryPermit}, nil
}

func (store *FileHealthStore) withExclusiveWrite(fn func() error) error {
	unlock, err := store.acquireLock()
	if err != nil {
		return err
	}
	defer unlock()
	if err := store.reloadLocked(context.Background()); err != nil {
		return err
	}
	return fn()
}

func (store *FileHealthStore) getLocked(tenant domain.TenantID, id domain.ProviderAccountID) (healthRecord, bool) {
	record, ok := store.byKey[healthKey{tenant: tenant, account: id}]
	return record, ok
}

func (store *FileHealthStore) putLocked(record healthRecord) {
	store.byKey[healthKey{tenant: record.TenantID, account: record.AccountID}] = record
}

func (store *FileHealthStore) persistPut(record healthRecord) error {
	record.Health.SummaryState = worstSummary(record.Health.Conditions)
	if err := validateHealthRecord(record); err != nil {
		return err
	}
	if err := store.appendLocked(record); err != nil {
		return err
	}
	store.putLocked(record)
	return nil
}

func (store *FileHealthStore) Initialize(ctx context.Context, init ports.HealthInitialize) (ports.AccountHealth, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.AccountHealth
	err := store.withExclusiveWrite(func() error {
		if _, ok := store.getLocked(init.Principal.TenantID, init.AccountID); ok {
			return ports.ErrAccountUpdateConflict
		}
		record := healthRecord{TenantID: init.Principal.TenantID, AccountID: init.AccountID, Health: init.Health}
		tr, err := commitMutation(ctx, init.Audit, true, []ports.HealthTransition{{
			NewCondition: conditionAt(init.Health, domain.HealthScope{Kind: domain.HealthScopeAccount}),
			Scope:        domain.HealthScope{Kind: domain.HealthScopeAccount},
			Outcome:      "health_initialize",
		}}, store.persistPut, record)
		if err != nil {
			return err
		}
		out = tr.Result
		return nil
	})
	return out, err
}

func (store *FileHealthStore) ObserveCooldown(ctx context.Context, obs ports.CooldownObservation) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.HealthTransition
	err := store.withExclusiveWrite(func() error {
		tr, err := applyObserveCooldown(ctx, store.getLocked, store.persistPut, obs)
		out = tr
		return err
	})
	return out, err
}

func (store *FileHealthStore) ClaimRecoveryPermit(ctx context.Context, claim ports.RecoveryPermitClaim) (ports.ClaimResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.ClaimResult
	err := store.withExclusiveWrite(func() error {
		res, err := applyClaim(ctx, store.getLocked, store.persistPut, claim)
		out = res
		return err
	})
	return out, err
}

func (store *FileHealthStore) RenewAfterDependencyFailure(ctx context.Context, renew ports.DependencyFailureRenewal) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.HealthTransition
	err := store.withExclusiveWrite(func() error {
		tr, err := applyDependencyRenewal(ctx, store.getLocked, store.persistPut, renew)
		out = tr
		return err
	})
	return out, err
}

func (store *FileHealthStore) ResolveRecovery(ctx context.Context, resolution ports.RecoveryResolution) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.HealthTransition
	err := store.withExclusiveWrite(func() error {
		tr, err := applyResolve(ctx, store.getLocked, store.persistPut, resolution)
		out = tr
		return err
	})
	return out, err
}

func (store *FileHealthStore) RecordHardFailure(ctx context.Context, obs ports.HardFailureObservation) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.HealthTransition
	err := store.withExclusiveWrite(func() error {
		tr, err := applyHardFailure(ctx, store.getLocked, store.persistPut, obs)
		out = tr
		return err
	})
	return out, err
}

func (store *FileHealthStore) ResetForEnableProbe(ctx context.Context, reset ports.EnableProbeReset) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.HealthTransition
	err := store.withExclusiveWrite(func() error {
		tr, err := applyEnableReset(ctx, store.getLocked, store.persistPut, reset)
		out = tr
		return err
	})
	return out, err
}

func (store *FileHealthStore) ClearPermit(ctx context.Context, clear ports.PermitClear) (ports.AccountHealth, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.AccountHealth
	err := store.withExclusiveWrite(func() error {
		res, err := applyClearPermit(ctx, store.getLocked, store.persistPut, clear)
		out = res
		return err
	})
	return out, err
}

func (store *FileHealthStore) RecordActivation(ctx context.Context, act ports.ActivationHealth) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.HealthTransition
	err := store.withExclusiveWrite(func() error {
		tr, err := applyActivation(ctx, store.getLocked, store.persistPut, act)
		out = tr
		return err
	})
	return out, err
}

func (store *FileHealthStore) ResetForCredentialEpoch(ctx context.Context, reset ports.CredentialEpochReset) (ports.HealthTransition, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	var out ports.HealthTransition
	err := store.withExclusiveWrite(func() error {
		tr, err := applyCredentialEpoch(ctx, store.getLocked, store.persistPut, reset)
		out = tr
		return err
	})
	return out, err
}

// --- shared logical transition implementations ---

type getFn func(domain.TenantID, domain.ProviderAccountID) (healthRecord, bool)
type putFn func(healthRecord) error

func requireAudit(audit ports.HealthMutationAudit) error {
	if audit == nil {
		return ports.ErrRequiredHealthAudit
	}
	return nil
}

// commitMutation validates the final record, invokes the batch audit once under
// the same serialization boundary, then persists. No partial audit at the store
// boundary: either the full transitions slice is accepted or nothing is written.
func commitMutation(ctx context.Context, audit ports.HealthMutationAudit, required bool, transitions []ports.HealthTransition, put putFn, record healthRecord) (ports.HealthTransition, error) {
	if required {
		if err := requireAudit(audit); err != nil {
			return ports.HealthTransition{}, err
		}
	}
	record.Health.SummaryState = worstSummary(record.Health.Conditions)
	if err := validateHealthRecord(record); err != nil {
		return ports.HealthTransition{}, err
	}
	result := ports.AccountHealth{Health: record.Health, RecoveryPermit: record.RecoveryPermit}
	for i := range transitions {
		transitions[i].Result = result
	}
	if audit != nil {
		if err := audit(ctx, transitions); err != nil {
			return ports.HealthTransition{}, err
		}
	}
	if err := put(record); err != nil {
		return ports.HealthTransition{}, err
	}
	// Return the last transition as the mutation primary (fresh scope / sole event).
	last := transitions[len(transitions)-1]
	last.Result = result
	return last, nil
}

func applyObserveCooldown(ctx context.Context, get getFn, put putFn, obs ports.CooldownObservation) (ports.HealthTransition, error) {
	if err := requireAudit(obs.Audit); err != nil {
		return ports.HealthTransition{}, err
	}
	record, ok := get(obs.Principal.TenantID, obs.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	priorPermit := record.RecoveryPermit
	if obs.ConsumePermit.Owner != "" {
		if record.RecoveryPermit.Owner != obs.ConsumePermit.Owner {
			return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
		}
		if !healthHasExactCondition(record, obs.ConsumePermit) {
			return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
		}
	}

	var batch []ports.HealthTransition
	// Claimed-scope renew uses ITS timer/reason/source — never the fresh scope's.
	if obs.ConsumePermit.Owner != "" && !sameHealthScope(obs.ConsumePermit.Scope, obs.Scope) && obs.ClaimedScopeReason != "" {
		priorClaimed := conditionAt(record.Health, obs.ConsumePermit.Scope)
		claimedSource := obs.ClaimedScopeSourceClass
		if claimedSource == "" {
			claimedSource = domain.HealthSourceRecoveryProbe
		}
		proj := domain.ProviderAccount{Health: record.Health, Credential: domain.CredentialMetadata{Version: obs.CredentialVersion}}
		proj = proj.WithScopedCooldownSource(obs.ObservedAt, obs.ConsumePermit.Scope, obs.ClaimedScopeReason, obs.ClaimedScopeRetryNotBefore, obs.CredentialVersion, claimedSource)
		record.Health = proj.Health
		record.RecoveryPermit = domain.RecoveryPermit{}
		claimedNew := conditionAt(record.Health, obs.ConsumePermit.Scope)
		batch = append(batch, ports.HealthTransition{
			PriorCondition: priorClaimed, NewCondition: claimedNew, PriorPermit: priorPermit,
			Scope: claimedNew.Scope, Outcome: "cooldown_renew",
		})
		priorPermit = domain.RecoveryPermit{}
	}

	prior := conditionAt(record.Health, obs.Scope)
	source := obs.SourceClass
	if source == "" {
		source = domain.HealthSourceUpstreamAttempt
	}
	proj := domain.ProviderAccount{Health: record.Health, Credential: domain.CredentialMetadata{Version: obs.CredentialVersion}}
	proj = proj.WithScopedCooldownSource(obs.ObservedAt, obs.Scope, obs.Reason, obs.RetryNotBefore, obs.CredentialVersion, source)
	record.Health = proj.Health
	if obs.ConsumePermit.Owner != "" || sameHealthScope(priorPermit.Scope, obs.Scope) {
		record.RecoveryPermit = domain.RecoveryPermit{}
	}
	newCond := conditionAt(record.Health, obs.Scope)
	outcome := "cooldown_create"
	if prior.ConditionRevision > 0 {
		outcome = "cooldown_renew"
	}
	batch = append(batch, ports.HealthTransition{
		PriorCondition: prior, NewCondition: newCond, PriorPermit: priorPermit,
		NewPermit: record.RecoveryPermit, Scope: newCond.Scope, Outcome: outcome,
	})
	return commitMutation(ctx, obs.Audit, true, batch, put, record)
}

// applyClearPermit drops the private recovery permit under optional CAS fencing.
// Empty ExpectedPermit is administrative unconditional clear; non-empty requires
// exact Owner+Scope+ConditionRevision+CredentialVersion match or returns
// ErrAccountUpdateConflict without mutating the durable row.
func applyClearPermit(ctx context.Context, get getFn, put putFn, clear ports.PermitClear) (ports.AccountHealth, error) {
	record, ok := get(clear.Principal.TenantID, clear.AccountID)
	if !ok {
		return ports.AccountHealth{}, ports.ErrHealthNotFound
	}
	prior := record.RecoveryPermit
	if prior.Owner == "" {
		// No observable control transition — nothing to audit or persist.
		return ports.AccountHealth{Health: record.Health, RecoveryPermit: record.RecoveryPermit}, nil
	}
	// Request-owned cleanup must not delete a newer/different owner's permit.
	if clear.ExpectedPermit.Owner != "" {
		if !sameRecoveryPermit(prior, clear.ExpectedPermit) {
			return ports.AccountHealth{}, ports.ErrAccountUpdateConflict
		}
	}
	record.RecoveryPermit = domain.RecoveryPermit{}
	tr, err := commitMutation(ctx, clear.Audit, true, []ports.HealthTransition{{
		PriorPermit: prior, Scope: prior.Scope, Outcome: "permit_clear",
	}}, put, record)
	if err != nil {
		return ports.AccountHealth{}, err
	}
	return tr.Result, nil
}

func sameRecoveryPermit(a, b domain.RecoveryPermit) bool {
	return a.Owner == b.Owner &&
		sameHealthScope(a.Scope, b.Scope) &&
		a.ConditionRevision == b.ConditionRevision &&
		a.CredentialVersion == b.CredentialVersion
}

func applyClaim(ctx context.Context, get getFn, put putFn, claim ports.RecoveryPermitClaim) (ports.ClaimResult, error) {
	// Claim audit is optional (private fencing).
	record, ok := get(claim.Principal.TenantID, claim.AccountID)
	if !ok {
		return ports.ClaimResult{}, ports.ErrHealthNotFound
	}
	if record.RecoveryPermit.Owner != "" {
		return ports.ClaimResult{}, ports.ErrAccountUpdateConflict
	}
	permit := domain.RecoveryPermit{
		Owner: claim.Owner, Scope: claim.Scope,
		ConditionRevision: claim.ConditionRevision, CredentialVersion: claim.CredentialVersion,
	}
	if !healthHasExactCondition(record, permit) {
		return ports.ClaimResult{}, ports.ErrAccountUpdateConflict
	}
	priorPermit := record.RecoveryPermit
	record.RecoveryPermit = permit
	tr, err := commitMutation(ctx, claim.Audit, false, []ports.HealthTransition{{
		PriorPermit: priorPermit, NewPermit: permit, Scope: permit.Scope, Outcome: "recovery_permit_claim",
		PriorCondition: conditionAt(record.Health, permit.Scope), NewCondition: conditionAt(record.Health, permit.Scope),
	}}, put, record)
	if err != nil {
		return ports.ClaimResult{}, err
	}
	return ports.ClaimResult{Permit: permit, Result: tr.Result}, nil
}

func applyDependencyRenewal(ctx context.Context, get getFn, put putFn, renew ports.DependencyFailureRenewal) (ports.HealthTransition, error) {
	if err := requireAudit(renew.Audit); err != nil {
		return ports.HealthTransition{}, err
	}
	record, ok := get(renew.Principal.TenantID, renew.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	if record.RecoveryPermit.Owner != renew.Permit.Owner {
		return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
	}
	if !healthHasExactCondition(record, renew.Permit) {
		return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
	}
	prior := conditionAt(record.Health, renew.Permit.Scope)
	reason := prior.Reason
	if reason == "" {
		reason = domain.HealthReasonProviderRateLimited
	}
	priorPermit := record.RecoveryPermit
	proj := domain.ProviderAccount{Health: record.Health, Credential: domain.CredentialMetadata{Version: renew.Permit.CredentialVersion}}
	proj = proj.WithScopedCooldownSource(
		renew.ObservedAt, renew.Permit.Scope, reason, renew.RetryNotBefore,
		renew.Permit.CredentialVersion, domain.HealthSourceRecoveryProbe,
	)
	record.Health = proj.Health
	record.RecoveryPermit = domain.RecoveryPermit{}
	newCond := conditionAt(record.Health, renew.Permit.Scope)
	return commitMutation(ctx, renew.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: newCond, PriorPermit: priorPermit,
		Scope: newCond.Scope, Outcome: "dependency_failure_renewal",
	}}, put, record)
}

func applyResolve(ctx context.Context, get getFn, put putFn, resolution ports.RecoveryResolution) (ports.HealthTransition, error) {
	if err := requireAudit(resolution.Audit); err != nil {
		return ports.HealthTransition{}, err
	}
	record, ok := get(resolution.Principal.TenantID, resolution.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	if record.RecoveryPermit.Owner != resolution.Permit.Owner {
		return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
	}
	if !healthHasExactCondition(record, resolution.Permit) {
		return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
	}
	prior := conditionAt(record.Health, resolution.Permit.Scope)
	priorPermit := record.RecoveryPermit
	proj := domain.ProviderAccount{Health: record.Health, RecoveryPermit: record.RecoveryPermit, Credential: domain.CredentialMetadata{Version: resolution.Permit.CredentialVersion}}
	proj = proj.WithScopedRecovery(resolution.ObservedAt, resolution.Permit)
	record.Health = proj.Health
	record.RecoveryPermit = domain.RecoveryPermit{}
	return commitMutation(ctx, resolution.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: conditionAt(record.Health, resolution.Permit.Scope),
		PriorPermit: priorPermit, Scope: resolution.Permit.Scope, Outcome: "recovery_success",
	}}, put, record)
}

func applyHardFailure(ctx context.Context, get getFn, put putFn, obs ports.HardFailureObservation) (ports.HealthTransition, error) {
	if err := requireAudit(obs.Audit); err != nil {
		return ports.HealthTransition{}, err
	}
	record, ok := get(obs.Principal.TenantID, obs.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	if obs.ConsumePermit.Owner != "" {
		if record.RecoveryPermit.Owner != obs.ConsumePermit.Owner ||
			!sameHealthScope(record.RecoveryPermit.Scope, obs.ConsumePermit.Scope) ||
			record.RecoveryPermit.ConditionRevision != obs.ConsumePermit.ConditionRevision ||
			record.RecoveryPermit.CredentialVersion != obs.ConsumePermit.CredentialVersion {
			return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
		}
		if !healthHasExactCondition(record, obs.ConsumePermit) {
			return ports.HealthTransition{}, ports.ErrAccountUpdateConflict
		}
	}
	priorPermit := record.RecoveryPermit
	prior := conditionAt(record.Health, domain.HealthScope{Kind: domain.HealthScopeAccount})
	record.RecoveryPermit = domain.RecoveryPermit{}

	if obs.PendingOnly {
		// Durable hard fence for the failed pending version: persist account-scope
		// expired/credential_rejected at CredentialVersion while keeping all
		// other-version (origin) scopes. Condition identity is scope+version.
		if obs.CredentialVersion <= 0 {
			return ports.HealthTransition{}, fmt.Errorf("pending-only hard failure requires positive credential version")
		}
		kept := make([]domain.HealthCondition, 0, len(record.Health.Conditions)+1)
		for _, c := range record.Health.Conditions {
			if c.CredentialVersion == obs.CredentialVersion {
				// Replace any provisional evidence for the failed pending version.
				continue
			}
			kept = append(kept, c)
		}
		// Prior account-scope for this pending version (if any) for exact audit.
		priorPending := domain.HealthCondition{}
		for _, c := range record.Health.Conditions {
			if c.CredentialVersion == obs.CredentialVersion && c.Scope.Kind == domain.HealthScopeAccount {
				priorPending = c
				break
			}
		}
		if priorPending.State == "" {
			priorPending = prior
		}
		rejected := domain.HealthCondition{
			Scope:             domain.HealthScope{Kind: domain.HealthScopeAccount},
			State:             domain.HealthExpired,
			Reason:            domain.HealthReasonCredentialRejected,
			CredentialVersion: obs.CredentialVersion,
			ObservedAt:        obs.ObservedAt,
			Remediation:       domain.RemediationReauthenticate,
			ConditionRevision: 1,
			SourceClass:       domain.HealthSourceRequiredProbe,
		}
		if priorPending.ConditionRevision > 0 {
			rejected.ConditionRevision = priorPending.ConditionRevision + 1
		}
		kept = append(kept, rejected)
		record.Health.Conditions = kept
		return commitMutation(ctx, obs.Audit, true, []ports.HealthTransition{{
			PriorCondition: priorPending, NewCondition: rejected, PriorPermit: priorPermit,
			Scope: rejected.Scope, Outcome: "pending_hard_rejection",
		}}, put, record)
	}

	proj := domain.ProviderAccount{
		Health: record.Health, Credential: domain.CredentialMetadata{Version: obs.CredentialVersion},
	}
	proj = proj.WithCredentialRejected(obs.ObservedAt)
	record.Health = proj.Health
	newCond := conditionAt(record.Health, domain.HealthScope{Kind: domain.HealthScopeAccount})
	return commitMutation(ctx, obs.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: newCond, PriorPermit: priorPermit,
		Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, Outcome: "hard_auth_rejection",
	}}, put, record)
}

func applyEnableReset(ctx context.Context, get getFn, put putFn, reset ports.EnableProbeReset) (ports.HealthTransition, error) {
	if err := requireAudit(reset.Audit); err != nil {
		return ports.HealthTransition{}, err
	}
	record, ok := get(reset.Principal.TenantID, reset.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	priorPermit := record.RecoveryPermit
	prior := conditionAtVersion(record.Health, domain.HealthScope{Kind: domain.HealthScopeAccount}, reset.CredentialVersion)
	// Reset only current-version account-scope; keep other versions and op/model scopes.
	kept := make([]domain.HealthCondition, 0, len(record.Health.Conditions)+1)
	for _, c := range record.Health.Conditions {
		if c.Scope.Kind == domain.HealthScopeAccount && c.CredentialVersion == reset.CredentialVersion {
			continue
		}
		// Also drop legacy account-scope rows that lack version fencing when
		// resetting the current credential epoch.
		if c.Scope.Kind == domain.HealthScopeAccount && reset.CredentialVersion > 0 && c.CredentialVersion == 0 {
			continue
		}
		kept = append(kept, c)
	}
	accountUnknown := domain.HealthCondition{
		Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthUnknown,
		Reason: domain.HealthReasonInitialUnprobed, CredentialVersion: reset.CredentialVersion,
		ObservedAt: reset.ObservedAt, Remediation: domain.RemediationNone,
		SourceClass: domain.HealthSourceRequiredProbe,
	}
	kept = append(kept, accountUnknown)
	record.Health.Conditions = kept
	record.RecoveryPermit = domain.RecoveryPermit{}
	return commitMutation(ctx, reset.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: accountUnknown, PriorPermit: priorPermit,
		Scope: accountUnknown.Scope, Outcome: "enable_probe_reset",
	}}, put, record)
}

func applyActivation(ctx context.Context, get getFn, put putFn, act ports.ActivationHealth) (ports.HealthTransition, error) {
	if err := requireAudit(act.Audit); err != nil {
		return ports.HealthTransition{}, err
	}
	record, ok := get(act.Principal.TenantID, act.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	prior := conditionAt(record.Health, domain.HealthScope{Kind: domain.HealthScopeAccount})
	healthy := domain.HealthCondition{
		Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthHealthy,
		Reason: domain.HealthReasonProbeSucceeded, CredentialVersion: act.CredentialVersion,
		ObservedAt: act.ObservedAt, Remediation: domain.RemediationNone,
		SourceClass: domain.HealthSourceRequiredProbe,
	}
	// Merge account-scope for THIS credential version only; keep other versions
	// and all operation/model scopes (I-HEALTH-SCOPED + version coexistence).
	prior = conditionAtVersion(record.Health, domain.HealthScope{Kind: domain.HealthScopeAccount}, act.CredentialVersion)
	conditions := make([]domain.HealthCondition, 0, len(record.Health.Conditions)+1)
	replaced := false
	for _, c := range record.Health.Conditions {
		if c.Scope.Kind == domain.HealthScopeAccount && c.CredentialVersion == act.CredentialVersion {
			if !replaced {
				conditions = append(conditions, healthy)
				replaced = true
			}
			continue
		}
		conditions = append(conditions, c)
	}
	if !replaced {
		conditions = append(conditions, healthy)
	}
	record.Health.Conditions = conditions
	return commitMutation(ctx, act.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: healthy, Scope: healthy.Scope, Outcome: "activation",
	}}, put, record)
}

func applyCredentialEpoch(ctx context.Context, get getFn, put putFn, reset ports.CredentialEpochReset) (ports.HealthTransition, error) {
	if err := requireAudit(reset.Audit); err != nil {
		return ports.HealthTransition{}, err
	}
	if reset.NewCredentialVersion <= 0 && reset.PreserveCredentialVersion <= 0 {
		return ports.HealthTransition{}, fmt.Errorf("credential epoch requires a positive new or preserve version")
	}
	record, ok := get(reset.Principal.TenantID, reset.AccountID)
	if !ok {
		return ports.HealthTransition{}, ports.ErrHealthNotFound
	}
	priorPermit := record.RecoveryPermit
	prior := conditionAt(record.Health, domain.HealthScope{Kind: domain.HealthScopeAccount})
	record.RecoveryPermit = domain.RecoveryPermit{}

	if reset.PreserveCredentialVersion > 0 {
		// Replacement: keep only still-current usable credential fencing.
		kept := make([]domain.HealthCondition, 0, len(record.Health.Conditions))
		for _, c := range record.Health.Conditions {
			if c.CredentialVersion == reset.PreserveCredentialVersion {
				kept = append(kept, c)
			}
		}
		record.Health.Conditions = kept
		return commitMutation(ctx, reset.Audit, true, []ports.HealthTransition{{
			PriorCondition: prior, NewCondition: conditionAt(record.Health, domain.HealthScope{Kind: domain.HealthScopeAccount}),
			PriorPermit: priorPermit, Scope: domain.HealthScope{Kind: domain.HealthScopeAccount},
			Outcome: "credential_epoch_preserve",
		}}, put, record)
	}

	// First-connect: full epoch reset to unknown at the new credential version.
	unknown := domain.HealthCondition{
		Scope: domain.HealthScope{Kind: domain.HealthScopeAccount}, State: domain.HealthUnknown,
		Reason: domain.HealthReasonInitialUnprobed, CredentialVersion: reset.NewCredentialVersion,
		ObservedAt: reset.ObservedAt, Remediation: domain.RemediationNone,
		SourceClass: domain.HealthSourceRequiredProbe,
	}
	record.Health.Conditions = []domain.HealthCondition{unknown}
	return commitMutation(ctx, reset.Audit, true, []ports.HealthTransition{{
		PriorCondition: prior, NewCondition: unknown, PriorPermit: priorPermit,
		Scope: unknown.Scope, Outcome: "credential_epoch_reset",
	}}, put, record)
}

func conditionAt(summary domain.HealthSummary, scope domain.HealthScope) domain.HealthCondition {
	for _, c := range summary.Conditions {
		if sameHealthScope(c.Scope, scope) {
			return c
		}
	}
	return domain.HealthCondition{}
}

func conditionAtVersion(summary domain.HealthSummary, scope domain.HealthScope, credentialVersion int) domain.HealthCondition {
	for _, c := range summary.Conditions {
		if sameHealthScope(c.Scope, scope) && c.CredentialVersion == credentialVersion {
			return c
		}
	}
	return domain.HealthCondition{}
}

func sameHealthScope(a, b domain.HealthScope) bool {
	return a.Kind == b.Kind && a.Operation == b.Operation && a.ModelSlug == b.ModelSlug
}

func healthHasExactCondition(record healthRecord, required domain.RecoveryPermit) bool {
	for _, condition := range record.Health.Conditions {
		if sameHealthScope(condition.Scope, required.Scope) &&
			condition.ConditionRevision == required.ConditionRevision &&
			condition.CredentialVersion == required.CredentialVersion {
			return true
		}
	}
	return false
}

func worstSummary(conditions []domain.HealthCondition) domain.HealthState {
	if len(conditions) == 0 {
		return domain.HealthUnknown
	}
	order := map[domain.HealthState]int{
		domain.HealthHealthy: 1, domain.HealthUnknown: 2, domain.HealthDegraded: 3,
		domain.HealthCoolingDown: 4, domain.HealthExpired: 5, domain.HealthChallenged: 6, domain.HealthBlocked: 7,
	}
	worst := conditions[0].State
	for _, c := range conditions[1:] {
		if order[c.State] > order[worst] {
			worst = c.State
		}
	}
	return worst
}

func (store *FileHealthStore) reloadLocked(_ context.Context) error {
	file, err := os.Open(store.path)
	if err != nil {
		if os.IsNotExist(err) {
			store.byKey = make(map[healthKey]healthRecord)
			return nil
		}
		return err
	}
	defer file.Close()

	next := make(map[healthKey]healthRecord)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		if string(line) == "null" {
			return fmt.Errorf("health ledger line %d: null record", lineNo)
		}
		var record healthRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return fmt.Errorf("health ledger line %d: %w", lineNo, err)
		}
		if err := validateHealthRecord(record); err != nil {
			return fmt.Errorf("health ledger line %d: %w", lineNo, err)
		}
		next[healthKey{tenant: record.TenantID, account: record.AccountID}] = record
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	store.byKey = next
	return nil
}

func (store *FileHealthStore) appendLocked(record healthRecord) error {
	dir := filepath.Dir(store.path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	file, err := os.OpenFile(store.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(data); err != nil {
		return err
	}
	return file.Sync()
}

func (store *FileHealthStore) acquireLock() (func(), error) {
	dir := filepath.Dir(store.lock)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return nil, err
		}
	}
	file, err := os.OpenFile(store.lock, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("%w: health store exclusive lock held", ports.ErrDependencyUnavailable)
		}
		return nil, err
	}
	_, _ = file.WriteString("pixelplus-health-lock\n")
	if err := file.Close(); err != nil {
		_ = os.Remove(store.lock)
		return nil, err
	}
	return func() { _ = os.Remove(store.lock) }, nil
}

func validateHealthRecord(record healthRecord) error {
	if record.TenantID == "" {
		return errors.New("health record missing tenant_id")
	}
	if record.AccountID == "" {
		return errors.New("health record missing account_id")
	}
	if record.Health.Conditions == nil {
		return errors.New("health record missing conditions array")
	}
	seen := make(map[string]struct{}, len(record.Health.Conditions))
	for i, condition := range record.Health.Conditions {
		if err := validateHealthCondition(condition, i); err != nil {
			return err
		}
		// Identity is scope + credential version so pending rejection can coexist
		// with origin evidence (I-HEALTH-CURRENT-VERSION / dual-version fence).
		key := string(condition.Scope.Kind) + "|" + condition.Scope.Operation + "|" + condition.Scope.ModelSlug + "|" + fmt.Sprintf("%d", condition.CredentialVersion)
		if _, ok := seen[key]; ok {
			return fmt.Errorf("duplicate health scope+version %q", key)
		}
		seen[key] = struct{}{}
	}
	if len(record.Health.Conditions) > 0 {
		expected := worstSummary(record.Health.Conditions)
		if record.Health.SummaryState != expected {
			return fmt.Errorf("summary_state %q inconsistent with conditions (want %q)", record.Health.SummaryState, expected)
		}
	}
	if err := validateRecoveryPermit(record); err != nil {
		return err
	}
	return nil
}

func validateRecoveryPermit(record healthRecord) error {
	permit := record.RecoveryPermit
	empty := permit.Owner == "" &&
		permit.Scope.Kind == "" &&
		permit.Scope.Operation == "" &&
		permit.Scope.ModelSlug == "" &&
		permit.ConditionRevision == 0 &&
		permit.CredentialVersion == 0
	if empty {
		return nil
	}
	// All-or-none: partial permit fencing is not restorable.
	if permit.Owner == "" || permit.Scope.Kind == "" || permit.ConditionRevision <= 0 || permit.CredentialVersion <= 0 {
		return errors.New("recovery permit fields must be all present with positive revision/version")
	}
	if err := validateHealthScopeShape(permit.Scope, -1); err != nil {
		return fmt.Errorf("recovery permit scope: %w", err)
	}
	var matched *domain.HealthCondition
	for i := range record.Health.Conditions {
		c := &record.Health.Conditions[i]
		if sameHealthScope(c.Scope, permit.Scope) &&
			c.ConditionRevision == permit.ConditionRevision &&
			c.CredentialVersion == permit.CredentialVersion {
			matched = c
			break
		}
	}
	if matched == nil {
		return errors.New("recovery permit does not reference an existing condition revision")
	}
	if matched.State != domain.HealthCoolingDown {
		return errors.New("recovery permit must reference a cooling_down condition")
	}
	return nil
}

func validateHealthScopeShape(scope domain.HealthScope, index int) error {
	prefix := "health condition"
	if index < 0 {
		prefix = "scope"
	} else {
		prefix = fmt.Sprintf("health condition %d", index)
	}
	switch scope.Kind {
	case domain.HealthScopeAccount:
		if scope.Operation != "" || scope.ModelSlug != "" {
			return fmt.Errorf("%s account scope must have empty operation/model", prefix)
		}
	case domain.HealthScopeOperation:
		if scope.Operation == "" {
			return fmt.Errorf("%s operation scope requires operation", prefix)
		}
		if !domain.CapabilityOperation(scope.Operation).Valid() {
			return fmt.Errorf("%s operation %q is not a canonical capability operation", prefix, scope.Operation)
		}
		if scope.ModelSlug != "" {
			return fmt.Errorf("%s operation scope must have empty model", prefix)
		}
	case domain.HealthScopeModel:
		if scope.Operation == "" || scope.ModelSlug == "" {
			return fmt.Errorf("%s model scope requires operation and model slug", prefix)
		}
		if !domain.CapabilityOperation(scope.Operation).Valid() {
			return fmt.Errorf("%s operation %q is not a canonical capability operation", prefix, scope.Operation)
		}
	case "":
		return fmt.Errorf("%s missing scope kind", prefix)
	default:
		return fmt.Errorf("%s invalid scope kind %q", prefix, scope.Kind)
	}
	return nil
}

// maxBackoffLevelForReason mirrors domain progressive-backoff caps derived from
// CooldownBaseAndMax so restore rejects unbounded BackoffLevel values.
func maxBackoffLevelForReason(reason domain.HealthReason) int {
	base, maximum := domain.CooldownBaseAndMax(reason)
	if base <= 0 || maximum <= 0 || base >= maximum {
		return 1
	}
	level := 1
	duration := base
	for duration < maximum {
		level++
		if duration > maximum/2 {
			duration = maximum
		} else {
			duration *= 2
		}
	}
	return level
}

func isKnownHealthReason(reason domain.HealthReason) bool {
	switch reason {
	case domain.HealthReasonInitialUnprobed, domain.HealthReasonProbeSucceeded, domain.HealthReasonCredentialRejected,
		domain.HealthReasonSuccessWindow, domain.HealthReasonElevatedErrorRate, domain.HealthReasonUpstreamUnavailable,
		domain.HealthReasonUpstreamTimeout, domain.HealthReasonProviderRateLimited, domain.HealthReasonProviderQuotaExhausted,
		domain.HealthReasonChallengeDetected, domain.HealthReasonCredentialExpired, domain.HealthReasonProtocolDrift,
		domain.HealthReasonProviderAccountBanned, domain.HealthReasonRecoveryProbeFailed:
		return true
	default:
		return false
	}
}

func isKnownHealthRemediation(remediation domain.Remediation) bool {
	switch remediation {
	case domain.RemediationNone, domain.RemediationSubmitCredential, domain.RemediationReauthenticate,
		domain.RemediationWaitProviderCooldown, domain.RemediationContactOperator, domain.RemediationAccountRemediation,
		domain.RemediationEnableAccount, domain.RemediationAckRisk, domain.RemediationCompleteOAuth,
		domain.RemediationAuthModeUnavailable, domain.RemediationCapabilityUnverified, domain.RemediationSnapshotStale,
		domain.RemediationCapabilityUnsupported:
		return true
	default:
		return false
	}
}

func validateStateReasonRemediation(condition domain.HealthCondition, index int) error {
	prefix := fmt.Sprintf("health condition %d", index)
	switch condition.State {
	case domain.HealthCoolingDown:
		switch condition.Reason {
		case domain.HealthReasonProviderRateLimited, domain.HealthReasonProviderQuotaExhausted, domain.HealthReasonRecoveryProbeFailed:
		default:
			return fmt.Errorf("%s cooling_down requires rate/quota/recovery-probe-failed reason, got %q", prefix, condition.Reason)
		}
		if condition.Remediation != domain.RemediationWaitProviderCooldown {
			return fmt.Errorf("%s cooling_down requires wait_provider_cooldown", prefix)
		}
		if condition.RetryNotBefore.IsZero() {
			return fmt.Errorf("%s cooling_down requires finite RetryNotBefore", prefix)
		}
		if !condition.ObservedAt.IsZero() && !condition.RetryNotBefore.Time().After(condition.ObservedAt.Time()) {
			return fmt.Errorf("%s RetryNotBefore must be after ObservedAt", prefix)
		}
	case domain.HealthExpired:
		switch condition.Reason {
		case domain.HealthReasonCredentialRejected, domain.HealthReasonCredentialExpired:
		default:
			return fmt.Errorf("%s expired requires credential_rejected/credential_expired reason", prefix)
		}
		if condition.Remediation != domain.RemediationReauthenticate {
			return fmt.Errorf("%s expired requires reauthenticate", prefix)
		}
		if !condition.RetryNotBefore.IsZero() {
			return fmt.Errorf("%s expired must not carry RetryNotBefore", prefix)
		}
	case domain.HealthUnknown:
		if condition.Reason != domain.HealthReasonInitialUnprobed {
			return fmt.Errorf("%s unknown requires initial_unprobed", prefix)
		}
		if condition.Remediation != domain.RemediationNone && condition.Remediation != domain.RemediationSubmitCredential {
			return fmt.Errorf("%s unknown remediation %q not allowed", prefix, condition.Remediation)
		}
		if !condition.RetryNotBefore.IsZero() {
			return fmt.Errorf("%s unknown must not carry RetryNotBefore", prefix)
		}
	case domain.HealthHealthy:
		switch condition.Reason {
		case domain.HealthReasonProbeSucceeded, domain.HealthReasonSuccessWindow:
		default:
			return fmt.Errorf("%s healthy requires probe_succeeded/success_window", prefix)
		}
		if condition.Remediation != domain.RemediationNone {
			return fmt.Errorf("%s healthy requires remediation none", prefix)
		}
		if !condition.RetryNotBefore.IsZero() {
			return fmt.Errorf("%s healthy must not carry RetryNotBefore", prefix)
		}
	case domain.HealthChallenged:
		if condition.Reason != domain.HealthReasonChallengeDetected {
			return fmt.Errorf("%s challenged requires challenge_detected", prefix)
		}
		if condition.Remediation != domain.RemediationContactOperator && condition.Remediation != domain.RemediationAccountRemediation {
			return fmt.Errorf("%s challenged remediation %q not canonical", prefix, condition.Remediation)
		}
		if !condition.RetryNotBefore.IsZero() {
			return fmt.Errorf("%s challenged must not carry RetryNotBefore", prefix)
		}
	case domain.HealthBlocked:
		switch condition.Reason {
		case domain.HealthReasonProviderAccountBanned, domain.HealthReasonProtocolDrift:
		default:
			return fmt.Errorf("%s blocked requires banned/protocol_drift reason", prefix)
		}
		if condition.Remediation != domain.RemediationContactOperator && condition.Remediation != domain.RemediationAccountRemediation {
			return fmt.Errorf("%s blocked remediation %q not canonical", prefix, condition.Remediation)
		}
		if !condition.RetryNotBefore.IsZero() {
			return fmt.Errorf("%s blocked must not carry RetryNotBefore", prefix)
		}
	case domain.HealthDegraded:
		switch condition.Reason {
		case domain.HealthReasonElevatedErrorRate, domain.HealthReasonUpstreamUnavailable, domain.HealthReasonUpstreamTimeout:
		default:
			return fmt.Errorf("%s degraded reason %q not allowed", prefix, condition.Reason)
		}
		if condition.Remediation != domain.RemediationNone && condition.Remediation != domain.RemediationContactOperator {
			return fmt.Errorf("%s degraded remediation %q not allowed", prefix, condition.Remediation)
		}
		if !condition.RetryNotBefore.IsZero() {
			return fmt.Errorf("%s degraded must not carry RetryNotBefore", prefix)
		}
	}
	return nil
}

func validateHealthCondition(condition domain.HealthCondition, index int) error {
	if condition.State == "" {
		return fmt.Errorf("health condition %d missing state", index)
	}
	switch condition.State {
	case domain.HealthHealthy, domain.HealthUnknown, domain.HealthDegraded, domain.HealthCoolingDown,
		domain.HealthExpired, domain.HealthChallenged, domain.HealthBlocked:
	default:
		return fmt.Errorf("health condition %d invalid state %q", index, condition.State)
	}
	if !isKnownHealthReason(condition.Reason) {
		return fmt.Errorf("health condition %d invalid reason %q", index, condition.Reason)
	}
	if !isKnownHealthRemediation(condition.Remediation) {
		return fmt.Errorf("health condition %d invalid remediation %q", index, condition.Remediation)
	}
	if err := validateHealthScopeShape(condition.Scope, index); err != nil {
		return err
	}
	if condition.ObservedAt.IsZero() {
		return fmt.Errorf("health condition %d missing ObservedAt", index)
	}
	if condition.State != domain.HealthCoolingDown && !condition.RetryNotBefore.IsZero() {
		return fmt.Errorf("health condition %d non-cooling must not carry RetryNotBefore", index)
	}
	if err := validateStateReasonRemediation(condition, index); err != nil {
		return err
	}
	if condition.ConditionRevision < 0 || condition.ConditionRevision > 1_000_000 {
		return fmt.Errorf("health condition %d invalid condition_revision", index)
	}
	// Durable cooling/hard evidence requires a positive fencing revision.
	if condition.State == domain.HealthCoolingDown || condition.State == domain.HealthExpired ||
		condition.State == domain.HealthChallenged || condition.State == domain.HealthBlocked {
		if condition.ConditionRevision <= 0 {
			return fmt.Errorf("health condition %d requires positive condition_revision", index)
		}
	}
	if condition.CredentialVersion < 0 {
		return fmt.Errorf("health condition %d negative credential_version", index)
	}
	// Cooling and hard evidence require positive credential fencing; draft
	// unknown/initial_unprobed may still be zero before first store.
	switch condition.State {
	case domain.HealthCoolingDown, domain.HealthExpired, domain.HealthChallenged, domain.HealthBlocked:
		if condition.CredentialVersion <= 0 {
			return fmt.Errorf("health condition %d requires positive credential_version", index)
		}
	}
	if condition.State == domain.HealthCoolingDown {
		maxLevel := maxBackoffLevelForReason(condition.Reason)
		if condition.BackoffLevel < 1 || condition.BackoffLevel > maxLevel {
			return fmt.Errorf("health condition %d backoff_level %d out of reason bound [1,%d]", index, condition.BackoffLevel, maxLevel)
		}
	} else if condition.BackoffLevel < 0 || condition.BackoffLevel > 64 {
		return fmt.Errorf("health condition %d invalid backoff_level", index)
	}
	if condition.SourceClass != "" {
		switch condition.SourceClass {
		case domain.HealthSourceRequiredProbe, domain.HealthSourceRecoveryProbe, domain.HealthSourceUpstreamAttempt,
			domain.HealthSourceProviderResetHint, domain.HealthSourceOperatorClassification, domain.HealthSourceAggregateCircuit:
		default:
			return fmt.Errorf("health condition %d invalid source_class %q", index, condition.SourceClass)
		}
	}
	return nil
}

// UnavailableHealthStore is installed after startup restoration fails.
type UnavailableHealthStore struct{}

func NewUnavailableHealthStore() *UnavailableHealthStore { return &UnavailableHealthStore{} }

func (*UnavailableHealthStore) Restore(context.Context) error { return ports.ErrDependencyUnavailable }
func (*UnavailableHealthStore) Read(context.Context, domain.SecurityPrincipal, domain.ProviderAccountID) (ports.AccountHealth, error) {
	return ports.AccountHealth{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) Initialize(context.Context, ports.HealthInitialize) (ports.AccountHealth, error) {
	return ports.AccountHealth{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) ObserveCooldown(context.Context, ports.CooldownObservation) (ports.HealthTransition, error) {
	return ports.HealthTransition{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) ClaimRecoveryPermit(context.Context, ports.RecoveryPermitClaim) (ports.ClaimResult, error) {
	return ports.ClaimResult{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) RenewAfterDependencyFailure(context.Context, ports.DependencyFailureRenewal) (ports.HealthTransition, error) {
	return ports.HealthTransition{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) ResolveRecovery(context.Context, ports.RecoveryResolution) (ports.HealthTransition, error) {
	return ports.HealthTransition{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) RecordHardFailure(context.Context, ports.HardFailureObservation) (ports.HealthTransition, error) {
	return ports.HealthTransition{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) ResetForEnableProbe(context.Context, ports.EnableProbeReset) (ports.HealthTransition, error) {
	return ports.HealthTransition{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) ClearPermit(context.Context, ports.PermitClear) (ports.AccountHealth, error) {
	return ports.AccountHealth{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) RecordActivation(context.Context, ports.ActivationHealth) (ports.HealthTransition, error) {
	return ports.HealthTransition{}, ports.ErrDependencyUnavailable
}
func (*UnavailableHealthStore) ResetForCredentialEpoch(context.Context, ports.CredentialEpochReset) (ports.HealthTransition, error) {
	return ports.HealthTransition{}, ports.ErrDependencyUnavailable
}

var (
	_ ports.HealthStore = (*MemoryHealthStore)(nil)
	_ ports.HealthStore = (*FileHealthStore)(nil)
	_ ports.HealthStore = (*UnavailableHealthStore)(nil)
)
