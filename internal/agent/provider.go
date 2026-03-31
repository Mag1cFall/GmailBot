// AI 服务商管理，支持多 provider fallback
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

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
	FetchContextWindow(ctx context.Context, model string) (int, error)
}

// OpenAIProvider OpenAI 兼容 API 服务商
type OpenAIProvider struct {
	name       string
	client     *openai.Client
	baseURL    string
	apiKey     string
	model      string
	httpClient *http.Client
}

// NewOpenAIProvider 创建 OpenAI 兴容 API 服务商
func NewOpenAIProvider(name, baseURL, apiKey, model string) *OpenAIProvider {
	cfg := openai.DefaultConfig(apiKey)
	cfg.BaseURL = strings.TrimSuffix(baseURL, "/")
	return &OpenAIProvider{
		name:       name,
		client:     openai.NewClientWithConfig(cfg),
		baseURL:    strings.TrimSuffix(baseURL, "/"),
		apiKey:     apiKey,
		model:      model,
		httpClient: &http.Client{Timeout: 10 * time.Second},
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

	start := time.Now()
	resp, err := p.client.CreateChatCompletion(ctx, chatReq)
	elapsed := time.Since(start)
	if err != nil {
		slog.Error("llm request failed", "provider", p.name, "model", chatReq.Model, "elapsed", elapsed, "error", err)
		return ChatResponse{}, err
	}
	if len(resp.Choices) == 0 {
		slog.Error("llm empty response", "provider", p.name, "model", chatReq.Model, "elapsed", elapsed)
		return ChatResponse{}, errors.New("empty model response")
	}

	choice := resp.Choices[0].Message
	slog.Info("llm response", "provider", p.name, "model", chatReq.Model, "elapsed", elapsed, "prompt_tokens", resp.Usage.PromptTokens, "completion_tokens", resp.Usage.CompletionTokens, "tool_calls", len(choice.ToolCalls))
	return ChatResponse{
		Content:   strings.TrimSpace(choice.Content),
		ToolCalls: choice.ToolCalls,
	}, nil
}

func (p *OpenAIProvider) FetchContextWindow(ctx context.Context, model string) (int, error) {
	targetModel := p.resolveModel(model)
	if targetModel == "" {
		return 0, errors.New("empty model")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(p.apiKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(p.apiKey))
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, fmt.Errorf("fetch models failed: %s", resp.Status)
	}
	var payload struct {
		Data []struct {
			ID            string          `json:"id"`
			ContextWindow json.RawMessage `json:"context_window"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0, err
	}
	for _, item := range payload.Data {
		if strings.TrimSpace(item.ID) != targetModel {
			continue
		}
		window := parseContextWindow(item.ContextWindow)
		if window > 0 {
			return window, nil
		}
		return 0, fmt.Errorf("model %s missing context_window", targetModel)
	}
	return 0, fmt.Errorf("model %s not found in models response", targetModel)
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
	mu                    sync.RWMutex
	providers             []Provider
	activeIndex           int
	fallbackContextWindow int
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
	pm.activeIndex = 0
	pm.fallbackContextWindow = cfg.AIContextMaxTokens
	if pm.fallbackContextWindow <= 0 {
		pm.fallbackContextWindow = 128000
	}

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
	activeIndex := pm.activeIndex
	pm.mu.RUnlock()

	if len(providers) == 0 {
		return ChatResponse{}, errors.New("no providers configured")
	}

	var lastErr error
	for attempt := 0; attempt < len(providers); attempt++ {
		idx := (activeIndex + attempt) % len(providers)
		p := providers[idx]
		resp, err := p.Chat(ctx, req)
		if err == nil {
			pm.mu.Lock()
			pm.activeIndex = idx
			pm.mu.Unlock()
			return resp, nil
		}
		lastErr = err
		if attempt < len(providers)-1 {
			slog.Warn("provider failed, falling back", "provider", p.Name(), "error", err)
		}
	}
	return ChatResponse{}, fmt.Errorf("all providers failed, last error: %w", lastErr)
}

// FetchContextWindow 向当前活跃 provider 查询模型 context window，失败时使用内部全局属性倖退尤其是配置默认延迟
func (pm *ProviderManager) FetchContextWindow(ctx context.Context) int {
	pm.mu.RLock()
	providers := make([]Provider, len(pm.providers))
	copy(providers, pm.providers)
	activeIndex := pm.activeIndex
	fallback := pm.fallbackContextWindow
	pm.mu.RUnlock()
	if fallback <= 0 {
		fallback = 128000
	}
	if len(providers) == 0 {
		return fallback
	}
	if activeIndex < 0 || activeIndex >= len(providers) {
		activeIndex = 0
	}
	window, err := providers[activeIndex].FetchContextWindow(ctx, "")
	if err != nil || window <= 0 {
		if err != nil {
			slog.Warn("fetch context window failed, using fallback", "provider", providers[activeIndex].Name(), "error", err, "fallback", fallback)
		}
		return fallback
	}
	return window
}

// PrimaryModel 返回主服务商的模型名
func (pm *ProviderManager) PrimaryModel() string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()
	if len(pm.providers) == 0 {
		return "unknown"
	}
	index := pm.activeIndex
	if index < 0 || index >= len(pm.providers) {
		index = 0
	}
	if p, ok := pm.providers[index].(*OpenAIProvider); ok {
		return p.model
	}
	return pm.providers[index].Name()
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

// parseContextWindow 从 JSON 标准属性中提取整型 context window，支持 int/float64/string 三种格式
func parseContextWindow(raw json.RawMessage) int {
	if len(raw) == 0 {
		return 0
	}
	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil {
		return asInt
	}
	var asFloat float64
	if err := json.Unmarshal(raw, &asFloat); err == nil {
		return int(asFloat)
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		asString = strings.TrimSpace(asString)
		if asString == "" {
			return 0
		}
		var parsed int
		_, _ = fmt.Sscanf(asString, "%d", &parsed)
		return parsed
	}
	return 0
}
