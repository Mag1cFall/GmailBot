package dashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gmailbot/internal/agent"
	"gmailbot/internal/metrics"
	"gmailbot/internal/plugin"
	"gmailbot/internal/store"
)

func TestStatusAndToolToggleEndpoints(t *testing.T) {
	registry := agent.NewToolRegistry()
	registry.Register(&agent.ToolDef{Name: "demo", Description: "demo", Active: true})
	pluginMgr := plugin.NewManager(registry, nil, map[string]any{})
	if err := pluginMgr.Register(&fakePlugin{}); err != nil {
		t.Fatalf("register plugin failed: %v", err)
	}
	metricSet := metrics.New()
	metricSet.MessagesTotal.Store(3)
	server := NewServer("", "", pluginMgr, registry, &store.Store{}, agent.NewProviderManager(), metricSet, nil)

	statusReq := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	statusRes := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(statusRes, statusReq)
	if statusRes.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", statusRes.Code)
	}
	t.Logf("status response: %s", strings.TrimSpace(statusRes.Body.String()))
	var payload map[string]any
	if err := json.Unmarshal(statusRes.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode status failed: %v", err)
	}
	if payload["messages_total"].(float64) != 3 {
		t.Fatalf("unexpected payload: %#v", payload)
	}

	indexReq := httptest.NewRequest(http.MethodGet, "/", nil)
	indexRes := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(indexRes, indexReq)
	if indexRes.Code != http.StatusOK || !strings.Contains(indexRes.Body.String(), "小 AstrBot") {
		t.Fatalf("unexpected dashboard html: %s", indexRes.Body.String())
	}
	t.Logf("dashboard html prefix: %s", strings.SplitN(strings.TrimSpace(indexRes.Body.String()), "\n", 2)[0])

	toggleReq := httptest.NewRequest(http.MethodPost, "/api/tools/demo/toggle", strings.NewReader(`{"enabled":false}`))
	toggleRes := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(toggleRes, toggleReq)
	if toggleRes.Code != http.StatusOK {
		t.Fatalf("unexpected toggle status: %d", toggleRes.Code)
	}
	t.Logf("toggle response: %s", strings.TrimSpace(toggleRes.Body.String()))
	tool, ok := registry.Get("demo")
	if !ok || tool.Active {
		t.Fatalf("expected tool to be disabled, got %#v", tool)
	}
}

type fakePlugin struct{}

func (p *fakePlugin) Name() string                     { return "fake" }
func (p *fakePlugin) Description() string              { return "fake" }
func (p *fakePlugin) Init(ctx *plugin.Context) error   { return nil }
func (p *fakePlugin) Shutdown() error                  { return nil }
func (p *fakePlugin) Commands() []plugin.Command       { return nil }
func (p *fakePlugin) EventHandlers() []plugin.EventSub { return nil }
