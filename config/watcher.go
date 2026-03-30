// 配置文件热重载监听
package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ReloadCallback 配置重载回调
type ReloadCallback func(Config)

// Watcher 监听 .env 文件变化并触发重载
type Watcher struct {
	mu        sync.Mutex
	callbacks []ReloadCallback
	stopChan  chan struct{}
	stopOnce  sync.Once
	debounce  time.Duration
	lastMod   time.Time
	envPath   string
	pollEvery time.Duration
}

// NewWatcher 创建配置监听器
func NewWatcher(debounceMS int) *Watcher {
	if debounceMS <= 0 {
		debounceMS = 800
	}
	envPath, _ := filepath.Abs(".env")
	return &Watcher{
		stopChan:  make(chan struct{}),
		debounce:  time.Duration(debounceMS) * time.Millisecond,
		envPath:   envPath,
		pollEvery: 2 * time.Second,
	}
}

// OnReload 注册重载回调
func (w *Watcher) OnReload(cb ReloadCallback) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.callbacks = append(w.callbacks, cb)
}

// Start 启动文件轮询
func (w *Watcher) Start() {
	info, err := os.Stat(w.envPath)
	if err == nil {
		w.lastMod = info.ModTime()
	}

	go w.pollLoop()
	slog.Info("config watcher started", "path", w.envPath)
}

// Stop 停止监听
func (w *Watcher) Stop() {
	w.stopOnce.Do(func() {
		close(w.stopChan)
	})
}

// pollLoop 定时轮询 .env 修改时间，有变化则防抖后触发重载
func (w *Watcher) pollLoop() {
	interval := w.pollEvery
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-w.stopChan:
			return
		case <-ticker.C:
			info, err := os.Stat(w.envPath)
			if err != nil {
				continue
			}
			if info.ModTime().After(w.lastMod) {
				w.lastMod = info.ModTime()
				time.Sleep(w.debounce)
				w.fireReload()
			}
		}
	}
}

// fireReload 重新加载配置并依次调用所有注册的回调
func (w *Watcher) fireReload() {
	cfg := loadFromPath(w.envPath)
	w.mu.Lock()
	cbs := make([]ReloadCallback, len(w.callbacks))
	copy(cbs, w.callbacks)
	w.mu.Unlock()

	slog.Info("config reloaded", "config", cfg.String())
	for _, cb := range cbs {
		cb(cfg)
	}
}
