package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWatcherReloadsUpdatedEnvValues(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")
	writeEnv := func(model string) {
		content := "BOT_TOKEN=test-bot\nAI_BASE_URL=http://localhost\nAI_API_KEY=test-key\nAI_MODEL=" + model + "\n"
		if err := os.WriteFile(envPath, []byte(content), 0644); err != nil {
			t.Fatalf("write env failed: %v", err)
		}
	}
	writeEnv("model-a")

	keys := []string{"BOT_TOKEN", "AI_BASE_URL", "AI_API_KEY", "AI_MODEL"}
	restore := snapshotEnv(keys)
	defer restore()
	for _, key := range keys {
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset env %s failed: %v", key, err)
		}
	}

	watcher := NewWatcher(10)
	watcher.envPath = envPath
	watcher.pollEvery = 50 * time.Millisecond
	reloaded := make(chan Config, 1)
	watcher.OnReload(func(cfg Config) {
		reloaded <- cfg
	})
	watcher.Start()
	defer watcher.Stop()

	initial := loadFromPath(envPath)
	if initial.AIModel != "model-a" {
		t.Fatalf("expected initial model-a, got %q", initial.AIModel)
	}

	time.Sleep(100 * time.Millisecond)
	writeEnv("model-b")

	select {
	case cfg := <-reloaded:
		if cfg.AIModel != "model-b" {
			t.Fatalf("expected reloaded model-b, got %q", cfg.AIModel)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expected watcher reload callback")
	}
}

func snapshotEnv(keys []string) func() {
	values := map[string]*string{}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			copied := value
			values[key] = &copied
			continue
		}
		values[key] = nil
	}
	return func() {
		for _, key := range keys {
			if values[key] == nil {
				_ = os.Unsetenv(key)
				continue
			}
			_ = os.Setenv(key, *values[key])
		}
	}
}
