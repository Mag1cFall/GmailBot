// AI 服务商管理，支持多 provider fallback
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"gmailbot/config"

	openai "github.com/sashabaranov/go-openai"
)

// ChatMessage 对话消息
type ChatMessage struct {
	Role       string
	Content    string
	ToolCallID string
	Name       string
	ToolCalls  []openai.ToolCall
}

// ChatRequest 对话请求
type ChatRequest struct {
	Messages []ChatMessage
	Tools    []openai.Tool
	Model    string
}

// ChatResponse 对话响应
type ChatResponse struct {
	Content   string
	ToolCalls []openai.ToolCall
}

// ProviderInfo 服务商基本信息
type ProviderInfo struct {
	Name  string `json:"name"`
	Model string `json:"model"`
}

// Provider AI 服务商接口
type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
}

// OpenAIProvider OpenAI 兼容 API 服务商
type OpenAIProvider struct {
	name   string
	client *openai.Client
	model  string
}

// NewOpenAIProvider 创建 OpenAI 兴容 API 服务商
func NewOpenAIProvider(name, baseURL, apiKey, model string) *OpenAIProvider {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = strings.TrimSuffix(baseURL, "/")
	return &OpenAIProvider{
		name:   name,
		client: openai.NewClientWithConfig(cfg),
		model:  model,
	}
}

// Name 返回服务商名称
func (p *OpenAIProvider) Name() string {
	return p.name
}

// Chat 向 OpenAI 兴容接口发起对话
func (p *OpenAIProvider) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	messages := make([]openai.ChatCompletionMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		msg := openai.ChatCompletionMessage{
			Role:       m.Role,
			Content:    m.Content,
			ToolCallID: m.ToolCallID,
			Name:       m.Name,
			ToolCalls:  m.ToolCalls,
		}
		messages = append(messages, msg)
	}

	chatReq := openai.ChatCompletionRequest{
		Model:    p.resolveModel(req.Model),
		Messages: messages,
	}
	if len(req.Tools) > 0 {
		chatReq.Tools = req.Tools
	}

	resp, err := p.client.CreateChatCompletion(ctx, chatReq)
	if err != nil {
		return ChatResponse{}, err
	}
	if len(resp.Choices) == 0 {
		return ChatResponse{}, errors.New("empty model response")
	}

	choice := resp.Choices[0].Message
	return ChatResponse{
		Content:   strings.TrimSpace(choice.Content),
		ToolCalls: choice.ToolCalls,
	}, nil
}

// resolveModel 返回实际使用的模型，允许单次请求覆盖
func (p *OpenAIProvider) resolveModel(override string) string {
	if strings.TrimSpace(override) != "" {
		return strings.TrimSpace(override)
	}
	return p.model
}

// ProviderManager 管理多个 AI 服务商，失败自动 fallback
type ProviderManager struct {
	mu        sync.RWMutex
	providers []Provider
}

// NewProviderManager 创建空的服务商管理器
func NewProviderManager() *ProviderManager {
	return &ProviderManager{}
}

// LoadFromConfig 从配置重建服务商列表
func (pm *ProviderManager) LoadFromConfig(cfg config.Config) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.providers = nil

	for _, pc := range cfg.Providers() {
		switch pc.Type {
		case "openai_compat", "openai", "":
			p := NewOpenAIProvider(pc.Name, pc.BaseURL, pc.APIKey, pc.Model)
			pm.providers = append(pm.providers, p)
		default:
			p := NewOpenAIProvider(pc.Name, pc.BaseURL, pc.APIKey, pc.Model)
			pm.providers = append(pm.providers, p)
		}
	}
}

// Chat 向 provider 发起对话，失败时逐个尝试 fallback
func (pm *ProviderManager) Chat(ctx context.Context, req ChatRequest) (ChatResponse, error) {
	pm.mu.RLock()
	providers := make([]Provider, len(pm.providers))
	copy(providers, pm.providers)
	pm.mu.RUnlock()

	if len(providers) == 0 {
		return ChatResponse{}, errors.New("no providers configured")
	}

	var lastErr error
	for i, p := range providers {
		resp, err := p.Chat(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if i < len(providers)-1 {
			slog.Warn("provider failed, falling back", "provider", p.Name(), "error", err)
		}
	}
	return ChatResponse{}, fmt.Errorf("all providers failed, last error: %w", lastErr)
}

// PrimaryModel 返回主服务商的模型名
func (pm *ProviderManager) PrimaryModel() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if len(pm.providers) == 0 {
		return "unknown"
	}
	if p, ok := pm.providers[0].(*OpenAIProvider); ok {
		return p.model
	}
	return pm.providers[0].Name()
}

// Providers 返回所有服务商的基本信息
func (pm *ProviderManager) Providers() []ProviderInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	infos := make([]ProviderInfo, 0, len(pm.providers))
	for _, provider := range pm.providers {
		info := ProviderInfo{Name: provider.Name()}
		if openAIProvider, ok := provider.(*OpenAIProvider); ok {
			info.Model = openAIProvider.model
		}
		infos = append(infos, info)
	}
	return infos
}
