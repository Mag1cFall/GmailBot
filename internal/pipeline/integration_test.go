package pipeline

import (
	"context"
	"errors"
	"testing"

	"gmailbot/internal/platform"
)

func TestPipelineAppliesAuthRateLimitAIAndSafetyStages(t *testing.T) {
	p := New()
	p.AddStage(&AuthCheckStage{
		CheckFunc: func(ctx context.Context, msg platform.UnifiedMessage) error {
			allowed, _ := msg.Extra["authorized"].(bool)
			if !allowed {
				return errors.New("请先绑定邮箱")
			}
			return nil
		},
	})
	p.AddStage(NewRateLimitStage(1))
	p.AddStage(&AIProcessStage{
		HandleFunc: func(ctx context.Context, msg platform.UnifiedMessage) (platform.UnifiedResponse, error) {
			return platform.UnifiedResponse{Text: "结果包含 sk-test-123456"}, nil
		},
	})
	p.AddStage(&SafetyFilterStage{Patterns: []string{"sk-test-123456"}})

	unauthorized := &Event{Message: platform.UnifiedMessage{Platform: "telegram", UserID: "1", Text: "hello", Extra: map[string]any{"authorized": false}}}
	if err := p.Execute(context.Background(), unauthorized); err != nil {
		t.Fatalf("unexpected unauthorized pipeline error: %v", err)
	}
	if !unauthorized.Aborted || unauthorized.AbortMsg != "请先绑定邮箱" {
		t.Fatalf("expected auth abort, got %#v", unauthorized)
	}

	ctx := context.WithValue(context.Background(), ctxKeyNow{}, int64(100))
	authorized := &Event{Message: platform.UnifiedMessage{Platform: "telegram", UserID: "1", Text: "hello", Extra: map[string]any{"authorized": true}}}
	if err := p.Execute(ctx, authorized); err != nil {
		t.Fatalf("unexpected authorized pipeline error: %v", err)
	}
	if authorized.Aborted {
		t.Fatalf("expected authorized request to continue, got %#v", authorized)
	}
	if authorized.Response.Text != "结果包含 **************" {
		t.Fatalf("expected redacted response, got %q", authorized.Response.Text)
	}

	rateLimited := &Event{Message: platform.UnifiedMessage{Platform: "telegram", UserID: "1", Text: "again", Extra: map[string]any{"authorized": true}}}
	if err := p.Execute(ctx, rateLimited); err != nil {
		t.Fatalf("unexpected rate limited pipeline error: %v", err)
	}
	if !rateLimited.Aborted || rateLimited.AbortMsg != "消息频率过高，请稍后再试。" {
		t.Fatalf("expected rate limit abort, got %#v", rateLimited)
	}
}

func TestSafetyFilterStageMasksAllSensitiveMatches(t *testing.T) {
	stage := &SafetyFilterStage{Patterns: []string{"Token-ABC123", "secret-value"}}
	evt := &Event{}
	if err := stage.Process(context.Background(), evt, func(context.Context, *Event) error {
		evt.Response = platform.UnifiedResponse{Text: "Token-ABC123 和 secret-value 都要脱敏"}
		return nil
	}); err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if evt.Response.Text != "************ 和 ************ 都要脱敏" {
		t.Fatalf("unexpected redacted text: %q", evt.Response.Text)
	}
}
