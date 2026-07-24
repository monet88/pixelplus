package httptransport

import (
	"net/http"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// renderJobWire is the safe Public API projection of a Render Job.
// It never includes prompt text, image bytes, credentials, or temporary URLs.
type renderJobWire struct {
	JobID             string            `json:"job_id"`
	Operation         string            `json:"operation"`
	Model             string            `json:"model,omitempty"`
	LifecycleState    string            `json:"lifecycle_state"`
	ExecutionPhase    string            `json:"execution_phase,omitempty"`
	StateRevision     int64             `json:"state_revision"`
	Progress          progressWire      `json:"progress"`
	ProviderAccountID string            `json:"provider_account_id,omitempty"`
	OutputEntries     []outputEntryWire `json:"output_entries,omitempty"`
	CommitStatus      string            `json:"commit_status,omitempty"`
	// FailureClass/Stage are safe terminal classifications only (no secrets).
	FailureClass string            `json:"failure_class,omitempty"`
	FailureStage string            `json:"failure_stage,omitempty"`
	Cancel       *cancelFieldsWire `json:"cancel,omitempty"`
	CreatedAt    string            `json:"created_at"`
	UpdatedAt    string            `json:"updated_at"`
	// re_render is always false on output-retry responses when present.
	ReRender *bool `json:"re_render,omitempty"`
}

type progressWire struct {
	Source    string `json:"source"`
	Value     *int   `json:"value,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

type outputEntryWire struct {
	OutputEntryID string `json:"output_entry_id"`
	Position      int    `json:"position"`
	DeliveryState string `json:"delivery_state"`
	AssetID       string `json:"asset_id,omitempty"`
	ContentType   string `json:"content_type,omitempty"`
	ByteSize      int64  `json:"byte_size,omitempty"`
	Checksum      string `json:"checksum,omitempty"`
	FailureClass  string `json:"failure_class,omitempty"`
}

type cancelFieldsWire struct {
	RequestedAt            string `json:"requested_at,omitempty"`
	UpstreamAbortAttempted bool   `json:"upstream_abort_attempted"`
	UpstreamStopConfirmed  bool   `json:"upstream_stop_confirmed"`
}

func toRenderJobWire(job domain.RenderJob) renderJobWire {
	wire := renderJobWire{
		JobID:             string(job.JobID),
		Operation:         string(job.Operation),
		Model:             job.Model,
		LifecycleState:    string(job.Lifecycle),
		StateRevision:     job.StateRevision,
		ProviderAccountID: string(job.ProviderAccountID),
		CreatedAt:         timestampString(job.CreatedAt),
		UpdatedAt:         timestampString(job.UpdatedAt),
		Progress: progressWire{
			Source:    string(job.Progress.Source),
			UpdatedAt: timestampString(job.Progress.UpdatedAt),
		},
	}
	if job.ExecutionPhase.Valid() && !job.Lifecycle.Terminal() {
		wire.ExecutionPhase = string(job.ExecutionPhase)
	}
	if job.Progress.Value >= 0 {
		value := job.Progress.Value
		wire.Progress.Value = &value
	}
	if job.CommitStatus.Valid() && job.CommitStatus != domain.CommitNotStarted {
		wire.CommitStatus = string(job.CommitStatus)
	}
	if job.Lifecycle == domain.JobFailed && job.FailureClass != "" {
		wire.FailureClass = string(job.FailureClass)
		if job.FailureStage != "" {
			wire.FailureStage = string(job.FailureStage)
		}
	}
	if len(job.OutputEntries) > 0 {
		wire.OutputEntries = make([]outputEntryWire, 0, len(job.OutputEntries))
		for _, entry := range job.OutputEntries {
			item := outputEntryWire{
				OutputEntryID: string(entry.ID),
				Position:      entry.Position,
				DeliveryState: string(entry.DeliveryState),
				ContentType:   entry.ContentType,
				ByteSize:      entry.ByteSize,
				Checksum:      entry.Checksum,
				FailureClass:  entry.PlacementFailureClass,
			}
			if entry.DeliveryState == domain.OutputAvailable && entry.AssetID != "" {
				item.AssetID = string(entry.AssetID)
			}
			wire.OutputEntries = append(wire.OutputEntries, item)
		}
	}
	if !job.CancelRequestedAt.IsZero() || job.Lifecycle == domain.JobCancelRequested || job.Lifecycle == domain.JobCanceled {
		wire.Cancel = &cancelFieldsWire{
			RequestedAt:            timestampString(job.CancelRequestedAt),
			UpstreamAbortAttempted: job.Lifecycle == domain.JobCancelRequested || job.Lifecycle == domain.JobCanceled,
			// Stop is confirmed only for terminal canceled with no residual.
			UpstreamStopConfirmed: job.Lifecycle == domain.JobCanceled,
		}
	}
	return wire
}

func writeRenderJob(writer http.ResponseWriter, statusCode int, job domain.RenderJob) {
	writeJSON(writer, statusCode, toRenderJobWire(job))
}

// outputRetryWire is the stable OutputRetryResponse projection (async accepted
// placement recovery). It is not a full RenderJob body.
type outputRetryWire struct {
	JobID         string `json:"job_id"`
	OutputEntryID string `json:"output_entry_id"`
	DeliveryState string `json:"delivery_state"`
	AssetID       string `json:"asset_id,omitempty"`
	// re_render is always false on this surface (#14 §8.4).
	ReRender bool `json:"re_render"`
}

func writeOutputRetry(writer http.ResponseWriter, statusCode int, result application.OutputDeliveryResult) {
	wire := outputRetryWire{
		JobID:         string(result.Job.JobID),
		OutputEntryID: string(result.Entry.ID),
		DeliveryState: string(result.Entry.DeliveryState),
		ReRender:      false,
	}
	if result.Entry.DeliveryState == domain.OutputAvailable && result.Entry.AssetID != "" {
		wire.AssetID = string(result.Entry.AssetID)
	}
	writeJSON(writer, statusCode, wire)
}
