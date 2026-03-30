// 进程内事件总线
package event

import (
	"context"
	"sync"
)

// Event 事件
type Event struct {
	Type    string
	Source  string
	Payload map[string]any
}

// Handler 事件处理函数
type Handler func(ctx context.Context, evt Event)

// Bus 事件总线，支持按类型订阅
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string][]Handler
}

// NewBus 创建事件总线
func NewBus() *Bus {
	return &Bus{subscribers: map[string][]Handler{}}
}

// Subscribe 订阅指定类型事件
func (b *Bus) Subscribe(eventType string, handler Handler) {
	if handler == nil {
		return
	}
	b.mu.Lock()
	b.subscribers[eventType] = append(b.subscribers[eventType], handler)
	b.mu.Unlock()
}

// Publish 发布事件，异步通知所有订阅者
func (b *Bus) Publish(ctx context.Context, evt Event) {
	b.mu.RLock()
	handlers := append([]Handler(nil), b.subscribers[evt.Type]...)
	b.mu.RUnlock()
	for _, handler := range handlers {
		go handler(ctx, evt)
	}
}
