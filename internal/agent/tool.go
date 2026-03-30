// 工具注册表和执行框架
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"gmailbot/internal/metrics"

	openai "github.com/sashabaranov/go-openai"
)

// ToolHandler 工具处理函数签名
type ToolHandler func(ctx *ToolContext, args json.RawMessage) (string, error)

// ToolContext 工具执行上下文
type ToolContext struct {
	Context   context.Context
	TgUserID  int64
	Platform  string
	UserID    string
	SessionID string
	Extra     map[string]any
}

// ToolDef 工具定义
type ToolDef struct {
	Name        string
	Description string
	Parameters  json.RawMessage
	Handler     ToolHandler
	Active      bool
	Category    string
}

// OpenAISchema 转换为 OpenAI Tool 格式
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

// ToolRegistry 工具注册表
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]*ToolDef
	order []string
}

// NewToolRegistry 创建工具注册表
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]*ToolDef),
	}
}

// Register 注册工具
func (r *ToolRegistry) Register(tool *ToolDef) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[tool.Name]; !exists {
		r.order = append(r.order, tool.Name)
	}
	r.tools[tool.Name] = tool
}

// Unregister 移除工具
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

// Get 按名称查找工具
func (r *ToolRegistry) Get(name string) (*ToolDef, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// SetActive 启用或禁用指定工具
func (r *ToolRegistry) SetActive(name string, active bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t, ok := r.tools[name]; ok {
		t.Active = active
	}
}

// Execute 执行指定工具
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

// OpenAITools 返回所有已启用工具的 OpenAI 格式定义
func (r *ToolRegistry) OpenAITools() []openai.Tool {
	return openAIToolsFromDefs(r.ActiveTools())
}

// FilteredActiveTools 按白名单过滤已启用工具
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

// OpenAIToolsFromDefs 将工具定义列表转换为 OpenAI Tool 格式（导出）
func OpenAIToolsFromDefs(tools []*ToolDef) []openai.Tool {
	return openAIToolsFromDefs(tools)
}

// openAIToolsFromDefs 将工具定义列表转换为 OpenAI Tool 格式（内部）
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

// ActiveTools 返回所有已启用的工具
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

// AllTools 返回所有工具（含禁用的）
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

// ToolsByCategory 按分类返回工具列表
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

// Count 返回注册的工具数量
func (r *ToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// ParseToolArgs 泛型解析工具参数
func ParseToolArgs[T any](raw json.RawMessage) (T, error) {
	var out T
	if len(raw) == 0 {
		return out, nil
	}
	err := json.Unmarshal(raw, &out)
	return out, err
}

// ToJSON 序列化为 JSON 字符串
func ToJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
