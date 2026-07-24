package vault

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// ErrEmptyRenderDigestKey rejects digesters built without a confidential key.
var ErrEmptyRenderDigestKey = errors.New("render digester key is empty")

// HMACRenderDigester is the confidential keyed digester for render create
// fingerprints and prompt digests. The key is process-private; it is never
// logged, written to wire, or exposed through application/domain APIs.
type HMACRenderDigester struct {
	key []byte
}

// NewHMACRenderDigester builds a digester. key must be non-empty; production
// composition fails closed when no durable/injected key is configured.
func NewHMACRenderDigester(key []byte) (*HMACRenderDigester, error) {
	if len(key) == 0 {
		return nil, ErrEmptyRenderDigestKey
	}
	// Copy so callers cannot mutate the digester's key after construction.
	copied := append([]byte(nil), key...)
	return &HMACRenderDigester{key: copied}, nil
}

// DigestPrompt returns HMAC-SHA256 hex of the prompt under the confidential key.
func (d *HMACRenderDigester) DigestPrompt(prompt string) string {
	return d.mac("render.prompt", []byte(prompt))
}

// CreateFingerprint returns a keyed fingerprint over create-side-effect inputs.
func (d *HMACRenderDigester) CreateFingerprint(
	operation domain.RenderOperation,
	model, prompt string,
	inputs []domain.AssetID,
	mask domain.AssetID,
) domain.Fingerprint {
	const sep = "\x1f"
	var payload []byte
	payload = append(payload, []byte("create_render_job")...)
	payload = append(payload, sep...)
	payload = append(payload, []byte(operation)...)
	payload = append(payload, sep...)
	payload = append(payload, []byte(model)...)
	payload = append(payload, sep...)
	payload = append(payload, []byte(prompt)...)
	payload = append(payload, sep...)
	payload = append(payload, []byte(joinAssetIDs(inputs))...)
	payload = append(payload, sep...)
	payload = append(payload, []byte(mask)...)
	return domain.Fingerprint(d.mac("render.create_fingerprint", payload))
}

func (d *HMACRenderDigester) mac(purpose string, material []byte) string {
	mac := hmac.New(sha256.New, d.key)
	_, _ = mac.Write([]byte(purpose))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(material)
	return hex.EncodeToString(mac.Sum(nil))
}

func joinAssetIDs(ids []domain.AssetID) string {
	if len(ids) == 0 {
		return ""
	}
	out := string(ids[0])
	for i := 1; i < len(ids); i++ {
		out += "," + string(ids[i])
	}
	return out
}

// FixtureRenderDigestKey is the deterministic key for controlled fixtures only.
// Production MUST inject a durable configured key or fail closed.
const FixtureRenderDigestKey = "pixelplus-fixture-render-digest-key-v1"

// FailClosedRenderDigester refuses to mint digests when no key is configured.
// Used only so production composition can start with readiness closed.
type FailClosedRenderDigester struct{}

// DigestPrompt returns empty (unusable for replay identity).
func (FailClosedRenderDigester) DigestPrompt(string) string { return "" }

// CreateFingerprint returns empty fingerprint (create path fails closed via
// readiness / validation elsewhere).
func (FailClosedRenderDigester) CreateFingerprint(
	domain.RenderOperation, string, string, []domain.AssetID, domain.AssetID,
) domain.Fingerprint {
	return ""
}

var (
	_ ports.RenderDigester = (*HMACRenderDigester)(nil)
	_ ports.RenderDigester = FailClosedRenderDigester{}
)
