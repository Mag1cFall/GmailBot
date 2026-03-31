// Gmail 工具插件，注册邮件操作到 Agent
package gmail

import (
	"context"
	"encoding/json"

	"gmailbot/internal/agent"
	"gmailbot/internal/plugin"
)

// GmailPlugin Gmail 能力插件
type GmailPlugin struct {
	service *Service
	pending *PendingStore
}

// NewPlugin 创建 Gmail 插件
func NewPlugin(service *Service, pending *PendingStore) *GmailPlugin {
	return &GmailPlugin{service: service, pending: pending}
}

func (p *GmailPlugin) Name() string                     { return "gmail" }
func (p *GmailPlugin) Description() string              { return "Gmail 邮件能力" }
func (p *GmailPlugin) Shutdown() error                  { return nil }
func (p *GmailPlugin) Commands() []plugin.Command       { return nil }
func (p *GmailPlugin) EventHandlers() []plugin.EventSub { return nil }

// Init 注册所有 Gmail 工具
func (p *GmailPlugin) Init(ctx *plugin.Context) error {
	p.registerListEmails(ctx.Registry)
	p.registerGetEmail(ctx.Registry)
	p.registerGetLabels(ctx.Registry)
	p.registerSummarizeEmails(ctx.Registry)
	p.registerSendEmail(ctx.Registry)
	p.registerReplyEmail(ctx.Registry)
	p.registerForwardEmail(ctx.Registry)
	p.registerCreateLabel(ctx.Registry)
	p.registerDeleteLabel(ctx.Registry)
	p.registerModifyLabels(ctx.Registry)
	return nil
}

// registerListEmails 注册 list_emails 工具
func (p *GmailPlugin) registerListEmails(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "list_emails",
		Description: "列出指定数量的邮件",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"n":{"type":"integer","description":"返回数量，建议1-20"},
				"query":{"type":"string","description":"Gmail 搜索语法，例如 is:unread 或 from:xxx"}
			}
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				N     int    `json:"n"`
				Query string `json:"query"`
			}
			agent.ParseToolArgs[any](raw)
			json.Unmarshal(raw, &req)
			emails, err := p.service.ListEmails(context.Background(), tc.TgUserID, req.N, req.Query)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"emails": emails})
		},
		Active:   true,
		Category: "gmail",
	})
}

// registerGetEmail 注册 get_email 工具
func (p *GmailPlugin) registerGetEmail(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "get_email",
		Description: "读取指定邮件的详细正文",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"邮件 ID"}
			},
			"required":["id"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				ID string `json:"id"`
			}
			json.Unmarshal(raw, &req)
			email, err := p.service.GetEmail(context.Background(), tc.TgUserID, req.ID)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(email)
		},
		Active:   true,
		Category: "gmail",
	})
}

// registerGetLabels 注册 get_labels 工具
func (p *GmailPlugin) registerGetLabels(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "get_labels",
		Description: "获取 Gmail 标签列表",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{}
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			labels, err := p.service.GetLabels(context.Background(), tc.TgUserID)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"labels": labels})
		},
		Active:   true,
		Category: "gmail",
	})
}

// registerSummarizeEmails 注册 summarize_emails 工具
func (p *GmailPlugin) registerSummarizeEmails(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "summarize_emails",
		Description: "拉取一批邮件供模型生成总结",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"n":{"type":"integer","description":"邮件数量"},
				"query":{"type":"string","description":"Gmail 搜索语句"}
			}
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				N     int    `json:"n"`
				Query string `json:"query"`
			}
			json.Unmarshal(raw, &req)
			if req.Query == "" {
				req.Query = "newer_than:7d"
			}
			if req.N <= 0 || req.N > 50 {
				req.N = 15
			}
			emails, err := p.service.ListEmails(context.Background(), tc.TgUserID, req.N, req.Query)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{
				"summary_hint": "以下是邮件摘要原始素材，请你据此输出结构化总结",
				"emails":       emails,
			})
		},
		Active:   true,
		Category: "gmail",
	})
}

// registerSendEmail 注册 send_email 工具
func (p *GmailPlugin) registerSendEmail(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "send_email",
		Description: "发送一封新邮件（调用后不会立即发送，系统会暂存草稿并显示确认按钮，用户点击确认后才真正发送）。当用户要求发邮件时直接调用此工具，无需先用文本展示草稿",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"to":{"type":"string","description":"收件人邮箱地址"},
				"subject":{"type":"string","description":"邮件主题"},
				"body":{"type":"string","description":"邮件正文"}
			},
			"required":["to","subject","body"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				To      string `json:"to"`
				Subject string `json:"subject"`
				Body    string `json:"body"`
			}
			json.Unmarshal(raw, &req)
			p.pending.Set(&PendingDraft{
				Type:     DraftSend,
				TgUserID: tc.TgUserID,
				To:       req.To,
				Subject:  req.Subject,
				Body:     req.Body,
			})
			return agent.ToJSON(map[string]any{
				"status":  "pending_confirm",
				"to":      req.To,
				"subject": req.Subject,
				"body":    req.Body,
				"message": "草稿已准备好，请在 Telegram 点击【✅ 确认发送】按鈕完成发送，或【✏️ 修改】【❌ 取消】",
			})
		},
		Active:   true,
		Category: "gmail",
	})
}

// registerReplyEmail 注册 reply_email 工具
func (p *GmailPlugin) registerReplyEmail(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "reply_email",
		Description: "回复一封邮件（调用后不会立即发送，系统会暂存草稿并显示确认按钮，用户确认后才真正发送）",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"要回复的邮件 ID"},
				"body":{"type":"string","description":"回复正文"}
			},
			"required":["id","body"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				ID   string `json:"id"`
				Body string `json:"body"`
			}
			json.Unmarshal(raw, &req)
			p.pending.Set(&PendingDraft{
				Type:      DraftReply,
				TgUserID:  tc.TgUserID,
				RefMailID: req.ID,
				Body:      req.Body,
			})
			return agent.ToJSON(map[string]any{
				"status":  "pending_confirm",
				"body":    req.Body,
				"message": "回复草稿已准备，请在 Telegram 点击【✅ 确认发送】按鈕",
			})
		},
		Active:   true,
		Category: "gmail",
	})
}

// registerForwardEmail 注册 forward_email 工具
func (p *GmailPlugin) registerForwardEmail(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "forward_email",
		Description: "转发一封邮件给指定收件人（调用后不会立即发送，系统会暂存草稿并显示确认按钮，用户确认后才真正发送）",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"id":{"type":"string","description":"要转发的邮件 ID"},
				"to":{"type":"string","description":"转发目标邮箱地址"}
			},
			"required":["id","to"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				ID string `json:"id"`
				To string `json:"to"`
			}
			json.Unmarshal(raw, &req)
			p.pending.Set(&PendingDraft{
				Type:      DraftForward,
				TgUserID:  tc.TgUserID,
				RefMailID: req.ID,
				To:        req.To,
			})
			return agent.ToJSON(map[string]any{
				"status":  "pending_confirm",
				"to":      req.To,
				"message": "转发草稿已准备，请在 Telegram 点击【✅ 确认发送】按鈕",
			})
		},
		Active:   true,
		Category: "gmail",
	})
}

// registerCreateLabel 注册 create_label 工具
func (p *GmailPlugin) registerCreateLabel(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "create_label",
		Description: "创建一个新的 Gmail 标签",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"name":{"type":"string","description":"标签名称"}
			},
			"required":["name"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				Name string `json:"name"`
			}
			json.Unmarshal(raw, &req)
			label, err := p.service.CreateLabel(context.Background(), tc.TgUserID, req.Name)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(label)
		},
		Active:   true,
		Category: "gmail",
	})
}

// registerDeleteLabel 注册 delete_label 工具
func (p *GmailPlugin) registerDeleteLabel(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "delete_label",
		Description: "删除一个 Gmail 标签",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"label_id":{"type":"string","description":"标签 ID"}
			},
			"required":["label_id"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				LabelID string `json:"label_id"`
			}
			json.Unmarshal(raw, &req)
			err := p.service.DeleteLabel(context.Background(), tc.TgUserID, req.LabelID)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"status": "deleted"})
		},
		Active:   true,
		Category: "gmail",
	})
}

// registerModifyLabels 注册 modify_labels 工具
func (p *GmailPlugin) registerModifyLabels(r *agent.ToolRegistry) {
	r.Register(&agent.ToolDef{
		Name:        "modify_labels",
		Description: "给邮件添加或移除标签（可用于标记已读/未读、归档等）",
		Parameters: json.RawMessage(`{
			"type":"object",
			"properties":{
				"email_id":{"type":"string","description":"邮件 ID"},
				"add_labels":{"type":"array","items":{"type":"string"},"description":"要添加的标签 ID 列表"},
				"remove_labels":{"type":"array","items":{"type":"string"},"description":"要移除的标签 ID 列表"}
			},
			"required":["email_id"]
		}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				EmailID      string   `json:"email_id"`
				AddLabels    []string `json:"add_labels"`
				RemoveLabels []string `json:"remove_labels"`
			}
			json.Unmarshal(raw, &req)
			err := p.service.ModifyMessageLabels(context.Background(), tc.TgUserID, req.EmailID, req.AddLabels, req.RemoveLabels)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"status": "modified"})
		},
		Active:   true,
		Category: "gmail",
	})
}
