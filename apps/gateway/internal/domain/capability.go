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

// WeakerOfferableStatus projects the more restrictive of two offerable statuses.
// Callers must only pass statuses already known to be in the offerable set.
func WeakerOfferableStatus(left, right CapabilityStatus) CapabilityStatus {
	if left == CapabilityConditionallySupported || right == CapabilityConditionallySupported {
		return CapabilityConditionallySupported
	}
	if left == CapabilityVerified {
		return right
	}
	return left
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
	// Always clone reference maps so derived recomputation never mutates the
	// store-owned snapshot or a shared observation map (data-race / corruption).
	snapshot.Operations = cloneOperationFacts(snapshot.Operations)
	snapshot.Models = cloneModelCapabilities(snapshot.Models)
	snapshot.Freshness = DeriveFreshness(snapshot.VerifiedAt, snapshot.TTLClass, snapshot.Invalidated, now)
	for op, fact := range snapshot.Operations {
		fact.Offerable = snapshot.Freshness == SnapshotFresh && fact.Status.Offerable()
		snapshot.Operations[op] = fact
	}
	return snapshot
}

// WithAccountOfferGate ANDs fact-level offerable with account usability so
// management inspect and listModels share the same authorization signal.
func (snapshot CapabilitySnapshot) WithAccountOfferGate(allows bool) CapabilitySnapshot {
	if allows {
		return snapshot
	}
	snapshot.Operations = cloneOperationFacts(snapshot.Operations)
	for op, fact := range snapshot.Operations {
		fact.Offerable = false
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
	// Clone before fill/default so minting never mutates adapter-owned maps.
	operations = cloneOperationFacts(operations)
	for _, op := range PrimaryCapabilityOperations() {
		if _, ok := operations[op]; !ok {
			operations[op] = CapabilityFact{
				Status:        CapabilityUnverified,
				EvidenceClass: EvidenceLiveProbe,
				ProbeSurface:  probeSurface,
			}
		}
	}
	// Model rows must always carry all five primary operation keys so wire
	// serialization matches the frozen ModelCapability schema.
	normalizedModels := cloneModelCapabilities(models)
	for i := range normalizedModels {
		normalizedModels[i].Operations = normalizeModelOperations(normalizedModels[i].Operations)
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
		Models:     normalizedModels,
	}
	return snapshot.WithDerivedFreshness(verifiedAt.Time())
}

func cloneOperationFacts(source map[CapabilityOperation]CapabilityFact) map[CapabilityOperation]CapabilityFact {
	if source == nil {
		return map[CapabilityOperation]CapabilityFact{}
	}
	cloned := make(map[CapabilityOperation]CapabilityFact, len(source))
	for op, fact := range source {
		cloned[op] = fact
	}
	return cloned
}

func cloneModelCapabilities(source []ModelCapability) []ModelCapability {
	if len(source) == 0 {
		return nil
	}
	cloned := make([]ModelCapability, len(source))
	for i, model := range source {
		model.Operations = normalizeModelOperations(model.Operations)
		cloned[i] = model
	}
	return cloned
}

func normalizeModelOperations(source map[CapabilityOperation]CapabilityStatus) map[CapabilityOperation]CapabilityStatus {
	normalized := make(map[CapabilityOperation]CapabilityStatus, len(PrimaryCapabilityOperations()))
	for _, op := range PrimaryCapabilityOperations() {
		if status, ok := source[op]; ok {
			normalized[op] = status
			continue
		}
		normalized[op] = CapabilityUnverified
	}
	return normalized
}
