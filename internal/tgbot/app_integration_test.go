package tgbot

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gmailbot/config"
	agentpkg "gmailbot/internal/agent"
	"gmailbot/internal/event"
	"gmailbot/internal/memory"
	"gmailbot/internal/platform"
	"gmailbot/internal/plugin"
	"gmailbot/internal/store"
)

func TestAppHandleMessageBlocksUnauthorizedUsers(t *testing.T) {
	app, _, calls := newTestApp(t)

	resp, err := app.HandleMessage(context.Background(), platform.UnifiedMessage{
		Platform: "telegram",
		UserID:   "42",
		Text:     "帮我处理邮件",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if resp.Text != "请先执行 /auth 绑定邮箱后再使用 AI 对话。" {
		t.Fatalf("unexpected auth response: %#v", resp)
	}
	if calls.ServerCalls() != 0 {
		t.Fatalf("expected model server to stay unused, got %d calls", calls.ServerCalls())
	}
}

func TestAppHandleMessageRunsPipelineAndPluginTool(t *testing.T) {
	app, st, calls := newTestApp(t)
	if err := st.SaveUserTokens(context.Background(), 42, "user@example.com", "access", "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("save tokens failed: %v", err)
	}

	resp, err := app.HandleMessage(context.Background(), platform.UnifiedMessage{
		Platform: "telegram",
		UserID:   "42",
		Text:     "帮我处理邮件",
	})
	if err != nil {
		t.Fatalf("handle message failed: %v", err)
	}
	if calls.ToolCalls() != 1 {
		t.Fatalf("expected plugin tool to be called once, got %d", calls.ToolCalls())
	}
	if calls.ServerCalls() != 2 {
		t.Fatalf("expected two model calls, got %d", calls.ServerCalls())
	}
	if strings.Contains(resp.Text, calls.Secret()) {
		t.Fatalf("expected secret to be redacted from response, got %q", resp.Text)
	}
	if !strings.Contains(resp.Text, strings.Repeat("*", len(calls.Secret()))) {
		t.Fatalf("expected masked secret in response, got %q", resp.Text)
	}
	if !strings.Contains(calls.RequestBody(0), "demo_tool") {
		t.Fatalf("expected first request to expose plugin tool, got %s", calls.RequestBody(0))
	}
	if !strings.Contains(calls.RequestBody(1), `\"status\":\"ok\"`) {
		t.Fatalf("expected second request to include tool result, got %s", calls.RequestBody(1))
	}
	if !resp.Markdown {
		t.Fatalf("expected markdown response, got %#v", resp)
	}
}

type appTestState struct {
	mu          sync.Mutex
	serverCalls int
	toolCalls   int
	requestBody []string
	secret      string
}

func (s *appTestState) ServerCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.serverCalls
}

func (s *appTestState) ToolCalls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.toolCalls
}

func (s *appTestState) RequestBody(index int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if index >= len(s.requestBody) {
		return ""
	}
	return s.requestBody[index]
}

func (s *appTestState) Secret() string {
	return s.secret
}

func newTestApp(t *testing.T) (*App, *store.Store, *appTestState) {
	t.Helper()
	state := &appTestState{secret: "super-secret-key"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var payload map[string]any
		body, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("decode request failed: %v", err)
		}
		state.mu.Lock()
		state.serverCalls++
		callIndex := state.serverCalls
		state.requestBody = append(state.requestBody, string(body))
		state.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if callIndex == 1 {
			_, _ = w.Write([]byte(`{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"mock","choices":[{"index":0,"finish_reason":"tool_calls","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"demo_tool","arguments":"{\"value\":\"ok\"}"}}]}}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"chatcmpl-2","object":"chat.completion","created":1,"model":"mock","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"处理完成 super-secret-key"}}]}`))
	}))
	t.Cleanup(server.Close)

	st, err := store.Init(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("init store failed: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	registry := agentpkg.NewToolRegistry()
	pluginMgr := plugin.NewManager(registry, event.NewBus(), map[string]any{})
	if err := pluginMgr.Register(&appTestPlugin{state: state}); err != nil {
		t.Fatalf("register plugin failed: %v", err)
	}
	ag := agentpkg.NewAgent(config.Config{
		AIBaseURL:              server.URL,
		AIAPIKey:               state.secret,
		AIModel:                "mock",
		MessageRateLimitPerMin: 3,
		AIToolMaxSteps:         3,
	}, registry, st)
	app, err := NewApp(config.Config{
		AIBaseURL:              server.URL,
		AIAPIKey:               state.secret,
		AIModel:                "mock",
		MessageRateLimitPerMin: 3,
		AIToolMaxSteps:         3,
		MemoryRoot:             filepath.Join(t.TempDir(), "memory"),
	}, st, nil, ag, memory.NewStore(filepath.Join(t.TempDir(), "memory")))
	if err != nil {
		t.Fatalf("new app failed: %v", err)
	}
	return app, st, state
}

type appTestPlugin struct {
	state *appTestState
}

func (p *appTestPlugin) Name() string                     { return "app-test" }
func (p *appTestPlugin) Description() string              { return "app-test" }
func (p *appTestPlugin) Shutdown() error                  { return nil }
func (p *appTestPlugin) Commands() []plugin.Command       { return nil }
func (p *appTestPlugin) EventHandlers() []plugin.EventSub { return nil }

func (p *appTestPlugin) Init(ctx *plugin.Context) error {
	ctx.Registry.Register(&agentpkg.ToolDef{
		Name:        "demo_tool",
		Description: "demo tool",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`),
		Active:      true,
		Handler: func(tc *agentpkg.ToolContext, raw json.RawMessage) (string, error) {
			p.state.mu.Lock()
			p.state.toolCalls++
			p.state.mu.Unlock()
			return `{"status":"ok"}`, nil
		},
	})
	return nil
}
