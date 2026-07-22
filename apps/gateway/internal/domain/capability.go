package domain

import "time"

// CapabilityOperation is one of the five primary capability-bearing operations.
type CapabilityOperation string

const (
	CapabilityOpChat            CapabilityOperation = "chat"
	CapabilityOpChatStreaming   CapabilityOperation = "chat_streaming"
	CapabilityOpImageGeneration CapabilityOperation = "image_generation"
	CapabilityOpImageEdit       CapabilityOperation = "image_edit"
	CapabilityOpInpaint         CapabilityOperation = "inpaint"
)

// PrimaryCapabilityOperations returns the five AC-required operations.
func PrimaryCapabilityOperations() []CapabilityOperation {
	return []CapabilityOperation{
		CapabilityOpChat,
		CapabilityOpChatStreaming,
		CapabilityOpImageGeneration,
		CapabilityOpImageEdit,
		CapabilityOpInpaint,
	}
}

// CapabilityStatus is the per-operation/per-model verification token.
type CapabilityStatus string

const (
	CapabilityVerified               CapabilityStatus = "verified"
	CapabilityConditionallySupported CapabilityStatus = "conditionally_supported"
	CapabilityUnsupported            CapabilityStatus = "unsupported"
	CapabilityUnverified             CapabilityStatus = "unverified"
)

// Offerable reports whether the status may be client-facing.
func (status CapabilityStatus) Offerable() bool {
	return status == CapabilityVerified || status == CapabilityConditionallySupported
}

// SnapshotFreshness is the derived lifecycle of a Capability Snapshot.
type SnapshotFreshness string

const (
	SnapshotFresh   SnapshotFreshness = "fresh"
	SnapshotStale   SnapshotFreshness = "stale"
	SnapshotInvalid SnapshotFreshness = "invalid"
)

// EvidenceClass is how a capability fact was learned.
type EvidenceClass string

const (
	EvidenceReferenceLearned EvidenceClass = "reference_learned"
	EvidenceUpstreamVerified EvidenceClass = "upstream_verified"
	EvidenceLiveProbe        EvidenceClass = "live_probe"
	EvidenceHybrid           EvidenceClass = "hybrid"
)

// TTLClass is a named freshness budget.
type TTLClass string

const (
	TTLProbeLive TTLClass = "TTL-PROBE-LIVE"
	TTLInherited TTLClass = "TTL-INHERITED"
	TTLDiscovery TTLClass = "TTL-DISCOVERY"
	TTLDegraded  TTLClass = "TTL-DEGRADED"
)

// StreamingClass distinguishes real vs synthetic streaming.
type StreamingClass string

const (
	StreamingReal      StreamingClass = "real"
	StreamingSynthetic StreamingClass = "synthetic"
)

// CapabilityFact is one operation-level fact on a snapshot.
type CapabilityFact struct {
	Status         CapabilityStatus
	Offerable      bool
	EvidenceClass  EvidenceClass
	ProbeSurface   string
	StreamingClass StreamingClass
}

// Provenance is safe non-secret evidence provenance.
type Provenance struct {
	EvidenceClass EvidenceClass
	ProbeSurface  string
	ObservedAt    Timestamp
}

// ModelCapability is one observed model row. Operations map every primary op.
type ModelCapability struct {
	ModelSlug      string
	Operations     map[CapabilityOperation]CapabilityStatus
	SurfaceBinding string
	ObservedAt     Timestamp
}

// CapabilitySnapshot is the Tenant-owned per-account capability evidence.
// It never carries credential material.
type CapabilitySnapshot struct {
	ProviderAccountID ProviderAccountID
	AuthMode          AuthMode
	CredentialVersion int
	VerifiedAt        Timestamp
	Freshness         SnapshotFreshness
	TTLClass          TTLClass
	Provenance        []Provenance
	Operations        map[CapabilityOperation]CapabilityFact
	Models            []ModelCapability
	// Invalidated marks hard non-use (credential change, durable gate, explicit purge).
	Invalidated bool
}

// ModelOffer is one client-facing offerable model/operation pair.
type ModelOffer struct {
	ProviderAccountID ProviderAccountID
	Operation         CapabilityOperation
	OperationStatus   CapabilityStatus
	ModelSlug         string
	Offerable         bool
	StreamingClass    StreamingClass
	Freshness         SnapshotFreshness
	VerifiedAt        Timestamp
}

// DefaultProbeLiveTTL is the MVP numeric budget for TTL-PROBE-LIVE. #17 may retune.
const DefaultProbeLiveTTL = 15 * time.Minute

// DeriveFreshness computes freshness from verified_at, TTL class, now, and invalidation.
func DeriveFreshness(verifiedAt Timestamp, ttlClass TTLClass, invalidated bool, now time.Time) SnapshotFreshness {
	if invalidated {
		return SnapshotInvalid
	}
	if verifiedAt.IsZero() {
		return SnapshotInvalid
	}
	budget := DefaultProbeLiveTTL
	switch ttlClass {
	case TTLInherited:
		budget = 10 * time.Minute
	case TTLDiscovery:
		budget = 5 * time.Minute
	case TTLDegraded:
		budget = 2 * time.Minute
	}
	if now.After(verifiedAt.Time().Add(budget)) {
		return SnapshotStale
	}
	return SnapshotFresh
}

// IsOfferablePair reports whether a model+operation pair may appear on /models.
func (snapshot CapabilitySnapshot) IsOfferablePair(op CapabilityOperation, model ModelCapability, now time.Time) bool {
	freshness := DeriveFreshness(snapshot.VerifiedAt, snapshot.TTLClass, snapshot.Invalidated, now)
	if freshness != SnapshotFresh {
		return false
	}
	status, ok := model.Operations[op]
	if !ok || !status.Offerable() {
		return false
	}
	// Operation-level status must also be offerable.
	opFact, ok := snapshot.Operations[op]
	if !ok || !opFact.Status.Offerable() {
		return false
	}
	return true
}

// WithDerivedFreshness returns a copy with Freshness and per-op offerable flags recomputed.
func (snapshot CapabilitySnapshot) WithDerivedFreshness(now time.Time) CapabilitySnapshot {
	snapshot.Freshness = DeriveFreshness(snapshot.VerifiedAt, snapshot.TTLClass, snapshot.Invalidated, now)
	for op, fact := range snapshot.Operations {
		fact.Offerable = snapshot.Freshness == SnapshotFresh && fact.Status.Offerable()
		snapshot.Operations[op] = fact
	}
	return snapshot
}

// NewLiveProbeSnapshot builds a credential-version-bound snapshot from live probe evidence.
func NewLiveProbeSnapshot(
	accountID ProviderAccountID,
	mode AuthMode,
	version int,
	verifiedAt Timestamp,
	operations map[CapabilityOperation]CapabilityFact,
	models []ModelCapability,
	probeSurface string,
) CapabilitySnapshot {
	if operations == nil {
		operations = map[CapabilityOperation]CapabilityFact{}
	}
	for _, op := range PrimaryCapabilityOperations() {
		if _, ok := operations[op]; !ok {
			operations[op] = CapabilityFact{
				Status:        CapabilityUnverified,
				EvidenceClass: EvidenceLiveProbe,
				ProbeSurface:  probeSurface,
			}
		}
	}
	snapshot := CapabilitySnapshot{
		ProviderAccountID: accountID,
		AuthMode:          mode,
		CredentialVersion: version,
		VerifiedAt:        verifiedAt,
		TTLClass:          TTLProbeLive,
		Provenance: []Provenance{{
			EvidenceClass: EvidenceLiveProbe,
			ProbeSurface:  probeSurface,
			ObservedAt:    verifiedAt,
		}},
		Operations: operations,
		Models:     models,
	}
	return snapshot.WithDerivedFreshness(verifiedAt.Time())
}
