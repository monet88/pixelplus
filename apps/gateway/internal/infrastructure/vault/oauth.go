package vault

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// FailClosedOAuthExchangeAdapter is the production foundation OAuth exchange
// adapter. No real Provider OAuth surface is wired yet, so every start/poll
// fails closed with ErrDependencyUnavailable rather than inventing a weaker
// local token path.
type FailClosedOAuthExchangeAdapter struct{}

// NewFailClosedOAuthExchangeAdapter builds the fail-closed foundation OAuth adapter.
func NewFailClosedOAuthExchangeAdapter() *FailClosedOAuthExchangeAdapter {
	return &FailClosedOAuthExchangeAdapter{}
}

// Start fails closed because no OAuth exchange surface is configured.
func (*FailClosedOAuthExchangeAdapter) Start(context.Context, ports.OAuthStartCommand) (ports.OAuthStartResult, error) {
	return ports.OAuthStartResult{}, ports.ErrDependencyUnavailable
}

// Poll fails closed because no OAuth exchange surface is configured.
func (*FailClosedOAuthExchangeAdapter) Poll(context.Context, ports.OAuthPollCommand) (ports.OAuthPollResult, error) {
	return ports.OAuthPollResult{}, ports.ErrDependencyUnavailable
}

// MemoryOAuthExchangeAdapter is a foundation memory OAuth adapter used only by
// controlled tests and local composition overrides. Production composition uses
// FailClosedOAuthExchangeAdapter instead.
type MemoryOAuthExchangeAdapter struct {
	mu      sync.Mutex
	next    atomic.Uint64
	records map[domain.OAuthAuthorizationID]*memoryOAuthRecord
}

type memoryOAuthRecord struct {
	authorization domain.OAuthAuthorization
	material      string
	consumed      bool
	outcome       domain.OAuthStatus
}

// NewMemoryOAuthExchangeAdapter builds an empty memory OAuth adapter.
func NewMemoryOAuthExchangeAdapter() *MemoryOAuthExchangeAdapter {
	return &MemoryOAuthExchangeAdapter{records: make(map[domain.OAuthAuthorizationID]*memoryOAuthRecord)}
}

// Start creates one pending journey with deterministic verification metadata.
func (adapter *MemoryOAuthExchangeAdapter) Start(_ context.Context, command ports.OAuthStartCommand) (ports.OAuthStartResult, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	sequence := adapter.next.Add(1)
	id := domain.OAuthAuthorizationID(fmt.Sprintf("oauth_%04d", sequence))
	expiresAt := domain.DefaultOAuthExpiry(time.Now().UTC())
	verificationURI := ""
	userCode := ""
	if command.Flow == domain.OAuthFlowDevice {
		verificationURI = "https://provider.example/device"
		userCode = fmt.Sprintf("CODE-%04d", sequence)
	} else {
		verificationURI = "https://provider.example/authorize"
	}
	authorization := domain.NewOAuthAuthorizationPending(
		id,
		command.AccountID,
		command.Purpose,
		command.Flow,
		verificationURI,
		userCode,
		expiresAt,
	)
	adapter.records[id] = &memoryOAuthRecord{
		authorization: authorization,
		material:      fmt.Sprintf("oauth_exchanged_material_%04d", sequence),
		outcome:       domain.OAuthStatusSucceeded,
	}
	return ports.OAuthStartResult{Authorization: authorization}, nil
}

// Poll returns the current status. The first successful poll hands material once.
func (adapter *MemoryOAuthExchangeAdapter) Poll(_ context.Context, command ports.OAuthPollCommand) (ports.OAuthPollResult, error) {
	adapter.mu.Lock()
	defer adapter.mu.Unlock()

	record, ok := adapter.records[command.AuthorizationID]
	if !ok || record.authorization.ProviderAccountID != command.AccountID {
		return ports.OAuthPollResult{}, ports.ErrOAuthAuthorizationNotVisible
	}
	if record.authorization.Status.Terminal() {
		return ports.OAuthPollResult{Authorization: record.authorization}, nil
	}

	// Default memory adapter auto-succeeds on first poll for local proof; tests
	// override outcomes through the contracttest controlled fake instead.
	record.authorization.Status = record.outcome
	if record.outcome == domain.OAuthStatusSucceeded {
		record.authorization.Remediation = domain.RemediationNone
		record.authorization.UserCode = ""
		record.authorization.VerificationURI = ""
		if !record.consumed {
			record.consumed = true
			return ports.OAuthPollResult{
				Authorization:     record.authorization,
				ExchangedMaterial: record.material,
			}, nil
		}
		return ports.OAuthPollResult{Authorization: record.authorization}, nil
	}
	record.authorization.Remediation = domain.RemediationCompleteOAuth
	record.authorization.UserCode = ""
	record.authorization.VerificationURI = ""
	return ports.OAuthPollResult{Authorization: record.authorization}, nil
}

var (
	_ ports.OAuthExchangeAdapter = (*FailClosedOAuthExchangeAdapter)(nil)
	_ ports.OAuthExchangeAdapter = (*MemoryOAuthExchangeAdapter)(nil)
)
