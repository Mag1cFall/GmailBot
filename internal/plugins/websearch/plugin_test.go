package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gmailbot/internal/agent"
	"gmailbot/internal/plugin"
)

func TestSearchAndReadURL(t *testing.T) {
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`<html><body><main>正文内容</main></body></html>`))
	}))
	defer pageServer.Close()
	plg := NewPlugin()
	results, err := parseDuckDuckGoResults(strings.NewReader(`<html><body><a class="result__a" href="https://example.com">Example</a><div class="result__snippet">hello world</div></body></html>`), 5)
	if err != nil {
		t.Fatalf("parse results failed: %v", err)
	}
	t.Logf("web_search parsed result: %#v", results[0])
	content, err := plg.ReadURL(context.Background(), pageServer.URL)
	if err != nil {
		t.Fatalf("read url failed: %v", err)
	}
	t.Logf("read_url content: %s", content)
	registry := agent.NewToolRegistry()
	if err := plg.Init(&plugin.Context{Registry: registry, Extra: map[string]any{}}); err != nil {
		t.Fatalf("init plugin failed: %v", err)
	}
	if len(results) != 1 || content != "正文内容" {
		t.Fatalf("unexpected results=%#v content=%s", results, content)
	}
}
