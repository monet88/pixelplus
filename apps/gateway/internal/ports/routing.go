package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ErrRoutingPolicyNotFound reports that the Tenant has no durable Routing
// Policy row yet. Application maps this to the fail-closed system default
// projection rather than inventing a public 404 on GET.
var ErrRoutingPolicyNotFound = errors.New("routing policy not found")

// RoutingPolicyChange is one atomic singleton replace for the authenticated
// Tenant. Application validates every referenced account before calling
// Replace so a rejected write never mutates durable state.
type RoutingPolicyChange struct {
	Principal domain.SecurityPrincipal
	Policy    domain.RoutingPolicy
}

// RoutingPolicyStore owns the Tenant singleton Routing Policy. Read is
// Tenant-partitioned; Replace is one atomic operation (ADR 0009 catalogue).
type RoutingPolicyStore interface {
	Read(context.Context, domain.SecurityPrincipal) (domain.RoutingPolicy, error)
	Replace(context.Context, RoutingPolicyChange) (domain.RoutingPolicy, error)
}
