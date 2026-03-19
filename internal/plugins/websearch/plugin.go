package websearch

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gmailbot/internal/agent"
	"gmailbot/internal/plugin"

	"golang.org/x/net/html"
)

type Plugin struct {
	client *http.Client
}

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

func NewPlugin() *Plugin {
	return &Plugin{client: &http.Client{Timeout: 15 * time.Second}}
}

func (p *Plugin) Name() string                     { return "websearch" }
func (p *Plugin) Description() string              { return "网页搜索与正文抓取" }
func (p *Plugin) Shutdown() error                  { return nil }
func (p *Plugin) Commands() []plugin.Command       { return nil }
func (p *Plugin) EventHandlers() []plugin.EventSub { return nil }

func (p *Plugin) Init(ctx *plugin.Context) error {
	p.registerWebSearch(ctx.Registry)
	p.registerReadURL(ctx.Registry)
	return nil
}

func (p *Plugin) registerWebSearch(registry *agent.ToolRegistry) {
	registry.Register(&agent.ToolDef{
		Name:        "web_search",
		Description: "使用 DuckDuckGo 搜索网页结果",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"搜索关键词"},"limit":{"type":"integer","description":"返回数量，默认5"}},"required":["query"]}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", err
			}
			results, err := p.Search(context.Background(), req.Query, req.Limit)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"results": results})
		},
		Active:   true,
		Category: "web",
	})
}

func (p *Plugin) registerReadURL(registry *agent.ToolRegistry) {
	registry.Register(&agent.ToolDef{
		Name:        "read_url",
		Description: "抓取网页并提取正文文本",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"url":{"type":"string","description":"网页地址"}},"required":["url"]}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				URL string `json:"url"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", err
			}
			content, err := p.ReadURL(context.Background(), req.URL)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"url": req.URL, "content": content})
		},
		Active:   true,
		Category: "web",
	})
}

func (p *Plugin) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}
	if limit <= 0 || limit > 10 {
		limit = 5
	}
	endpoint := "https://html.duckduckgo.com/html/?q=" + url.QueryEscape(query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "gmailbot/1.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("search request failed: %s", resp.Status)
	}
	return parseDuckDuckGoResults(resp.Body, limit)
}

func (p *Plugin) ReadURL(ctx context.Context, rawURL string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" {
		parsed.Scheme = "https"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "gmailbot/1.0")
	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("read url failed: %s", resp.Status)
	}
	return extractText(resp.Body)
}

func parseDuckDuckGoResults(reader io.Reader, limit int) ([]SearchResult, error) {
	node, err := html.Parse(reader)
	if err != nil {
		return nil, err
	}
	var results []SearchResult
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if len(results) >= limit || node == nil {
			return
		}
		if node.Type == html.ElementNode && node.Data == "a" && hasClass(node, "result__a") {
			result := SearchResult{Title: strings.TrimSpace(nodeText(node)), URL: getAttr(node, "href")}
			if container := node.Parent; container != nil {
				if snippet := findFirstByClass(container, "result__snippet"); snippet != nil {
					result.Snippet = strings.TrimSpace(nodeText(snippet))
				}
			}
			if result.Title != "" && result.URL != "" {
				results = append(results, result)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return results, nil
}

func extractText(reader io.Reader) (string, error) {
	node, err := html.Parse(reader)
	if err != nil {
		return "", err
	}
	var parts []string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node == nil {
			return
		}
		if node.Type == html.ElementNode && (node.Data == "script" || node.Data == "style" || node.Data == "noscript") {
			return
		}
		if node.Type == html.TextNode {
			text := strings.TrimSpace(node.Data)
			if text != "" {
				parts = append(parts, text)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	content := strings.Join(parts, " ")
	content = strings.Join(strings.Fields(content), " ")
	if len(content) > 8000 {
		content = content[:8000] + "..."
	}
	return content, nil
}

func hasClass(node *html.Node, className string) bool {
	for _, attr := range node.Attr {
		if attr.Key == "class" {
			for _, item := range strings.Fields(attr.Val) {
				if item == className {
					return true
				}
			}
		}
	}
	return false
}

func getAttr(node *html.Node, key string) string {
	for _, attr := range node.Attr {
		if attr.Key == key {
			return attr.Val
		}
	}
	return ""
}

func findFirstByClass(node *html.Node, className string) *html.Node {
	if node == nil {
		return nil
	}
	if node.Type == html.ElementNode && hasClass(node, className) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findFirstByClass(child, className); found != nil {
			return found
		}
	}
	return nil
}

func nodeText(node *html.Node) string {
	if node == nil {
		return ""
	}
	var parts []string
	var walk func(*html.Node)
	walk = func(current *html.Node) {
		if current == nil {
			return
		}
		if current.Type == html.TextNode {
			text := strings.TrimSpace(current.Data)
			if text != "" {
				parts = append(parts, text)
			}
		}
		for child := current.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(parts, " ")
}
