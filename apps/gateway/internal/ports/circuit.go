package ports

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ErrCircuitUnavailable reports that the Provider Surface Circuit gate could not
// be evaluated. It is a fail-closed dependency outcome: a caller that cannot
// read the shared circuit state MUST treat the matching surface as blocked so a
// corroborated outage is never bypassed (health/cooldown spec §12.6, §16).
var ErrCircuitUnavailable = errors.New("provider surface circuit unavailable")

// CircuitSurface identifies the cross-Tenant failure domain a Provider Surface
// Circuit gates. Per spec §12.1 the domain is deployment + region + Provider +
// Auth Mode + upstream surface, optionally narrowed by operation. It carries no
// Tenant or account identity because the circuit is shared platform state, not
// per-account health (§12.2, §12.7). Deployment and region are ambient process
// context supplied by the evaluator, so this query key names only the
// account-derived coordinates the request spine can prove.
type CircuitSurface struct {
	Provider domain.Provider
	AuthMode domain.AuthMode
	// Surface is optional on a query. Empty means any server-owned upstream
	// surface for the Provider/Auth Mode domain; stored circuit evidence should
	// remain concrete. It never carries a raw provider URL with credentials.
	Surface string
	// Operation optionally narrows the surface to one capability operation.
	// Empty on a query means any operation and therefore overlaps a concrete
	// operation circuit on the same Provider/Auth Mode/surface.
	Operation domain.CapabilityOperation
}

// CircuitState is the safe projection of whether a Provider Surface Circuit is
// currently open for a surface. Open true means new matching work is blocked
// except designated recovery canaries (§12.6). It MUST NOT carry other Tenants,
// account counts, or identities (§12.13).
type CircuitState struct {
	Open bool
}

// CircuitStore is the cross-Tenant Provider Surface Circuit gate. It answers
// whether new matching work is currently blocked for a surface without exposing
// the corroborating evidence. The circuit is distinct from per-account health
// and from the #7 Auth Mode kill: it adds blocking from correlated bounded
// evidence and never mutates an account's Health State (§12.2, §12.7).
//
// SurfaceOpen MUST fail closed: an unreadable circuit surfaces
// ErrCircuitUnavailable so the caller omits the matching work rather than
// hammering a possibly-open circuit. A composition with no circuit evaluator
// wired reports every surface closed (no open circuit) because absent evidence
// there is genuinely nothing to block; only a wired-but-failing store errors.
type CircuitStore interface {
	SurfaceOpen(context.Context, CircuitSurface) (CircuitState, error)
}
