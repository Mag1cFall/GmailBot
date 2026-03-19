package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

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
	telegramplatform "gmailbot/internal/platform/telegram"
	"gmailbot/internal/plugin"
	systemplugin "gmailbot/internal/plugins/system"
	websearchplugin "gmailbot/internal/plugins/websearch"
	"gmailbot/internal/store"
	"gmailbot/internal/tgbot"
	"gmailbot/internal/webhook"
)

func main() {
	logging.Init()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg := config.Load()
	st, err := store.Init(cfg.DBPath)
	if err != nil {
		fatal("init sqlite failed", "error", err)
	}
	defer st.Close()

	bus := event.NewBus()
	registry := agentpkg.NewToolRegistry()
	gmailService := gmail.NewService(cfg, st)
	pluginMgr := plugin.NewManager(registry, bus, map[string]any{"store": st})
	if err := pluginMgr.Register(gmail.NewPlugin(gmailService)); err != nil {
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

	agent := agentpkg.NewAgent(cfg, registry, st)
	personaMgr := persona.NewManager(st, cfg.DefaultPersona)
	agent.SetPersonaManager(personaMgr)
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

	app, err := tgbot.NewApp(cfg, st, gmailService, agent, memStore)
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
	bus.Subscribe("reminder.due", func(ctx context.Context, evt event.Event) {
		content, _ := evt.Payload["content"].(string)
		reminderID, _ := evt.Payload["id"].(string)
		userID, _ := evt.Payload["user_id"].(string)
		if strings.TrimSpace(userID) == "" {
			return
		}
		if err := telegramAdapter.Send(ctx, userID, platform.UnifiedResponse{Text: "⏰ 提醒\n\n" + content, Markdown: true}); err == nil && strings.TrimSpace(reminderID) != "" {
			_ = st.MarkReminderSent(context.Background(), reminderID)
		}
	})
	scheduler := tgbot.NewScheduler(st, gmailService, agent, func(ctx context.Context, platformName, userID string, resp platform.UnifiedResponse) error {
		return telegramAdapter.Send(ctx, userID, resp)
	})
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
	}

	handler := func(ctx context.Context, msg platform.UnifiedMessage) (platform.UnifiedResponse, error) {
		metrics.Default.MessagesTotal.Add(1)
		metrics.Default.MarkActiveUser(msg.Platform + ":" + msg.UserID)
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
		resp, err := app.HandleMessage(ctx, msg)
		if err != nil {
			metrics.Default.ErrorsTotal.Add(1)
		}
		if err == nil {
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

	if err := telegramAdapter.Start(ctx, handler); err != nil {
		fatal("adapter exited with error", "error", err)
	}
}

func fatal(message string, args ...any) {
	slog.Error(message, args...)
	os.Exit(1)
}
