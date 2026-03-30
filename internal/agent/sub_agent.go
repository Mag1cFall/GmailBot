// 子 Agent 编排，支持任务委派
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	openai "github.com/sashabaranov/go-openai"
)

// SubAgent 子 Agent 定义
type SubAgent struct {
	Name         string
	Instructions string
	ToolNames    []string
}

// SubAgentOrchestrator 子 Agent 编排器
type SubAgentOrchestrator struct {
	mu       sync.RWMutex
	agents   map[string]*SubAgent
	registry *ToolRegistry
	provider *ProviderManager
	maxSteps int
}

// NewSubAgentOrchestrator 创建子 Agent 编排器
func NewSubAgentOrchestrator(registry *ToolRegistry, provider *ProviderManager, maxSteps int) *SubAgentOrchestrator {
	return &SubAgentOrchestrator{
		agents:   make(map[string]*SubAgent),
		registry: registry,
		provider: provider,
		maxSteps: maxSteps,
	}
}

// RegisterAgent 注册子 Agent
func (o *SubAgentOrchestrator) RegisterAgent(sa *SubAgent) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.agents[sa.Name] = sa
}

// RegisterHandoffTools 注册 handoff 工具到主 registry
func (o *SubAgentOrchestrator) RegisterHandoffTools(registry *ToolRegistry) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, sa := range o.agents {
		agentCopy := sa
		registry.Register(&ToolDef{
			Name:        "handoff_to_" + agentCopy.Name,
			Description: fmt.Sprintf("将任务委派给 %s 子 Agent 处理", agentCopy.Name),
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"task":{"type":"string","description":"要委派的具体任务描述"}
				},
				"required":["task"]
			}`),
			Handler: func(tc *ToolContext, raw json.RawMessage) (string, error) {
				var req struct {
					Task string `json:"task"`
				}
				json.Unmarshal(raw, &req)
				return o.RunSubAgent(tc, agentCopy, req.Task)
			},
			Active:   true,
			Category: "agent",
		})
	}
}

// RunSubAgent 运行子 Agent 处理任务
func (o *SubAgentOrchestrator) RunSubAgent(parent *ToolContext, sa *SubAgent, task string) (string, error) {
	ctx := context.Background()
	toolCtx := &ToolContext{Extra: map[string]any{}}
	if parent != nil {
		ctx = parent.Context
		if ctx == nil {
			ctx = context.Background()
		}
		toolCtx = &ToolContext{
			Context:   ctx,
			TgUserID:  parent.TgUserID,
			Platform:  parent.Platform,
			UserID:    parent.UserID,
			SessionID: parent.SessionID,
			Extra:     cloneExtra(parent.Extra),
		}
	}
	var subTools []openai.Tool
	for _, name := range sa.ToolNames {
		if tool, ok := o.registry.Get(name); ok && tool.Active {
			subTools = append(subTools, tool.OpenAISchema())
		}
	}

	messages := []ChatMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: sa.Instructions,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: task,
		},
	}

	maxSteps := o.maxSteps
	if maxSteps <= 0 {
		maxSteps = 4
	}

	for i := 0; i < maxSteps; i++ {
		resp, err := o.provider.Chat(ctx, ChatRequest{
			Messages: messages,
			Tools:    subTools,
		})
		if err != nil {
			return "", err
		}

		if len(resp.ToolCalls) == 0 {
			content := strings.TrimSpace(resp.Content)
			if content == "" {
				content = "子 Agent 未生成有效回复。"
			}
			return content, nil
		}

		messages = append(messages, ChatMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		allowedTools := map[string]struct{}{}
		for _, name := range sa.ToolNames {
			allowedTools[name] = struct{}{}
		}
		for _, call := range resp.ToolCalls {
			if _, allowed := allowedTools[call.Function.Name]; !allowed {
				messages = append(messages, ChatMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: call.ID,
					Name:       call.Function.Name,
					Content:    fmt.Sprintf(`{"error":%q}`, "tool not allowed for sub-agent"),
				})
				continue
			}
			result, toolErr := o.registry.Execute(toolCtx, call.Function.Name, call.Function.Arguments)
			if toolErr != nil {
				result = fmt.Sprintf(`{"error":%q}`, toolErr.Error())
			}
			messages = append(messages, ChatMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    result,
			})
		}
	}

	return "子 Agent 达到步数上限。", nil
}

// cloneExtra 深拷贝 Extra map，避免子 Agent 修改父上下文
func cloneExtra(extra map[string]any) map[string]any {
	if len(extra) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(extra))
	for key, value := range extra {
		cloned[key] = value
	}
	return cloned
}
