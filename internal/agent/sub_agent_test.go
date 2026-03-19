package agent

import (
	"context"
	"encoding/json"
	"testing"

	openai "github.com/sashabaranov/go-openai"
)

func TestSubAgentHandoffUsesLimitedToolsAndParentContext(t *testing.T) {
	registry := NewToolRegistry()
	ctxMarker := struct{}{}
	allowedCalls := 0
	registry.Register(&ToolDef{
		Name:        "send_email",
		Description: "send",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"body":{"type":"string"}}}`),
		Active:      true,
		Handler: func(tc *ToolContext, args json.RawMessage) (string, error) {
			allowedCalls++
			if tc.Context.Value(ctxMarker) != "present" {
				t.Fatalf("expected parent context value to propagate")
			}
			if tc.Platform != "discord" || tc.UserID != "user-9" || tc.SessionID != "session-1" {
				t.Fatalf("unexpected propagated context: %#v", tc)
			}
			return `{"status":"sent"}`, nil
		},
	})
	registry.Register(&ToolDef{
		Name:        "web_search",
		Description: "search",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		Active:      true,
		Handler: func(tc *ToolContext, args json.RawMessage) (string, error) {
			t.Fatal("web_search should not be callable by sub-agent")
			return "", nil
		},
	})

	providerMgr := NewProviderManager()
	provider := newScriptedProvider("primary",
		scriptedResult{response: ChatResponse{ToolCalls: []openai.ToolCall{{
			ID:   "call_1",
			Type: openai.ToolTypeFunction,
			Function: openai.FunctionCall{
				Name:      "send_email",
				Arguments: `{"body":"hello"}`,
			},
		}}}},
		scriptedResult{response: ChatResponse{Content: "邮件已发送"}},
	)
	providerMgr.providers = []Provider{provider}

	orchestrator := NewSubAgentOrchestrator(registry, providerMgr, 3)
	orchestrator.RegisterAgent(&SubAgent{
		Name:         "email_writer",
		Instructions: "写邮件",
		ToolNames:    []string{"send_email"},
	})
	orchestrator.RegisterHandoffTools(registry)

	result, err := registry.Execute(&ToolContext{
		Context:   context.WithValue(context.Background(), ctxMarker, "present"),
		TgUserID:  99,
		Platform:  "discord",
		UserID:    "user-9",
		SessionID: "session-1",
		Extra:     map[string]any{"mode": "test"},
	}, "handoff_to_email_writer", `{"task":"给客户发确认邮件"}`)
	if err != nil {
		t.Fatalf("handoff tool execution failed: %v", err)
	}
	if result != "邮件已发送" {
		t.Fatalf("unexpected sub-agent result: %s", result)
	}
	if allowedCalls != 1 {
		t.Fatalf("expected allowed tool to be called once, got %d", allowedCalls)
	}

	requests := provider.Requests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 provider requests, got %d", len(requests))
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Function.Name != "send_email" {
		t.Fatalf("unexpected sub-agent tool list: %#v", requests[0].Tools)
	}
}
