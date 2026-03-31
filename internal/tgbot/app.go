// Telegram Bot 应用层，组装管线和命令路由
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

// ConfigOption 可修改配置项
type ConfigOption struct {
	Key     string
	Display string
}

// App Bot 应用核心
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
	pendingDraft  *gmail.PendingStore
}

// NewApp 创建 Bot 应用并初始化命令路由和管线
func NewApp(cfg config.Config, st *store.Store, gmailService *gmail.Service, pending *gmail.PendingStore, agent *agentpkg.Agent, memStore *memory.Store) (*App, error) {
	app := &App{
		cfg:           cfg,
		store:         st,
		gmail:         gmailService,
		ai:            agent,
		memory:        memStore,
		router:        platform.NewCommandRouter(),
		pendingConfig: map[string]string{},
		pendingDraft:  pending,
	}
	app.setupPipeline()
	if err := app.registerHandlers(); err != nil {
		return nil, err
	}
	return app, nil
}

// setupPipeline 构建消息处理管线，包括认证、限流、AI 处理和安全过滤
func (a *App) setupPipeline() {
	p := pipeline.New()
	p.AddStage(&pipeline.AuthCheckStage{
		CheckFunc: func(ctx context.Context, msg platform.UnifiedMessage) error {
			if msg.Platform == "webui" {
				return nil
			}
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

// HandleMessage 处理入站消息，先走命令路由再走管线
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
	return evt.Response, nil
}

// Commands 返回所有已注册命令
func (a *App) Commands() []platform.Command {
	return a.router.Commands()
}

// HandleSendDraftAction 处理发邮件草稿的用户操作：confirm/cancel/edit
// action: "confirm" 真正发送；"cancel" 取消；"edit" 提示用户输入修改内容
func (a *App) HandleSendDraftAction(ctx context.Context, tgUserID int64, action string) (platform.UnifiedResponse, error) {
	if a.pendingDraft == nil {
		return platform.UnifiedResponse{Text: "草稿系统未初始化"}, nil
	}
	switch action {
	case "confirm":
		id, err := a.pendingDraft.Confirm(ctx, a.gmail, tgUserID)
		if err != nil {
			return platform.UnifiedResponse{Text: "发送失败：" + err.Error()}, nil
		}
		return platform.UnifiedResponse{Text: fmt.Sprintf("✅ 邮件已发送（ID: %s）", id)}, nil
	case "cancel":
		a.pendingDraft.Pop(tgUserID)
		return platform.UnifiedResponse{Text: "❌ 已取消发送"}, nil
	case "edit":
		draft, ok := a.pendingDraft.Get(tgUserID)
		if !ok {
			return platform.UnifiedResponse{Text: "没有待确认的草稿，请重新描述需求"}, nil
		}
		_ = draft
		return platform.UnifiedResponse{Text: "✏️ 请告诉我需要修改什么（草稿仍保留，AI 将根据你的要求重写）"}, nil
	default:
		return platform.UnifiedResponse{Text: "未知操作"}, nil
	}
}

// HasPendingDraft 检测该 Telegram 用户是否有等待确认的邮件草稿
func (a *App) HasPendingDraft(tgUserID int64) bool {
	if a.pendingDraft == nil {
		return false
	}
	_, ok := a.pendingDraft.Get(tgUserID)
	return ok
}

// Reload 更新配置并重建管线
func (a *App) Reload(cfg config.Config) {
	a.cfg = cfg
	a.setupPipeline()
}

// SetPersonaManager 注入人设管理器
func (a *App) SetPersonaManager(manager *persona.Manager) {
	a.personaMgr = manager
}

// RegisterPluginCommands 将插件命令注册到路由器
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

// ConfigOptions 返回可编辑的配置项及当前预览展示
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

// HandleConfigSelection 记录用户将要修改的配置项并请求新値
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

// handlePendingConfig 如果用户处于配置输入状态则处理输入内容
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

// applyConfigValue 将关键値写入 .env 并触发热重载
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

// identityKey 生成用户唯一标识
func (a *App) identityKey(msg platform.UnifiedMessage) string {
	platformName := strings.TrimSpace(msg.Platform)
	if platformName == "" {
		platformName = "telegram"
	}
	return platformName + ":" + strings.TrimSpace(msg.UserID)
}

// resolveUserKey 解析用户内部 ID
func (a *App) resolveUserKey(ctx context.Context, msg platform.UnifiedMessage) (int64, error) {
	platformName := strings.TrimSpace(msg.Platform)
	if platformName == "" {
		platformName = "telegram"
	}
	return a.store.ResolvePlatformUserKey(ctx, platformName, msg.UserID)
}

// shortCtx 创建 20s 超时上下文，用于快速类操作
func (a *App) shortCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 20*time.Second)
}

// aiCtx 创建 AI 超时上下文
func (a *App) aiCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), time.Duration(a.cfg.AITimeoutSec)*time.Second)
}

// commandArgs 提取命令参数，跳过第一个命令词
func commandArgs(text string) []string {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) <= 1 {
		return nil
	}
	return parts[1:]
}

// parseBoundedInt 解析整数参数，并限制在 [min, max] 范围内
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

// trimForDisplay 截断过长文本用于展示
func trimForDisplay(text string, max int) string {
	text = strings.TrimSpace(text)
	if max <= 0 || len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

// formatEmailList 格式化邮件列表展示
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

// collectSensitivePatterns 收集敏感字符串用于安全过滤
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

// isSensitiveEnvKey 判断环境变量名是否属于敏感类
func isSensitiveEnvKey(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	for _, marker := range []string{"KEY", "TOKEN", "SECRET", "AUTH", "PASSWORD"} {
		if strings.Contains(key, marker) {
			return true
		}
	}
	return false
}
