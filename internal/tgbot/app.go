package tgbot

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"gmailbot/config"
	agentpkg "gmailbot/internal/agent"
	"gmailbot/internal/gmail"
	"gmailbot/internal/memory"
	"gmailbot/internal/persona"
	"gmailbot/internal/pipeline"
	"gmailbot/internal/platform"
	"gmailbot/internal/plugin"
	"gmailbot/internal/store"
)

type ConfigOption struct {
	Key     string
	Display string
}

type App struct {
	mu            sync.Mutex
	cfg           config.Config
	store         *store.Store
	gmail         *gmail.Service
	ai            *agentpkg.Agent
	memory        *memory.Store
	pipeline      *pipeline.Pipeline
	router        *platform.CommandRouter
	personaMgr    *persona.Manager
	pendingConfig map[string]string
}

func NewApp(cfg config.Config, st *store.Store, gmailService *gmail.Service, agent *agentpkg.Agent, memStore *memory.Store) (*App, error) {
	app := &App{
		cfg:           cfg,
		store:         st,
		gmail:         gmailService,
		ai:            agent,
		memory:        memStore,
		router:        platform.NewCommandRouter(),
		pendingConfig: map[string]string{},
	}
	app.setupPipeline()
	if err := app.registerHandlers(); err != nil {
		return nil, err
	}
	return app, nil
}

func (a *App) setupPipeline() {
	p := pipeline.New()
	p.AddStage(&pipeline.AuthCheckStage{
		CheckFunc: func(ctx context.Context, msg platform.UnifiedMessage) error {
			authorized, err := a.store.IsUserAuthorizedByIdentity(ctx, msg.Platform, msg.UserID)
			if err != nil {
				return err
			}
			if !authorized {
				return errors.New("请先执行 /auth 绑定邮箱后再使用 AI 对话。")
			}
			return nil
		},
	})
	if a.cfg.MessageRateLimitPerMin > 0 {
		p.AddStage(pipeline.NewRateLimitStage(a.cfg.MessageRateLimitPerMin))
	}
	p.AddStage(&pipeline.AIProcessStage{
		HandleFunc: func(ctx context.Context, msg platform.UnifiedMessage) (platform.UnifiedResponse, error) {
			return a.ai.HandleMessage(ctx, msg)
		},
	})
	safetyPatterns := collectSensitivePatterns(a.cfg)
	if len(safetyPatterns) > 0 {
		p.AddStage(&pipeline.SafetyFilterStage{Patterns: safetyPatterns})
	}
	a.pipeline = p
}

func (a *App) HandleMessage(ctx context.Context, msg platform.UnifiedMessage) (platform.UnifiedResponse, error) {
	msg.Text = strings.TrimSpace(msg.Text)
	if msg.Text == "" {
		return platform.UnifiedResponse{}, nil
	}
	if strings.TrimSpace(msg.Platform) == "" {
		msg.Platform = "telegram"
	}

	if !strings.HasPrefix(msg.Text, "/") {
		if resp, handled, err := a.handlePendingConfig(msg); handled || err != nil {
			return resp, err
		}
	}

	resp, handled, err := a.router.Handle(ctx, msg)
	if err != nil {
		return platform.UnifiedResponse{}, err
	}
	if handled {
		return resp, nil
	}
	if strings.HasPrefix(msg.Text, "/") {
		return platform.UnifiedResponse{}, nil
	}

	evt := &pipeline.Event{
		Message: msg,
		Extra:   map[string]any{},
	}
	if err := a.pipeline.Execute(ctx, evt); err != nil {
		return platform.UnifiedResponse{}, err
	}
	if evt.Aborted {
		return platform.UnifiedResponse{Text: evt.AbortMsg}, nil
	}
	if a.memory != nil {
		if userKey, err := a.resolveUserKey(context.Background(), msg); err == nil {
			go a.memory.SaveSessionTranscript(userKey, "active", "user", msg.Text)
			go a.memory.SaveSessionTranscript(userKey, "active", "assistant", evt.Response.Text)
		}
	}
	return evt.Response, nil
}

func (a *App) Commands() []platform.Command {
	return a.router.Commands()
}

func (a *App) Reload(cfg config.Config) {
	a.cfg = cfg
	a.setupPipeline()
}

func (a *App) SetPersonaManager(manager *persona.Manager) {
	a.personaMgr = manager
}

func (a *App) RegisterPluginCommands(commands []plugin.Command) error {
	for _, command := range commands {
		commandCopy := command
		if err := a.router.Register(platform.Command{
			Name:        commandCopy.Name,
			Description: commandCopy.Description,
			Handler: func(ctx context.Context, msg platform.UnifiedMessage, args []string) (platform.UnifiedResponse, error) {
				text, err := commandCopy.Handler(ctx, args)
				if err != nil {
					return platform.UnifiedResponse{}, err
				}
				return platform.UnifiedResponse{Text: text, Markdown: true}, nil
			},
		}); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) ConfigOptions() []ConfigOption {
	options := make([]ConfigOption, 0, len(config.EditableKeys))
	for _, key := range config.EditableKeys {
		val := os.Getenv(key)
		display := key + ": " + val
		if len(display) > 42 {
			display = key + ": " + val[:32] + "..."
		}
		options = append(options, ConfigOption{Key: key, Display: display})
	}
	return options
}

func (a *App) HandleConfigSelection(msg platform.UnifiedMessage, key string) (platform.UnifiedResponse, error) {
	valid := false
	for _, candidate := range config.EditableKeys {
		if candidate == key {
			valid = true
			break
		}
	}
	if !valid {
		return platform.UnifiedResponse{Text: "无效的配置项"}, nil
	}
	a.mu.Lock()
	a.pendingConfig[a.identityKey(msg)] = key
	a.mu.Unlock()
	currentVal := os.Getenv(key)
	return platform.UnifiedResponse{
		Text:     fmt.Sprintf("🔧 *%s*\n当前值: `%s`\n\n请发送新的值：", key, currentVal),
		Markdown: true,
	}, nil
}

func (a *App) handlePendingConfig(msg platform.UnifiedMessage) (platform.UnifiedResponse, bool, error) {
	identity := a.identityKey(msg)
	a.mu.Lock()
	key, ok := a.pendingConfig[identity]
	if ok {
		delete(a.pendingConfig, identity)
	}
	a.mu.Unlock()
	if !ok {
		return platform.UnifiedResponse{}, false, nil
	}
	resp, err := a.applyConfigValue(msg, key, msg.Text)
	return resp, true, err
}

func (a *App) applyConfigValue(msg platform.UnifiedMessage, key, value string) (platform.UnifiedResponse, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return platform.UnifiedResponse{Text: "❌ 值不能为空，操作取消。"}, nil
	}
	if err := config.UpdateEnvFile(key, value); err != nil {
		return platform.UnifiedResponse{Text: "❌ 写入 .env 失败：" + err.Error()}, nil
	}
	newCfg := config.Load()
	a.ai.Reload(newCfg)
	a.cfg = newCfg
	a.setupPipeline()
	return platform.UnifiedResponse{
		Text:     fmt.Sprintf("✅ %s 已更新为: `%s`\n配置已热重载生效。", key, value),
		Markdown: true,
	}, nil
}

func (a *App) identityKey(msg platform.UnifiedMessage) string {
	platformName := strings.TrimSpace(msg.Platform)
	if platformName == "" {
		platformName = "telegram"
	}
	return platformName + ":" + strings.TrimSpace(msg.UserID)
}

func (a *App) resolveUserKey(ctx context.Context, msg platform.UnifiedMessage) (int64, error) {
	platformName := strings.TrimSpace(msg.Platform)
	if platformName == "" {
		platformName = "telegram"
	}
	return a.store.ResolvePlatformUserKey(ctx, platformName, msg.UserID)
}

func (a *App) shortCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

func (a *App) aiCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(a.cfg.AITimeoutSec)*time.Second)
}

func commandArgs(text string) []string {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

func parseBoundedInt(args []string, def, min, max int) int {
	if len(args) == 0 {
		return def
	}
	v, err := strconv.Atoi(strings.TrimSpace(args[0]))
	if err != nil {
		return def
	}
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}

func trimForDisplay(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func formatEmailList(title string, emails []gmail.EmailSummary) string {
	lines := []string{title + "："}
	for i, item := range emails {
		lines = append(lines,
			fmt.Sprintf(
				"%d) %s\nFrom: %s\nDate: %s\nID: %s\nSnippet: %s",
				i+1,
				item.Subject,
				item.From,
				item.Date,
				item.ID,
				trimForDisplay(item.Snippet, 200),
			),
		)
	}
	lines = append(lines, "\n使用 /read <ID> 查看正文")
	return strings.Join(lines, "\n\n")
}

func collectSensitivePatterns(cfg config.Config) []string {
	var patterns []string
	seen := map[string]struct{}{}
	add := func(value string) {
		value = strings.TrimSpace(value)
		if len(value) <= 8 {
			return
		}
		if _, ok := seen[value]; ok {
			return
		}
		seen[value] = struct{}{}
		patterns = append(patterns, value)
	}
	add(cfg.AIAPIKey)
	add(cfg.BotToken)
	add(cfg.GoogleClientSecret)
	add(cfg.WebhookSecret)
	add(cfg.DashboardAuth)
	for _, provider := range cfg.AIFallbackProviders {
		add(provider.APIKey)
	}
	for _, entry := range os.Environ() {
		parts := strings.SplitN(entry, "=", 2)
		if len(parts) != 2 || !isSensitiveEnvKey(parts[0]) {
			continue
		}
		add(parts[1])
	}
	return patterns
}

func isSensitiveEnvKey(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "AUTH", "PASSWORD"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}
