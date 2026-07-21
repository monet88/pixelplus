// Package ports owns application-facing outbound Gateway contracts.
package ports

import (
	"context"
	"errors"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ErrInvalidJobReference rejects queue payloads without stable ownership.
var ErrInvalidJobReference = errors.New("invalid safe job reference")

// Clock makes time deterministic at contract-test boundaries.
type Clock interface {
	Now() time.Time
}

// IDGenerator makes server-owned identities deterministic at contract-test boundaries.
type IDGenerator interface {
	New(domain.IdentifierKind) (domain.Identifier, error)
}

// SafeJobReference is the secret-free queue projection of a durable JobRef.
type SafeJobReference struct {
	TenantID domain.Identifier
	JobID    domain.Identifier
}

// JobRef validates and converts the queue projection into its domain identity.
func (reference SafeJobReference) JobRef() (domain.JobRef, error) {
	job := domain.JobRef{
		TenantID: reference.TenantID,
		JobID:    reference.JobID,
	}
	if !job.Valid() {
		return domain.JobRef{}, ErrInvalidJobReference
	}
	return job, nil
}

// EnqueueReceipt preserves the accepted safe reference identity.
type EnqueueReceipt struct {
	Reference SafeJobReference
}

// JobHandler consumes one validated safe queue reference.
type JobHandler func(context.Context, SafeJobReference) error

// JobRuntime owns queue delivery and worker lifecycle plumbing.
type JobRuntime interface {
	Restore(context.Context) error
	Enqueue(context.Context, SafeJobReference) (EnqueueReceipt, error)
	Run(context.Context, JobHandler) error
	Close(context.Context) error
}
