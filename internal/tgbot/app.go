package tgbot

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gmailbot/config"
	"gmailbot/internal/ai"
	"gmailbot/internal/gmail"
	"gmailbot/internal/store"

	tele "gopkg.in/telebot.v3"
)

type App struct {
	cfg       config.Config
	bot       *tele.Bot
	store     *store.Store
	gmail     *gmail.Service
	ai        *ai.Agent
	scheduler *Scheduler
}

func NewApp(cfg config.Config, st *store.Store, gmailService *gmail.Service, agent *ai.Agent) (*App, error) {
	bot, err := tele.NewBot(tele.Settings{
		Token:  cfg.BotToken,
		Poller: &tele.LongPoller{Timeout: time.Duration(cfg.TelegramTimeoutSec) * time.Second},
	})
	if err != nil {
		return nil, fmt.Errorf("init telegram bot failed: %w", err)
	}

	app := &App{
		cfg:   cfg,
		bot:   bot,
		store: st,
		gmail: gmailService,
		ai:    agent,
	}
	app.registerHandlers()
	app.registerCommands()
	app.scheduler = NewScheduler(bot, st, gmailService, agent)
	return app, nil
}

func (a *App) Start(ctx context.Context) error {
	log.Printf("telegram bot starting (%s)", a.cfg.String())
	a.scheduler.Start()

	stopSignal := make(chan os.Signal, 1)
	signal.Notify(stopSignal, os.Interrupt, syscall.SIGTERM)

	go func() {
		select {
		case <-ctx.Done():
			log.Println("context canceled, stopping bot")
			a.Stop()
		case <-stopSignal:
			log.Println("signal received, stopping bot")
			a.Stop()
		}
	}()

	a.bot.Start()
	return nil
}

func (a *App) Stop() {
	a.scheduler.Stop()
	a.bot.Stop()
}
