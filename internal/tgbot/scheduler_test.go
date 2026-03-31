package tgbot

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"gmailbot/internal/gmail"
	"gmailbot/internal/platform"
	"gmailbot/internal/store"
	"gmailbot/internal/testutil"
)

func TestSchedulerPollNewEmailsUsesInitializationAndAIFilter(t *testing.T) {
	st := newSchedulerStore(t)
	if err := st.SaveUserTokens(context.Background(), 42, "user@example.com", "access", "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("save tokens failed: %v", err)
	}
	if err := st.SetAIPushEnabled(context.Background(), 42, true); err != nil {
		t.Fatalf("set ai push failed: %v", err)
	}

	mail := &fakeSchedulerMailService{responses: [][]gmail.EmailSummary{{
		{ID: "mail-a", Subject: "欢迎", From: "a@example.com", Snippet: "first"},
	}, {
		{ID: "mail-b", Subject: "账单", From: "billing@example.com", Snippet: "second"},
	}}}
	ai := &fakeSchedulerAgent{judgeImportant: true, judgeSummary: "重要邮件"}
	var sent []platform.UnifiedResponse
	var mu sync.Mutex
	scheduler := NewScheduler(st, mail, ai, func(ctx context.Context, platformName, userID string, resp platform.UnifiedResponse) error {
		mu.Lock()
		defer mu.Unlock()
		sent = append(sent, resp)
		return nil
	})

	user, err := st.GetUser(context.Background(), 42)
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	scheduler.pollNewEmails(context.Background(), user)
	if len(sent) != 0 {
		t.Fatalf("expected initialization run to skip sending, got %#v", sent)
	}
	seen, err := st.IsEmailSeen(context.Background(), 42, "mail-a")
	if err != nil || !seen {
		t.Fatalf("expected first email to be marked seen, seen=%v err=%v", seen, err)
	}

	scheduler.pollNewEmails(context.Background(), user)
	if ai.judgeCalls != 1 {
		t.Fatalf("expected one AI filter call, got %d", ai.judgeCalls)
	}
	if len(sent) != 1 {
		t.Fatalf("expected one notification, got %#v", sent)
	}
	if !strings.Contains(sent[0].Text, "mail-b") || !strings.Contains(sent[0].Text, "重要邮件") {
		t.Fatalf("unexpected notification text: %q", sent[0].Text)
	}
	seen, err = st.IsEmailSeen(context.Background(), 42, "mail-b")
	if err != nil || !seen {
		t.Fatalf("expected second email to be marked seen, seen=%v err=%v", seen, err)
	}
}

func TestSchedulerPushDailyDigestSendsDigestMessage(t *testing.T) {
	st := newSchedulerStore(t)
	if err := st.SaveUserTokens(context.Background(), 7, "user@example.com", "access", "refresh", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("save tokens failed: %v", err)
	}
	ai := &fakeSchedulerAgent{digest: "今日摘要"}
	mail := &fakeSchedulerMailService{}
	var sent []platform.UnifiedResponse
	scheduler := NewScheduler(st, mail, ai, func(ctx context.Context, platformName, userID string, resp platform.UnifiedResponse) error {
		sent = append(sent, resp)
		return nil
	})

	user, err := st.GetUser(context.Background(), 7)
	if err != nil {
		t.Fatalf("get user failed: %v", err)
	}
	scheduler.pushDailyDigest(context.Background(), user)
	if ai.digestCalls != 1 {
		t.Fatalf("expected one digest call, got %d", ai.digestCalls)
	}
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "今日摘要") {
		t.Fatalf("unexpected digest push: %#v", sent)
	}
}

type fakeSchedulerMailService struct {
	mu        sync.Mutex
	responses [][]gmail.EmailSummary
	index     int
}

func (f *fakeSchedulerMailService) ListEmails(ctx context.Context, tgUserID int64, n int, query string) ([]gmail.EmailSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.index >= len(f.responses) {
		return nil, nil
	}
	items := f.responses[f.index]
	f.index++
	return append([]gmail.EmailSummary(nil), items...), nil
}

type fakeSchedulerAgent struct {
	judgeImportant bool
	judgeSummary   string
	judgeCalls     int
	digest         string
	digestCalls    int
}

func (f *fakeSchedulerAgent) JudgeEmailImportance(ctx context.Context, tgUserID int64, subject, from, snippet string) (bool, string, error) {
	f.judgeCalls++
	return f.judgeImportant, f.judgeSummary, nil
}

func (f *fakeSchedulerAgent) GenerateDailyDigest(ctx context.Context, tgUserID int64) (string, error) {
	f.digestCalls++
	return f.digest, nil
}

func newSchedulerStore(t *testing.T) *store.Store {
	t.Helper()
	return testutil.NewTestStore(t)
}
