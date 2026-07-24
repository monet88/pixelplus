package persistence_test

import (
	"context"
	"errors"
	"testing"

	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/persistence"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

func TestMemoryRenderStagingPutUseRoundTrip(t *testing.T) {
	t.Parallel()

	store := persistence.NewMemoryRenderStagingStore()
	id := ports.StagingIdentity{
		TenantID:   "tenant_a",
		JobID:      "job_1",
		ManifestID: "manifest_1",
		EntryID:    "out_0",
		Checksum:   "abc",
	}
	payload := []byte{0x01, 0x02, 0x03}
	if err := store.Put(context.Background(), ports.StagingPut{
		Identity:    id,
		ContentType: domain.ContentTypePNG,
		Data:        payload,
	}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	var got []byte
	err := store.Use(context.Background(), ports.StagingAccess{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_a", ClientAPIKeyID: "key_a"},
		Identity:  id,
	}, func(data []byte) error {
		got = append([]byte(nil), data...)
		return nil
	})
	if err != nil {
		t.Fatalf("Use: %v", err)
	}
	if string(got) != string(payload) {
		t.Fatalf("Use data = %v, want %v", got, payload)
	}
}

func TestMemoryRenderStagingForeignTenantNotFound(t *testing.T) {
	t.Parallel()

	store := persistence.NewMemoryRenderStagingStore()
	id := ports.StagingIdentity{
		TenantID:   "tenant_a",
		JobID:      "job_1",
		ManifestID: "m",
		EntryID:    "e",
		Checksum:   "c",
	}
	_ = store.Put(context.Background(), ports.StagingPut{Identity: id, Data: []byte{1}})
	err := store.Use(context.Background(), ports.StagingAccess{
		Principal: domain.SecurityPrincipal{TenantID: "tenant_b", ClientAPIKeyID: "key_b"},
		Identity:  id,
	}, func([]byte) error { return nil })
	if !errors.Is(err, ports.ErrStagingNotFound) {
		t.Fatalf("Use foreign tenant error = %v, want ErrStagingNotFound", err)
	}
}

func TestUnavailableRenderStagingFailsClosed(t *testing.T) {
	t.Parallel()

	store := persistence.NewUnavailableRenderStagingStore()
	err := store.Put(context.Background(), ports.StagingPut{
		Identity: ports.StagingIdentity{
			TenantID: "t", JobID: "j", ManifestID: "m", EntryID: "e", Checksum: "c",
		},
		Data: []byte{1},
	})
	if !errors.Is(err, ports.ErrDependencyUnavailable) {
		t.Fatalf("Put error = %v, want ErrDependencyUnavailable", err)
	}
}
