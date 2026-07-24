package composition_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/monet88/pixelplus/apps/gateway/internal/composition"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
	"github.com/monet88/pixelplus/apps/gateway/internal/infrastructure/persistence"
	"github.com/monet88/pixelplus/apps/gateway/internal/ports"
)

// routingPrincipal authenticates one key with routing.read + routing.manage so
// public GET/PUT can exercise the UnavailableRoutingPolicyStore substitute.
type routingPrincipal struct {
	material  string
	principal domain.SecurityPrincipal
}

func (store routingPrincipal) Authenticate(_ context.Context, key ports.PresentedClientAPIKey) (domain.SecurityPrincipal, error) {
	if key.Material != store.material {
		return domain.SecurityPrincipal{}, ports.ErrAuthentication
	}
	return store.principal, nil
}

// AC: corrupt or lock-occupied routing-policy companion file fails closed at
// composition restore — readiness stays closed, GET/PUT return
// dependency_unavailable, and the durable ledger is not rewritten by a replace.
func TestRoutingPolicyRestoreFailureFailsClosedHTTP(t *testing.T) {
	t.Parallel()

	const bearer = "sk-pxp_locatorRoute_secretRoute"
	principal := routingPrincipal{
		material: bearer,
		principal: domain.SecurityPrincipal{
			TenantID:       "tenant_a",
			ClientAPIKeyID: "key_route",
			Scopes:         domain.NewScopeSet(domain.ScopeRoutingRead, domain.ScopeRoutingManage),
		},
	}

	cases := []struct {
		name string
		prep func(t *testing.T, accountPath string)
	}{
		{
			name: "occupied_lock",
			prep: func(t *testing.T, accountPath string) {
				t.Helper()
				lock := accountPath + ".routing-policy.ledger.lock"
				if err := os.WriteFile(lock, []byte("held\n"), 0o600); err != nil {
					t.Fatalf("write lock: %v", err)
				}
			},
		},
		{
			name: "corrupt_ledger",
			prep: func(t *testing.T, accountPath string) {
				t.Helper()
				ledger := accountPath + ".routing-policy.ledger"
				if err := os.WriteFile(ledger, []byte("not-json{\n"), 0o600); err != nil {
					t.Fatalf("write corrupt ledger: %v", err)
				}
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			accountPath := filepath.Join(dir, "accounts")
			// Seed empty account/health companions so only routing restore fails.
			if err := os.WriteFile(accountPath, nil, 0o600); err != nil {
				t.Fatalf("seed account path: %v", err)
			}
			tc.prep(t, accountPath)

			// Capture pre-restore ledger bytes for corrupt case (mutation proof).
			ledgerPath := accountPath + ".routing-policy.ledger"
			beforeLedger, _ := os.ReadFile(ledgerPath)

			runtime, err := composition.New(composition.Config{
				ProviderAccountStorePath: accountPath,
				StartupTimeout:           2 * time.Second,
			}, composition.Dependencies{
				Runtime:   inertJobRuntime{},
				Clock:     systemClock{},
				IDs:       sequenceIDsValue{},
				Principal: principal,
				// Force account/health success so the routing companion is the
				// sole readiness failure under the production file path.
				Accounts: persistence.NewMemoryAccountStore(),
				Health:   persistence.NewMemoryHealthStore(),
			})
			if err != nil {
				t.Fatalf("composition.New() error = %v", err)
			}
			t.Cleanup(func() {
				_ = runtime.Close(context.Background())
			})

			if runtime.Ready() {
				t.Fatal("Ready() = true, want false when routing policy restore fails")
			}
			if runtime.Healthy() != true {
				t.Fatal("Healthy() = false, want true (process lives; readiness closed)")
			}

			server := httptest.NewServer(runtime.Handler())
			t.Cleanup(server.Close)

			readyResp, err := server.Client().Get(server.URL + "/readyz")
			if err != nil {
				t.Fatalf("GET /readyz error = %v", err)
			}
			readyBody, _ := io.ReadAll(readyResp.Body)
			readyResp.Body.Close()
			if readyResp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("GET /readyz status = %d, want 503 (body=%s)", readyResp.StatusCode, readyBody)
			}

			getReq, err := http.NewRequest(http.MethodGet, server.URL+"/v1/routing-policy", nil)
			if err != nil {
				t.Fatalf("NewRequest GET: %v", err)
			}
			getReq.Header.Set("Authorization", "Bearer "+bearer)
			getResp, err := server.Client().Do(getReq)
			if err != nil {
				t.Fatalf("GET /v1/routing-policy error = %v", err)
			}
			getBody, _ := io.ReadAll(getResp.Body)
			getResp.Body.Close()
			if getResp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("GET routing status = %d, want 503 (body=%s)", getResp.StatusCode, getBody)
			}
			assertDependencyUnavailable(t, getBody)

			putBody := `{
				"candidate_accounts": [],
				"selection_order": [],
				"fallback_enabled": false,
				"fallback_chain": [],
				"fallback_auth_modes": [],
				"affinity": {"enabled": false},
				"lease_policy": {"enabled": false, "eligible_units": []}
			}`
			putReq, err := http.NewRequest(http.MethodPut, server.URL+"/v1/routing-policy", strings.NewReader(putBody))
			if err != nil {
				t.Fatalf("NewRequest PUT: %v", err)
			}
			putReq.Header.Set("Authorization", "Bearer "+bearer)
			putReq.Header.Set("Content-Type", "application/json")
			putResp, err := server.Client().Do(putReq)
			if err != nil {
				t.Fatalf("PUT /v1/routing-policy error = %v", err)
			}
			putPayload, _ := io.ReadAll(putResp.Body)
			putResp.Body.Close()
			if putResp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("PUT routing status = %d, want 503 (body=%s)", putResp.StatusCode, putPayload)
			}
			assertDependencyUnavailable(t, putPayload)

			afterLedger, err := os.ReadFile(ledgerPath)
			if err == nil && string(afterLedger) != string(beforeLedger) {
				t.Fatalf("routing ledger mutated after failed restore path: before=%q after=%q", beforeLedger, afterLedger)
			}
			// Occupied lock: ledger may not exist; still must not create a successful policy write.
			if _, err := os.Stat(ledgerPath); err == nil {
				if tc.name == "occupied_lock" && len(afterLedger) > 0 {
					// A successful Replace would write JSON records; none should appear
					// while the substitute Unavailable store is installed.
					if strings.Contains(string(afterLedger), "system_default") || strings.Contains(string(afterLedger), "key_route") {
						t.Fatalf("ledger contains policy write after unavailable path: %s", afterLedger)
					}
				}
			}
		})
	}
}

func assertDependencyUnavailable(t *testing.T, payload []byte) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode error body: %v (payload=%s)", err, payload)
	}
	if body["code"] != "dependency_unavailable" {
		t.Fatalf("code = %v, want dependency_unavailable (body=%s)", body["code"], payload)
	}
}

// systemClock advances so request-id generation and audit timestamps are non-zero.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC) }

type sequenceIDsValue struct{}

func (sequenceIDsValue) New(kind domain.IdentifierKind) (domain.Identifier, error) {
	return domain.Identifier(string(kind) + "_test"), nil
}
