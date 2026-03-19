package logging

import (
	"context"
	"log/slog"
	"os"
	"sync"
	"time"
)

type Entry struct {
	Time    time.Time      `json:"time"`
	Level   string         `json:"level"`
	Message string         `json:"message"`
	Attrs   map[string]any `json:"attrs"`
}

type RingBuffer struct {
	mu       sync.RWMutex
	entries  []Entry
	capacity int
	index    int
	filled   bool
}

type Manager struct {
	logger *slog.Logger
	buffer *RingBuffer
}

var (
	defaultManager *Manager
	once           sync.Once
)

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

func Logger() *slog.Logger {
	return Init().logger
}

func BufferEntries() []Entry {
	return Init().buffer.Entries()
}

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

type multiHandler struct {
	handlers []slog.Handler
}

func (h *multiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *multiHandler) Handle(ctx context.Context, record slog.Record) error {
	for _, handler := range h.handlers {
		if err := handler.Handle(ctx, record.Clone()); err != nil {
			return err
		}
	}
	return nil
}

func (h *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithAttrs(attrs))
	}
	return &multiHandler{handlers: handlers}
}

func (h *multiHandler) WithGroup(name string) slog.Handler {
	handlers := make([]slog.Handler, 0, len(h.handlers))
	for _, handler := range h.handlers {
		handlers = append(handlers, handler.WithGroup(name))
	}
	return &multiHandler{handlers: handlers}
}

type bufferHandler struct {
	buffer *RingBuffer
	attrs  []slog.Attr
	group  string
}

func (h *bufferHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return true
}

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

func (h *bufferHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	combined := append([]slog.Attr{}, h.attrs...)
	combined = append(combined, attrs...)
	return &bufferHandler{buffer: h.buffer, attrs: combined, group: h.group}
}

func (h *bufferHandler) WithGroup(name string) slog.Handler {
	group := name
	if h.group != "" {
		group = h.group + "." + name
	}
	return &bufferHandler{buffer: h.buffer, attrs: append([]slog.Attr{}, h.attrs...), group: group}
}

func (h *bufferHandler) attrKey(key string) string {
	if h.group == "" {
		return key
	}
	return h.group + "." + key
}
