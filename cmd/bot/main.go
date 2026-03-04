package main

import (
	"context"
	"log"

	"gmailbot/config"
	"gmailbot/internal/ai"
	"gmailbot/internal/gmail"
	"gmailbot/internal/store"
	"gmailbot/internal/tgbot"
)

func main() {
	cfg := config.Load()

	st, err := store.Init(cfg.DBPath)
	if err != nil {
		log.Fatalf("init sqlite failed: %v", err)
	}
	defer st.Close()

	gmailService := gmail.NewService(cfg, st)
	agent := ai.NewAgent(cfg, gmailService, st)

	app, err := tgbot.NewApp(cfg, st, gmailService, agent)
	if err != nil {
		log.Fatalf("init telegram app failed: %v", err)
	}
	if err = app.Start(context.Background()); err != nil {
		log.Fatalf("app exited with error: %v", err)
	}
}
