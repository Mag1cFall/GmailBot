package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gmailbot/config"
	"gmailbot/internal/persona"
	"gmailbot/internal/platform"

	openai "github.com/sashabaranov/go-openai"
)

func TestAgentHandleMessageRunsToolLoop(t *testing.T) {
	st := newTestStore(t)
	registry := NewToolRegistry()
	called := 0
	registry.Register(&ToolDef{
		Name:        "demo_tool",
		Description: "demo",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`),
		Active:      true,
		Handler: func(ctx *ToolContext, args json.RawMessage) (string, error) {
			called++
			var req struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(args, &req); err != nil {
				return "", err
			}
			if req.Value != "收件箱" {
				t.Fatalf("unexpected tool args: %s", req.Value)
			}
			return `{"status":"ok"}`, nil
		},
	})

	ag := NewAgent(config.Config{AIBaseURL: "http://example.com", AIAPIKey: "key", AIModel: "mock", AIToolMaxSteps: 3}, registry, st)
	provider := newScriptedProvider("primary",
		scriptedResult{response: ChatResponse{ToolCalls: []openai.ToolCall{{
			ID:   "call_1",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "demo_tool",
				Arguments: `{"value":"收件箱"}`,
			},
		}}}},
		scriptedResult{response: ChatResponse{Content: "已完成处理"}},
	)
	ag.providerMgr.providers = []Provider{provider}

	resp, err := ag.HandleMessage(context.Background(), platform.UnifiedMessage{
		Platform: "telegram",
		UserID:   "42",
		Text:     "帮我检查一下",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if resp.Text != "已完成处理" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if called != 1 {
		t.Fatalf("expected tool to be called once, got %d", called)
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(requests))
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Function.Name != "demo_tool" {
		t.Fatalf("unexpected tools on first request: %#v", requests[0].Tools)
	}
	if len(requests[1].Messages) == 0 {
		t.Fatal("expected tool result message on second request")
	}
	last := requests[1].Messages[len(requests[1].Messages)-1]
	if last.Role != openai.ChatMessageRoleTool || !strings.Contains(last.Content, `"status":"ok"`) {
		t.Fatalf("unexpected final message on second request: %#v", last)
	}
}

func TestAgentPersonaSwitchChangesPromptToolsAndModel(t *testing.T) {
	st := newTestStore(t)
	registry := NewToolRegistry()
	registry.Register(&ToolDef{Name: "gmail_tool", Description: "gmail", Active: true})
	registry.Register(&ToolDef{Name: "web_tool", Description: "web", Active: true})

	ag := NewAgent(config.Config{AIBaseURL: "http://example.com", AIAPIKey: "key", AIModel: "default-model", AIToolMaxSteps: 2}, registry, st)
	provider := newScriptedProvider("primary",
		scriptedResult{response: ChatResponse{Content: "limited"}},
		scriptedResult{response: ChatResponse{Content: "wide"}},
	)
	ag.providerMgr.providers = []Provider{provider}

	personaMgr := persona.NewManager(st, "gmail")
	personaMgr.Register(persona.Persona{Name: "limited", SystemPrompt: "仅限 Gmail 工具", Tools: []string{"gmail_tool"}})
	personaMgr.Register(persona.Persona{Name: "wide", SystemPrompt: "全部工具模式", Tools: []string{"gmail_tool", "web_tool"}, Model: "wide-model"})
	ag.SetPersonaManager(personaMgr)

	if _, err := personaMgr.SetActiveSessionPersona(context.Background(), "telegram", "7", "limited"); err != nil {
		t.Fatalf("set limited persona failed: %v", err)
	}
	if _, err := ag.HandleMessage(context.Background(), platform.UnifiedMessage{Platform: "telegram", UserID: "7", Text: "第一次"}); err != nil {
		t.Fatalf("handle limited persona failed: %v", err)
	}

	if _, err := personaMgr.SetActiveSessionPersona(context.Background(), "telegram", "7", "wide"); err != nil {
		t.Fatalf("set wide persona failed: %v", err)
	}
	if _, err := ag.HandleMessage(context.Background(), platform.UnifiedMessage{Platform: "telegram", UserID: "7", Text: "第二次"}); err != nil {
		t.Fatalf("handle wide persona failed: %v", err)
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(requests))
	}
	firstPrompt := requests[0].Messages[0].Content
	if !strings.Contains(firstPrompt, "仅限 Gmail 工具") {
		t.Fatalf("expected limited prompt override, got %q", firstPrompt)
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Function.Name != "gmail_tool" {
		t.Fatalf("unexpected limited persona tools: %#v", requests[0].Tools)
	}

	secondPrompt := requests[1].Messages[0].Content
	if !strings.Contains(secondPrompt, "全部工具模式") {
		t.Fatalf("expected wide prompt override, got %q", secondPrompt)
	}
	if len(requests[1].Tools) != 2 {
		t.Fatalf("expected two tools for wide persona, got %#v", requests[1].Tools)
	}
	if requests[1].Model != "wide-model" {
		t.Fatalf("expected persona model override, got %q", requests[1].Model)
	}
}
