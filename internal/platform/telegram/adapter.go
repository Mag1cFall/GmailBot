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

var headingRe = regexp.MustCompile(`^#{1,6}\s+(.+)$`)
var boldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)

type Adapter struct {
	app      *tgbot.App
	bot      *tele.Bot
	stopOnce sync.Once
}

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

func (a *Adapter) Name() string {
	return "telegram"
}

func (a *Adapter) Start(ctx context.Context, handler baseplatform.MessageHandler) error {
	for _, command := range a.app.Commands() {
		a.bot.Handle("/"+command.Name, a.handleText(ctx, handler))
	}
	a.bot.Handle(tele.OnText, a.handleText(ctx, handler))
	a.bot.Handle(&tele.Btn{Unique: "cfg"}, a.handleConfigCallback(ctx))
	a.registerCommands()

	go func() {
		<-ctx.Done()
		a.Stop()
	}()
	a.bot.Start()
	return nil
}

func (a *Adapter) Stop() error {
	a.stopOnce.Do(func() {
		a.bot.Stop()
	})
	return nil
}

func (a *Adapter) Send(ctx context.Context, userID string, resp baseplatform.UnifiedResponse) error {
	id, err := strconv.ParseInt(strings.TrimSpace(userID), 10, 64)
	if err != nil {
		return err
	}
	return a.sendToChat(ctx, &tele.User{ID: id}, resp, nil)
}

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
		}
		return a.sendToChat(ctx, c.Recipient(), resp, markup)
	}
}

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

func (a *Adapter) registerCommands() {
	commands := make([]tele.Command, 0, len(a.app.Commands()))
	for _, command := range a.app.Commands() {
		commands = append(commands, tele.Command{Text: command.Name, Description: command.Description})
	}
	_ = a.bot.SetCommands(commands)
}

func (a *Adapter) buildConfigMarkup() *tele.ReplyMarkup {
	markup := &tele.ReplyMarkup{}
	rows := make([][]tele.InlineButton, 0, len(a.app.ConfigOptions()))
	for _, option := range a.app.ConfigOptions() {
		rows = append(rows, []tele.InlineButton{{Unique: "cfg", Text: option.Display, Data: option.Key}})
	}
	markup.InlineKeyboard = rows
	return markup
}

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

func markupIfFirst(markup *tele.ReplyMarkup, index int) *tele.ReplyMarkup {
	if index == 0 {
		return markup
	}
	return nil
}

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

func isConfigCommand(text string) bool {
	text = strings.TrimSpace(text)
	return text == "/config" || strings.HasPrefix(text, "/config@")
}
