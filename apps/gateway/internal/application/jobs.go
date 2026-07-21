// Package application owns Gateway use cases and retry ownership policy.
package application

import (
	"context"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// ErrJobExecutionUnavailable fails closed until a durable job use case exists.
var ErrJobExecutionUnavailable = errors.New("job execution is unavailable")

// JobExecutor is the exported worker-facing application seam.
type JobExecutor interface {
	ExecuteJob(context.Context, domain.JobRef) error
}

type foundationJobExecutor struct{}

// NewFoundationJobExecutor returns the safe foundation worker used before job
// execution is implemented by a later vertical slice.
func NewFoundationJobExecutor() JobExecutor {
	return foundationJobExecutor{}
}

func (foundationJobExecutor) ExecuteJob(context.Context, domain.JobRef) error {
	return ErrJobExecutionUnavailable
}
