package agent

import (
	"context"
	"errors"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestProviderManagerFallsBackToNextProvider(t *testing.T) {
	pm := NewProviderManager()
	first := newScriptedProvider("first", scriptedResult{err: errors.New("boom")})
	second := newScriptedProvider("second", scriptedResult{response: ChatResponse{Content: "ok"}})
	pm.providers = []Provider{first, second}

	resp, err := pm.Chat(context.Background(), ChatRequest{
		Messages: []ChatMessage{{Role: openai.ChatMessageRoleUser, Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("chat failed: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if len(first.Requests()) != 1 {
		t.Fatalf("expected first provider to be tried once, got %d", len(first.Requests()))
	}
	if len(second.Requests()) != 1 {
		t.Fatalf("expected second provider to be tried once, got %d", len(second.Requests()))
	}
}
