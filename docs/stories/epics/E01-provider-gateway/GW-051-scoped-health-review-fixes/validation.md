# Validation

## Eighth-pass effective summary (permit CAS fence)

| Behavior | Test |
| --- | --- |
| v1 cooldown + historical v2 expired → summary cooling_down | `TestReplacementProbeRejectionPreservesOriginHealth` |
| v1 healthy + historical v2 expired → summary healthy | `TestReplacementRejectionHealthyOriginSummaryIgnoresV2Expired` |
| v2 activation ignores historical v1 cooling → summary healthy | `TestV2ActivationSummaryIgnoresHistoricalV1Cooling`, `TestDirectReauthenticationCutsOverPendingVersion` |
| draft v0 initial_unprobed → summary unknown | `TestCreateDraftProjectsUnknownSummary` |
| submit Complete stores projected health | `TestSubmitCredentialReplayMatchesProjectedHealth` |
| list projects effective summary | `TestListProjectsEffectiveHealthSummary` |
| get projects effective summary | `TestGetProjectsEffectiveHealthSummary` |
| credential ReplayTerminal HTTP no mutation | `TestCredentialTerminalReplayReturnsProjectedHealthWithoutMutation` |
| current-version gates (no weaken) | fixtures with CredentialVersion set; `TestListModelsOmitsNonRoutableActiveAccounts` |
| GET capability-snapshot open circuit (matching op false, unrelated true) | `TestGetCapabilitySnapshotHonorsMatchingOpenCircuit` — **PASS** |
| GET capability-snapshot circuit unreadable → all offerable=false, no leak | `TestGetCapabilitySnapshotFailsClosedWhenCircuitUnreadable` — **PASS** |
| Soft cooldown before admission (admitCalls==0) | `TestRecoveryProbeBeforeRetryNotBeforeDoesNotReachAdapter` — **PASS** |
| Occupied permit soft gate skips admission | `TestOccupiedRecoveryPermitSoftGateSkipsAdmission` — **PASS** |
| Disable-before-claim race: no Vault/Adapter, permit cleared, admit 1→2 | `TestDisableBeforeClaimRaceClearsPermitAndSkipsProtectedWork` — **PASS** |
| Hard health stop after partial AccountStore fail + retry | `TestHardRejectAccountStoreFailureNotActive` — **PASS** |
| Submit missing health fail-closed + vault revoke v1 | `TestSubmitCredentialMissingHealthFailsClosed` — **PASS** |
| Multi-scope audit batch atomic (0 partial events) | `TestMultiScopeCooldownAuditBatchFailureIsAtomic` — **PASS** |
| Store ClearPermit stale Expected preserves newer (Memory+File) | `TestClearPermitStaleExpectedPreservesNewer` — **PASS** |
| Request-owned cleanup vs newer permit (HTTP race) | `TestStaleRequestOwnedPermitClearPreservesNewerPermit` — **PASS** |

## PermitClear contract

| Caller | ExpectedPermit | Semantics |
| --- | --- | --- |
| Management disable | empty | Administrative unconditional clear |
| Probe post-claim abort / non-active cleanup | exact claim result | CAS; mismatch → conflict, preserve newer |

## Commands (executed green)

```text
gofmt touched Go files
go -C apps/gateway test ./internal/infrastructure/persistence -count=1 -timeout=120s
go -C apps/gateway test ./internal/contracttest -count=1 -timeout=180s
go -C apps/gateway test ./... -count=1 -timeout=300s
go -C apps/gateway build ./...
go -C apps/gateway vet ./...
go -C apps/gateway test -race ./internal/... -count=1 -timeout=300s
# Full -race ./... once flaked cmd/gateway TestServeListenerDrainsInFlightRequestOnShutdown; retry OK
git diff --check
```

## Residual risks

- Post-claim clear remains best-effort after gate failure (product outcome already fail-closed).
- No cross-port transaction; partial create/enable/submit/probe stay monotonic fail-closed.
- Unrelated flaky listener drain race under `-race` on Windows (cmd/gateway).

Coordinator owns commit/push. No GitHub comment/resolve/merge from this agent.
