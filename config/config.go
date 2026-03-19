package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type AIProviderConfig struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

type Config struct {
	BotToken               string
	GoogleClientID         string
	GoogleClientSecret     string
	OAuthRedirectURL       string
	AIBaseURL              string
	AIAPIKey               string
	AIModel                string
	DBPath                 string
	TelegramTimeoutSec     int
	AITimeoutSec           int
	AIProviderType         string
	AIFallbackProviders    []AIProviderConfig
	AIToolMaxSteps         int
	MemoryRoot             string
	KnowledgeRoot          string
	MessageRateLimitPerMin int
	ConfigWatchEnabled     bool
	ConfigWatchDebounceMS  int
	WebhookAddr            string
	WebhookSecret          string
	MCPServers             string
	DefaultPersona         string
	DashboardAddr          string
	DashboardAuth          string
}

func Load() Config {
	envPath, _ := filepath.Abs(".env")
	return loadFromPath(envPath)
}

func loadFromPath(envPath string) Config {
	if err := godotenv.Overload(envPath); err != nil {
		if os.IsNotExist(err) {
			slog.Info("no .env file, reading from environment")
		} else {
			slog.Warn("load .env failed, reading from environment", "path", envPath, "error", err)
		}
	}
	fallbackProviders := parseProviderConfigs(getOrDefault("AI_FALLBACK_PROVIDERS", ""))
	cfg := Config{
		BotToken:               mustGet("BOT_TOKEN"),
		GoogleClientID:         getOrDefault("GOOGLE_CLIENT_ID", ""),
		GoogleClientSecret:     getOrDefault("GOOGLE_CLIENT_SECRET", ""),
		OAuthRedirectURL:       getOrDefault("OAUTH_REDIRECT_URL", "http://localhost"),
		AIBaseURL:              mustGet("AI_BASE_URL"),
		AIAPIKey:               mustGet("AI_API_KEY"),
		AIModel:                mustGet("AI_MODEL"),
		DBPath:                 getOrDefault("DB_PATH", "./data/gmailbot.db"),
		TelegramTimeoutSec:     getIntOrDefault("TELEGRAM_TIMEOUT_SEC", 10),
		AITimeoutSec:           getIntOrDefault("AI_TIMEOUT_SEC", 90),
		AIProviderType:         getOrDefault("AI_PROVIDER_TYPE", "openai_compat"),
		AIFallbackProviders:    fallbackProviders,
		AIToolMaxSteps:         getIntOrDefault("AI_TOOL_MAX_STEPS", 6),
		MemoryRoot:             getOrDefault("MEMORY_ROOT", "./data/memory"),
		KnowledgeRoot:          getOrDefault("KNOWLEDGE_ROOT", "./data/knowledge"),
		MessageRateLimitPerMin: getIntOrDefault("MESSAGE_RATE_LIMIT_PER_MIN", 0),
		ConfigWatchEnabled:     getBoolOrDefault("CONFIG_WATCH_ENABLED", true),
		ConfigWatchDebounceMS:  getIntOrDefault("CONFIG_WATCH_DEBOUNCE_MS", 800),
		WebhookAddr:            getOrDefault("WEBHOOK_ADDR", ""),
		WebhookSecret:          getOrDefault("WEBHOOK_SECRET", ""),
		MCPServers:             getOrDefault("MCP_SERVERS", ""),
		DefaultPersona:         getOrDefault("DEFAULT_PERSONA", ""),
		DashboardAddr:          getOrDefault("DASHBOARD_ADDR", ""),
		DashboardAuth:          getOrDefault("DASHBOARD_AUTH", ""),
	}
	if cfg.TelegramTimeoutSec <= 0 {
		cfg.TelegramTimeoutSec = 10
	}
	if cfg.AITimeoutSec <= 0 {
		cfg.AITimeoutSec = int((90 * time.Second).Seconds())
	}
	if cfg.AIToolMaxSteps <= 0 {
		cfg.AIToolMaxSteps = 6
	}
	if cfg.ConfigWatchDebounceMS <= 0 {
		cfg.ConfigWatchDebounceMS = 800
	}
	return cfg
}

func mustGet(key string) string {
	v := os.Getenv(key)
	if v == "" {
		slog.Error("missing required env", "key", key)
		os.Exit(1)
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
		slog.Warn("invalid integer env, using fallback", "key", key, "value", raw, "fallback", def)
		return def
	}
	return v
}

func getBoolOrDefault(key string, def bool) bool {
	raw := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if raw == "" {
		return def
	}
	switch raw {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		slog.Warn("invalid boolean env, using fallback", "key", key, "value", raw, "fallback", def)
		return def
	}
}

func (c Config) String() string {
	return fmt.Sprintf(
		"db=%s ai_model=%s oauth_redirect=%s provider=%s fallbacks=%d",
		c.DBPath,
		c.AIModel,
		c.OAuthRedirectURL,
		c.AIProviderType,
		len(c.AIFallbackProviders),
	)
}

var EditableKeys = []string{
	"AI_MODEL",
	"AI_BASE_URL",
	"AI_API_KEY",
	"AI_PROVIDER_TYPE",
	"AI_FALLBACK_PROVIDERS",
	"AI_TOOL_MAX_STEPS",
	"AI_TIMEOUT_SEC",
	"TELEGRAM_TIMEOUT_SEC",
	"MEMORY_ROOT",
	"MESSAGE_RATE_LIMIT_PER_MIN",
	"WEBHOOK_ADDR",
	"DEFAULT_PERSONA",
	"MCP_SERVERS",
	"DASHBOARD_ADDR",
	"DASHBOARD_AUTH",
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

func (c Config) PrimaryProvider() AIProviderConfig {
	providerType := strings.TrimSpace(c.AIProviderType)
	if providerType == "" {
		providerType = "openai_compat"
	}
	return AIProviderConfig{
		Name:    "primary",
		Type:    providerType,
		BaseURL: strings.TrimSpace(c.AIBaseURL),
		APIKey:  strings.TrimSpace(c.AIAPIKey),
		Model:   strings.TrimSpace(c.AIModel),
	}
}

func (c Config) Providers() []AIProviderConfig {
	providers := []AIProviderConfig{c.PrimaryProvider()}
	for _, item := range c.AIFallbackProviders {
		provider := normalizeProviderConfig(item)
		if provider.Model == "" || provider.APIKey == "" {
			continue
		}
		providers = append(providers, provider)
	}
	return providers
}

func normalizeProviderConfig(item AIProviderConfig) AIProviderConfig {
	item.Name = strings.TrimSpace(item.Name)
	item.Type = strings.TrimSpace(item.Type)
	item.BaseURL = strings.TrimSpace(item.BaseURL)
	item.APIKey = strings.TrimSpace(item.APIKey)
	item.Model = strings.TrimSpace(item.Model)
	if item.Type == "" {
		item.Type = "openai_compat"
	}
	return item
}

func parseProviderConfigs(raw string) []AIProviderConfig {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var providers []AIProviderConfig
	if err := json.Unmarshal([]byte(raw), &providers); err != nil {
		slog.Warn("invalid AI_FALLBACK_PROVIDERS JSON", "error", err)
		return nil
	}
	for i := range providers {
		providers[i] = normalizeProviderConfig(providers[i])
	}
	return providers
}
