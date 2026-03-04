package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	BotToken           string
	GoogleClientID     string
	GoogleClientSecret string
	OAuthRedirectURL   string
	AIBaseURL          string
	AIAPIKey           string
	AIModel            string
	DBPath             string
	TelegramTimeoutSec int
	AITimeoutSec       int
}

func Load() Config {
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file, reading from environment")
	}
	cfg := Config{
		BotToken:           mustGet("BOT_TOKEN"),
		GoogleClientID:     mustGet("GOOGLE_CLIENT_ID"),
		GoogleClientSecret: mustGet("GOOGLE_CLIENT_SECRET"),
		OAuthRedirectURL:   getOrDefault("OAUTH_REDIRECT_URL", "http://localhost"),
		AIBaseURL:          mustGet("AI_BASE_URL"),
		AIAPIKey:           mustGet("AI_API_KEY"),
		AIModel:            mustGet("AI_MODEL"),
		DBPath:             getOrDefault("DB_PATH", "./data/gmailbot.db"),
		TelegramTimeoutSec: getIntOrDefault("TELEGRAM_TIMEOUT_SEC", 10),
		AITimeoutSec:       getIntOrDefault("AI_TIMEOUT_SEC", 90),
	}
	if cfg.TelegramTimeoutSec <= 0 {
		cfg.TelegramTimeoutSec = 10
	}
	if cfg.AITimeoutSec <= 0 {
		cfg.AITimeoutSec = int((90 * time.Second).Seconds())
	}
	return cfg
}

func mustGet(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("missing required env: %s", key)
	}
	return v
}

func getOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func getIntOrDefault(key string, def int) int {
	raw := os.Getenv(key)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		log.Printf("invalid integer env %s=%q, fallback to %d", key, raw, def)
		return def
	}
	return v
}

func (c Config) String() string {
	return fmt.Sprintf(
		"db=%s ai_model=%s oauth_redirect=%s",
		c.DBPath,
		c.AIModel,
		c.OAuthRedirectURL,
	)
}

var EditableKeys = []string{
	"AI_MODEL",
	"AI_BASE_URL",
	"AI_API_KEY",
	"AI_TIMEOUT_SEC",
	"TELEGRAM_TIMEOUT_SEC",
}

func UpdateEnvFile(key, value string) error {
	data, err := os.ReadFile(".env")
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		parts := strings.SplitN(trimmed, "=", 2)
		if len(parts) == 2 && parts[0] == key {
			lines[i] = key + "=" + value
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, key+"="+value)
	}
	os.Setenv(key, value)
	return os.WriteFile(".env", []byte(strings.Join(lines, "\n")), 0644)
}
