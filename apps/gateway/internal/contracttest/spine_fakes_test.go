package contracttest_test

import (
	"context"
	"fmt"
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
	oauth       domain.OAuthAuthorization
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
	forcedOAuth   domain.OAuthAuthorization
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
		return ports.ReplayDecision{Outcome: store.forced, TerminalAccount: store.forcedAccount, TerminalOAuth: store.forcedOAuth}, nil
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
		return ports.ReplayDecision{Outcome: ports.ReplayTerminal, TerminalAccount: existing.account, TerminalOAuth: existing.oauth}, nil
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
	record.oauth = result.OAuth
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
	log       *spineLog
	mu        sync.Mutex
	byTenant  map[domain.TenantID]map[domain.ProviderAccountID]domain.ProviderAccount
	createErr error
	updateErr error
	// updateFailTimes forces the next N Update calls to return updateErr (or
	// ErrDependencyUnavailable when updateErr is nil) so partial-success recovery
	// after vault put can be contract-tested.
	updateFailTimes atomic.Int32
	createCalls     atomic.Int32
	visibleCalls    atomic.Int32
	listCalls       atomic.Int32
	updateCalls     atomic.Int32
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

// Update persists a mutated account for the owning Tenant, mirroring the
// non-enumerating visibility contract: a foreign, unknown, or deleted id
// resolves to ErrAccountNotVisible so a lifecycle transition can never target a
// resource the principal cannot see (#6 section 5.1).
func (store *stubAccountStore) Update(_ context.Context, update ports.AccountUpdate) (domain.ProviderAccount, error) {
	store.updateCalls.Add(1)
	store.log.add("account.update")
	if remaining := store.updateFailTimes.Load(); remaining > 0 {
		store.updateFailTimes.Add(-1)
		if store.updateErr != nil {
			return domain.ProviderAccount{}, store.updateErr
		}
		return domain.ProviderAccount{}, ports.ErrDependencyUnavailable
	}
	if store.updateErr != nil {
		return domain.ProviderAccount{}, store.updateErr
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts, ok := store.byTenant[update.Principal.TenantID]
	if !ok {
		return domain.ProviderAccount{}, ports.ErrAccountNotVisible
	}
	existing, ok := accounts[update.Account.ID]
	if !ok || existing.Lifecycle == domain.LifecycleDeleted {
		return domain.ProviderAccount{}, ports.ErrAccountNotVisible
	}
	if update.RequireEmptyOAuthMarker && existing.ActiveOAuthAuthorizationID != "" {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireOAuthMarker != "" && existing.ActiveOAuthAuthorizationID != update.RequireOAuthMarker {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequireDraftLifecycle && existing.Lifecycle != domain.LifecycleDraft {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	if update.RequirePendingVersion > 0 && existing.PendingCredentialVersion != update.RequirePendingVersion {
		return domain.ProviderAccount{}, ports.ErrAccountUpdateConflict
	}
	accounts[update.Account.ID] = update.Account
	return update.Account, nil
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

// stubCredentialVault is a controlled Credential Vault. Put records the intake
// binding it received (never released to a response) so a test can prove the
// application forwarded the material once under the right Tenant/account/version
// binding; Validate returns a configurable pass/fail projection. Both count
// calls so a test can prove a pre-Vault gate rejection never reached the
// protected boundary.
type stubCredentialVault struct {
	log         *spineLog
	mu          sync.Mutex
	putErr      error
	validateErr error
	validResult ports.CredentialValidationResult
	putCalls    atomic.Int32
	validCalls  atomic.Int32
	lastIntake  ports.CredentialIntake
	// versions records the first successful put per account so concurrent or
	// recovery re-hands of the same version are idempotent and do not count as
	// a second durable put.
	versions map[domain.ProviderAccountID]int
	revoked  map[domain.ProviderAccountID]map[int]bool
}

func newStubCredentialVault(log *spineLog) *stubCredentialVault {
	return &stubCredentialVault{
		log:         log,
		validResult: ports.CredentialValidationResult{Valid: true},
		versions:    make(map[domain.ProviderAccountID]int),
		revoked:     make(map[domain.ProviderAccountID]map[int]bool),
	}
}

func (vault *stubCredentialVault) Put(_ context.Context, intake ports.CredentialIntake) error {
	vault.log.add("vault.put")
	if vault.putErr != nil {
		vault.putCalls.Add(1)
		return vault.putErr
	}
	vault.mu.Lock()
	defer vault.mu.Unlock()
	if existing, ok := vault.versions[intake.AccountID]; ok && existing >= intake.Version {
		// Idempotent re-hand of an already-stored version.
		vault.lastIntake = intake
		return nil
	}
	vault.putCalls.Add(1)
	vault.versions[intake.AccountID] = intake.Version
	vault.lastIntake = intake
	return nil
}

func (vault *stubCredentialVault) Validate(_ context.Context, _ ports.CredentialValidation) (ports.CredentialValidationResult, error) {
	vault.validCalls.Add(1)
	vault.log.add("vault.validate")
	if vault.validateErr != nil {
		return ports.CredentialValidationResult{}, vault.validateErr
	}
	return vault.validResult, nil
}

func (vault *stubCredentialVault) Revoke(_ context.Context, validation ports.CredentialValidation) error {
	vault.log.add("vault.revoke")
	if vault.validateErr != nil {
		return vault.validateErr
	}
	vault.mu.Lock()
	defer vault.mu.Unlock()
	versions := vault.revoked[validation.AccountID]
	if versions == nil {
		versions = make(map[int]bool)
		vault.revoked[validation.AccountID] = versions
	}
	versions[validation.Version] = true
	return nil
}

func (vault *stubCredentialVault) wasRevoked(accountID domain.ProviderAccountID, version int) bool {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	return vault.revoked[accountID][version]
}

func (vault *stubCredentialVault) intake() ports.CredentialIntake {
	vault.mu.Lock()
	defer vault.mu.Unlock()
	return vault.lastIntake
}

// stubProbeAdapter is a controlled Probe Adapter. Probe returns a configurable
// outcome (authenticated true/false) or a fail-closed dependency error, and
// counts calls so a test can prove a validation failure prevented the probe and
// a probe failure never activated the account.
type stubProbeAdapter struct {
	log       *spineLog
	probeErr  error
	outcome   ports.ProbeOutcome
	callCount atomic.Int32
}

func newStubProbeAdapter(log *spineLog) *stubProbeAdapter {
	return &stubProbeAdapter{log: log, outcome: ports.ProbeOutcome{Authenticated: true}}
}

func (adapter *stubProbeAdapter) Probe(_ context.Context, _ ports.ProbeCommand) (ports.ProbeOutcome, error) {
	adapter.callCount.Add(1)
	adapter.log.add("probe")
	if adapter.probeErr != nil {
		return ports.ProbeOutcome{}, adapter.probeErr
	}
	return adapter.outcome, nil
}

// stubCapabilityStore is a controlled Tenant-partitioned Capability Snapshot
// store. Tests seed snapshots and assert put/get counts to prove minting and
// non-disclosure of foreign Tenant evidence.
type stubCapabilityStore struct {
	log       *spineLog
	mu        sync.Mutex
	byTenant  map[domain.TenantID]map[domain.ProviderAccountID]domain.CapabilitySnapshot
	getCalls  atomic.Int32
	listCalls atomic.Int32
	putCalls  atomic.Int32
	putErr    error
}

func newStubCapabilityStore(log *spineLog) *stubCapabilityStore {
	return &stubCapabilityStore{log: log, byTenant: make(map[domain.TenantID]map[domain.ProviderAccountID]domain.CapabilitySnapshot)}
}

func (store *stubCapabilityStore) seed(tenant domain.TenantID, snapshot domain.CapabilitySnapshot) {
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts, ok := store.byTenant[tenant]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]domain.CapabilitySnapshot)
		store.byTenant[tenant] = accounts
	}
	accounts[snapshot.ProviderAccountID] = snapshot
}

func (store *stubCapabilityStore) Get(_ context.Context, principal domain.SecurityPrincipal, accountID domain.ProviderAccountID) (domain.CapabilitySnapshot, error) {
	store.getCalls.Add(1)
	store.log.add("capability.get")
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts, ok := store.byTenant[principal.TenantID]
	if !ok {
		return domain.CapabilitySnapshot{}, ports.ErrCapabilitySnapshotNotFound
	}
	snapshot, ok := accounts[accountID]
	if !ok {
		return domain.CapabilitySnapshot{}, ports.ErrCapabilitySnapshotNotFound
	}
	return snapshot, nil
}

func (store *stubCapabilityStore) List(_ context.Context, principal domain.SecurityPrincipal) ([]domain.CapabilitySnapshot, error) {
	store.listCalls.Add(1)
	store.log.add("capability.list")
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts := store.byTenant[principal.TenantID]
	result := make([]domain.CapabilitySnapshot, 0, len(accounts))
	for _, snapshot := range accounts {
		result = append(result, snapshot)
	}
	return result, nil
}

func (store *stubCapabilityStore) Put(_ context.Context, principal domain.SecurityPrincipal, snapshot domain.CapabilitySnapshot) error {
	store.putCalls.Add(1)
	store.log.add("capability.put")
	if store.putErr != nil {
		return store.putErr
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	accounts, ok := store.byTenant[principal.TenantID]
	if !ok {
		accounts = make(map[domain.ProviderAccountID]domain.CapabilitySnapshot)
		store.byTenant[principal.TenantID] = accounts
	}
	accounts[snapshot.ProviderAccountID] = snapshot
	return nil
}

// stubCapabilityAdapter returns a configurable observation used to mint
// snapshots after a successful probe.
type stubCapabilityAdapter struct {
	log         *spineLog
	observation ports.CapabilityObservation
	observeErr  error
	callCount   atomic.Int32
}

func newStubCapabilityAdapter(log *spineLog) *stubCapabilityAdapter {
	return &stubCapabilityAdapter{
		log: log,
		observation: ports.CapabilityObservation{
			ProbeSurface: "/backend-api/models",
			Operations: map[domain.CapabilityOperation]domain.CapabilityFact{
				domain.CapabilityOpChat: {
					Status:        domain.CapabilityVerified,
					EvidenceClass: domain.EvidenceLiveProbe,
					ProbeSurface:  "/backend-api/models",
				},
				domain.CapabilityOpChatStreaming: {
					Status:         domain.CapabilityVerified,
					EvidenceClass:  domain.EvidenceLiveProbe,
					ProbeSurface:   "/backend-api/models",
					StreamingClass: domain.StreamingReal,
				},
				domain.CapabilityOpImageGeneration: {
					Status:        domain.CapabilityConditionallySupported,
					EvidenceClass: domain.EvidenceLiveProbe,
					ProbeSurface:  "/backend-api/models",
				},
				domain.CapabilityOpImageEdit: {
					Status:        domain.CapabilityConditionallySupported,
					EvidenceClass: domain.EvidenceLiveProbe,
					ProbeSurface:  "/backend-api/models",
				},
				domain.CapabilityOpInpaint: {
					Status:        domain.CapabilityUnsupported,
					EvidenceClass: domain.EvidenceLiveProbe,
					ProbeSurface:  "/backend-api/models",
				},
			},
			Models: []domain.ModelCapability{{
				ModelSlug: "gpt-4o-mini",
				Operations: map[domain.CapabilityOperation]domain.CapabilityStatus{
					domain.CapabilityOpChat:            domain.CapabilityVerified,
					domain.CapabilityOpChatStreaming:   domain.CapabilityVerified,
					domain.CapabilityOpImageGeneration: domain.CapabilityUnsupported,
					domain.CapabilityOpImageEdit:       domain.CapabilityUnsupported,
					domain.CapabilityOpInpaint:         domain.CapabilityUnsupported,
				},
				SurfaceBinding: "chatgpt_web",
				ObservedAt:     domain.NewTimestamp(spineFixtureTime),
			}},
		},
	}
}

func (adapter *stubCapabilityAdapter) Observe(_ context.Context, _ ports.CapabilityObservationCommand) (ports.CapabilityObservation, error) {
	adapter.callCount.Add(1)
	adapter.log.add("capability.observe")
	if adapter.observeErr != nil {
		return ports.CapabilityObservation{}, adapter.observeErr
	}
	return adapter.observation, nil
}

var (
	_ ports.PrincipalStore     = (*stubPrincipalStore)(nil)
	_ ports.AdmissionStore     = (*stubAdmissionStore)(nil)
	_ ports.ReplayStore        = (*stubReplayStore)(nil)
	_ ports.AccountStore       = (*stubAccountStore)(nil)
	_ ports.AuditRecorder      = (*captureAudit)(nil)
	_ ports.TelemetryRecorder  = (*captureTelemetry)(nil)
	_ ports.RequestLogRecorder = (*captureRequestLog)(nil)
	_ ports.CredentialVault    = (*stubCredentialVault)(nil)
	_ ports.ProbeAdapter       = (*stubProbeAdapter)(nil)
	_ ports.CapabilityStore    = (*stubCapabilityStore)(nil)
	_ ports.CapabilityAdapter  = (*stubCapabilityAdapter)(nil)
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

// stubOAuthExchangeAdapter is a controlled OAuth exchange surface. Start records
// one journey; Poll returns a configurable pending/succeeded/failed outcome and
// hands exchanged material once on first success. Counts let tests prove reject
// paths never reached the adapter and successful exchange material never leaked
// onto the wire.
type stubOAuthExchangeAdapter struct {
	log        *spineLog
	mu         sync.Mutex
	startErr   error
	pollErr    error
	nextStatus domain.OAuthStatus
	material   string
	// startHold, when non-nil, blocks Start after the call is counted until the
	// channel closes. Contract tests use it to force concurrent start races.
	startHold <-chan struct{}
	// pollHold, when non-nil, blocks Poll after the call is counted until the
	// channel closes so concurrent settlement races can be observed.
	pollHold   <-chan struct{}
	startCalls atomic.Int32
	pollCalls  atomic.Int32
	records    map[domain.OAuthAuthorizationID]*stubOAuthRecord
	seq        atomic.Uint64
}

type stubOAuthRecord struct {
	authorization domain.OAuthAuthorization
	consumed      bool
}

func newStubOAuthExchangeAdapter(log *spineLog) *stubOAuthExchangeAdapter {
	return &stubOAuthExchangeAdapter{
		log:        log,
		nextStatus: domain.OAuthStatusAuthorizationPending,
		material:   "oauth_exchanged_material_secret",
		records:    make(map[domain.OAuthAuthorizationID]*stubOAuthRecord),
	}
}

func (adapter *stubOAuthExchangeAdapter) Start(_ context.Context, command ports.OAuthStartCommand) (ports.OAuthStartResult, error) {
	adapter.startCalls.Add(1)
	adapter.log.add("oauth.start")
	if adapter.startHold != nil {
		<-adapter.startHold
	}
	if adapter.startErr != nil {
		return ports.OAuthStartResult{}, adapter.startErr
	}
	sequence := adapter.seq.Add(1)
	id := command.AuthorizationID
	if id == "" {
		id = domain.OAuthAuthorizationID(fmt.Sprintf("oauth_%04d", sequence))
	}
	verificationURI := "https://provider.example/device"
	userCode := fmt.Sprintf("USER-%04d", sequence)
	if command.Flow == domain.OAuthFlowBrowser {
		userCode = ""
		verificationURI = "https://provider.example/authorize"
	}
	authorization := domain.NewOAuthAuthorizationPending(
		id,
		command.AccountID,
		command.Purpose,
		command.Flow,
		verificationURI,
		userCode,
		domain.DefaultOAuthExpiry(spineFixtureTime),
	)
	adapter.mu.Lock()
	adapter.records[id] = &stubOAuthRecord{authorization: authorization}
	adapter.mu.Unlock()
	return ports.OAuthStartResult{Authorization: authorization}, nil
}

func (adapter *stubOAuthExchangeAdapter) Poll(_ context.Context, command ports.OAuthPollCommand) (ports.OAuthPollResult, error) {
	adapter.pollCalls.Add(1)
	adapter.log.add("oauth.poll")
	if adapter.pollHold != nil {
		<-adapter.pollHold
	}
	if adapter.pollErr != nil {
		return ports.OAuthPollResult{}, adapter.pollErr
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	record, ok := adapter.records[command.AuthorizationID]
	if !ok || record.authorization.ProviderAccountID != command.AccountID {
		return ports.OAuthPollResult{}, ports.ErrOAuthAuthorizationNotVisible
	}
	if record.authorization.Status.Terminal() {
		// Succeeded journeys re-hand material so a retry after vault put + failed
		// account update can still finish settlement. Failed journeys never carry
		// material.
		if record.authorization.Status == domain.OAuthStatusSucceeded {
			return ports.OAuthPollResult{
				Authorization:     record.authorization,
				ExchangedMaterial: adapter.material,
			}, nil
		}
		return ports.OAuthPollResult{Authorization: record.authorization}, nil
	}
	status := adapter.nextStatus
	if status == "" {
		status = domain.OAuthStatusAuthorizationPending
	}
	record.authorization.Status = status
	switch status {
	case domain.OAuthStatusSucceeded:
		record.authorization.Remediation = domain.RemediationNone
		record.authorization.UserCode = ""
		record.authorization.VerificationURI = ""
		// Re-hand material until account settlement succeeds. A one-shot consume
		// would permanently 500 a retry after vault put + failed account update.
		return ports.OAuthPollResult{
			Authorization:     record.authorization,
			ExchangedMaterial: adapter.material,
		}, nil
	case domain.OAuthStatusFailed:
		record.authorization.Remediation = domain.RemediationCompleteOAuth
		record.authorization.UserCode = ""
		record.authorization.VerificationURI = ""
	default:
		record.authorization.Remediation = domain.RemediationCompleteOAuth
	}
	return ports.OAuthPollResult{Authorization: record.authorization}, nil
}

var _ ports.OAuthExchangeAdapter = (*stubOAuthExchangeAdapter)(nil)
