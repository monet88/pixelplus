package application

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// CreateChatCompletionCommand is the typed non-streaming chat completion request.
// Streaming (T16) and cancel (T17) are separate tickets; this surface is
// strictly stream=false.
type CreateChatCompletionCommand struct {
	RequestID            domain.Identifier
	PresentedKeyMaterial string
	IdempotencyKey       string
	Model                string
	Messages             []domain.ChatMessage
	// Options carries every remaining accepted request field: the generation
	// tuning values (shape-validated at the boundary, bound into the
	// idempotency fingerprint, not consumed by the Adapter until T19–T23) and
	// the documented x_pixelplus routing inputs (OpenAI-compatible contract
	// §3.5). See domain.ChatRequestOptions.
	Options domain.ChatRequestOptions
	// OversizeBody / MalformedBody are observed at the transport boundary and
	// carried as flags so the single normative A0→A1→A2 order is enforced here
	// (a pre-authenticated oversize still fails authentication_failed).
	OversizeBody  bool
	MalformedBody bool
}

// ChatResult is the synchronous completion outcome returned to transport.
type ChatResult struct {
	Completion domain.ChatCompletion
	RequestID  domain.Identifier
}

// ChatService runs the protected non-streaming chat spine: authenticate (A0),
// scope (A1), request validation + digest (A2), replay claim, admission (A3-A5),
// deterministic same-Tenant routing (P1-P4), selected-account gates (lifecycle,
// risk, capability, health, circuit), Vault/credential authorization, one
// synchronous non-streaming Adapter execution with a single-owner fallback walk
// (P5) requiring authoritative no-commit proof, and exactly-once accounting
// settlement against the original Tenant + Client API Key.
type ChatService struct {
	principal    ports.PrincipalStore
	admission    ports.AdmissionStore
	replay       ports.ChatReplayStore
	accounts     ports.AccountStore
	health       ports.HealthStore
	capabilities ports.CapabilityStore
	circuits     ports.CircuitStore
	routing      ports.RoutingPolicyStore
	affinity     ports.ChatAffinityStore
	vault        ports.CredentialVault
	digester     ports.ChatDigester
	authorized   ports.AuthorizedChat
	audit        ports.ChatAuditRecorder
	telemetry    ports.TelemetryRecorder
	requestLog   ports.RequestLogRecorder
	clock        ports.Clock
	ids          ports.IDGenerator
	// authorizedStream is the protected streaming execution boundary (T16). A
	// nil value keeps StreamChat failing closed as dependency_unavailable rather
	// than degrading a streaming request into a non-streaming answer.
	authorizedStream ports.AuthorizedChatStream
	// streamLeases records the hard P2 chat_stream account binding. A nil store
	// means no lease is taken; the stream still cannot hop accounts once content
	// has been delivered.
	streamLeases ports.ChatStreamLeaseStore
}

// ChatDependencies bundles the controlled ports the chat spine owns.
type ChatDependencies struct {
	Principal    ports.PrincipalStore
	Admission    ports.AdmissionStore
	Replay       ports.ChatReplayStore
	Accounts     ports.AccountStore
	Health       ports.HealthStore
	Capabilities ports.CapabilityStore
	Circuits     ports.CircuitStore
	Routing      ports.RoutingPolicyStore
	Affinity     ports.ChatAffinityStore
	Vault        ports.CredentialVault
	Digester     ports.ChatDigester
	Authorized   ports.AuthorizedChat
	Audit        ports.ChatAuditRecorder
	Telemetry    ports.TelemetryRecorder
	RequestLog   ports.RequestLogRecorder
	Clock        ports.Clock
	IDs          ports.IDGenerator
	// AuthorizedStream is the protected streaming execution boundary (T16).
	AuthorizedStream ports.AuthorizedChatStream
	// StreamLeases records hard chat_stream account leases (routing spec §5.2).
	StreamLeases ports.ChatStreamLeaseStore
}

// NewChatService validates and wires the chat spine dependencies.
func NewChatService(dependencies ChatDependencies) (*ChatService, error) {
	switch {
	case dependencies.Principal == nil:
		return nil, errors.New("application: principal store is required")
	case dependencies.Admission == nil:
		return nil, errors.New("application: admission store is required")
	case dependencies.Replay == nil:
		return nil, errors.New("application: chat replay store is required")
	case dependencies.Accounts == nil:
		return nil, errors.New("application: account store is required")
	case dependencies.Health == nil:
		return nil, errors.New("application: health store is required")
	case dependencies.Capabilities == nil:
		return nil, errors.New("application: capability store is required")
	case dependencies.Routing == nil:
		return nil, errors.New("application: routing policy store is required")
	case dependencies.Affinity == nil:
		return nil, errors.New("application: chat affinity store is required")
	case dependencies.Vault == nil:
		return nil, errors.New("application: credential vault is required")
	case dependencies.Digester == nil:
		return nil, errors.New("application: chat digester is required")
	case dependencies.Authorized == nil:
		return nil, errors.New("application: authorized chat port is required")
	case dependencies.Audit == nil:
		return nil, errors.New("application: chat audit recorder is required")
	case dependencies.Telemetry == nil:
		return nil, errors.New("application: telemetry recorder is required")
	case dependencies.RequestLog == nil:
		return nil, errors.New("application: request log recorder is required")
	case dependencies.Clock == nil:
		return nil, errors.New("application: clock is required")
	case dependencies.IDs == nil:
		return nil, errors.New("application: ID generator is required")
	}
	return &ChatService{
		principal:    dependencies.Principal,
		admission:    dependencies.Admission,
		replay:       dependencies.Replay,
		accounts:     dependencies.Accounts,
		health:       dependencies.Health,
		capabilities: dependencies.Capabilities,
		circuits:     dependencies.Circuits,
		routing:      dependencies.Routing,
		affinity:     dependencies.Affinity,
		vault:        dependencies.Vault,
		digester:     dependencies.Digester,
		authorized:   dependencies.Authorized,
		audit:        dependencies.Audit,
		telemetry:    dependencies.Telemetry,
		requestLog:   dependencies.RequestLog,
		clock:        dependencies.Clock,
		ids:          dependencies.IDs,

		authorizedStream: dependencies.AuthorizedStream,
		streamLeases:     dependencies.StreamLeases,
	}, nil
}

// CreateChatCompletion runs the full gate sequence and returns the canonical
// Provider-independent completion synchronously.
func (service *ChatService) CreateChatCompletion(ctx context.Context, command CreateChatCompletionCommand) (ChatResult, error) {
	sc := spineContext{
		operation: domain.OperationChatCompletion,
		requestID: service.resolveRequestID(command.RequestID),
		start:     service.clock.Now(),
	}

	principal, canonical, ok := service.authenticate(ctx, ports.PresentedClientAPIKey{Material: command.PresentedKeyMaterial})
	if !ok {
		return ChatResult{}, service.fail(ctx, sc, canonical)
	}
	sc.keyID = principal.ClientAPIKeyID

	// A1 scope.
	if !principal.Scopes.Has(domain.ChatOpCompletion.RequiredScope()) {
		return ChatResult{}, service.fail(ctx, sc, domain.NewForbidden())
	}

	// A2 size/malformed/request validation (single normative order).
	if command.OversizeBody {
		return ChatResult{}, service.fail(ctx, sc, domain.NewRequestTooLarge())
	}
	if command.MalformedBody {
		return ChatResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	if command.Model == "" || len(command.Messages) == 0 {
		return ChatResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}
	for _, message := range command.Messages {
		if !message.Valid() {
			return ChatResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
		}
	}
	if command.IdempotencyKey == "" || utf8.RuneCountInString(command.IdempotencyKey) > maxIdempotencyKeyLength {
		return ChatResult{}, service.fail(ctx, sc, domain.NewInvalidRequest())
	}

	// Keyed digester must succeed before any replay/admission side effect. The
	// fingerprint binds every accepted request field (model, messages, tuning,
	// x_pixelplus routing inputs), so a same-key request differing in any of
	// them conflicts instead of replaying (idempotency policy §5.2).
	fingerprint, err := service.digester.CreateFingerprint(domain.ChatOpCompletion, command.Model, command.Messages, command.Options)
	if err != nil {
		return ChatResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	identity := domain.ReplayIdentity{
		Scope: domain.ReplayScope{
			TenantID:       principal.TenantID,
			ClientAPIKeyID: principal.ClientAPIKeyID,
			Key:            command.IdempotencyKey,
		},
		Fingerprint: fingerprint,
	}
	decision, err := service.replay.Claim(ctx, identity)
	if err != nil {
		return ChatResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	switch decision.Outcome {
	case ports.ReplayClaimed:
		// sole owner continues below
	case ports.ReplayTerminal:
		// Matching replay: return the original completion with no new Adapter call
		// and no new admission debit.
		service.recordTelemetry(ctx, sc.operation, "", 200)
		service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), 200, "ok", sc.start)
		_ = service.chatAudit(ctx, sc, principal, decision.TerminalResult.ProviderAccountID, decision.TerminalResult.ExecutionID, "replayed")
		return ChatResult{Completion: decision.TerminalResult, RequestID: sc.requestID}, nil
	case ports.ReplayInProgress:
		return ChatResult{}, service.fail(ctx, sc, domain.NewIdempotencyInProgress())
	case ports.ReplayConflict:
		return ChatResult{}, service.fail(ctx, sc, domain.NewIdempotencyConflict())
	case ports.ReplayUncertain:
		return ChatResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	default:
		return ChatResult{}, service.fail(ctx, sc, domain.NewInternalError())
	}

	// A3-A5 admission before routing/side effects.
	reservation, canonical, ok := service.admit(ctx, principal, domain.OperationChatCompletion)
	if !ok {
		if abErr := service.abandon(ctx, identity); abErr != nil {
			return ChatResult{}, service.fail(ctx, sc, service.dependencyCanonical(abErr))
		}
		return ChatResult{}, service.fail(ctx, sc, canonical)
	}

	// Deterministic same-Tenant routing (P1 pin → P3 affinity → P4 policy).
	account, policy, canonical, ok := service.selectAccount(ctx, principal, domain.ChatOpCompletion, command.Model, sc.start, command.Options.ProviderAccountID, command.Options.ConversationID)
	if !ok {
		return ChatResult{}, service.failAfterRollback(ctx, sc, canonical, reservation, identity)
	}

	executionID, err := service.ids.New(domain.IdentifierKindExecution)
	if err != nil {
		return ChatResult{}, service.failAfterRollback(ctx, sc, domain.NewInternalError(), reservation, identity)
	}

	// One synchronous execution with a single-owner fallback walk (P5). This is
	// the ONLY place a chat re-attempt is decided; neither the Adapter nor the
	// transport retries autonomously.
	completion, canonical, ok := service.runExecution(ctx, sc, principal, account, policy, command, executionID)
	reservation.SettlementKey = chatSettlementKey(principal, executionID)
	if !ok {
		// Exactly-once accounting/concurrency settlement against original
		// Tenant+key on terminal failure too.
		if err := service.admission.Reconcile(ctx, reservation); err != nil {
			return ChatResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
		}
		// Authoritative no-commit proof known: abandon the fresh claim so a later
		// retry can re-claim (no steal; we own it). Commit unknown: leave the
		// claim untouched — never replace or steal an uncertain execution.
		if canonical.Code != domain.ErrCodeExecutionPossiblyCommitted {
			_ = service.abandon(ctx, identity)
		}
		return ChatResult{}, service.fail(ctx, sc, canonical)
	}

	// Exactly-once accounting/concurrency settlement against original Tenant+key.
	if err := service.admission.Reconcile(ctx, reservation); err != nil {
		return ChatResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}

	// Record terminal replay and success audit.
	if err := service.replay.Complete(ctx, identity, ports.ChatReplayResult{Completion: completion}); err != nil {
		// Uncertain replay leaves the client to retry; do not return the completion
		// as if it were replay-stable.
		return ChatResult{}, service.fail(ctx, sc, domain.NewIdempotencyUncertain())
	}

	// P3 affinity: remember the account that served this conversation. Best-effort
	// by design — affinity is a soft preference (routing spec §5.1), never an
	// authority, so a record failure only loses a hint and must never fail a
	// completed execution.
	if command.Options.ConversationID != "" && policy.Affinity.Enabled {
		_ = service.affinity.Record(ctx, domain.ChatAffinityScope{TenantID: principal.TenantID, Key: command.Options.ConversationID}, completion.ProviderAccountID)
	}
	if err := service.chatAudit(ctx, sc, principal, completion.ProviderAccountID, completion.ExecutionID, "completed"); err != nil {
		return ChatResult{}, service.fail(ctx, sc, service.dependencyCanonical(err))
	}
	service.recordTelemetry(ctx, sc.operation, "", 200)
	service.recordRequestLog(ctx, sc.requestID, principal.ClientAPIKeyID, string(sc.operation), 200, "ok", sc.start)
	return ChatResult{Completion: completion, RequestID: sc.requestID}, nil
}

// chatRequest is the validated execution inputs shared by primary + fallback.
type chatRequest struct {
	model    string
	messages []domain.ChatMessage
}

// runExecution walks the primary account then, on an authoritative no-commit
// outcome with fallback enabled, exactly one ordered fallback chain (P5).
func (service *ChatService) runExecution(
	ctx context.Context,
	sc spineContext,
	principal domain.SecurityPrincipal,
	primary domain.ProviderAccount,
	policy domain.RoutingPolicy,
	command CreateChatCompletionCommand,
	executionID domain.Identifier,
) (domain.ChatCompletion, domain.CanonicalError, bool) {
	request := chatRequest{model: command.Model, messages: command.Messages}
	attempts := service.attemptAccounts(ctx, sc, principal, primary, policy, domain.ChatOpCompletion, request, command.Options.AllowFallback)

	for index, account := range attempts {
		completion, canonical, committed := service.attemptOnAccount(ctx, sc, principal, account, request, executionID)
		if committed {
			return completion, domain.CanonicalError{}, true
		}
		if canonical.Code == domain.ErrCodeExecutionPossiblyCommitted {
			// Commit unknown: fail closed, never fall back or replace.
			return domain.ChatCompletion{}, canonical, false
		}
		if index < len(attempts)-1 {
			// Authoritative no-commit proof: continue the single fallback walk.
			continue
		}
		// not_committed and no further fallback candidate: terminal failure.
		return domain.ChatCompletion{}, canonical, false
	}
	return domain.ChatCompletion{}, domain.NewInternalError(), false
}

// attemptAccounts is the primary account plus, when fallback is enabled and the
// primary is not committed, exactly the viable FallbackChain ordered once.
// allowFallback is the client x_pixelplus hint; it only permits (never forces)
// the fallback walk and remains subject to Tenant Routing Policy and the
// single-owner authoritative no-commit proof.
//
// operation is the operation the client actually requested (non-streaming chat
// or streaming). Candidates MUST be vetted against it rather than a hardcoded
// one: vetting a streaming request's fallback targets against non-streaming
// `chat` would both admit accounts that cannot stream and discard accounts that
// can (routing spec §6.3 — the capability match is on the requested `op`+`m`).
func (service *ChatService) attemptAccounts(
	ctx context.Context,
	sc spineContext,
	principal domain.SecurityPrincipal,
	primary domain.ProviderAccount,
	policy domain.RoutingPolicy,
	operation domain.ChatOperation,
	request chatRequest,
	allowFallback bool,
) []domain.ProviderAccount {
	attempts := []domain.ProviderAccount{primary}
	if !policy.FallbackEnabled && !allowFallback {
		return attempts
	}
	var fallback []domain.ProviderAccount
	for _, id := range policy.FallbackChain {
		account, err := service.accounts.Visible(ctx, principal, id)
		if err != nil {
			continue
		}
		if !principal.AllowsProviderAccount(id) {
			continue
		}
		// NF-XMODE (routing spec §6.2, §7.1): a fallback target on a DIFFERENT
		// Auth Mode is permitted only when the Tenant policy explicitly names
		// BOTH the primary's and the target's mode in fallback_auth_modes;
		// otherwise the walk fails closed on the primary's own outcome.
		// Same-mode fallback moves between accounts, not modes, so it needs no
		// listing. Prohibited/experimental modes are already excluded by
		// candidateRejection below.
		if account.AuthMode != primary.AuthMode && !fallbackModesAllow(policy.FallbackAuthModes, primary.AuthMode, account.AuthMode) {
			continue
		}
		if _, ok := service.candidateRejection(ctx, principal, account, operation, request.model, sc.start); !ok {
			continue
		}
		fallback = append(fallback, account)
	}
	return append(attempts, fallback...)
}

// fallbackModesAllow reports whether the policy's fallback_auth_modes names
// both the primary and the target Auth Mode (routing spec §6.2: cross-mode
// fallback is allowed only if the policy names both modes; it is never silent).
func fallbackModesAllow(modes []domain.AuthMode, primary domain.AuthMode, target domain.AuthMode) bool {
	havePrimary, haveTarget := false, false
	for _, mode := range modes {
		if mode == primary {
			havePrimary = true
		}
		if mode == target {
			haveTarget = true
		}
	}
	return havePrimary && haveTarget
}

// attemptOnAccount runs the selected-account gates + Vault + authorized chat for
// one account. committed=true only on a committed canonical completion.
func (service *ChatService) attemptOnAccount(
	ctx context.Context,
	sc spineContext,
	principal domain.SecurityPrincipal,
	account domain.ProviderAccount,
	request chatRequest,
	executionID domain.Identifier,
) (domain.ChatCompletion, domain.CanonicalError, bool) {
	// X2 selected-account reaffirmation (chat lifecycle §3.1 X2): immediately
	// before any credential access, re-assert the chosen account's current
	// usability + capability. A candidate that degraded between selection and
	// Adapter entry is rejected here rather than reaching upstream.
	if canonical, ok := service.candidateRejection(ctx, principal, account, domain.ChatOpCompletion, request.model, sc.start); !ok {
		return domain.ChatCompletion{}, canonical, false
	}

	// Vault presence gate: credential version must be authorized before Adapter.
	validation, err := service.vault.Validate(ctx, ports.CredentialValidation{
		Principal: principal,
		AccountID: account.ID,
		AuthMode:  account.AuthMode,
		Version:   account.Credential.Version,
	})
	if err != nil {
		if errors.Is(err, ports.ErrCredentialAbsent) {
			return domain.ChatCompletion{}, domain.NewAccountNotUsable(domain.RemediationSubmitCredential), false
		}
		return domain.ChatCompletion{}, service.dependencyCanonical(err), false
	}
	if !validation.Valid {
		return domain.ChatCompletion{}, domain.NewAccountNotUsable(domain.RemediationSubmitCredential), false
	}

	outcome, err := service.authorized.Chat(ctx, ports.AuthorizedChatRequest{
		Principal:    principal,
		AccountID:    account.ID,
		AuthMode:     account.AuthMode,
		Version:      account.Credential.Version,
		Operation:    domain.ChatOpCompletion,
		Model:        request.model,
		Messages:     request.messages,
		RequestID:    sc.requestID,
		ExecutionID:  executionID,
		SendBoundary: noopChatSendBoundary{},
	})
	if err != nil {
		if errors.Is(err, ports.ErrCredentialAbsent) {
			return domain.ChatCompletion{}, domain.NewAccountNotUsable(domain.RemediationSubmitCredential), false
		}
		if errors.Is(err, ports.ErrChatAdapterUnavailable) || errors.Is(err, ports.ErrDependencyUnavailable) {
			return domain.ChatCompletion{}, domain.NewDependencyUnavailable(), false
		}
		return domain.ChatCompletion{}, service.dependencyCanonical(err), false
	}
	return service.classifyOutcome(outcome, sc, principal, account, request, executionID)
}

// classifyOutcome maps the safe Adapter ChatOutcome to a canonical result.
func (service *ChatService) classifyOutcome(
	outcome domain.ChatOutcome,
	sc spineContext,
	principal domain.SecurityPrincipal,
	account domain.ProviderAccount,
	request chatRequest,
	executionID domain.Identifier,
) (domain.ChatCompletion, domain.CanonicalError, bool) {
	switch outcome.Class {
	case domain.ChatOutcomeCommitted:
		// A committed-class outcome without authoritative commit proof
		// (CommitUnknown) must not be returned as committed: fail closed as
		// possibly_committed before any replay Complete or accounting settlement,
		// so an uncertain upstream execution is never treated as committed
		// (decision 0012).
		if outcome.Commit == domain.CommitUnknown {
			return domain.ChatCompletion{}, domain.NewExecutionPossiblyCommitted(), false
		}
		if outcome.Commit != domain.CommitCommitted {
			// No authoritative no-commit proof and not committed: malformed outcome.
			return domain.ChatCompletion{}, domain.NewExecutionPossiblyCommitted(), false
		}
		// A committed result must carry the exact canonical choice shape (assistant
		// message, valid finish_class). A malformed committed completion fails
		// closed (never HTTP 200) and, because we cannot prove non-commit, never
		// falls back or releases the replay claim.
		if !validCommittedCompletion(outcome.Completion) {
			return domain.ChatCompletion{}, domain.NewExecutionPossiblyCommitted(), false
		}
		completion := outcome.Completion
		// Ensure the canonical safe metadata is complete and Provider-independent.
		if completion.ID == "" {
			completion.ID = executionID
		}
		if completion.ExecutionID == "" {
			completion.ExecutionID = executionID
		}
		if completion.RequestID == "" {
			completion.RequestID = sc.requestID
		}
		if completion.Object == "" {
			completion.Object = "chat.completion"
		}
		if completion.Created.IsZero() {
			completion.Created = service.nowTS()
		}
		completion.Model = request.model
		completion.ProviderAccountID = account.ID
		return completion, domain.CanonicalError{}, true
	case domain.ChatOutcomeNotCommitted:
		// Authoritative no-commit proof from the Adapter. Map to the provider
		// runtime canonical when a safe class is present, else generic rejection.
		return domain.ChatCompletion{}, service.notCommittedCanonical(outcome.FailureClass), false
	case domain.ChatOutcomeUnknown:
		return domain.ChatCompletion{}, domain.NewExecutionPossiblyCommitted(), false
	default:
		return domain.ChatCompletion{}, domain.NewInternalError(), false
	}
}

// validCommittedCompletion reports whether a committed ChatOutcome carries the
// exact canonical choice shape required to be returned as HTTP 200: at least one
// choice, each with an assistant message and a valid finish_class. A malformed
// committed result must fail closed rather than be presented as successful
// (OpenAI-compatible contract §3.4/§3.6).
func validCommittedCompletion(completion domain.ChatCompletion) bool {
	if len(completion.Choices) == 0 {
		return false
	}
	for _, choice := range completion.Choices {
		if choice.Message.Role != domain.ChatRoleAssistant {
			return false
		}
		if !choice.FinishClass.Valid() {
			return false
		}
	}
	return true
}

// notCommittedCanonical maps a safe provider failure class to its canonical
// error. The Adapter's NotCommitted outcome is authoritative no-commit proof
// regardless of class, so an unrecognized/empty class maps to the generic
// provider_rejected — never to possibly_committed, which would discard the
// proof, block the single-owner fallback walk, and leak the replay claim
// (decision 0012).
func (service *ChatService) notCommittedCanonical(class domain.ErrorCode) domain.CanonicalError {
	switch class {
	case domain.ErrCodeProviderRateLimited:
		return domain.NewProviderRateLimited()
	case domain.ErrCodeProviderQuotaExhausted:
		return domain.NewProviderQuotaExhausted()
	case domain.ErrCodeProviderAuthExpired:
		return domain.NewProviderAuthExpired()
	case domain.ErrCodeProviderChallenged:
		return domain.NewProviderChallenged()
	case domain.ErrCodeProviderBanned:
		return domain.NewProviderBanned()
	case domain.ErrCodeProviderRejected:
		return domain.NewProviderRejected()
	case domain.ErrCodeUpstreamTimeout:
		return domain.NewUpstreamTimeout()
	case domain.ErrCodeUpstreamUnavailable:
		return domain.NewUpstreamUnavailable()
	case domain.ErrCodeUpstreamProtocolDrift:
		return domain.NewUpstreamProtocolDrift()
	default:
		return domain.NewProviderRejected()
	}
}

// selectAccount builds the deterministic same-Tenant candidate and returns the
// primary account plus the policy (for fallback). P1 pin → P3 affinity → P4
// policy precedence; never widens. A non-empty pinned ID restricts selection to
// that same-Tenant candidate (x_pixelplus provider_account_id); a foreign or
// unknown pin fails closed 404-class non-enumerating before candidate work
// (routing spec §4.1 P1, §3.2; chat spec §8 rule 1), while a visible
// same-Tenant pin is still vetted by C0–C5 and fails with the specific gate
// class.
func (service *ChatService) selectAccount(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	operation domain.ChatOperation,
	model string,
	now time.Time,
	pinned domain.ProviderAccountID,
	conversationID string,
) (domain.ProviderAccount, domain.RoutingPolicy, domain.CanonicalError, bool) {
	policy, err := service.routing.Read(ctx, principal)
	if err != nil {
		if errors.Is(err, ports.ErrRoutingPolicyNotFound) {
			policy = domain.FailClosedDefaultRoutingPolicy()
		} else {
			return domain.ProviderAccount{}, policy, service.dependencyCanonical(err), false
		}
	}

	if pinned != "" {
		// P1 visibility: a foreign or unknown pin is indistinguishable
		// (ErrAccountNotVisible) and maps to the non-enumerating 404-class —
		// never a 403/409 that would confirm existence, and never a fallback
		// candidate. Zero Adapter call, zero Vault decrypt for the foreign id.
		if _, err := service.accounts.Visible(ctx, principal, pinned); err != nil {
			if errors.Is(err, ports.ErrAccountNotVisible) {
				return domain.ProviderAccount{}, policy, domain.NewResourceNotFound(), false
			}
			return domain.ProviderAccount{}, policy, service.dependencyCanonical(err), false
		}
	}

	order := policy.SelectionOrder
	if len(order) == 0 {
		order = policy.CandidateAccounts
	}
	if len(order) == 0 {
		return domain.ProviderAccount{}, policy, domain.NewRoutingNoCandidate(), false
	}

	var candidates []domain.ProviderAccount
	var lastCanonical domain.CanonicalError
	for _, id := range order {
		if pinned != "" && id != pinned {
			// Routing pin: only the named same-Tenant candidate may serve this
			// request. Foreign/unpinned candidates are never selected.
			continue
		}
		account, err := service.accounts.Visible(ctx, principal, id)
		if err != nil {
			continue
		}
		if !principal.AllowsProviderAccount(id) {
			continue
		}
		if canonical, ok := service.candidateRejection(ctx, principal, account, operation, model, now); !ok {
			lastCanonical = canonical
			continue
		}
		candidates = append(candidates, account)
	}
	if len(candidates) == 0 {
		if lastCanonical.Code != "" {
			return domain.ProviderAccount{}, policy, lastCanonical, false
		}
		return domain.ProviderAccount{}, policy, domain.NewRoutingNoCandidate(), false
	}

	// P3 affinity (routing spec §5.1, chat spec §5.2): a policy-enabled soft
	// preference for the account that last served this conversation. It only
	// selects within the surviving candidate set — a preferred account that left
	// the set (or a store miss/error) falls through to P4, never to a foreign
	// or cross-Auth-Mode account. An explicit pin (P1) outranks affinity.
	if pinned == "" && conversationID != "" && policy.Affinity.Enabled {
		if preferred, ok, err := service.affinity.Preferred(ctx, domain.ChatAffinityScope{TenantID: principal.TenantID, Key: conversationID}); err == nil && ok {
			for _, candidate := range candidates {
				if candidate.ID == preferred {
					return candidate, policy, domain.CanonicalError{}, true
				}
			}
		}
	}

	// P4: first surviving policy-ordered candidate (deterministic).
	return candidates[0], policy, domain.CanonicalError{}, true
}

func (service *ChatService) candidateRejection(
	ctx context.Context,
	principal domain.SecurityPrincipal,
	account domain.ProviderAccount,
	operation domain.ChatOperation,
	model string,
	now time.Time,
) (domain.CanonicalError, bool) {
	// C3 risk / C2 usability.
	if account.AuthMode.Prohibited() || account.AuthMode.Experimental() {
		return domain.NewAuthModeUnavailable(), false
	}
	if account.AuthMode.RequiresRiskAck() && !account.RiskAcknowledged {
		return domain.NewRiskAckRequired(), false
	}
	if account.Lifecycle != domain.LifecycleActive {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	// C2/C5 health: load the AUTHORITATIVE HealthStore snapshot (ADR 0009), never
	// the AccountStore copy. HealthStore owns scoped conditions and recovery
	// permits; blocked accounts must not reach credential/Adapter execution, and
	// missing health evidence must fail closed rather than fail open.
	health, err := service.health.Read(ctx, principal, account.ID)
	if err != nil {
		if errors.Is(err, ports.ErrHealthNotFound) {
			return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
		}
		return service.dependencyCanonical(err), false
	}
	if health.Health.SummaryState == domain.HealthUnknown {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if account.Controls.Drain == domain.DrainDraining || account.Controls.Quarantine == domain.QuarantineQuarantined {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	if !account.Controls.AuthModeExecutionEnabled {
		return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
	}
	// C5 health: cooling/blocked/challenged/expired on the matching scoped
	// condition only. A condition scoped to a different operation or model must
	// not block this request's account+operation+model pair (health spec §5 rule
	// 7, I-HEALTH-SCOPED).
	for _, condition := range health.Health.Conditions {
		if condition.CredentialVersion != account.Credential.Version {
			continue
		}
		scoped := chatHealthScopeCovers(condition.Scope, operation, model)
		switch condition.State {
		case domain.HealthCoolingDown:
			if scoped {
				return domain.NewProviderCooldownBlocked(0), false
			}
		case domain.HealthBlocked, domain.HealthChallenged, domain.HealthExpired:
			if scoped {
				return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
			}
		case domain.HealthUnknown:
			if condition.Scope.Kind == domain.HealthScopeAccount {
				return domain.NewAccountNotUsable(domain.RemediationAccountRemediation), false
			}
		}
	}

	// C4 capability.
	snapshot, err := service.capabilities.Get(ctx, principal, account.ID)
	if err != nil {
		if errors.Is(err, ports.ErrCapabilitySnapshotNotFound) {
			return domain.NewCapabilityUnverified(), false
		}
		return service.dependencyCanonical(err), false
	}
	derived := snapshot.WithDerivedFreshness(now)
	switch derived.Freshness {
	case domain.SnapshotStale, domain.SnapshotInvalid:
		return domain.NewSnapshotStale(), false
	case domain.SnapshotFresh:
	default:
		return domain.NewCapabilityUnverified(), false
	}
	capOp := operation.CapabilityOperation()
	opFact, ok := derived.Operations[capOp]
	if !ok || !opFact.Status.Offerable() {
		if ok && opFact.Status == domain.CapabilityUnsupported {
			return domain.NewCapabilityUnsupported(), false
		}
		return domain.NewCapabilityUnverified(), false
	}
	// Model availability when models are present.
	if model != "" && len(derived.Models) > 0 {
		found := false
		for _, m := range derived.Models {
			if m.ModelSlug != model {
				continue
			}
			if derived.IsOfferablePair(capOp, m, now) {
				found = true
				break
			}
		}
		if !found {
			return domain.NewModelUnavailable(), false
		}
	}

	// Circuit gate when wired.
	if service.circuits != nil {
		circuit, err := service.circuits.SurfaceOpen(ctx, ports.CircuitSurface{
			Provider:  account.Provider,
			AuthMode:  account.AuthMode,
			Operation: capOp,
		})
		if err != nil {
			if errors.Is(err, ports.ErrCircuitUnavailable) {
				return domain.NewDependencyUnavailable(), false
			}
			return service.dependencyCanonical(err), false
		}
		if circuit.Open {
			return domain.NewProviderCooldownBlocked(0), false
		}
	}
	return domain.CanonicalError{}, true
}

// chatHealthScopeCovers reports whether a health condition's scope covers this
// candidate account+operation+model pair (health spec §5 rule 7, I-HEALTH-SCOPED).
// Account scope covers the account; operation scope covers only the matching
// chat operation; model scope covers only the matching operation+model. A
// condition scoped elsewhere must not block this request.
func chatHealthScopeCovers(scope domain.HealthScope, operation domain.ChatOperation, model string) bool {
	switch scope.Kind {
	case domain.HealthScopeAccount:
		return true
	case domain.HealthScopeOperation:
		return scope.Operation == string(operation.CapabilityOperation())
	case domain.HealthScopeModel:
		return scope.Operation == string(operation.CapabilityOperation()) && scope.ModelSlug == model
	}
	return false
}

// authenticate resolves presented Client API Key material to a Security
// Principal. All failures (missing, malformed, unknown, wrong-secret, revoked)
// map to the single indistinguishable authentication_failed.
func (service *ChatService) authenticate(ctx context.Context, key ports.PresentedClientAPIKey) (domain.SecurityPrincipal, domain.CanonicalError, bool) {
	principal, err := service.principal.Authenticate(ctx, key)
	if err != nil {
		return domain.SecurityPrincipal{}, domain.NewAuthenticationFailed(), false
	}
	if !principal.Valid() {
		return domain.SecurityPrincipal{}, domain.NewAuthenticationFailed(), false
	}
	return principal, domain.CanonicalError{}, true
}

func (service *ChatService) admit(ctx context.Context, principal domain.SecurityPrincipal, operation domain.OperationToken) (ports.AdmissionReservation, domain.CanonicalError, bool) {
	decision, reservation, err := service.admission.Admit(ctx, ports.AdmissionRequest{Principal: principal, Operation: operation})
	if err != nil {
		return ports.AdmissionReservation{}, service.dependencyCanonical(err), false
	}
	if decision.Admitted {
		return reservation, domain.CanonicalError{}, true
	}
	switch decision.Stage {
	case ports.AdmissionStageRateLimit:
		return ports.AdmissionReservation{}, domain.NewRateLimit(), false
	case ports.AdmissionStageConcurrency:
		return ports.AdmissionReservation{}, domain.NewConcurrencyLimit(), false
	case ports.AdmissionStageQuota:
		return ports.AdmissionReservation{}, domain.NewQuotaExhausted(), false
	default:
		return ports.AdmissionReservation{}, domain.NewInternalError(), false
	}
}

// chatSettlementKey is the keyed-idempotent settlement identity for one chat
// execution: settled exactly once against the original Tenant + Client API Key
// (chat spec §6.5.5 — reconciliation remains charged to the originating
// client_api_key_id).
func chatSettlementKey(principal domain.SecurityPrincipal, executionID domain.Identifier) string {
	return string(principal.TenantID) + "/" + string(principal.ClientAPIKeyID) + "/" + string(executionID) + "/chat_occupancy"
}

func (service *ChatService) abandon(ctx context.Context, identity domain.ReplayIdentity) error {
	return service.replay.Abandon(ctx, identity)
}

func (service *ChatService) failAfterRollback(
	ctx context.Context,
	sc spineContext,
	primary domain.CanonicalError,
	reservation ports.AdmissionReservation,
	identity domain.ReplayIdentity,
) error {
	// Pre-adapter rejection: release occupancy + abandon the fresh claim.
	rbErr := errors.Join(
		service.admission.Reconcile(ctx, reservation),
		service.abandon(ctx, identity),
	)
	if rbErr != nil {
		return service.fail(ctx, sc, service.dependencyCanonical(rbErr))
	}
	return service.fail(ctx, sc, primary)
}

func (service *ChatService) dependencyCanonical(err error) domain.CanonicalError {
	if errors.Is(err, ports.ErrDependencyUnavailable) ||
		errors.Is(err, ports.ErrChatAdapterUnavailable) ||
		errors.Is(err, ports.ErrChatDigesterUnavailable) {
		return domain.NewDependencyUnavailable()
	}
	return domain.NewInternalError()
}

func (service *ChatService) chatAudit(ctx context.Context, sc spineContext, principal domain.SecurityPrincipal, accountID domain.ProviderAccountID, executionID domain.Identifier, outcome string) error {
	action := chatAuditAction(sc.operation, outcome)
	return service.audit.Record(ctx, ports.ChatAuditEvent{
		Action:            action,
		TenantID:          principal.TenantID,
		ClientAPIKeyID:    principal.ClientAPIKeyID,
		ProviderAccountID: accountID,
		RequestID:         sc.requestID,
		ExecutionID:       executionID,
		Outcome:           outcome,
	})
}

// chatAuditAction maps the spine operation plus terminal outcome onto the
// audit action. A streaming spine must never label its events with the
// non-streaming action, otherwise the audit trail reports a stream terminal as
// `chat_completion.completed` and the streaming actions never appear.
func chatAuditAction(operation domain.OperationToken, outcome string) ports.ChatAuditAction {
	// A replay is a replay on both spines: it re-delivers a durable record without
	// a new Adapter call, so it must stay distinguishable from a live terminal.
	// Checking the streaming operation first would label a streamed replay
	// `stream_terminal`, making it indistinguishable from a fresh generation.
	if outcome == "replayed" {
		return ports.AuditChatReplayed
	}
	if operation == domain.OperationChatCompletionStreaming {
		if outcome == "stream_opened" {
			return ports.AuditChatStreamOpened
		}
		return ports.AuditChatStreamTerminal
	}
	return ports.AuditChatCompleted
}

func (service *ChatService) fail(ctx context.Context, sc spineContext, canonical domain.CanonicalError) error {
	canonical = canonical.WithRequestID(sc.requestID)
	statusCode := canonical.HTTPStatus()
	service.recordTelemetry(ctx, sc.operation, canonical.Code, statusCode)
	service.recordRequestLog(ctx, sc.requestID, sc.keyID, string(sc.operation), statusCode, string(canonical.Code), sc.start)
	return canonical
}

func (service *ChatService) recordTelemetry(ctx context.Context, operation domain.OperationToken, code domain.ErrorCode, statusCode int) {
	_ = service.telemetry.Record(ctx, ports.TelemetryEvent{
		Operation:  operation,
		Code:       code,
		StatusCode: statusCode,
	})
}

func (service *ChatService) recordRequestLog(ctx context.Context, requestID domain.Identifier, keyID domain.ClientAPIKeyID, action string, statusCode int, message string, start time.Time) {
	_ = service.requestLog.Record(ctx, ports.RequestLog{
		RequestID:  requestID,
		UserID:     keyID,
		Action:     action,
		DurationMS: service.durationMS(start),
		StatusCode: statusCode,
		Message:    message,
	})
}

func (service *ChatService) durationMS(start time.Time) int64 {
	return service.clock.Now().Sub(start).Milliseconds()
}

func (service *ChatService) nowTS() domain.Timestamp {
	return domain.NewTimestamp(service.clock.Now())
}

func (service *ChatService) resolveRequestID(boundaryID domain.Identifier) domain.Identifier {
	if boundaryID != "" {
		return boundaryID
	}
	id, err := service.ids.New(domain.IdentifierKindRequest)
	if err != nil {
		return domain.Identifier("request_unavailable")
	}
	return id
}

// noopChatSendBoundary is the synchronous payload-send marker. It is passed to
// AuthorizedChat so the send surface is marked immediately before Adapter entry.
type noopChatSendBoundary struct{}

// MarkPayloadSent is a no-op for a synchronous single attempt; re-attempt and
// occupancy semantics are owned by the application execution layer, so no
// durable cross-request state is required here.
func (noopChatSendBoundary) MarkPayloadSent(context.Context) error { return nil }
