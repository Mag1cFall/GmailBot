package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gmailbot/config"
	"gmailbot/internal/store"

	openai "github.com/sashabaranov/go-openai"
)

const summaryInstruction = `你是一个对话历史压缩助手，请将以下历史对话压缩为一段结构化摘要，供 AI 助手继续对话使用。

压缩规范（必须全部遵守）：

1. 用户意图与目标：首先陈述用户的原始目标或初始问题，保持用户视角（用"用户希望……"或"用户正在……"表述），不要改变用户的意图
2. 进展与结论：覆盖每个核心话题的讨论过程和最终结论，重点突出最近的活动焦点
3. 工具调用：如有工具调用，按工具名分组，说明调用了多少次、关键入参和输出结果（不需要完整内容，只需关键结论）
4. 当前状态：如有未完成的任务或等待用户确认的事项，明确列出
5. 语言：使用用户在对话中使用的语言输出摘要
6. 格式：纯文本，不使用 Markdown，句子简洁，每个要点不超过两句话`

// ContextManager 上下文管理器，负责 token 估算、超限警告和 LLM 摘要压缩
type ContextManager struct {
	providerMgr *ProviderManager
	warnTokens  int
	maxTokens   int
	keepRecent  int
}

// NewContextManager 创建上下文管理器，尝试从 Provider 探测模型 context window，失败则使用配置兑退
func NewContextManager(ctx context.Context, cfg config.Config, providerMgr *ProviderManager) *ContextManager {
	maxTokens := cfg.AIContextMaxTokens
	if maxTokens <= 0 && providerMgr != nil {
		maxTokens = providerMgr.FetchContextWindow(ctx)
	}
	if maxTokens <= 0 {
		maxTokens = 128000
	}
	warnTokens := cfg.AIContextWarnTokens
	if warnTokens <= 0 {
		warnTokens = int(float64(maxTokens) * 0.78)
	}
	if warnTokens >= maxTokens {
		warnTokens = maxTokens - 1
	}
	if warnTokens <= 0 {
		warnTokens = maxTokens
	}
	keepRecent := cfg.AIContextKeepRecent
	if keepRecent <= 0 {
		keepRecent = 6
	}
	return &ContextManager{
		providerMgr: providerMgr,
		warnTokens:  warnTokens,
		maxTokens:   maxTokens,
		keepRecent:  keepRecent,
	}
}

// WarnTokens 返回警告阈値
func (m *ContextManager) WarnTokens() int {
	if m == nil {
		return 0
	}
	return m.warnTokens
}

// MaxTokens 返回最大 token 限制
func (m *ContextManager) MaxTokens() int {
	if m == nil {
		return 0
	}
	return m.maxTokens
}

// KeepRecent 返回压缩后保留的最近轮数
func (m *ContextManager) KeepRecent() int {
	if m == nil {
		return 0
	}
	return m.keepRecent
}

// EstimateTokens 估算消息列表的 token 数，中文字符×0.6、其他字符×0.3
func (m *ContextManager) EstimateTokens(msgs []ChatMessage) int {
	total := 0
	for _, msg := range msgs {
		total += estimateTextTokens(msg.Content)
		if len(msg.ToolCalls) > 0 {
			payload, err := json.Marshal(msg.ToolCalls)
			if err == nil {
				total += estimateTextTokens(string(payload))
			}
		}
	}
	return total
}

func (m *ContextManager) fixMessages(msgs []ChatMessage) []ChatMessage {
	if len(msgs) == 0 {
		return msgs
	}
	fixed := make([]ChatMessage, 0, len(msgs))
	var pendingAssistant *ChatMessage
	pendingTools := make([]ChatMessage, 0)
	flush := func() {
		if pendingAssistant != nil && len(pendingTools) > 0 {
			fixed = append(fixed, *pendingAssistant)
			fixed = append(fixed, pendingTools...)
		}
		pendingAssistant = nil
		pendingTools = pendingTools[:0]
	}
	for _, msg := range msgs {
		if msg.Role == openai.ChatMessageRoleTool {
			if pendingAssistant != nil {
				pendingTools = append(pendingTools, msg)
			}
			continue
		}
		if msg.Role == openai.ChatMessageRoleAssistant && len(msg.ToolCalls) > 0 {
			flush()
			copyMsg := msg
			pendingAssistant = &copyMsg
			continue
		}
		flush()
		fixed = append(fixed, msg)
	}
	flush()
	return fixed
}

func (m *ContextManager) splitHistory(msgs []ChatMessage, keepRecent int) ([]ChatMessage, []ChatMessage, []ChatMessage) {
	systemMessages, nonSystemMessages := splitSystemMessages(msgs)
	if keepRecent <= 0 || len(nonSystemMessages) == 0 {
		return systemMessages, nil, nonSystemMessages
	}
	userCount := 0
	splitIndex := -1
	for i := len(nonSystemMessages) - 1; i >= 0; i-- {
		if nonSystemMessages[i].Role != openai.ChatMessageRoleUser {
			continue
		}
		userCount++
		if userCount == keepRecent {
			splitIndex = i
			break
		}
	}
	if splitIndex <= 0 {
		return systemMessages, nil, nonSystemMessages
	}
	toSummarize := m.fixMessages(nonSystemMessages[:splitIndex])
	recent := nonSystemMessages[splitIndex:]
	return systemMessages, toSummarize, recent
}

// LLMSummaryCompress 对消息历史进行 LLM 摘要压缩，保留最近 keepRecent 轮，失败则返回错误
func (m *ContextManager) LLMSummaryCompress(ctx context.Context, msgs []ChatMessage) ([]ChatMessage, error) {
	if m == nil || len(msgs) == 0 {
		return msgs, nil
	}
	if m.providerMgr == nil {
		return nil, fmt.Errorf("provider manager is nil")
	}
	systemMessages, toSummarize, recentMessages := m.splitHistory(msgs, m.keepRecent)
	if len(toSummarize) == 0 {
		return msgs, nil
	}
	payload := append([]ChatMessage{}, toSummarize...)
	payload = append(payload, ChatMessage{Role: openai.ChatMessageRoleUser, Content: summaryInstruction})
	resp, err := m.providerMgr.Chat(ctx, ChatRequest{Messages: payload})
	if err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(resp.Content)
	if summary == "" {
		return nil, fmt.Errorf("empty summary content")
	}
	result := append([]ChatMessage{}, systemMessages...)
	result = append(result,
		ChatMessage{Role: openai.ChatMessageRoleUser, Content: "历史摘要: " + summary},
		ChatMessage{Role: openai.ChatMessageRoleAssistant, Content: "已了解对话历史摘要"},
	)
	result = append(result, recentMessages...)
	return result, nil
}

func (m *ContextManager) halvingTruncate(msgs []ChatMessage) []ChatMessage {
	if len(msgs) <= 2 {
		return msgs
	}
	systemMessages, nonSystemMessages := splitSystemMessages(msgs)
	if len(nonSystemMessages) <= 1 {
		return msgs
	}
	deleteCount := len(nonSystemMessages) / 2
	if deleteCount <= 0 || deleteCount >= len(nonSystemMessages) {
		return msgs
	}
	truncated := nonSystemMessages[deleteCount:]
	if firstUser := firstUserIndex(truncated); firstUser >= 0 {
		truncated = truncated[firstUser:]
	}
	result := append([]ChatMessage{}, systemMessages...)
	result = append(result, ensureFirstUser(truncated, nonSystemMessages)...)
	return m.fixMessages(result)
}

// Process 处理消息列表，自动修复配对、警告或压缩，返回处理后的消息、是否已警告、错误
func (m *ContextManager) Process(ctx context.Context, msgs []ChatMessage) ([]ChatMessage, bool, error) {
	if m == nil {
		return msgs, false, nil
	}
	result := m.fixMessages(msgs)
	tokens := m.EstimateTokens(result)
	if tokens < m.warnTokens {
		return result, false, nil
	}
	if tokens < m.maxTokens {
		return result, true, nil
	}
	compressed, err := m.LLMSummaryCompress(ctx, result)
	if err != nil {
		compressed = m.halvingTruncate(result)
	} else if m.EstimateTokens(compressed) >= m.maxTokens {
		compressed = m.halvingTruncate(compressed)
	}
	return compressed, false, nil
}

func estimateTextTokens(text string) int {
	chineseCount := 0
	for _, r := range text {
		if r >= '\u4e00' && r <= '\u9fff' {
			chineseCount++
		}
	}
	otherCount := len([]rune(text)) - chineseCount
	return int(float64(chineseCount)*0.6 + float64(otherCount)*0.3)
}

func splitSystemMessages(msgs []ChatMessage) ([]ChatMessage, []ChatMessage) {
	firstNonSystem := len(msgs)
	for i, msg := range msgs {
		if msg.Role != openai.ChatMessageRoleSystem {
			firstNonSystem = i
			break
		}
	}
	return append([]ChatMessage{}, msgs[:firstNonSystem]...), append([]ChatMessage{}, msgs[firstNonSystem:]...)
}

func firstUserIndex(msgs []ChatMessage) int {
	for i, msg := range msgs {
		if msg.Role == openai.ChatMessageRoleUser {
			return i
		}
	}
	return -1
}

func ensureFirstUser(truncated []ChatMessage, original []ChatMessage) []ChatMessage {
	if len(truncated) == 0 {
		return truncated
	}
	if truncated[0].Role == openai.ChatMessageRoleUser {
		return truncated
	}
	for _, msg := range original {
		if msg.Role == openai.ChatMessageRoleUser {
			return append([]ChatMessage{msg}, truncated...)
		}
	}
	return truncated
}

func sessionMessagesToChatMessages(items []store.SessionMessage) []ChatMessage {
	messages := make([]ChatMessage, 0, len(items))
	for _, item := range items {
		messages = append(messages, ChatMessage{
			Role:       strings.TrimSpace(item.Role),
			Content:    item.Content,
			ToolCallID: strings.TrimSpace(item.ToolCallID),
			Name:       strings.TrimSpace(item.Name),
			ToolCalls:  sessionToolCallsToOpenAI(item.ToolCalls),
		})
	}
	return messages
}

func chatMessagesToSessionMessages(items []ChatMessage) []store.SessionMessage {
	now := time.Now().UTC()
	messages := make([]store.SessionMessage, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.Role) == "" || item.Role == openai.ChatMessageRoleSystem {
			continue
		}
		messages = append(messages, store.SessionMessage{
			Role:       strings.TrimSpace(item.Role),
			Content:    item.Content,
			ToolCallID: strings.TrimSpace(item.ToolCallID),
			Name:       strings.TrimSpace(item.Name),
			ToolCalls:  openAIToolCallsToSession(item.ToolCalls),
			CreatedAt:  now,
		})
	}
	return messages
}

func sessionToolCallsToOpenAI(items []store.SessionToolCall) []openai.ToolCall {
	if len(items) == 0 {
		return nil
	}
	out := make([]openai.ToolCall, 0, len(items))
	for _, item := range items {
		out = append(out, openai.ToolCall{
			ID:   item.ID,
			Type: openai.ToolType(item.Type),
			Function: openai.FunctionCall{
				Name:      item.Function.Name,
				Arguments: item.Function.Arguments,
			},
		})
	}
	return out
}

func openAIToolCallsToSession(items []openai.ToolCall) []store.SessionToolCall {
	if len(items) == 0 {
		return nil
	}
	out := make([]store.SessionToolCall, 0, len(items))
	for _, item := range items {
		out = append(out, store.SessionToolCall{
			ID:   item.ID,
			Type: string(item.Type),
			Function: store.SessionToolFunction{
				Name:      item.Function.Name,
				Arguments: item.Function.Arguments,
			},
		})
	}
	return out
}

// EstimateSessionMessagesTokens 估算 SessionMessage 列表的 token 数量，供 session 统计使用
func EstimateSessionMessagesTokens(items []store.SessionMessage) int {
	manager := &ContextManager{}
	return manager.EstimateTokens(sessionMessagesToChatMessages(items))
}
