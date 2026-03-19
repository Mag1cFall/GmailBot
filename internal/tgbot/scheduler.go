package tgbot

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"gmailbot/internal/gmail"
	"gmailbot/internal/platform"
	"gmailbot/internal/store"

	"github.com/robfig/cron/v3"
)

type SendFunc func(ctx context.Context, platformName, userID string, resp platform.UnifiedResponse) error

type schedulerMailService interface {
	ListEmails(ctx context.Context, tgUserID int64, n int, query string) ([]gmail.EmailSummary, error)
}

type schedulerAgent interface {
	JudgeEmailImportance(ctx context.Context, tgUserID int64, subject, from, snippet string) (bool, string, error)
	GenerateDailyDigest(ctx context.Context, tgUserID int64) (string, error)
}

type Scheduler struct {
	cron  *cron.Cron
	send  SendFunc
	store *store.Store
	gmail schedulerMailService
	ai    schedulerAgent

	mu          sync.Mutex
	lastCheck   map[string]time.Time
	lastDigest  map[string]string
	initialized map[string]bool
}

func NewScheduler(st *store.Store, gmailService schedulerMailService, agent schedulerAgent, send SendFunc) *Scheduler {
	c := cron.New(cron.WithSeconds(), cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)))
	s := &Scheduler{
		cron:        c,
		send:        send,
		store:       st,
		gmail:       gmailService,
		ai:          agent,
		lastCheck:   map[string]time.Time{},
		lastDigest:  map[string]string{},
		initialized: map[string]bool{},
	}
	_, _ = c.AddFunc("0 * * * * *", s.runMinuteTasks)
	_, _ = c.AddFunc("0 0 3 * * *", s.runCleanup)
	return s
}

func (s *Scheduler) Start() {
	s.cron.Start()
}

func (s *Scheduler) Stop() {
	stopCtx := s.cron.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(3 * time.Second):
	}
}

func (s *Scheduler) runMinuteTasks() {
	ctx, cancel := context.WithTimeout(context.Background(), 55*time.Second)
	defer cancel()
	users, err := s.store.ListAuthorizedUsers(ctx)
	if err != nil {
		return
	}
	now := time.Now()
	for _, user := range users {
		if s.shouldCheck(user, now) {
			s.pollNewEmails(ctx, user)
		}
		if s.shouldDigest(user, now) {
			s.pushDailyDigest(ctx, user)
		}
	}
}

func (s *Scheduler) shouldCheck(user store.User, now time.Time) bool {
	if user.CheckIntervalMin <= 0 {
		return false
	}
	identity := schedulerIdentity(user)
	s.mu.Lock()
	defer s.mu.Unlock()
	last, ok := s.lastCheck[identity]
	if !ok || now.Sub(last) >= time.Duration(user.CheckIntervalMin)*time.Minute {
		s.lastCheck[identity] = now
		return true
	}
	return false
}

func (s *Scheduler) shouldDigest(user store.User, now time.Time) bool {
	if len(user.DigestTimes) == 0 {
		return false
	}
	current := now.Format("15:04")
	matched := false
	for _, item := range user.DigestTimes {
		if current == item {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}
	key := fmt.Sprintf("%s_%s_%s", schedulerIdentity(user), now.Format("2006-01-02"), current)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastDigest[key] == "sent" {
		return false
	}
	s.lastDigest[key] = "sent"
	return true
}

func (s *Scheduler) pollNewEmails(ctx context.Context, user store.User) {
	interval := user.CheckIntervalMin
	if interval <= 0 {
		interval = 5
	}
	query := fmt.Sprintf("is:unread newer_than:%dm", interval+1)
	emails, err := s.gmail.ListEmails(ctx, user.TgUserID, 20, query)
	if err != nil {
		return
	}
	identity := schedulerIdentity(user)
	if len(emails) == 0 {
		s.setInitialized(identity)
		return
	}
	if !s.isInitialized(identity) {
		for _, item := range emails {
			_ = s.store.MarkEmailSeen(ctx, user.TgUserID, item.ID)
		}
		s.setInitialized(identity)
		return
	}
	for i := len(emails) - 1; i >= 0; i-- {
		item := emails[i]
		seen, seenErr := s.store.IsEmailSeen(ctx, user.TgUserID, item.ID)
		if seenErr != nil || seen {
			continue
		}
		if user.AIPushEnabled {
			s.pushWithAIFilter(ctx, user, item)
		} else {
			s.pushEmailNotify(context.Background(), user, item, "")
		}
		_ = s.store.MarkEmailSeen(ctx, user.TgUserID, item.ID)
	}
}

func (s *Scheduler) pushWithAIFilter(ctx context.Context, user store.User, item gmail.EmailSummary) {
	aiCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	verdict, summary, err := s.ai.JudgeEmailImportance(aiCtx, user.TgUserID, item.Subject, item.From, item.Snippet)
	if err != nil {
		s.pushEmailNotify(context.Background(), user, item, "")
		return
	}
	if !verdict {
		return
	}
	s.pushEmailNotify(context.Background(), user, item, summary)
}

func (s *Scheduler) pushEmailNotify(ctx context.Context, user store.User, item gmail.EmailSummary, aiSummary string) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📬 *新邮件*\n*主题:* %s\n*发件人:* %s\nID: `%s`", item.Subject, item.From, item.ID))
	if aiSummary != "" {
		sb.WriteString("\n\n💡 " + aiSummary)
	}
	sb.WriteString(fmt.Sprintf("\n\n/read %s", item.ID))
	if s.send != nil {
		_ = s.send(ctx, user.Platform, user.UserID, platform.UnifiedResponse{Text: sb.String(), Markdown: true})
	}
}

func (s *Scheduler) pushDailyDigest(ctx context.Context, user store.User) {
	digest, err := s.ai.GenerateDailyDigest(ctx, user.TgUserID)
	if err != nil {
		return
	}
	if s.send != nil {
		_ = s.send(ctx, user.Platform, user.UserID, platform.UnifiedResponse{Text: "🗓 *每日邮件摘要*\n\n" + digest, Markdown: true})
	}
}

func (s *Scheduler) isInitialized(identity string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized[identity]
}

func (s *Scheduler) setInitialized(identity string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized[identity] = true
}

func schedulerIdentity(user store.User) string {
	platformName := strings.TrimSpace(user.Platform)
	if platformName == "" {
		platformName = "telegram"
	}
	userID := strings.TrimSpace(user.UserID)
	if userID == "" {
		userID = fmt.Sprintf("%d", user.TgUserID)
	}
	return platformName + ":" + userID
}

func (s *Scheduler) runCleanup() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = s.store.CleanOldSeenEmails(ctx, 7)
	s.mu.Lock()
	today := time.Now().Format("2006-01-02")
	for key := range s.lastDigest {
		if !strings.Contains(key, today) {
			delete(s.lastDigest, key)
		}
	}
	s.mu.Unlock()
}
