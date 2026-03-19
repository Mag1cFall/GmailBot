package event

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBusPublishDeliversToSubscribers(t *testing.T) {
	bus := NewBus()
	var wg sync.WaitGroup
	wg.Add(2)

	results := make(chan string, 2)
	handler := func(ctx context.Context, evt Event) {
		defer wg.Done()
		value, _ := evt.Payload["value"].(string)
		results <- value
	}

	bus.Subscribe("message.received", handler)
	bus.Subscribe("message.received", handler)
	bus.Publish(context.Background(), Event{
		Type:   "message.received",
		Source: "telegram",
		Payload: map[string]any{
			"value": "ok",
		},
	})

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected publish to reach all subscribers")
	}

	close(results)
	count := 0
	for result := range results {
		if result != "ok" {
			t.Fatalf("unexpected payload: %q", result)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("expected 2 deliveries, got %d", count)
	}
}

func TestBusPublishIgnoresUnsubscribedEventTypes(t *testing.T) {
	bus := NewBus()
	called := make(chan struct{}, 1)
	bus.Subscribe("message.received", func(ctx context.Context, evt Event) {
		called <- struct{}{}
	})

	bus.Publish(context.Background(), Event{Type: "message.sent", Source: "telegram"})

	select {
	case <-called:
		t.Fatal("unexpected subscriber call")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestBusPublishConcurrentDeliversEveryEvent(t *testing.T) {
	bus := NewBus()
	const subscriberCount = 4
	const publishCount = 20
	var wg sync.WaitGroup
	wg.Add(subscriberCount * publishCount)
	results := make(chan int, subscriberCount*publishCount)

	for i := 0; i < subscriberCount; i++ {
		bus.Subscribe("message.concurrent", func(ctx context.Context, evt Event) {
			defer wg.Done()
			value, _ := evt.Payload["index"].(int)
			results <- value
		})
	}

	for i := 0; i < publishCount; i++ {
		bus.Publish(context.Background(), Event{Type: "message.concurrent", Payload: map[string]any{"index": i}})
	}

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected concurrent publishes to reach all subscribers")
	}
	close(results)
	if len(results) != subscriberCount*publishCount {
		t.Fatalf("unexpected delivery count: %d", len(results))
	}
}
