// Package observability owns secret-free logs, metrics, and audit delivery.
package observability

import (
	"context"
	"log/slog"

	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// SlogAuditRecorder emits the secret-free product/security audit projection to
// the structured logger. It records only safe actor, Tenant, resource, and
// outcome fields; no credential, prompt, or bearer material can reach it
// because the port type carries none (#21 observability).
type SlogAuditRecorder struct {
	logger *slog.Logger
}

// NewSlogAuditRecorder builds an audit recorder over the given logger.
func NewSlogAuditRecorder(logger *slog.Logger) *SlogAuditRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogAuditRecorder{logger: logger}
}

// Record writes one safe audit event.
func (recorder *SlogAuditRecorder) Record(_ context.Context, event ports.AuditEvent) error {
	recorder.log(event)
	return nil
}

// RecordBatch accepts the complete logical mutation as one call. The batch is
// accepted wholly before any emission; there is no sequential per-event
// accept/fail path. Slog delivery itself cannot fail, so once accepted every
// event in the batch is emitted (or none were accepted).
func (recorder *SlogAuditRecorder) RecordBatch(_ context.Context, events []ports.AuditEvent) error {
	if len(events) == 0 {
		return nil
	}
	// Accept first (no fail path for slog), then emit the whole batch.
	for _, event := range events {
		recorder.log(event)
	}
	return nil
}

func (recorder *SlogAuditRecorder) log(event ports.AuditEvent) {
	recorder.logger.Info("gateway.audit",
		"action", string(event.Action),
		"tenant_id", string(event.TenantID),
		"client_api_key_id", string(event.ClientAPIKeyID),
		"provider_account_id", string(event.ProviderAccountID),
		"request_id", string(event.RequestID),
		"outcome", event.Outcome,
	)
}

// SlogTelemetryRecorder emits safe operational telemetry aggregated by stable
// operation, code, and status only. It never uses prompt, Asset, credential, or
// bearer values as labels.
type SlogTelemetryRecorder struct {
	logger *slog.Logger
}

// NewSlogTelemetryRecorder builds a telemetry recorder over the given logger.
func NewSlogTelemetryRecorder(logger *slog.Logger) *SlogTelemetryRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogTelemetryRecorder{logger: logger}
}

// Record writes one safe telemetry event.
func (recorder *SlogTelemetryRecorder) Record(_ context.Context, event ports.TelemetryEvent) error {
	recorder.logger.Debug("gateway.telemetry",
		"operation", string(event.Operation),
		"code", string(event.Code),
		"status_code", event.StatusCode,
	)
	return nil
}

// SlogRequestLogRecorder emits exactly one canonical JSON request log line per
// HTTP request using the fixed safe field set from #21. It is never an
// authorization proof.
type SlogRequestLogRecorder struct {
	logger *slog.Logger
}

// NewSlogRequestLogRecorder builds a request-log recorder over the given logger.
func NewSlogRequestLogRecorder(logger *slog.Logger) *SlogRequestLogRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogRequestLogRecorder{logger: logger}
}

// Record writes the single canonical request log line.
func (recorder *SlogRequestLogRecorder) Record(_ context.Context, log ports.RequestLog) error {
	recorder.logger.Info("gateway.request",
		"request_id", string(log.RequestID),
		"user_id", string(log.UserID),
		"action", log.Action,
		"duration_ms", log.DurationMS,
		"status_code", log.StatusCode,
		"message", log.Message,
	)
	return nil
}

var (
	_ ports.AuditRecorder      = (*SlogAuditRecorder)(nil)
	_ ports.AuditBatchRecorder = (*SlogAuditRecorder)(nil)
	_ ports.TelemetryRecorder  = (*SlogTelemetryRecorder)(nil)
	_ ports.RequestLogRecorder = (*SlogRequestLogRecorder)(nil)
)

// SlogAssetAuditRecorder emits the secret-free Asset product/security audit
// projection to the structured logger. It records only safe actor, Tenant,
// resource, and outcome fields; no Asset bytes, prompt, credential, or foreign
// id can reach it because the port type carries none (#13 section 8.5,
// I-ASSET-REDACT).
type SlogAssetAuditRecorder struct {
	logger *slog.Logger
}

// NewSlogAssetAuditRecorder builds an Asset audit recorder over the given logger.
func NewSlogAssetAuditRecorder(logger *slog.Logger) *SlogAssetAuditRecorder {
	if logger == nil {
		logger = slog.Default()
	}
	return &SlogAssetAuditRecorder{logger: logger}
}

// Record writes one safe Asset audit event.
func (recorder *SlogAssetAuditRecorder) Record(_ context.Context, event ports.AssetAuditEvent) error {
	recorder.logger.Info("gateway.audit",
		"action", string(event.Action),
		"tenant_id", string(event.TenantID),
		"client_api_key_id", string(event.ClientAPIKeyID),
		"asset_id", string(event.AssetID),
		"request_id", string(event.RequestID),
		"outcome", event.Outcome,
	)
	return nil
}

var _ ports.AssetAuditRecorder = (*SlogAssetAuditRecorder)(nil)
