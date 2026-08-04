package domain

import (
	"testing"
)

func TestChatOperationMappings(t *testing.T) {
	if !ChatOpCompletion.Valid() {
		t.Fatalf("ChatOpCompletion should be valid")
	}
	if ChatOpCompletion.CapabilityOperation() != CapabilityOpChat {
		t.Fatalf("ChatOpCompletion should map to capability op chat, got %q", ChatOpCompletion.CapabilityOperation())
	}
	if ChatOpCompletion.RequiredScope() != ScopeChatCompletions {
		t.Fatalf("ChatOpCompletion should require chat.completions, got %q", ChatOpCompletion.RequiredScope())
	}
	if ChatOperation("bogus").Valid() {
		t.Fatalf("bogus chat operation should be invalid")
	}
}

func TestFinishClassValidity(t *testing.T) {
	for _, valid := range []FinishClass{FinishStop, FinishLength, FinishContentFilter, FinishCanceled, FinishFailed} {
		if !valid.Valid() {
			t.Fatalf("%q should be a valid finish class", valid)
		}
	}
	if FinishClass("bogus").Valid() {
		t.Fatalf("bogus finish class should be invalid")
	}
}

func TestChatMessageValid(t *testing.T) {
	msg := ChatMessage{Role: ChatRoleUser, Content: "hello"}
	if !msg.Valid() {
		t.Fatalf("valid user message reported invalid")
	}
	if (ChatMessage{Role: ChatRoleUser, Content: ""}).Valid() {
		t.Fatalf("empty content message reported valid")
	}
	if (ChatMessage{Role: ChatRole("bogus"), Content: "x"}).Valid() {
		t.Fatalf("bogus role message reported valid")
	}
}

func TestChatOutcomeCarriesAuthoritativeNoCommit(t *testing.T) {
	notCommitted := ChatOutcome{Class: ChatOutcomeNotCommitted, Commit: CommitNotCommitted}
	if notCommitted.Commit != CommitNotCommitted {
		t.Fatalf("not_committed outcome must carry CommitNotCommitted (authoritative no-commit proof)")
	}
	unknown := ChatOutcome{Class: ChatOutcomeUnknown, Commit: CommitUnknown}
	if !unknown.Commit.ForbidsReplacement() {
		t.Fatalf("unknown commit must forbid replacement (fail closed)")
	}
}

func TestScopeChatCompletionsGranted(t *testing.T) {
	set := NewScopeSet(ScopeChatCompletions)
	if !set.Has(ScopeChatCompletions) {
		t.Fatalf("scope set should contain chat.completions")
	}
	if set.Has(ScopeImagesGenerate) {
		t.Fatalf("scope set should not contain unrelated scope")
	}
}
