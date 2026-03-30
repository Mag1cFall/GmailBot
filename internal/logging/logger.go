// 日志系统，支持环形缓冲区存储近期日志
package logging

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

// Entry 日志条目
type Entry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs"`
}

// RingBuffer 环形日志缓冲区
type RingBuffer struct {
	mu       sync.RWMutex
	entries  []Entry
	capacity int
	index    int
	filled   bool
}

// Manager 日志管理器
type Manager struct {
	logger *slog.Logger
	buffer *RingBuffer
}

var (
	defaultManager *Manager
	once           sync.Once
)

// Init 初始化日志管理器（单例）
func Init() *Manager {
	once.Do(func() {
		buffer := &RingBuffer{capacity: 500}
		stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})
		ringHandler := &bufferHandler{buffer: buffer}
		logger := slog.New(&multiHandler{handlers: []slog.Handler{stderrHandler, ringHandler}})
		slog.SetDefault(logger)
		defaultManager = &Manager{logger: logger, buffer: buffer}
	})
	return defaultManager
}

// Logger 获取全局 logger
func Logger() *slog.Logger {
	return Init().logger
}

// BufferEntries 获取缓冲区中的日志条目
func BufferEntries() []Entry {
	return Init().buffer.Entries()
}

// Add 将日志条目写入环形缓冲区，超出容量后覆盖最早的条目
func (b *RingBuffer) Add(entry Entry) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capacity <= 0 {
		b.capacity = 500
	}
	if len(b.entries) < b.capacity {
		b.entries = append(b.entries, entry)
		if len(b.entries) == b.capacity {
			b.index = 0
			b.filled = true
		}
		return
	}
	b.entries[b.index] = entry
	b.index = (b.index + 1) % b.capacity
	b.filled = true
}

// Entries 按时间顺序返回缓冲区内所有日志条目
func (b *RingBuffer) Entries() []Entry {
	b.mu.RLock()
	defer b.mu.RUnlock()
	if !b.filled {
		return append([]Entry(nil), b.entries...)
	}
	entries := make([]Entry, 0, len(b.entries))
	entries = append(entries, b.entries[b.index:]...)
	entries = append(entries, b.entries[:b.index]...)
	return entries
}

// multiHandler 将多个 slog.Handler 并联
type multiHandler struct {
	handlers []slog.Handler
}

// Enabled 如果任何子 handler 启用该级别则返回 true
func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

// Handle 将日志分发给所有子 handler
func (h *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if err := handler.Handle(ctx, record.Clone()); err != nil {
			return err
		}
	}
	return nil
}

// WithAttrs 克隆并向所有子 handler 添加属性
func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithAttrs(attrs))
	}
	return &multiHandler{handlers: handlers}
}

// WithGroup 克隆并向所有子 handler 设置分组
func (h *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithGroup(name))
	}
	return &multiHandler{handlers: handlers}
}

// bufferHandler 将日志写入环形内存缓冲区的 slog.Handler
type bufferHandler struct {
	buffer *RingBuffer
	attrs  []slog.Attr
	group  string
}

// Enabled 始终返回 true，接受任意级别
func (h *bufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return true
}

// Handle 将日志记录转化为 Entry 写入缓冲区
func (h *bufferHandler) Handle(ctx context.Context, record slog.Record) error {
	attrs := map[string]any{}
	for _, attr := range h.attrs {
		attrs[h.attrKey(attr.Key)] = attr.Value.Any()
	}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[h.attrKey(attr.Key)] = attr.Value.Any()
		return true
	})
	h.buffer.Add(Entry{Time: record.Time, Level: record.Level.String(), Message: record.Message, Attrs: attrs})
	return nil
}

// WithAttrs 克隆并添加属性
func (h *bufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := append([]slog.Attr{}, h.attrs...)
	combined = append(combined, attrs...)
	return &bufferHandler{buffer: h.buffer, attrs: combined, group: h.group}
}

// WithGroup 克隆并设置分组名
func (h *bufferHandler) WithGroup(name string) slog.Handler {
	group := name
	if h.group != "" {
		group = h.group + "." + name
	}
	return &bufferHandler{buffer: h.buffer, attrs: append([]slog.Attr{}, h.attrs...), group: group}
}

// attrKey 返回属性的完整键名，如果有分组则加前缀
func (h *bufferHandler) attrKey(key string) string {
	if h.group == "" {
		return key
	}
	return h.group + "." + key
}
