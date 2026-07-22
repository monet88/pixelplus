package httptransport

import (
	"net/http"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// Capability Snapshot and model-list wire DTOs mirror the frozen Public API v1
// schemas. Field names and omitempty choices match CapabilitySnapshot,
// CapabilityFact, ModelCapability, Provenance, ModelListResponse, and ModelOffer.

type capabilitySnapshotWire struct {
	ProviderAccountID string                        `json:"provider_account_id"`
	CredentialVersion int                           `json:"credential_version"`
	VerifiedAt        string                        `json:"verified_at"`
	Freshness         string                        `json:"freshness"`
	TTLClass          string                        `json:"ttl_class"`
	Provenance        []provenanceWire              `json:"provenance"`
	Operations        map[string]capabilityFactWire `json:"operations"`
	Models            []modelCapabilityWire         `json:"models"`
}

type capabilityFactWire struct {
	Status         string `json:"status"`
	Offerable      bool   `json:"offerable"`
	EvidenceClass  string `json:"evidence_class"`
	ProbeSurface   string `json:"probe_surface,omitempty"`
	StreamingClass string `json:"streaming_class,omitempty"`
}

type provenanceWire struct {
	EvidenceClass string `json:"evidence_class"`
	ProbeSurface  string `json:"probe_surface,omitempty"`
	ObservedAt    string `json:"observed_at"`
}

type modelCapabilityWire struct {
	ModelSlug      string            `json:"model_slug"`
	Operations     map[string]string `json:"operations"`
	SurfaceBinding string            `json:"surface_binding,omitempty"`
	ObservedAt     string            `json:"observed_at"`
}

type modelListResponseWire struct {
	Object string            `json:"object"`
	Data   []modelObjectWire `json:"data"`
}

type modelObjectWire struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	OwnedBy string              `json:"owned_by"`
	X       modelXPixelPlusWire `json:"x_pixelplus"`
}

type modelXPixelPlusWire struct {
	Offers []modelOfferWire `json:"offers"`
}

type modelOfferWire struct {
	ProviderAccountID string `json:"provider_account_id"`
	Operation         string `json:"operation"`
	OperationStatus   string `json:"operation_status"`
	ModelSlug         string `json:"model_slug"`
	Offerable         bool   `json:"offerable"`
	StreamingClass    string `json:"streaming_class,omitempty"`
	Freshness         string `json:"freshness"`
	VerifiedAt        string `json:"verified_at,omitempty"`
}

func toCapabilitySnapshotWire(snapshot domain.CapabilitySnapshot) capabilitySnapshotWire {
	operations := make(map[string]capabilityFactWire, len(snapshot.Operations))
	for op, fact := range snapshot.Operations {
		operations[string(op)] = capabilityFactWire{
			Status:         string(fact.Status),
			Offerable:      fact.Offerable,
			EvidenceClass:  string(fact.EvidenceClass),
			ProbeSurface:   fact.ProbeSurface,
			StreamingClass: string(fact.StreamingClass),
		}
	}
	provenance := make([]provenanceWire, 0, len(snapshot.Provenance))
	for _, item := range snapshot.Provenance {
		provenance = append(provenance, provenanceWire{
			EvidenceClass: string(item.EvidenceClass),
			ProbeSurface:  item.ProbeSurface,
			ObservedAt:    timestampString(item.ObservedAt),
		})
	}
	models := make([]modelCapabilityWire, 0, len(snapshot.Models))
	for _, model := range snapshot.Models {
		ops := make(map[string]string, len(model.Operations))
		for op, status := range model.Operations {
			ops[string(op)] = string(status)
		}
		models = append(models, modelCapabilityWire{
			ModelSlug:      model.ModelSlug,
			Operations:     ops,
			SurfaceBinding: model.SurfaceBinding,
			ObservedAt:     timestampString(model.ObservedAt),
		})
	}
	return capabilitySnapshotWire{
		ProviderAccountID: string(snapshot.ProviderAccountID),
		CredentialVersion: snapshot.CredentialVersion,
		VerifiedAt:        timestampString(snapshot.VerifiedAt),
		Freshness:         string(snapshot.Freshness),
		TTLClass:          string(snapshot.TTLClass),
		Provenance:        provenance,
		Operations:        operations,
		Models:            models,
	}
}

func writeCapabilitySnapshot(writer http.ResponseWriter, statusCode int, snapshot domain.CapabilitySnapshot) {
	writeJSON(writer, statusCode, toCapabilitySnapshotWire(snapshot))
}

func writeModelList(writer http.ResponseWriter, offers []domain.ModelOffer) {
	// Group offers by model slug into OpenAI-like model objects. Empty list is
	// valid when the Tenant has no currently offerable pairs.
	bySlug := make(map[string][]domain.ModelOffer)
	order := make([]string, 0)
	for _, offer := range offers {
		if _, ok := bySlug[offer.ModelSlug]; !ok {
			order = append(order, offer.ModelSlug)
		}
		bySlug[offer.ModelSlug] = append(bySlug[offer.ModelSlug], offer)
	}

	data := make([]modelObjectWire, 0, len(order))
	for _, slug := range order {
		wireOffers := make([]modelOfferWire, 0, len(bySlug[slug]))
		var created int64
		for _, offer := range bySlug[slug] {
			if !offer.VerifiedAt.IsZero() && created == 0 {
				created = offer.VerifiedAt.Time().Unix()
			}
			wireOffers = append(wireOffers, modelOfferWire{
				ProviderAccountID: string(offer.ProviderAccountID),
				Operation:         string(offer.Operation),
				OperationStatus:   string(offer.OperationStatus),
				ModelSlug:         offer.ModelSlug,
				Offerable:         true,
				StreamingClass:    string(offer.StreamingClass),
				Freshness:         string(offer.Freshness),
				VerifiedAt:        timestampString(offer.VerifiedAt),
			})
		}
		data = append(data, modelObjectWire{
			ID:      slug,
			Object:  "model",
			Created: created,
			OwnedBy: "pixelplus",
			X:       modelXPixelPlusWire{Offers: wireOffers},
		})
	}
	writeJSON(writer, http.StatusOK, modelListResponseWire{Object: "list", Data: data})
}
