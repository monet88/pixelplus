package domain_test

import (
	"errors"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// TestValidateMaskRelationshipAcceptsMatchingDimensions asserts a mask whose
// kind and pixel dimensions match its target input passes validation (#13
// section 4.3).
func TestValidateMaskRelationshipAcceptsMatchingDimensions(t *testing.T) {
	t.Parallel()

	input := domain.Asset{Kind: domain.AssetKindInput, Width: 64, Height: 64}
	mask := domain.Asset{Kind: domain.AssetKindMask, Width: 64, Height: 64}

	if err := domain.ValidateMaskRelationship(input, mask); err != nil {
		t.Fatalf("ValidateMaskRelationship() error = %v, want nil", err)
	}
}

// TestValidateMaskRelationshipRejectsWrongKind asserts a non-mask-kind
// reference is ErrInvalidMask, distinct from a dimension mismatch.
func TestValidateMaskRelationshipRejectsWrongKind(t *testing.T) {
	t.Parallel()

	input := domain.Asset{Kind: domain.AssetKindInput, Width: 64, Height: 64}
	notMask := domain.Asset{Kind: domain.AssetKindInput, Width: 64, Height: 64}

	err := domain.ValidateMaskRelationship(input, notMask)
	if !errors.Is(err, domain.ErrInvalidMask) {
		t.Fatalf("ValidateMaskRelationship() error = %v, want ErrInvalidMask", err)
	}
	if errors.Is(err, domain.ErrMaskDimensionMismatch) {
		t.Fatal("wrong-kind mask must not also report ErrMaskDimensionMismatch")
	}
}

// TestValidateMaskRelationshipRejectsDimensionMismatch asserts a correctly
// kinded mask with different width/height from the input is the distinct
// ErrMaskDimensionMismatch outcome (#13 section 4.3).
func TestValidateMaskRelationshipRejectsDimensionMismatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input domain.Asset
		mask  domain.Asset
	}{
		{
			name:  "width differs",
			input: domain.Asset{Kind: domain.AssetKindInput, Width: 64, Height: 64},
			mask:  domain.Asset{Kind: domain.AssetKindMask, Width: 32, Height: 64},
		},
		{
			name:  "height differs",
			input: domain.Asset{Kind: domain.AssetKindInput, Width: 64, Height: 64},
			mask:  domain.Asset{Kind: domain.AssetKindMask, Width: 64, Height: 32},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := domain.ValidateMaskRelationship(test.input, test.mask)
			if !errors.Is(err, domain.ErrMaskDimensionMismatch) {
				t.Fatalf("ValidateMaskRelationship() error = %v, want ErrMaskDimensionMismatch", err)
			}
		})
	}
}
