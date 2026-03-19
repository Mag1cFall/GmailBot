package plugin

import (
	"context"
	"testing"
	"time"

	"gmailbot/internal/agent"
	"gmailbot/internal/event"
)

func TestManagerCollectsCommandsAndSubscribesEvents(t *testing.T) {
	registry := agent.NewToolRegistry()
	bus := event.NewBus()
	mgr := NewManager(registry, bus, map[string]any{"answer": 42})

	called := make(chan string, 1)
	plg := &fakePlugin{eventHandler: func(ctx context.Context, evt event.Event) {
		called <- evt.Type
	}}
	if err := mgr.Register(plg); err != nil {
		t.Fatalf("register plugin failed: %v", err)
	}
	if len(mgr.Commands()) != 1 || mgr.Commands()[0].Name != "demo" {
		t.Fatalf("unexpected commands: %#v", mgr.Commands())
	}
	bus.Publish(context.Background(), event.Event{Type: "demo.event"})
	select {
	case value := <-called:
		if value != "demo.event" {
			t.Fatalf("unexpected event type: %s", value)
		}
	case <-time.After(time.Second):
		t.Fatal("expected event handler to be called")
	}
	if plg.initValue != 42 {
		t.Fatalf("expected extra context value, got %v", plg.initValue)
	}
}

type fakePlugin struct {
	initValue    any
	eventHandler event.Handler
}

func (p *fakePlugin) Name() string        { return "fake" }
func (p *fakePlugin) Description() string { return "fake plugin" }
func (p *fakePlugin) Init(ctx *Context) error {
	p.initValue = ctx.Extra["answer"]
	return nil
}
func (p *fakePlugin) Shutdown() error { return nil }
func (p *fakePlugin) Commands() []Command {
	return []Command{{Name: "demo", Description: "demo", Handler: func(ctx context.Context, args []string) (string, error) { return "ok", nil }}}
}
func (p *fakePlugin) EventHandlers() []EventSub {
	return []EventSub{{EventType: "demo.event", Handler: p.eventHandler}}
}
