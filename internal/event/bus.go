package event

import (
	"context"
	"sync"
)

type Event struct {
	Type    string
	Source  string
	Payload map[string]any
}

type Handler func(ctx context.Context, evt Event)

type Bus struct {
	mu          sync.RWMutex
	subscribers map[string][]Handler
}

func NewBus() *Bus {
	return &Bus{subscribers: map[string][]Handler{}}
}

func (b *Bus) Subscribe(eventType string, handler Handler) {
	if handler == nil {
		return
	}
	b.mu.Lock()
	b.subscribers[eventType] = append(b.subscribers[eventType], handler)
	b.mu.Unlock()
}

func (b *Bus) Publish(ctx context.Context, evt Event) {
	b.mu.RLock()
	handlers := append([]Handler(nil), b.subscribers[evt.Type]...)
	b.mu.RUnlock()
	for _, handler := range handlers {
		go handler(ctx, evt)
	}
}
