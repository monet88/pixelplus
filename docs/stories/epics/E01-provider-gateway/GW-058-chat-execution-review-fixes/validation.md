# Validation

## Planned proof (public seam: fixture + real composition + HTTP)

| Behavior | Test |
| --- | --- |
| Explicit pin selects the pinned same-Tenant account (P1) | `TestChatExplicitPinSelectsPinnedAccount` |
| Foreign pin → 404 `resource_not_found`, 0 Adapter calls | `TestChatExplicitPinForeignIsNotFound` |
| Unknown pin → same 404 (non-enumeration indistinguishable) | `TestChatExplicitPinForeignIsNotFound` |
| Same-Tenant pinned but gated → specific class, not 404 | `TestChatExplicitPinSameTenantGatedKeepsSpecificClass` |
| Cross-mode fallback without both modes listed → fail closed | `TestChatFallbackCrossModeRequiresPolicyListedModes` |
| Cross-mode fallback with both modes listed → walks once | `TestChatFallbackCrossModeWithListedModes` |
| Affinity prefers prior account over policy order (P3) | `TestChatConversationAffinityPrefersPriorAccount` |
| Affinity yields to P4 when preferred leaves candidate set | `TestChatConversationAffinityPrefersPriorAccount` |
| Contract-declared fields accepted (temperature/max_tokens/top_p/n/stop/user/name/array content) | `TestChatRequestWireAcceptsContractDeclaredFields` |
| Non-text content part → 400 `invalid_request` | `TestChatRequestWireRejectsNonTextContentPart` |
| Existing fallback/replay/gate proofs unchanged | `TestChatFallback*`, `TestChatReplay*`, `TestChatGateOrder*` |

## Commands (executed green)

```text
gofmt touched Go files (chat_fakes_test.go re-aligned)
go -C apps/gateway build ./...
go -C apps/gateway vet ./...
go -C apps/gateway test ./... -count=1 -timeout=300s        # 9 packages ok
go -C apps/gateway test -race ./internal/... -count=1 -timeout=300s
go -C apps/gateway test ./internal/contracttest -run 'TestChatExplicitPin|TestChatFallbackCrossMode|TestChatConversationAffinity|TestChatRequestWire' -count=1 -v  # 8/8 PASS
git diff --check
gitnexus detect_changes: 43 changed symbols, all inside the chat spine +
composition wiring + decision/story docs; no render/account/asset process
affected.
```

## Residual risks

- Affinity preference is process-local by decision; restart degrades to P4
  policy selection (safe: preference, not authority). Durable store deferred.
- Render spine has no fallback walk, so `fallback_auth_modes` is unenforced
  there today; recorded as a separate discovery, not this story's scope.

Coordinator owns commit/push. No GitHub comment/resolve/merge from this agent.
