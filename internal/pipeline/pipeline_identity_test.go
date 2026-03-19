package pipeline

import (
	"context"
	"testing"

	"gmailbot/internal/platform"
)

func TestRateLimitStageUsesPlatformAwareIdentity(t *testing.T) {
	stage := NewRateLimitStage(1)
	first := &Event{Message: platform.UnifiedMessage{Platform: "telegram", UserID: "1", Text: "hello"}}
	second := &Event{Message: platform.UnifiedMessage{Platform: "telegram", UserID: "1", Text: "again"}}
	third := &Event{Message: platform.UnifiedMessage{Platform: "discord", UserID: "1", Text: "allowed"}}

	ctx := context.WithValue(context.Background(), ctxKeyNow{}, int64(100))
	if err := stage.Process(ctx, first, func(context.Context, *Event) error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := stage.Process(ctx, second, func(context.Context, *Event) error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !second.Aborted {
		t.Fatal("expected second message to be rate limited")
	}
	if err := stage.Process(ctx, third, func(context.Context, *Event) error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if third.Aborted {
		t.Fatal("expected different platform identity to bypass first identity limit")
	}
}

func TestAIProcessStageSetsUnifiedResponse(t *testing.T) {
	stage := &AIProcessStage{
		HandleFunc: func(ctx context.Context, msg platform.UnifiedMessage) (platform.UnifiedResponse, error) {
			if msg.UserID != "42" {
				t.Fatalf("unexpected user id: %s", msg.UserID)
			}
			return platform.UnifiedResponse{Text: "ok", Markdown: true}, nil
		},
	}
	evt := &Event{Message: platform.UnifiedMessage{Platform: "telegram", UserID: "42", Text: "hi"}}

	if err := stage.Process(context.Background(), evt, func(context.Context, *Event) error { return nil }); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Response.Text != "ok" || !evt.Response.Markdown {
		t.Fatalf("unexpected response: %#v", evt.Response)
	}
}
