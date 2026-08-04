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

// ErrEmptyChatDigestKey rejects digesters built without a confidential key.
var ErrEmptyChatDigestKey = errors.New("chat digester key is empty")

// ErrWeakChatDigestKey rejects digesters built with keys shorter than the
// minimum strength bound (32 bytes).
var ErrWeakChatDigestKey = errors.New("chat digester key is too short")

// MinChatDigestKeyBytes is the minimum confidential key length.
const MinChatDigestKeyBytes = 32

// HMACChatDigester is the confidential keyed digester for chat create
// fingerprints. The key is process-private; it is never logged, written to
// wire, or exposed through application/domain APIs.
type HMACChatDigester struct {
	key []byte
}

// NewHMACChatDigester builds a digester. key must be at least
// MinChatDigestKeyBytes; production composition fails closed when no durable
// configured/injected key of sufficient strength is available.
func NewHMACChatDigester(key []byte) (*HMACChatDigester, error) {
	if len(key) == 0 {
		return nil, ErrEmptyChatDigestKey
	}
	if len(key) < MinChatDigestKeyBytes {
		return nil, ErrWeakChatDigestKey
	}
	copied := append([]byte(nil), key...)
	return &HMACChatDigester{key: copied}, nil
}

// chatFingerprintPayload is a typed structured encoding for chat create
// fingerprints. JSON field names and array order make boundary shifts from
// delimiter injection impossible (model/message content may contain arbitrary
// bytes including \u001f). V2 binds every accepted request field — generation
// tuning and x_pixelplus routing inputs included — so a same-key request that
// differs in any contracted field conflicts instead of replaying (idempotency
// policy §5.2). Optional fields use omitempty so an absent field and its
// zero-value semantic equivalent fingerprint identically.
type chatFingerprintPayload struct {
	V         int                  `json:"v"`
	Operation string               `json:"op"`
	Model     string               `json:"model"`
	Messages  []chatMessagePayload `json:"messages"`
	Options   chatOptionsPayload   `json:"options"`
}

type chatMessagePayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// chatOptionsPayload encodes the accepted request fields beyond model and
// messages. Pointer/nilable fields stay presence-aware: an absent tuning field
// and a present one never collide.
type chatOptionsPayload struct {
	Temperature       *float64 `json:"temperature,omitempty"`
	MaxTokens         *int     `json:"max_tokens,omitempty"`
	TopP              *float64 `json:"top_p,omitempty"`
	N                 *int     `json:"n,omitempty"`
	Stop              []string `json:"stop,omitempty"`
	User              string   `json:"user,omitempty"`
	ProviderAccountID string   `json:"provider_account_id,omitempty"`
	AllowFallback     bool     `json:"allow_fallback,omitempty"`
	ConversationID    string   `json:"conversation_id,omitempty"`
}

// CreateFingerprint returns a keyed fingerprint over chat create-side-effect
// inputs: the operation identity, model, ordered canonical messages, and the
// remaining accepted request fields carried by options.
func (d *HMACChatDigester) CreateFingerprint(
	operation domain.ChatOperation,
	model string,
	messages []domain.ChatMessage,
	options domain.ChatRequestOptions,
) (domain.Fingerprint, error) {
	if d == nil || len(d.key) < MinChatDigestKeyBytes {
		return "", ports.ErrChatDigesterUnavailable
	}
	msgs := make([]chatMessagePayload, 0, len(messages))
	for _, m := range messages {
		msgs = append(msgs, chatMessagePayload{Role: string(m.Role), Content: m.Content, Name: m.Name})
	}
	if msgs == nil {
		msgs = []chatMessagePayload{}
	}
	payload := chatFingerprintPayload{
		V:         2,
		Operation: string(operation),
		Model:     model,
		Messages:  msgs,
		Options: chatOptionsPayload{
			Temperature:       options.Temperature,
			MaxTokens:         options.MaxTokens,
			TopP:              options.TopP,
			N:                 options.N,
			Stop:              options.Stop,
			User:              options.User,
			ProviderAccountID: string(options.ProviderAccountID),
			AllowFallback:     options.AllowFallback,
			ConversationID:    options.ConversationID,
		},
	}
	canonical, err := json.Marshal(payload)
	if err != nil {
		return "", errors.Join(ports.ErrChatDigesterUnavailable, err)
	}
	return domain.Fingerprint(d.mac("chat.create_fingerprint", canonical)), nil
}

func (d *HMACChatDigester) mac(purpose string, material []byte) string {
	mac := hmac.New(sha256.New, d.key)
	_, _ = mac.Write([]byte(purpose))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write(material)
	return hex.EncodeToString(mac.Sum(nil))
}

// FixtureChatDigestKey is the deterministic ≥32-byte key for controlled fixtures
// only. Production MUST inject a durable configured key or fail closed.
const FixtureChatDigestKey = "pixelplus-fixture-chat-digest-key-v1"

// FailClosedChatDigester refuses to mint digests when no key is configured.
// Create product paths must treat its errors as dependency_unavailable before
// replay/admission side effects (not only /readyz).
type FailClosedChatDigester struct{}

// CreateFingerprint fails closed.
func (FailClosedChatDigester) CreateFingerprint(
	domain.ChatOperation, string, []domain.ChatMessage, domain.ChatRequestOptions,
) (domain.Fingerprint, error) {
	return "", ports.ErrChatDigesterUnavailable
}

var (
	_ ports.ChatDigester = (*HMACChatDigester)(nil)
	_ ports.ChatDigester = FailClosedChatDigester{}
)
