package vault

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// ErrEmptyRenderDigestKey rejects digesters built without a confidential key.
var ErrEmptyRenderDigestKey = errors.New("render digester key is empty")

// ErrWeakRenderDigestKey rejects digesters built with keys shorter than the
// minimum strength bound (32 bytes).
var ErrWeakRenderDigestKey = errors.New("render digester key is too short")

// MinRenderDigestKeyBytes is the minimum confidential key length.
const MinRenderDigestKeyBytes = 32

// HMACRenderDigester is the confidential keyed digester for render create
// fingerprints and prompt digests. The key is process-private; it is never
// logged, written to wire, or exposed through application/domain APIs.
type HMACRenderDigester struct {
	key []byte
}

// NewHMACRenderDigester builds a digester. key must be at least
// MinRenderDigestKeyBytes; production composition fails closed when no durable
// configured/injected key of sufficient strength is available.
func NewHMACRenderDigester(key []byte) (*HMACRenderDigester, error) {
	if len(key) == 0 {
		return nil, ErrEmptyRenderDigestKey
	}
	if len(key) < MinRenderDigestKeyBytes {
		return nil, ErrWeakRenderDigestKey
	}
	// Copy so callers cannot mutate the digester's key after construction.
	copied := append([]byte(nil), key...)
	return &HMACRenderDigester{key: copied}, nil
}

// DigestPrompt returns HMAC-SHA256 hex of the prompt under the confidential key.
func (d *HMACRenderDigester) DigestPrompt(prompt string) (string, error) {
	if d == nil || len(d.key) < MinRenderDigestKeyBytes {
		return "", ports.ErrRenderDigesterUnavailable
	}
	return d.mac("render.prompt", []byte(prompt)), nil
}

// createFingerprintPayload is a typed structured encoding for create fingerprints.
// JSON field names and array order make boundary shifts from delimiter injection
// impossible (model/prompt may contain arbitrary bytes including \u001f).
type createFingerprintPayload struct {
	V         int      `json:"v"`
	Operation string   `json:"op"`
	Model     string   `json:"model"`
	Prompt    string   `json:"prompt"`
	Inputs    []string `json:"inputs"`
	Mask      string   `json:"mask"`
}

// CreateFingerprint returns a keyed fingerprint over create-side-effect inputs.
func (d *HMACRenderDigester) CreateFingerprint(
	operation domain.RenderOperation,
	model, prompt string,
	inputs []domain.AssetID,
	mask domain.AssetID,
) (domain.Fingerprint, error) {
	if d == nil || len(d.key) < MinRenderDigestKeyBytes {
		return "", ports.ErrRenderDigesterUnavailable
	}
	inputStrs := make([]string, 0, len(inputs))
	for _, id := range inputs {
		inputStrs = append(inputStrs, string(id))
	}
	// Preserve empty inputs as empty array (not null) for deterministic JSON.
	if inputStrs == nil {
		inputStrs = []string{}
	}
	payload := createFingerprintPayload{
		V:         1,
		Operation: string(operation),
		Model:     model,
		Prompt:    prompt,
		Inputs:    inputStrs,
		Mask:      string(mask),
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return "", errors.Join(ports.ErrRenderDigesterUnavailable, err)
	}
	return domain.Fingerprint(d.mac("render.create_fingerprint", canonical)), nil
}

func (d *HMACRenderDigester) mac(purpose string, material []byte) string {
	mac := hmac.New(sha256.New, d.key)
	_, _ = mac.Write([]byte(purpose))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(material)
	return hex.EncodeToString(mac.Sum(nil))
}

// FixtureRenderDigestKey is the deterministic ≥32-byte key for controlled fixtures only.
// Production MUST inject a durable configured key or fail closed.
const FixtureRenderDigestKey = "pixelplus-fixture-render-digest-key-v1"

// FailClosedRenderDigester refuses to mint digests when no key is configured.
// Create product paths must treat its errors as dependency_unavailable before
// replay/admission/job side effects (not only /readyz).
type FailClosedRenderDigester struct{}

// DigestPrompt fails closed.
func (FailClosedRenderDigester) DigestPrompt(string) (string, error) {
	return "", ports.ErrRenderDigesterUnavailable
}

// CreateFingerprint fails closed.
func (FailClosedRenderDigester) CreateFingerprint(
	domain.RenderOperation, string, string, []domain.AssetID, domain.AssetID,
) (domain.Fingerprint, error) {
	return "", ports.ErrRenderDigesterUnavailable
}

var (
	_ ports.RenderDigester = (*HMACRenderDigester)(nil)
	_ ports.RenderDigester = FailClosedRenderDigester{}
)
