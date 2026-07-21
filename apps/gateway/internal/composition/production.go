package composition

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/jobs"
)

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

type randomIDGenerator struct{}

func (randomIDGenerator) New(kind domain.IdentifierKind) (domain.Identifier, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate %s ID: %w", kind, err)
	}
	return domain.Identifier(string(kind) + "_" + hex.EncodeToString(bytes)), nil
}

// ProductionDependencies wires the standard-library foundation implementations.
func ProductionDependencies() Dependencies {
	return Dependencies{
		Runtime: jobs.New(),
		Clock:   systemClock{},
		IDs:     randomIDGenerator{},
	}
}
