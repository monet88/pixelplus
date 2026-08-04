# Validation

## Planned proof (public seam: fixture + real composition + HTTP)

| Behavior | Test |
| --- | --- |
| Explicit pin selects the pinned same-Tenant account (P1) | `TestChatExplicitPinSelectsPinnedAccount` |
| Foreign pin → 404 `resource_not_found`, 0 Adapter calls, 0 Vault validates | `TestChatExplicitPinForeignIsNotFound` |
| Unknown pin → same 404 (non-enumeration indistinguishable) | `TestChatExplicitPinForeignIsNotFound` |
| Same-Tenant pinned but gated → specific class, not 404 | `TestChatExplicitPinSameTenantGatedKeepsSpecificClass` |
| Cross-mode fallback without both modes listed → fail closed | `TestChatFallbackCrossModeRequiresPolicyListedModes` |
| Cross-mode fallback with both modes listed → walks once | `TestChatFallbackCrossModeWithListedModes` |
| Affinity prefers prior account over policy order (P3) | `TestChatConversationAffinityPrefersPriorAccount` |
| Affinity yields to P4 when preferred leaves candidate set | `TestChatConversationAffinityPrefersPriorAccount` |
| Contract-declared fields accepted (temperature/max_tokens/top_p/n/stop/user/name/array content) | `TestChatRequestWireAcceptsContractDeclaredFields` |
| Non-text content part → 400 `invalid_request` | `TestChatRequestWireRejectsInvalidShapes` |
| JSON null on non-nullable fields (stream/temperature/max_tokens/top_p/n) → 400 | `TestChatRequestWireRejectsInvalidShapes` |
| Null item inside `stop` array → 400 `invalid_request` | `TestChatRequestWireRejectsInvalidShapes` |
| Fingerprint binds every accepted field; any one-field difference → 409 conflict | `TestChatReplayFingerprintCoversAcceptedRequestFields` |
| Canonical-equal stop forms (`"END"` vs `["END"]`) replay, no false conflict | `TestChatReplayFingerprintCanonicalizesStopForm` |
| Fingerprint options/name binding (unit-level, keyed fixture digester) | `TestStubChatDigesterOptionsCovered` |
| Existing fallback/replay/gate proofs unchanged | `TestChatFallback*`, `TestChatReplay*`, `TestChatGateOrder*` |

## Commands (executed green)

```text
gofmt touched Go files (chat wire/digester/fakes re-aligned)
go -C apps/gateway build ./...
go -C apps/gateway vet ./...
go -C apps/gateway test ./... -count=1 -timeout=300s        # 9 packages ok
go -C apps/gateway test -race ./internal/... -count=1 -timeout=300s
go -C apps/gateway test ./internal/contracttest -run 'TestChatExplicitPin|TestChatFallbackCrossMode|TestChatConversationAffinity|TestChatRequestWire|TestChatReplayFingerprint|TestStubChatDigester' -count=1 -v  # 13/13 PASS
git diff --check
gitnexus detect_changes: 52 changed symbols, all inside the chat spine +
composition wiring + decision/story docs; only the two chat completion
processes affected (no render/account/asset process).
```

## Residual risks

- Affinity preference is process-local by decision; restart degrades to P4
  policy selection (safe: preference, not authority). Durable store deferred.
- Render spine has no fallback walk, so `fallback_auth_modes` is unenforced
  there today; recorded as a separate discovery, not this story's scope.
- A top-level `"stop": null` is still treated as absent (decision 0012 item
  12); only null items inside the array reject. Null handling for the
  remaining string fields (`user`, message `name`, `x_pixelplus` internals)
  is unchanged and was not flagged; tighten only if a future contract review
  asks for it.

Coordinator owns commit/push. No GitHub comment/resolve/merge from this agent.
