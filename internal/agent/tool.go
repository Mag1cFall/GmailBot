package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"gmailbot/internal/metrics"

	openai "github.com/sashabaranov/go-openai"
)

type ToolHandler func(ctx *ToolContext, args json.RawMessage) (string, error)

type ToolContext struct {
	Context   context.Context
	TgUserID  int64
	Platform  string
	UserID    string
	SessionID string
	Extra     map[string]any
}

type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Handler     ToolHandler
	Active      bool
	Category    string
}

func (t *ToolDef) OpenAISchema() openai.Tool {
	params := t.Parameters
	if len(params) == 0 {
		params = json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return openai.Tool{
		Type: openai.ToolTypeFunction,
		Function: &openai.FunctionDefinition{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  params,
		},
	}
}

type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*ToolDef
	order []string
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]*ToolDef),
	}
}

func (r *ToolRegistry) Register(tool *ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[tool.Name]; !exists {
		r.order = append(r.order, tool.Name)
	}
	r.tools[tool.Name] = tool
}

func (r *ToolRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
	for i, n := range r.order {
		if n == name {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
}

func (r *ToolRegistry) Get(name string) (*ToolDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

func (r *ToolRegistry) SetActive(name string, active bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tools[name]; ok {
		t.Active = active
	}
}

func (r *ToolRegistry) Execute(ctx *ToolContext, name string, rawArgs string) (string, error) {
	r.mu.RLock()
	tool, ok := r.tools[name]
	r.mu.RUnlock()
	if !ok {
		return "", fmt.Errorf("unknown tool: %s", name)
	}
	if !tool.Active {
		return "", fmt.Errorf("tool %s is disabled", name)
	}
	metrics.Default.ToolCallsTotal.Add(1)
	result, err := tool.Handler(ctx, json.RawMessage(rawArgs))
	if err != nil {
		metrics.Default.ErrorsTotal.Add(1)
	}
	return result, err
}

func (r *ToolRegistry) OpenAITools() []openai.Tool {
	return openAIToolsFromDefs(r.ActiveTools())
}

func (r *ToolRegistry) FilteredActiveTools(allowed []string) []*ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if allowed == nil {
		var out []*ToolDef
		for _, name := range r.order {
			t, ok := r.tools[name]
			if !ok || !t.Active {
				continue
			}
			out = append(out, t)
		}
		return out
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	var filtered []*ToolDef
	for _, name := range r.order {
		t, ok := r.tools[name]
		if !ok || !t.Active {
			continue
		}
		if _, ok := allowedSet[t.Name]; !ok {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

func OpenAIToolsFromDefs(tools []*ToolDef) []openai.Tool {
	return openAIToolsFromDefs(tools)
}

func openAIToolsFromDefs(tools []*ToolDef) []openai.Tool {
	out := make([]openai.Tool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || !tool.Active {
			continue
		}
		out = append(out, tool.OpenAISchema())
	}
	return out
}

func (r *ToolRegistry) ActiveTools() []*ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*ToolDef
	for _, name := range r.order {
		t, ok := r.tools[name]
		if !ok || !t.Active {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (r *ToolRegistry) AllTools() []*ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*ToolDef
	for _, name := range r.order {
		if t, ok := r.tools[name]; ok {
			out = append(out, t)
		}
	}
	return out
}

func (r *ToolRegistry) ToolsByCategory(category string) []*ToolDef {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*ToolDef
	for _, name := range r.order {
		t, ok := r.tools[name]
		if !ok || t.Category != category {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (r *ToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

func ParseToolArgs[T any](raw json.RawMessage) (T, error) {
	var out T
	if len(raw) == 0 {
		return out, nil
	}
	err := json.Unmarshal(raw, &out)
	return out, err
}

func ToJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
