package vault_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	vaultpkg "github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/vault"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

func TestHMACRenderDigesterStableAndRejectsUnkeyedDictionary(t *testing.T) {
	t.Parallel()

	d, err := vaultpkg.NewHMACRenderDigester([]byte(vaultpkg.FixtureRenderDigestKey))
	if err != nil {
		t.Fatalf("NewHMACRenderDigester: %v", err)
	}
	prompt := "a red circle"
	digest, err := d.DigestPrompt(prompt)
	if err != nil {
		t.Fatalf("DigestPrompt: %v", err)
	}
	fp, err := d.CreateFingerprint(domain.RenderOpImageGeneration, "gpt-image-1", prompt, nil, "")
	if err != nil {
		t.Fatalf("CreateFingerprint: %v", err)
	}

	// Stability under same key.
	d2, _ := d.DigestPrompt(prompt)
	if d2 != digest {
		t.Fatal("DigestPrompt not stable under same key")
	}
	fpAgain, _ := d.CreateFingerprint(domain.RenderOpImageGeneration, "gpt-image-1", prompt, nil, "")
	if fpAgain != fp {
		t.Fatal("CreateFingerprint not stable under same key")
	}
	// Changed prompt => different fingerprint (idempotency conflict input).
	fp2, _ := d.CreateFingerprint(domain.RenderOpImageGeneration, "gpt-image-1", "different prompt", nil, "")
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
	// Different key => different digest (key lifecycle matters).
	other, err := vaultpkg.NewHMACRenderDigester([]byte("another-fixture-key-material-32b!!"))
	if err != nil {
		t.Fatalf("other digester: %v", err)
	}
	otherDigest, _ := other.DigestPrompt(prompt)
	if otherDigest == digest {
		t.Fatal("distinct keys produced identical digests")
	}
}

func TestEmptyAndWeakKeyFailClosed(t *testing.T) {
	t.Parallel()
	if _, err := vaultpkg.NewHMACRenderDigester(nil); err == nil {
		t.Fatal("empty key must fail")
	}
	if _, err := vaultpkg.NewHMACRenderDigester([]byte("short")); !errors.Is(err, vaultpkg.ErrWeakRenderDigestKey) {
		t.Fatalf("short key error = %v, want ErrWeakRenderDigestKey", err)
	}
}

// Separator collision: model containing \u001f must not shift field boundaries
// under structured encoding (would collide with delimiter concat designs).
func TestStructuredFingerprintRejectsSeparatorCollision(t *testing.T) {
	t.Parallel()

	d, err := vaultpkg.NewHMACRenderDigester([]byte(vaultpkg.FixtureRenderDigestKey))
	if err != nil {
		t.Fatalf("digester: %v", err)
	}
	// Crafted pair that collides under "\x1f" concatenation of model+prompt.
	// model="a\x1fb" prompt="c"  vs  model="a" prompt="b\x1fc"
	fp1, err := d.CreateFingerprint(domain.RenderOpImageGeneration, "a\x1fb", "c", nil, "")
	if err != nil {
		t.Fatalf("fp1: %v", err)
	}
	fp2, err := d.CreateFingerprint(domain.RenderOpImageGeneration, "a", "b\x1fc", nil, "")
	if err != nil {
		t.Fatalf("fp2: %v", err)
	}
	if fp1 == fp2 {
		t.Fatal("separator-shift pair produced identical fingerprints; structured encoding required")
	}
}

func TestFailClosedDigesterReturnsError(t *testing.T) {
	t.Parallel()
	var d vaultpkg.FailClosedRenderDigester
	if _, err := d.DigestPrompt("x"); !errors.Is(err, ports.ErrRenderDigesterUnavailable) {
		t.Fatalf("DigestPrompt err = %v", err)
	}
	if _, err := d.CreateFingerprint(domain.RenderOpImageGeneration, "m", "p", nil, ""); !errors.Is(err, ports.ErrRenderDigesterUnavailable) {
		t.Fatalf("CreateFingerprint err = %v", err)
	}
}
