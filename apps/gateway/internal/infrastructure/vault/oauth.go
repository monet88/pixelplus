package vault

import (
	"context"

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

var _ ports.OAuthExchangeAdapter = (*FailClosedOAuthExchangeAdapter)(nil)
