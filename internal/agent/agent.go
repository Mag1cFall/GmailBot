package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"gmailbot/config"
	"gmailbot/internal/persona"
	"gmailbot/internal/platform"
	"gmailbot/internal/store"

	openai "github.com/sashabaranov/go-openai"
)

const langRules = `
语言规范（所有输出必须遵守）：
- 简体中文
- emoji 只用在每个大段落/大标题前面，子项内容不要加 emoji，保持视觉干净
- 信息完整但措辞简洁，用连贯的短句表达，减少「- 」分点罗列，避免每个信息点都拆成独立的列表项
- 末尾如果需要追问，固定使用一句简短的话（例如「需要查看原文告诉我。」），严禁罗列多条建议或命令选项
- 意图一致：不改变用户问题的原始意图，不擅自增加无关内容
- 使用 Markdown 格式（加粗、分隔线），但控制层级深度
- 使用正向表达，避免无必要的否定和逻辑反转，禁止"这不是...而是"、"而不是"等转折
- 确保句法结构完整、语义逻辑清晰，严禁单字替代短语（不得用"写"替代"修改"，不得用"若"替代"如果"，不得用"回"替代"回复"）
- 禁止使用互联网黑话，包括但不限于：结论、口径、稳、更稳、坑、走、抓手、路径、落地、定性、定调、倒逼、落盘、落成、粒度、收敛、收紧、收束、聚焦、门禁、P95、P99、对账、治理、基线、加固、根因
- 禁止使用以下措辞："我直接把"、"下面把你"、"你现在"、"你只需要"、"二选一"、"我不跟你"、"你要我"、"要是你"、"如果你坚持"、"但你得"、"不需要你决定"、"不需要你认同"、"你的问题是"、"你的担忧是"、"已XX"、"说明如下"、"答复如下"、"不涉及XX"、"不说教"、"不鸡汤"、"不装"、"不躲"、"不绕"
`

type PromptSection struct {
	Label   string
	Content string
}

type PromptBuilder struct {
	mu       sync.RWMutex
	sections []PromptSection
}

func NewPromptBuilder() *PromptBuilder {
	return &PromptBuilder{}
}

func (pb *PromptBuilder) SetSection(label, content string) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	for i, s := range pb.sections {
		if s.Label == label {
			pb.sections[i].Content = content
			return
		}
	}
	pb.sections = append(pb.sections, PromptSection{Label: label, Content: content})
}

func (pb *PromptBuilder) RemoveSection(label string) {
	pb.mu.Lock()
	defer pb.mu.Unlock()
	for i, s := range pb.sections {
		if s.Label == label {
			pb.sections = append(pb.sections[:i], pb.sections[i+1:]...)
			return
		}
	}
}

func (pb *PromptBuilder) Build(registry *ToolRegistry) string {
	if registry == nil {
		return pb.BuildWithTools(nil, nil)
	}
	return pb.BuildWithTools(registry.ActiveTools(), nil)
}

func (pb *PromptBuilder) BuildWithTools(tools []*ToolDef, overrides map[string]string) string {
	pb.mu.RLock()
	sections := append([]PromptSection(nil), pb.sections...)
	pb.mu.RUnlock()
	var sb strings.Builder
	seen := map[string]struct{}{}
	for _, section := range sections {
		content := section.Content
		if overrides != nil {
			if override, ok := overrides[section.Label]; ok && strings.TrimSpace(override) != "" {
				content = override
			}
		}
		if strings.TrimSpace(content) == "" {
			continue
		}
		seen[section.Label] = struct{}{}
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}
	if overrides != nil {
		for label, content := range overrides {
			if _, ok := seen[label]; ok || strings.TrimSpace(content) == "" {
				continue
			}
			sb.WriteString(content)
			sb.WriteString("\n\n")
		}
	}

	if len(tools) > 0 {
		sb.WriteString("可用工具列表：\n")
		for _, tool := range tools {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", tool.Name, tool.Description))
		}
		sb.WriteString("\n")
	}

	sb.WriteString(langRules)
	return strings.TrimSpace(sb.String())
}

type Agent struct {
	mu            sync.RWMutex
	providerMgr   *ProviderManager
	registry      *ToolRegistry
	store         *store.Store
	promptBuilder *PromptBuilder
	maxToolSteps  int
	personaMgr    *persona.Manager
}

func NewAgent(cfg config.Config, registry *ToolRegistry, st *store.Store) *Agent {
	pm := NewProviderManager()
	pm.LoadFromConfig(cfg)

	pb := NewPromptBuilder()
	pb.SetSection("identity", `你是用户的私人 Gmail 管理助手，运行在 Telegram 上。

核心能力：通过函数调用直接操作用户的 Gmail（查信、读信、发信、回信、分类、摘要、标签管理）。

回复规范：
- 简洁有力，列举时用编号或符号列表
- 涉及金融/安全类邮件时附上简短风险提示`)

	maxSteps := cfg.AIToolMaxSteps
	if maxSteps <= 0 {
		maxSteps = 6
	}

	return &Agent{
		providerMgr:   pm,
		registry:      registry,
		store:         st,
		promptBuilder: pb,
		maxToolSteps:  maxSteps,
	}
}

func (a *Agent) Registry() *ToolRegistry {
	return a.registry
}

func (a *Agent) PromptBuilder() *PromptBuilder {
	return a.promptBuilder
}

func (a *Agent) ProviderManager() *ProviderManager {
	return a.providerMgr
}

func (a *Agent) SetPersonaManager(manager *persona.Manager) {
	a.personaMgr = manager
}

func (a *Agent) Reload(cfg config.Config) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.providerMgr.LoadFromConfig(cfg)
	if cfg.AIToolMaxSteps > 0 {
		a.maxToolSteps = cfg.AIToolMaxSteps
	}
}

func (a *Agent) Model() string {
	return a.providerMgr.PrimaryModel()
}

func (a *Agent) HandleUserMessage(ctx context.Context, tgUserID int64, userText string) (string, error) {
	resp, err := a.HandleMessage(ctx, platform.UnifiedMessage{
		Platform:  "telegram",
		UserID:    strconv.FormatInt(tgUserID, 10),
		SessionID: "active",
		Text:      userText,
	})
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func (a *Agent) HandleMessage(ctx context.Context, msg platform.UnifiedMessage) (platform.UnifiedResponse, error) {
	userText := strings.TrimSpace(msg.Text)
	if userText == "" {
		return platform.UnifiedResponse{}, errors.New("empty user message")
	}
	if strings.TrimSpace(msg.Platform) == "" {
		msg.Platform = "telegram"
	}
	if strings.TrimSpace(msg.UserID) == "" {
		return platform.UnifiedResponse{}, errors.New("empty user id")
	}

	session, err := a.store.GetOrCreateActiveSessionByIdentity(ctx, msg.Platform, msg.UserID)
	if err != nil {
		return platform.UnifiedResponse{}, err
	}
	if msg.SessionID == "" {
		msg.SessionID = session.ID
	}

	selectedPersona := persona.Persona{}
	if a.personaMgr != nil {
		resolved, err := a.personaMgr.ActivePersona(ctx, msg.Platform, msg.UserID)
		if err == nil {
			selectedPersona = resolved
		}
	}
	selectedTools := a.registry.FilteredActiveTools(selectedPersona.Tools)
	overrides := map[string]string{}
	if strings.TrimSpace(selectedPersona.SystemPrompt) != "" {
		overrides["identity"] = selectedPersona.SystemPrompt
	}
	messages := make([]ChatMessage, 0, len(session.Messages)+2)
	messages = append(messages, ChatMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: a.promptBuilder.BuildWithTools(selectedTools, overrides),
	})
	for _, item := range session.Messages {
		if item.Role != "user" && item.Role != "assistant" {
			continue
		}
		messages = append(messages, ChatMessage{
			Role:    item.Role,
			Content: item.Content,
		})
	}
	messages = append(messages, ChatMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: userText,
	})

	if _, err = a.store.AppendActiveSessionMessageByIdentity(ctx, msg.Platform, msg.UserID, "user", userText); err != nil {
		return platform.UnifiedResponse{}, err
	}

	toolCtx := &ToolContext{
		Context:   ctx,
		TgUserID:  session.TgUserID,
		Platform:  msg.Platform,
		UserID:    msg.UserID,
		SessionID: msg.SessionID,
		Extra:     map[string]any{},
	}
	reply, err := a.chatWithTools(ctx, toolCtx, messages, selectedTools, selectedPersona.Model)
	if err != nil {
		return platform.UnifiedResponse{}, err
	}
	if _, err = a.store.AppendActiveSessionMessageByIdentity(ctx, msg.Platform, msg.UserID, "assistant", reply); err != nil {
		return platform.UnifiedResponse{}, err
	}
	return platform.UnifiedResponse{Text: reply, Markdown: true}, nil
}

func (a *Agent) GenerateDailyDigest(ctx context.Context, tgUserID int64) (string, error) {
	toolCtx := &ToolContext{Context: ctx, TgUserID: tgUserID, Platform: "telegram", UserID: strconv.FormatInt(tgUserID, 10), Extra: map[string]any{}}
	listTool, ok := a.registry.Get("list_emails")
	if !ok {
		return "", errors.New("list_emails tool not available")
	}

	args, _ := json.Marshal(map[string]any{"n": 20, "query": "newer_than:1d"})
	result, err := listTool.Handler(toolCtx, args)
	if err != nil {
		return "", err
	}

	var emailData struct {
		Emails json.RawMessage `json:"emails"`
	}
	if err := json.Unmarshal([]byte(result), &emailData); err != nil {
		return "", err
	}
	if string(emailData.Emails) == "[]" || string(emailData.Emails) == "null" {
		return "今天暂无可摘要的新邮件。", nil
	}

	messages := []ChatMessage{
		{
			Role: openai.ChatMessageRoleSystem,
			Content: "你是用户的私人邮件分析师。根据提供的邮件列表，输出一份精炼的每日摘要。\n\n" +
				"输出格式（严格遵守）：\n" +
				"### 📢 重要事项\n" +
				"按优先级列出需要关注的内容，同一事件的多封通知合并为一条。\n\n" +
				"### ✅ 待办建议\n" +
				"给出具体可执行的行动项，每条以动词开头。\n\n" +
				"### ⚠️ 风险点\n" +
				"识别潜在的财务、安全等与用户直接相关的风险。\n\n" +
				"规则：\n" +
				"- 广告/营销/订阅通知/新闻简报/第三方服务故障播报，归入末尾「其他」一句话带过\n" +
				"- 同一服务的多封状态更新邮件合并为一条，只给出最终状态\n" +
				"- 没有重要内容时直接说「今日无需特别关注的邮件」，不硬凑\n" +
				"- 只输出摘要本身，末尾不追问、不提供选项" + langRules,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: "请总结以下邮件 JSON：\n" + string(emailData.Emails),
		},
	}

	resp, err := a.providerMgr.Chat(ctx, ChatRequest{Messages: messages})
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "摘要生成成功，但模型未返回内容。", nil
	}
	return content, nil
}

func (a *Agent) JudgeEmailImportance(ctx context.Context, tgUserID int64, subject, from, snippet string) (bool, string, error) {
	prompt := fmt.Sprintf(
		"判断以下邮件是否值得立即推送通知给用户。\n\n"+
			"✅ 推送（important=true）：\n"+
			"账单/付款/银行通知、安全警告/异常登录、面试/offer/合同、需要用户回复的邮件、来自真实联系人的私人邮件、服务到期/续费提醒\n\n"+
			"❌ 不推送（important=false）：\n"+
			"营销广告/促销/折扣、订阅通知/新闻简报、自动化报告/CI通知、社交平台通知（点赞/关注）、第三方服务故障/状态更新、产品功能更新公告\n\n"+
			"邮件信息：\n主题: %s\n发件人: %s\n摘要: %s\n\n"+
			"仅回复 JSON：{\"important\": true/false, \"reason\": \"一句话说明\"}\n"+langRules,
		subject, from, snippet,
	)

	resp, err := a.providerMgr.Chat(ctx, ChatRequest{
		Messages: []ChatMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
	})
	if err != nil {
		return false, "", err
	}

	raw := strings.TrimSpace(resp.Content)
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start >= 0 && end > start {
		raw = raw[start : end+1]
	}

	var result struct {
		Important bool   `json:"important"`
		Reason    string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		return true, "", nil
	}
	return result.Important, result.Reason, nil
}

func (a *Agent) chatWithTools(ctx context.Context, toolCtx *ToolContext, messages []ChatMessage, toolDefs []*ToolDef, model string) (string, error) {
	tools := OpenAIToolsFromDefs(toolDefs)
	allowedTools := map[string]struct{}{}
	for _, tool := range toolDefs {
		if tool != nil {
			allowedTools[tool.Name] = struct{}{}
		}
	}
	chatMessages := make([]ChatMessage, len(messages))
	copy(chatMessages, messages)

	a.mu.RLock()
	maxSteps := a.maxToolSteps
	a.mu.RUnlock()

	for i := 0; i < maxSteps; i++ {
		resp, err := a.providerMgr.Chat(ctx, ChatRequest{
			Messages: chatMessages,
			Tools:    tools,
			Model:    model,
		})
		if err != nil {
			return "", err
		}

		if len(resp.ToolCalls) == 0 {
			content := strings.TrimSpace(resp.Content)
			if content == "" {
				content = "我暂时无法生成有效回复，请稍后再试。"
			}
			return content, nil
		}

		chatMessages = append(chatMessages, ChatMessage{
			Role:      openai.ChatMessageRoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		for _, call := range resp.ToolCalls {
			if _, allowed := allowedTools[call.Function.Name]; !allowed {
				chatMessages = append(chatMessages, ChatMessage{
					Role:       openai.ChatMessageRoleTool,
					ToolCallID: call.ID,
					Name:       call.Function.Name,
					Content:    fmt.Sprintf(`{"error":%q}`, "tool not allowed for current persona"),
				})
				continue
			}
			result, toolErr := a.registry.Execute(toolCtx, call.Function.Name, call.Function.Arguments)
			if toolErr != nil {
				result = fmt.Sprintf(`{"error":%q}`, toolErr.Error())
			}
			chatMessages = append(chatMessages, ChatMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    result,
			})
		}
	}

	return "已达到函数调用上限，请缩小问题范围后重试。", nil
}
