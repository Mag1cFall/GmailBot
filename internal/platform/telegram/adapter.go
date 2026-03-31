// Telegram 平台适配器
package telegram

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"gmailbot/config"
	baseplatform "gmailbot/internal/platform"
	"gmailbot/internal/tgbot"

	tele "gopkg.in/telebot.v3"
)

// headingRe 匹配 Markdown 标题
var headingRe = regexp.MustCompile(`^#{1,6}\s+(.+)$`)
// boldRe 匹配 Markdown 粗体
var boldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)

// Adapter Telegram Bot 适配器
type Adapter struct {
	app      *tgbot.App
	bot      *tele.Bot
	stopOnce sync.Once
}

// NewAdapter 创建 Telegram 适配器
func NewAdapter(cfg config.Config, app *tgbot.App) (*Adapter, error) {
	bot, err := tele.NewBot(tele.Settings{
		Token:  cfg.BotToken,
		Poller: &tele.LongPoller{Timeout: time.Duration(cfg.TelegramTimeoutSec) * time.Second},
	})
	if err != nil {
		return nil, fmt.Errorf("init telegram bot failed: %w", err)
	}
	return &Adapter{app: app, bot: bot}, nil
}

// Name 返回平台名称
func (a *Adapter) Name() string {
	return "telegram"
}

// Start 启动消息轮询，注册命令和文本处理器
func (a *Adapter) Start(ctx context.Context, handler baseplatform.MessageHandler) error {
	for _, command := range a.app.Commands() {
		a.bot.Handle("/"+command.Name, a.handleText(ctx, handler))
	}
	a.bot.Handle(tele.OnText, a.handleText(ctx, handler))
	a.bot.Handle(&tele.Btn{Unique: "cfg"}, a.handleConfigCallback(ctx))
	a.bot.Handle(&tele.Btn{Unique: "draft_confirm"}, a.handleDraftCallback(ctx, "confirm"))
	a.bot.Handle(&tele.Btn{Unique: "draft_cancel"}, a.handleDraftCallback(ctx, "cancel"))
	a.bot.Handle(&tele.Btn{Unique: "draft_edit"}, a.handleDraftCallback(ctx, "edit"))
	a.registerCommands()

	go func() {
		<-ctx.Done()
		a.Stop()
	}()
	a.bot.Start()
	return nil
}

// Stop 优雅关闭 Bot
func (a *Adapter) Stop() error {
	a.stopOnce.Do(func() {
		a.bot.Stop()
	})
	return nil
}

// Send 向指定 Telegram 用户发送响应
func (a *Adapter) Send(ctx context.Context, userID string, resp baseplatform.UnifiedResponse) error {
	id, err := strconv.ParseInt(strings.TrimSpace(userID), 10, 64)
	if err != nil {
		return err
	}
	return a.sendToChat(ctx, &tele.User{ID: id}, resp, nil)
}

// handleText 将 Telegram 消息转换为统一消息并分发给处理函数
func (a *Adapter) handleText(ctx context.Context, handler baseplatform.MessageHandler) func(tele.Context) error {
	return func(c tele.Context) error {
		msg := baseplatform.UnifiedMessage{
			Platform:  a.Name(),
			UserID:    strconv.FormatInt(c.Sender().ID, 10),
			SessionID: "active",
			Text:      strings.TrimSpace(c.Text()),
			Extra:     map[string]any{},
		}
		resp, err := handler(ctx, msg)
		if err != nil {
			return c.Send("处理失败：" + err.Error())
		}
		if resp.Text == "" && len(resp.Attachments) == 0 {
			return nil
		}
		var markup *tele.ReplyMarkup
		if isConfigCommand(msg.Text) {
			markup = a.buildConfigMarkup()
		} else if a.app.HasPendingDraft(c.Sender().ID) {
			markup = a.buildSendDraftMarkup()
		}
		return a.sendToChat(ctx, c.Recipient(), resp, markup)
	}
}

// handleConfigCallback 处理配置内联键盘回调
func (a *Adapter) handleConfigCallback(ctx context.Context) func(tele.Context) error {
	return func(c tele.Context) error {
		resp, err := a.app.HandleConfigSelection(baseplatform.UnifiedMessage{
			Platform:  a.Name(),
			UserID:    strconv.FormatInt(c.Sender().ID, 10),
			SessionID: "active",
		}, c.Callback().Data)
		_ = c.Respond()
		if err != nil {
			return c.Send("处理失败：" + err.Error())
		}
		return a.sendToChat(ctx, c.Recipient(), resp, nil)
	}
}

// handleDraftCallback 处理草稿确认/取消/修改回调
func (a *Adapter) handleDraftCallback(ctx context.Context, action string) func(tele.Context) error {
	return func(c tele.Context) error {
		_ = c.Respond()
		resp, err := a.app.HandleSendDraftAction(ctx, c.Sender().ID, action)
		if err != nil {
			return c.Send("操作失败：" + err.Error())
		}
		return a.sendToChat(ctx, c.Recipient(), resp, nil)
	}
}

// registerCommands 向 Telegram 注册命令列表
func (a *Adapter) registerCommands() {
	commands := make([]tele.Command, 0, len(a.app.Commands()))
	for _, command := range a.app.Commands() {
		commands = append(commands, tele.Command{Text: command.Name, Description: command.Description})
	}
	_ = a.bot.SetCommands(commands)
}

// buildConfigMarkup 构建配置项内联键盘
func (a *Adapter) buildConfigMarkup() *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	rows := make([][]tele.InlineButton, 0, len(a.app.ConfigOptions()))
	for _, option := range a.app.ConfigOptions() {
		rows = append(rows, []tele.InlineButton{{Unique: "cfg", Text: option.Display, Data: option.Key}})
	}
	markup.InlineKeyboard = rows
	return markup
}

// buildSendDraftMarkup 构建发邮件确认面板：确认发送 / 修改 / 取消
func (a *Adapter) buildSendDraftMarkup() *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	markup.InlineKeyboard = [][]tele.InlineButton{{
		{Unique: "draft_confirm", Text: "✅ 确认发送"},
		{Unique: "draft_edit", Text: "✏️ 修改"},
		{Unique: "draft_cancel", Text: "❌ 取消"},
	}}
	return markup
}

// sendToChat 发送响应，加马克需要减词且超长自动分片
func (a *Adapter) sendToChat(ctx context.Context, recipient tele.Recipient, resp baseplatform.UnifiedResponse, markup *tele.ReplyMarkup) error {
	text := resp.Text
	if resp.Markdown {
		text = mdToTelegram(text)
	}
	chunks := splitBySize(text, 3500)
	for i, chunk := range chunks {
		if resp.Markdown {
			if _, err := a.bot.Send(recipient, chunk, tele.ModeMarkdown, markupIfFirst(markup, i)); err == nil {
				continue
			}
		}
		if _, err := a.bot.Send(recipient, chunk, markupIfFirst(markup, i)); err != nil {
			return err
		}
	}
	return nil
}

// markupIfFirst 仅第一个分片携带键盘
func markupIfFirst(markup *tele.ReplyMarkup, index int) *tele.ReplyMarkup {
	if index == 0 {
		return markup
	}
	return nil
}

// splitBySize 按字节数将文本拆分为多片，尽量不切断行
func splitBySize(text string, chunkSize int) []string {
	if len(text) <= chunkSize {
		return []string{text}
	}
	var chunks []string
	lines := strings.Split(text, "\n")
	var current strings.Builder
	for _, line := range lines {
		for len(line) > chunkSize {
			segment := line[:chunkSize]
			if current.Len() > 0 {
				chunks = append(chunks, current.String())
				current.Reset()
			}
			chunks = append(chunks, segment)
			line = line[chunkSize:]
		}
		if current.Len()+len(line)+1 > chunkSize && current.Len() > 0 {
			chunks = append(chunks, current.String())
			current.Reset()
		}
		if current.Len() > 0 {
			current.WriteString("\n")
		}
		current.WriteString(line)
	}
	if current.Len() > 0 {
		chunks = append(chunks, current.String())
	}
	return chunks
}

// mdToTelegram 将部分 Markdown 语法转换为 Telegram 支持的标记
func mdToTelegram(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if match := headingRe.FindStringSubmatch(trimmed); match != nil {
			content := strings.ReplaceAll(strings.TrimSpace(match[1]), "*", "")
			if content != "" {
				lines[i] = "*" + content + "*"
			}
			continue
		}
		if trimmed == "---" || trimmed == "***" || trimmed == "___" {
			lines[i] = "———"
			continue
		}
		if strings.HasPrefix(trimmed, "> ") {
			lines[i] = "┃ " + strings.TrimPrefix(trimmed, "> ")
			continue
		}
		if strings.HasPrefix(trimmed, ">") && len(trimmed) > 1 {
			lines[i] = "┃ " + strings.TrimPrefix(trimmed, ">")
			continue
		}
	}
	result := strings.Join(lines, "\n")
	result = boldRe.ReplaceAllString(result, "*$1*")
	result = strings.ReplaceAll(result, "⇒", "→")
	return result
}

// isConfigCommand 判断是否为 /config 命令
func isConfigCommand(text string) bool {
	text = strings.TrimSpace(text)
	return text == "/config" || strings.HasPrefix(text, "/config@")
}
