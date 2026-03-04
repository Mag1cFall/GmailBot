package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"gmailbot/config"
	"gmailbot/internal/gmail"
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

type Agent struct {
	mu           sync.RWMutex
	client       *openai.Client
	model        string
	gmailService *gmail.Service
	store        *store.Store
	systemPrompt string
}

func NewAgent(cfg config.Config, gmailService *gmail.Service, st *store.Store) *Agent {
	clientConfig := openai.DefaultConfig(cfg.AIAPIKey)
	clientConfig.BaseURL = strings.TrimSuffix(cfg.AIBaseURL, "/")
	return &Agent{
		client:       openai.NewClientWithConfig(clientConfig),
		model:        cfg.AIModel,
		gmailService: gmailService,
		store:        st,
		systemPrompt: `你是用户的私人 Gmail 管理助手，运行在 Telegram 上。

核心能力：通过函数调用直接操作用户的 Gmail（查信、读信、分类、摘要）。

回复规范：
- 简洁有力，列举时用编号或符号列表
- 涉及金融/安全类邮件时附上简短风险提示` + langRules,
	}
}

func (a *Agent) Reload(cfg config.Config) {
	a.mu.Lock()
	defer a.mu.Unlock()
	clientConfig := openai.DefaultConfig(cfg.AIAPIKey)
	clientConfig.BaseURL = strings.TrimSuffix(cfg.AIBaseURL, "/")
	a.client = openai.NewClientWithConfig(clientConfig)
	a.model = cfg.AIModel
}

func (a *Agent) Model() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

func (a *Agent) snapshot() (*openai.Client, string) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.client, a.model
}

func (a *Agent) HandleUserMessage(ctx context.Context, tgUserID int64, userText string) (string, error) {
	userText = strings.TrimSpace(userText)
	if userText == "" {
		return "", errors.New("empty user message")
	}

	session, err := a.store.GetOrCreateActiveSession(ctx, tgUserID)
	if err != nil {
		return "", err
	}

	messages := make([]openai.ChatCompletionMessage, 0, len(session.Messages)+2)
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: a.systemPrompt,
	})
	for _, item := range session.Messages {
		if item.Role != "user" && item.Role != "assistant" {
			continue
		}
		messages = append(messages, openai.ChatCompletionMessage{
			Role:    item.Role,
			Content: item.Content,
		})
	}
	messages = append(messages, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: userText,
	})

	if _, err = a.store.AppendActiveSessionMessage(ctx, tgUserID, "user", userText); err != nil {
		return "", err
	}

	reply, err := a.chatWithTools(ctx, tgUserID, messages)
	if err != nil {
		return "", err
	}
	if _, err = a.store.AppendActiveSessionMessage(ctx, tgUserID, "assistant", reply); err != nil {
		return "", err
	}
	return reply, nil
}

func (a *Agent) GenerateDailyDigest(ctx context.Context, tgUserID int64) (string, error) {
	emails, err := a.gmailService.ListEmails(ctx, tgUserID, 20, "newer_than:1d")
	if err != nil {
		return "", err
	}
	if len(emails) == 0 {
		return "今天暂无可摘要的新邮件。", nil
	}

	payload, marshalErr := json.Marshal(emails)
	if marshalErr != nil {
		return "", marshalErr
	}
	client, model := a.snapshot()
	req := openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
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
				Content: "请总结以下邮件 JSON：\n" + string(payload),
			},
		},
	}

	resp, err := client.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("empty digest response")
	}
	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return "摘要生成成功，但模型未返回内容。", nil
	}
	return content, nil
}

// JudgeEmailImportance 讓 AI 判斷郵件是否值得推送，返回 (重要, 一句話摘要, error)
func (a *Agent) JudgeEmailImportance(ctx context.Context, tgUserID int64, email gmail.EmailSummary) (bool, string, error) {
	prompt := fmt.Sprintf(
		"判断以下邮件是否值得立即推送通知给用户。\n\n"+
			"✅ 推送（important=true）：\n"+
			"账单/付款/银行通知、安全警告/异常登录、面试/offer/合同、需要用户回复的邮件、来自真实联系人的私人邮件、服务到期/续费提醒\n\n"+
			"❌ 不推送（important=false）：\n"+
			"营销广告/促销/折扣、订阅通知/新闻简报、自动化报告/CI通知、社交平台通知（点赞/关注）、第三方服务故障/状态更新、产品功能更新公告\n\n"+
			"邮件信息：\n主题: %s\n发件人: %s\n摘要: %s\n\n"+
			"仅回复 JSON：{\"important\": true/false, \"reason\": \"一句话说明\"}\n" + langRules,
		email.Subject, email.From, email.Snippet,
	)

	client, model := a.snapshot()
	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: model,
		Messages: []openai.ChatCompletionMessage{
			{Role: openai.ChatMessageRoleUser, Content: prompt},
		},
	})
	if err != nil {
		return false, "", err
	}
	if len(resp.Choices) == 0 {
		return false, "", errors.New("empty judge response")
	}

	raw := strings.TrimSpace(resp.Choices[0].Message.Content)
	// 提取 JSON（模型有時會在 JSON 外加說明文字）
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
		// 解析失敗時保守地推送
		return true, "", nil
	}
	return result.Important, result.Reason, nil
}

func (a *Agent) chatWithTools(ctx context.Context, tgUserID int64, messages []openai.ChatCompletionMessage) (string, error) {
	tools := toolDefinitions()
	chatMessages := make([]openai.ChatCompletionMessage, len(messages))
	copy(chatMessages, messages)

	client, model := a.snapshot()

	for i := 0; i < 4; i++ {
		resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
			Model:    model,
			Messages: chatMessages,
			Tools:    tools,
		})
		if err != nil {
			return "", err
		}
		if len(resp.Choices) == 0 {
			return "", errors.New("empty model response")
		}

		modelMsg := resp.Choices[0].Message
		if len(modelMsg.ToolCalls) == 0 {
			content := strings.TrimSpace(modelMsg.Content)
			if content == "" {
				content = "我暂时无法生成有效回复，请稍后再试。"
			}
			return content, nil
		}

		chatMessages = append(chatMessages, modelMsg)
		for _, call := range modelMsg.ToolCalls {
			result, toolErr := a.executeTool(ctx, tgUserID, call.Function.Name, call.Function.Arguments)
			if toolErr != nil {
				result = fmt.Sprintf(`{"error":%q}`, toolErr.Error())
			}
			chatMessages = append(chatMessages, openai.ChatCompletionMessage{
				Role:       openai.ChatMessageRoleTool,
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    result,
			})
		}
	}

	return "已达到函数调用上限，请缩小问题范围后重试。", nil
}

func (a *Agent) executeTool(ctx context.Context, tgUserID int64, name string, args string) (string, error) {
	switch name {
	case "list_emails":
		var req struct {
			N     int    `json:"n"`
			Query string `json:"query"`
		}
		parseArgs(args, &req)
		emails, err := a.gmailService.ListEmails(ctx, tgUserID, req.N, req.Query)
		if err != nil {
			return "", err
		}
		return toJSON(map[string]any{"emails": emails})

	case "get_email":
		var req struct {
			ID string `json:"id"`
		}
		if err := parseArgs(args, &req); err != nil {
			return "", err
		}
		email, err := a.gmailService.GetEmail(ctx, tgUserID, req.ID)
		if err != nil {
			return "", err
		}
		return toJSON(email)

	case "get_labels":
		labels, err := a.gmailService.GetLabels(ctx, tgUserID)
		if err != nil {
			return "", err
		}
		return toJSON(map[string]any{"labels": labels})

	case "summarize_emails":
		var req struct {
			N     int    `json:"n"`
			Query string `json:"query"`
		}
		parseArgs(args, &req)
		if req.Query == "" {
			req.Query = "newer_than:7d"
		}
		if req.N <= 0 || req.N > 50 {
			req.N = 15
		}
		emails, err := a.gmailService.ListEmails(ctx, tgUserID, req.N, req.Query)
		if err != nil {
			return "", err
		}
		return toJSON(map[string]any{
			"summary_hint": "以下是邮件摘要原始素材，请你据此输出结构化总结",
			"emails":       emails,
		})

	default:
		return "", fmt.Errorf("unsupported tool %s", name)
	}
}

func parseArgs(raw string, out any) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	return json.Unmarshal([]byte(raw), out)
}

func toJSON(v any) (string, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func toolDefinitions() []openai.Tool {
	return []openai.Tool{
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "list_emails",
				Description: "列出指定数量的邮件",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"n":{"type":"integer","description":"返回数量，建议1-20"},
						"query":{"type":"string","description":"Gmail 搜索语法，例如 is:unread 或 from:xxx"}
					}
				}`),
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "get_email",
				Description: "读取指定邮件的详细正文",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"id":{"type":"string","description":"邮件 ID"}
					},
					"required":["id"]
				}`),
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "get_labels",
				Description: "获取 Gmail 标签列表",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{}
				}`),
			},
		},
		{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        "summarize_emails",
				Description: "拉取一批邮件供模型生成总结",
				Parameters: json.RawMessage(`{
					"type":"object",
					"properties":{
						"n":{"type":"integer","description":"邮件数量"},
						"query":{"type":"string","description":"Gmail 搜索语句"}
					}
				}`),
			},
		},
	}
}
