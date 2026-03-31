package agent

import (
	"context"
	"strings"
	"testing"

	"gmailbot/config"

	openai "github.com/sashabaranov/go-openai"
)

func TestContextManagerFixMessagesDropsOrphans(t *testing.T) {
	mgr := &ContextManager{}
	toolCalls := []openai.ToolCall{{
		ID:   "call-1",
		Type: openai.ToolTypeFunction,
		Function: openai.FunctionCall{
			Name:      "demo",
			Arguments: `{}`,
		},
	}}
	fixed := mgr.fixMessages([]ChatMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system"},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "orphan", Name: "demo", Content: "ignored"},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: toolCalls},
		{Role: openai.ChatMessageRoleTool, ToolCallID: "call-1", Name: "demo", Content: "ok"},
		{Role: openai.ChatMessageRoleAssistant, Content: "done"},
		{Role: openai.ChatMessageRoleAssistant, ToolCalls: toolCalls},
		{Role: openai.ChatMessageRoleUser, Content: "next"},
	})
	if len(fixed) != 5 {
		t.Fatalf("unexpected fixed messages length: %d %#v", len(fixed), fixed)
	}
	if fixed[1].Role != openai.ChatMessageRoleAssistant || len(fixed[1].ToolCalls) != 1 {
		t.Fatalf("expected valid assistant tool call chain, got %#v", fixed[1])
	}
	if fixed[2].Role != openai.ChatMessageRoleTool || fixed[2].ToolCallID != "call-1" {
		t.Fatalf("expected paired tool result, got %#v", fixed[2])
	}
	if fixed[4].Role != openai.ChatMessageRoleUser {
		t.Fatalf("expected final user message to remain, got %#v", fixed[4])
	}
}

func TestContextManagerProcessWarnsBeforeCompression(t *testing.T) {
	mgr := &ContextManager{warnTokens: 10, maxTokens: 40, keepRecent: 1}
	msgs := []ChatMessage{
		{Role: openai.ChatMessageRoleSystem, Content: "system"},
		{Role: openai.ChatMessageRoleUser, Content: strings.Repeat("你", 20)},
	}
	processed, warned, err := mgr.Process(context.Background(), msgs)
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if !warned {
		t.Fatal("expected warn state")
	}
	if len(processed) != len(msgs) {
		t.Fatalf("expected messages unchanged, got %#v", processed)
	}
}

func TestAgentCompactSessionSummarizesHistory(t *testing.T) {
	st := newTestStore(t)
	ag := NewAgent(config.Config{
		AIBaseURL:           "http://example.com",
		AIAPIKey:            "key",
		AIModel:             "mock",
		AIContextMaxTokens:  50,
		AIContextKeepRecent: 1,
	}, NewToolRegistry(), st)
	provider := newScriptedProvider("primary", scriptedResult{response: ChatResponse{Content: "压缩摘要"}})
	provider.window = 50
	ag.providerMgr.providers = []Provider{provider}
	ag.ctxMgr = &ContextManager{providerMgr: ag.providerMgr, warnTokens: 10, maxTokens: 50, keepRecent: 1}
	if _, err := st.AppendActiveSessionMessageByIdentity(context.Background(), "telegram", "99", "user", strings.Repeat("历史消息", 12)); err != nil {
		t.Fatalf("append first user failed: %v", err)
	}
	if _, err := st.AppendActiveSessionMessageByIdentity(context.Background(), "telegram", "99", "assistant", strings.Repeat("历史回复", 12)); err != nil {
		t.Fatalf("append first assistant failed: %v", err)
	}
	if _, err := st.AppendActiveSessionMessageByIdentity(context.Background(), "telegram", "99", "user", "最近的问题"); err != nil {
		t.Fatalf("append latest user failed: %v", err)
	}
	before, after, err := ag.CompactSession(context.Background(), "telegram", "99")
	if err != nil {
		t.Fatalf("compact session failed: %v", err)
	}
	if before <= after {
		t.Fatalf("expected token count to shrink, before=%d after=%d", before, after)
	}
	session, err := st.GetOrCreateActiveSessionByIdentity(context.Background(), "telegram", "99")
	if err != nil {
		t.Fatalf("load session failed: %v", err)
	}
	foundSummary := false
	for _, item := range session.Messages {
		if strings.Contains(item.Content, "历史摘要: 压缩摘要") {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Fatalf("expected summary message in session, got %#v", session.Messages)
	}
}
