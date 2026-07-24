package vault_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	vaultpkg "github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/vault"
)

func TestHMACRenderDigesterStableAndRejectsUnkeyedDictionary(t *testing.T) {
	t.Parallel()

	d, err := vaultpkg.NewHMACRenderDigester([]byte(vaultpkg.FixtureRenderDigestKey))
	if err != nil {
		t.Fatalf("NewHMACRenderDigester: %v", err)
	}
	prompt := "a red circle"
	digest := d.DigestPrompt(prompt)
	fp := d.CreateFingerprint(domain.RenderOpImageGeneration, "gpt-image-1", prompt, nil, "")

	// Stability under same key.
	if d.DigestPrompt(prompt) != digest {
		t.Fatal("DigestPrompt not stable under same key")
	}
	if d.CreateFingerprint(domain.RenderOpImageGeneration, "gpt-image-1", prompt, nil, "") != fp {
		t.Fatal("CreateFingerprint not stable under same key")
	}
	// Changed prompt => different fingerprint (idempotency conflict input).
	fp2 := d.CreateFingerprint(domain.RenderOpImageGeneration, "gpt-image-1", "different prompt", nil, "")
	if fp == fp2 {
		t.Fatal("distinct prompts produced identical fingerprints")
	}
	// Must not embed raw prompt or record separators.
	if strings.Contains(string(fp), prompt) || strings.Contains(digest, prompt) {
		t.Fatal("digest embeds raw prompt")
	}
	// Dictionary oracle: unkeyed SHA-256(prompt) must not equal keyed digest.
	raw := sha256.Sum256([]byte(prompt))
	unkeyed := hex.EncodeToString(raw[:])
	if digest == unkeyed {
		t.Fatal("PromptDigest equals unkeyed SHA-256(prompt); dictionary oracle remains")
	}
	// Unkeyed fingerprint of same canonical concat must not match keyed fp.
	// (naive dictionary candidate)
	if string(fp) == unkeyed {
		t.Fatal("fingerprint equals unkeyed SHA-256 of prompt alone")
	}
	// Different key => different digest (key lifecycle matters).
	other, err := vaultpkg.NewHMACRenderDigester([]byte("another-fixture-key-material"))
	if err != nil {
		t.Fatalf("other digester: %v", err)
	}
	if other.DigestPrompt(prompt) == digest {
		t.Fatal("distinct keys produced identical digests")
	}
}

func TestEmptyKeyFailsClosed(t *testing.T) {
	t.Parallel()
	if _, err := vaultpkg.NewHMACRenderDigester(nil); err == nil {
		t.Fatal("empty key must fail")
	}
}
