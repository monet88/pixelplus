package contracttest_test

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// spineLog is a shared, ordered record of the outbound port calls the request
// spine makes. It lets a contract test prove the normative admission order
// (authenticate -> replay.claim -> admit -> account.create) through real
// composition without reaching into private application state.
type spineLog struct {
	mu     sync.Mutex
	events []string
}

func (log *spineLog) add(event string) {
	if log == nil {
		return
	}
	log.mu.Lock()
	defer log.mu.Unlock()
	log.events = append(log.events, event)
}

func (log *spineLog) snapshot() []string {
	log.mu.Lock()
	defer log.mu.Unlock()
	return append([]string(nil), log.events...)
}

// stubPrincipalStore authenticates presented bearer material against a fixed
// map. Any unknown material returns the single indistinguishable authentication
// failure and never forms a principal (#8 section 4.3).
type stubPrincipalStore struct {
	log        *spineLog
	principals map[string]domain.SecurityPrincipal
	calls      atomic.Int32
}

func (store *stubPrincipalStore) Authenticate(_ context.Context, key ports.PresentedClientAPIKey) (domain.SecurityPrincipal, error) {
	store.calls.Add(1)
	store.log.add("authenticate")
	principal, ok := store.principals[key.Material]
	if !ok {
		return domain.SecurityPrincipal{}, ports.ErrAuthentication
	}
	return principal, nil
}

// stubAdmissionStore admits by default and can be configured to reject at a
// named normative stage. It counts admit/reconcile so a test can prove that a
// rejected or non-enumerating request never debited admission.
type stubAdmissionStore struct {
	log            *spineLog
	rejectStage    ports.AdmissionStage
	admitErr       error
	admitCalls     atomic.Int32
	reconcileCalls atomic.Int32
}

func (store *stubAdmissionStore) Admit(_ context.Context, request ports.AdmissionRequest) (ports.AdmissionDecision, ports.AdmissionReservation, error) {
	store.admitCalls.Add(1)
	store.log.add("admit")
	if store.admitErr != nil {
		return ports.AdmissionDecision{}, ports.AdmissionReservation{}, store.admitErr
	}
	if store.rejectStage != "" {
		return ports.AdmissionDecision{Admitted: false, Stage: store.rejectStage}, ports.AdmissionReservation{}, nil
	}
	return ports.AdmissionDecision{Admitted: true},
		ports.AdmissionReservation{Principal: request.Principal, Operation: request.Operation},
		nil
}

func (store *stubAdmissionStore) Reconcile(context.Context, ports.AdmissionReservation) error {
	store.reconcileCalls.Add(1)
	return nil
}

type stubReplayRecord struct {
	fingerprint domain.Fingerprint
	terminal    bool
	account     domain.ProviderAccount
}

// stubReplayStore performs a real atomic claim so the concurrency acceptance
// criterion is proven, not simulated. A test may pre-seed records to force a
// conflict/in-progress/terminal outcome, or set forced to exercise the
// uncertain no-steal path.
type stubReplayStore struct {
	log           *spineLog
	mu            sync.Mutex
	records       map[domain.ReplayScope]*stubReplayRecord
	forced        ports.ReplayOutcome
	forcedAccount domain.ProviderAccount
	completeErr   error
	claimCalls    atomic.Int32
	completeCalls atomic.Int32
	abandonCalls  atomic.Int32
}

func newStubReplayStore(log *spineLog) *stubReplayStore {
	return &stubReplayStore{log: log, records: make(map[domain.ReplayScope]*stubReplayRecord)}
}

func (store *stubReplayStore) Claim(_ context.Context, identity domain.ReplayIdentity) (ports.ReplayDecision, error) {
	store.claimCalls.Add(1)
	store.log.add("replay.claim")
	store.mu.Lock()
	defer store.mu.Unlock()

	if store.forced != "" {
		return ports.ReplayDecision{Outcome: store.forced, TerminalAccount: store.forcedAccount}, nil
	}
	existing, ok := store.records[identity.Scope]
	if !ok {
		store.records[identity.Scope] = &stubReplayRecord{fingerprint: identity.Fingerprint}
		return ports.ReplayDecision{Outcome: ports.ReplayClaimed}, nil
	}
	if existing.fingerprint != identity.Fingerprint {
		return ports.ReplayDecision{Outcome: ports.ReplayConflict}, nil
	}
	if existing.terminal {
		return ports.ReplayDecision{Outcome: ports.ReplayTerminal, TerminalAccount: existing.account}, nil
	}
	return ports.ReplayDecision{Outcome: ports.ReplayInProgress}, nil
}

func (store *stubReplayStore) Complete(_ context.Context, identity domain.ReplayIdentity, result ports.ReplayResult) error {
	store.completeCalls.Add(1)
	store.log.add("replay.complete")
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.completeErr != nil {
		return store.completeErr
	}
	record, ok := store.records[identity.Scope]
	if !ok {
		record = &stubReplayRecord{fingerprint: identity.Fingerprint}
		store.records[identity.Scope] = record
	}
	record.terminal = true
	record.account = result.Account
	return nil
}

func (store *stubReplayStore) Abandon(_ context.Context, identity domain.ReplayIdentity) error {
	store.abandonCalls.Add(1)
	store.log.add("replay.abandon")
	store.mu.Lock()
	defer store.mu.Unlock()
	record, ok := store.records[identity.Scope]
	if !ok {
		return nil
	}
	if record.terminal || record.fingerprint != identity.Fingerprint {
		return nil
	}
	delete(store.records, identity.Scope)
	return nil
}

// stubAccountStore keeps same-Tenant, non-enumerating visibility. Foreign,
// unknown, and deleted identifiers all return ErrAccountNotVisible so the
// outcome is indistinguishable (#6 section 5.1).
type stubAccountStore struct {
	log          *spineLog
	mu           sync.Mutex
	byTenant     map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount
	createErr    error
	createCalls  atomic.Int32
	visibleCalls atomic.Int32
	listCalls    atomic.Int32
}

func newStubAccountStore(log *spineLog) *stubAccountStore {
	return &stubAccountStore{log: log, byTenant: make(map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount)}
}

func (store *stubAccountStore) seed(tenant domain.TenantID, account domain.ProviderAccount) {
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts, ok := store.byTenant[tenant]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]domain.ProviderAccount)
		store.byTenant[tenant] = accounts
	}
	accounts[account.ID] = account
}

func (store *stubAccountStore) Create(_ context.Context, creation ports.AccountCreation) (domain.ProviderAccount, error) {
	store.createCalls.Add(1)
	store.log.add("account.create")
	if store.createErr != nil {
		return domain.ProviderAccount{}, store.createErr
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	tenant := creation.Principal.TenantID
	accounts, ok := store.byTenant[tenant]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]domain.ProviderAccount)
		store.byTenant[tenant] = accounts
	}
	accounts[creation.Account.ID] = creation.Account
	return creation.Account, nil
}

func (store *stubAccountStore) Visible(_ context.Context, principal domain.SecurityPrincipal, id domain.ProviderAccountID) (domain.ProviderAccount, error) {
	store.visibleCalls.Add(1)
	store.log.add("account.visible")
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts, ok := store.byTenant[principal.TenantID]
	if !ok {
		return domain.ProviderAccount{}, ports.ErrAccountNotVisible
	}
	account, ok := accounts[id]
	if !ok || account.Lifecycle == domain.LifecycleDeleted {
		return domain.ProviderAccount{}, ports.ErrAccountNotVisible
	}
	return account, nil
}

func (store *stubAccountStore) List(_ context.Context, principal domain.SecurityPrincipal) ([]domain.ProviderAccount, error) {
	store.listCalls.Add(1)
	store.log.add("account.list")
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts := store.byTenant[principal.TenantID]
	result := make([]domain.ProviderAccount, 0, len(accounts))
	for _, account := range accounts {
		if account.Lifecycle == domain.LifecycleDeleted {
			continue
		}
		result = append(result, account)
	}
	return result, nil
}

// captureAudit records the safe audit projections emitted by the spine.
type captureAudit struct {
	mu     sync.Mutex
	events []ports.AuditEvent
}

func (recorder *captureAudit) Record(_ context.Context, event ports.AuditEvent) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, event)
	return nil
}

func (recorder *captureAudit) snapshot() []ports.AuditEvent {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]ports.AuditEvent(nil), recorder.events...)
}

// captureTelemetry records the safe telemetry projections emitted by the spine.
type captureTelemetry struct {
	mu     sync.Mutex
	events []ports.TelemetryEvent
}

func (recorder *captureTelemetry) Record(_ context.Context, event ports.TelemetryEvent) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.events = append(recorder.events, event)
	return nil
}

func (recorder *captureTelemetry) snapshot() []ports.TelemetryEvent {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]ports.TelemetryEvent(nil), recorder.events...)
}

// captureRequestLog records the single canonical request log per request.
type captureRequestLog struct {
	mu   sync.Mutex
	logs []ports.RequestLog
}

func (recorder *captureRequestLog) Record(_ context.Context, log ports.RequestLog) error {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	recorder.logs = append(recorder.logs, log)
	return nil
}

func (recorder *captureRequestLog) snapshot() []ports.RequestLog {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return append([]ports.RequestLog(nil), recorder.logs...)
}

var (
	_ ports.PrincipalStore     = (*stubPrincipalStore)(nil)
	_ ports.AdmissionStore     = (*stubAdmissionStore)(nil)
	_ ports.ReplayStore        = (*stubReplayStore)(nil)
	_ ports.AccountStore       = (*stubAccountStore)(nil)
	_ ports.AuditRecorder      = (*captureAudit)(nil)
	_ ports.TelemetryRecorder  = (*captureTelemetry)(nil)
	_ ports.RequestLogRecorder = (*captureRequestLog)(nil)
)

// replayOutcome adapts a test string to the ports.ReplayOutcome value the stub
// replay store forces, keeping the test tables readable.
func replayOutcome(value string) ports.ReplayOutcome {
	return ports.ReplayOutcome(value)
}

// equalPrefix reports whether got begins with the want sequence. The spine may
// emit trailing safe observations (audit/telemetry/log) after the ordered gate
// calls, so the test asserts the normative prefix rather than exact equality.
func equalPrefix(got, want []string) bool {
	if len(got) < len(want) {
		return false
	}
	for index := range want {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
