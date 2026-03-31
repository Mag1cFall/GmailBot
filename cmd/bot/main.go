// 程序入口，初始化各组件并启动 Telegram Bot
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"gmailbot/config"
	agentpkg "gmailbot/internal/agent"
	"gmailbot/internal/dashboard"
	"gmailbot/internal/event"
	"gmailbot/internal/gmail"
	"gmailbot/internal/knowledge"
	"gmailbot/internal/logging"
	"gmailbot/internal/mcp"
	"gmailbot/internal/memory"
	"gmailbot/internal/metrics"
	"gmailbot/internal/persona"
	"gmailbot/internal/platform"
	larkplatform "gmailbot/internal/platform/lark"
	qqplatform "gmailbot/internal/platform/qq"
	telegramplatform "gmailbot/internal/platform/telegram"
	webuiplatform "gmailbot/internal/platform/webui"
	"gmailbot/internal/plugin"
	systemplugin "gmailbot/internal/plugins/system"
	websearchplugin "gmailbot/internal/plugins/websearch"
	"gmailbot/internal/store"
	"gmailbot/internal/tgbot"
	"gmailbot/internal/webhook"
)

func main() {
	logging.Init()
	// 监听 SIGINT/SIGTERM，优雅关闭
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	slog.Info("config loaded", "ai_model", cfg.AIModel, "config", cfg.String())
	st, err := store.Init(cfg.DBDSN)
	if err != nil {
		fatal("init mysql failed", "error", err)
	}
	defer st.Close()
	slog.Info("database connected")

	// 事件总线、工具注册表、Gmail 服务
	bus := event.NewBus()
	registry := agentpkg.NewToolRegistry()
	gmailService := gmail.NewService(cfg, st)
	gmailPending := gmail.NewPendingStore()
	pluginMgr := plugin.NewManager(registry, bus, map[string]any{"store": st})
	if err := pluginMgr.Register(gmail.NewPlugin(gmailService, gmailPending)); err != nil {
		fatal("register gmail plugin failed", "error", err)
	}
	memStore := memory.NewStore(cfg.MemoryRoot)
	if err := pluginMgr.Register(memory.NewPlugin(memStore)); err != nil {
		fatal("register memory plugin failed", "error", err)
	}
	kbStore := knowledge.NewStore(cfg.KnowledgeRoot)
	if err := pluginMgr.Register(knowledge.NewPlugin(kbStore)); err != nil {
		fatal("register knowledge plugin failed", "error", err)
	}
	if err := pluginMgr.Register(websearchplugin.NewPlugin()); err != nil {
		fatal("register websearch plugin failed", "error", err)
	}
	if err := pluginMgr.Register(systemplugin.NewPlugin()); err != nil {
		fatal("register system plugin failed", "error", err)
	}
	defer pluginMgr.Shutdown()

	// 初始化 AI Agent 与人设管理器
	agent := agentpkg.NewAgent(cfg, registry, st)
	personaMgr := persona.NewManager(st, cfg.DefaultPersona)
	agent.SetPersonaManager(personaMgr)
	// 注册子 Agent：邮件撰写、邮件搜索
	orchestrator := agentpkg.NewSubAgentOrchestrator(registry, agent.ProviderManager(), cfg.AIToolMaxSteps)
	orchestrator.RegisterAgent(&agentpkg.SubAgent{
		Name:         "email_writer",
		Instructions: "你是一个专业的邮件撰写助手。用户会给你邮件需求，你需要撰写邮件并通过工具发送。使用简体中文。",
		ToolNames:    []string{"send_email", "reply_email", "forward_email", "list_emails", "get_email"},
	})
	orchestrator.RegisterAgent(&agentpkg.SubAgent{
		Name:         "email_searcher",
		Instructions: "你是一个邮件搜索专家。用户会给你搜索需求，你需要通过工具搜索邮件并给出结果摘要。使用简体中文。",
		ToolNames:    []string{"list_emails", "get_email", "get_labels", "summarize_emails"},
	})
	orchestrator.RegisterHandoffTools(registry)
	if strings.TrimSpace(cfg.MCPServers) != "" {
		mcpManager, err := mcp.NewManager(cfg.MCPServers, registry)
		if err != nil {
			fatal("init mcp manager failed", "error", err)
		}
		if err := mcpManager.Start(ctx); err != nil {
			fatal("start mcp manager failed", "error", err)
		}
		defer mcpManager.Shutdown()
	}

	app, err := tgbot.NewApp(cfg, st, gmailService, gmailPending, agent, memStore)
	if err != nil {
		fatal("init telegram service failed", "error", err)
	}
	app.SetPersonaManager(personaMgr)
	if err := app.RegisterPluginCommands(pluginMgr.Commands()); err != nil {
		fatal("register plugin commands failed", "error", err)
	}
	telegramAdapter, err := telegramplatform.NewAdapter(cfg, app)
	if err != nil {
		fatal("init telegram adapter failed", "error", err)
	}
	slog.Info("telegram adapter ready")
	adapters := []platform.Adapter{telegramAdapter}
	adapterByName := map[string]platform.Adapter{telegramAdapter.Name(): telegramAdapter}
	if strings.TrimSpace(cfg.WebUIAddr) != "" {
		adapter := webuiplatform.NewAdapter(cfg.WebUIAddr, st, cfg.DashboardAuth, app)
		adapters = append(adapters, adapter)
		adapterByName[adapter.Name()] = adapter
		slog.Info("webui adapter registered", "addr", cfg.WebUIAddr)
	}
	if strings.TrimSpace(cfg.LarkAppID) != "" && strings.TrimSpace(cfg.LarkAppSecret) != "" {
		adapter := larkplatform.NewAdapter(cfg.LarkAppID, cfg.LarkAppSecret, cfg.LarkBotName)
		adapters = append(adapters, adapter)
		adapterByName[adapter.Name()] = adapter
	}
	if strings.TrimSpace(cfg.QQAppID) != "" && strings.TrimSpace(cfg.QQSecret) != "" {
		adapter := qqplatform.NewAdapter(cfg.QQAppID, cfg.QQSecret, cfg.QQEnableGroup)
		adapters = append(adapters, adapter)
		adapterByName[adapter.Name()] = adapter
	}
	// sendResponse 根据平台名路由到对应 Adapter 发送响应
	sendResponse := func(ctx context.Context, platformName, userID string, resp platform.UnifiedResponse) error {
		platformName = strings.TrimSpace(platformName)
		if platformName == "" {
			platformName = telegramAdapter.Name()
		}
		adapter, ok := adapterByName[platformName]
		if !ok {
			return fmt.Errorf("adapter %s not registered", platformName)
		}
		return adapter.Send(ctx, userID, resp)
	}
	// 订阅提醒到期事件，发送 Telegram 通知并标记已发
	bus.Subscribe("reminder.due", func(ctx context.Context, evt event.Event) {
		content, _ := evt.Payload["content"].(string)
		reminderID, _ := evt.Payload["id"].(string)
		userID, _ := evt.Payload["user_id"].(string)
		platformName, _ := evt.Payload["platform"].(string)
		if strings.TrimSpace(userID) == "" {
			return
		}
		if err := sendResponse(ctx, platformName, userID, platform.UnifiedResponse{Text: "⏰ 提醒\n\n" + content, Markdown: true}); err == nil && strings.TrimSpace(reminderID) != "" {
			_ = st.MarkReminderSent(context.Background(), reminderID)
		}
	})
	scheduler := tgbot.NewScheduler(st, gmailService, agent, sendResponse)
	scheduler.Start()
	defer scheduler.Stop()
	if strings.TrimSpace(cfg.DashboardAddr) != "" {
		dashboardServer := dashboard.NewServer(cfg.DashboardAddr, cfg.DashboardAuth, pluginMgr, registry, st, agent.ProviderManager(), metrics.Default, func(newCfg config.Config) {
			agent.Reload(newCfg)
			app.Reload(newCfg)
		})
		if err := dashboardServer.Start(); err != nil {
			fatal("start dashboard failed", "error", err)
		}
		defer dashboardServer.Stop(context.Background())
		slog.Info("dashboard started", "addr", cfg.DashboardAddr)
	}

	// 消息处理器：更新指标、发布事件、调用 App 处理
	handler := func(ctx context.Context, msg platform.UnifiedMessage) (platform.UnifiedResponse, error) {
		metrics.Default.MessagesTotal.Add(1)
		metrics.Default.MarkActiveUser(msg.Platform + ":" + msg.UserID)
		textPreview := msg.Text
		if len(textPreview) > 60 {
			textPreview = textPreview[:60] + "..."
		}
		slog.Info("message received", "platform", msg.Platform, "user", msg.UserID, "text", textPreview)
		bus.Publish(ctx, event.Event{
			Type:   "message.received",
			Source: msg.Platform,
			Payload: map[string]any{
				"platform":   msg.Platform,
				"user_id":    msg.UserID,
				"session_id": msg.SessionID,
				"text":       msg.Text,
			},
		})
		start := time.Now()
		resp, err := app.HandleMessage(ctx, msg)
		elapsed := time.Since(start)
		if err != nil {
			metrics.Default.ErrorsTotal.Add(1)
			slog.Error("message handling failed", "platform", msg.Platform, "user", msg.UserID, "elapsed", elapsed, "error", err)
		} else {
			respPreview := resp.Text
			if len(respPreview) > 80 {
				respPreview = respPreview[:80] + "..."
			}
			slog.Info("message handled", "platform", msg.Platform, "user", msg.UserID, "elapsed", elapsed, "reply_len", len(resp.Text))
			bus.Publish(ctx, event.Event{
				Type:   "message.responded",
				Source: msg.Platform,
				Payload: map[string]any{
					"platform":   msg.Platform,
					"user_id":    msg.UserID,
					"session_id": msg.SessionID,
					"text":       resp.Text,
				},
			})
		}
		return resp, err
	}
	// 启动 telegram 以外的所有 Adapter
	for _, adapter := range adapters[1:] {
		adapter := adapter
		go func() {
			if err := adapter.Start(ctx, handler); err != nil {
				slog.Error("adapter exited with error", "adapter", adapter.Name(), "error", err)
			}
		}()
		defer adapter.Stop()
	}

	var webhookServer *webhook.Server
	if cfg.WebhookAddr != "" {
		webhookServer = webhook.NewServer(cfg.WebhookAddr, cfg.WebhookSecret)
		if err := webhookServer.Start(); err != nil {
			slog.Error("webhook server start failed", "error", err)
		}
		defer webhookServer.Stop()
	}

	if cfg.ConfigWatchEnabled {
		watcher := config.NewWatcher(cfg.ConfigWatchDebounceMS)
		watcher.OnReload(func(newCfg config.Config) {
			agent.Reload(newCfg)
			app.Reload(newCfg)
			bus.Publish(context.Background(), event.Event{Type: "config.reloaded", Source: "config", Payload: map[string]any{"config": newCfg.String()}})
		})
		watcher.Start()
		defer watcher.Stop()
	}

	slog.Info("all components initialized, starting telegram polling...")
	if err := telegramAdapter.Start(ctx, handler); err != nil {
		fatal("adapter exited with error", "error", err)
	}
}

// fatal 打印错误日志并退出
func fatal(message string, args ...any) {
	slog.Error(message, args...)
	os.Exit(1)
}
