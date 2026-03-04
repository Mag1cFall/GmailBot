package tgbot

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"gmailbot/internal/ai"
	"gmailbot/internal/gmail"
	"gmailbot/internal/store"

	"github.com/robfig/cron/v3"
	tele "gopkg.in/telebot.v3"
)

type Scheduler struct {
	cron *cron.Cron
	bot  *tele.Bot

	store *store.Store
	gmail *gmail.Service
	ai    *ai.Agent

	mu          sync.Mutex
	lastCheck   map[int64]time.Time
	lastDigest  map[string]string
	initialized map[int64]bool
}

func NewScheduler(bot *tele.Bot, st *store.Store, gmailService *gmail.Service, agent *ai.Agent) *Scheduler {
	c := cron.New(
		cron.WithSeconds(),
		cron.WithChain(cron.SkipIfStillRunning(cron.DefaultLogger)),
	)
	s := &Scheduler{
		cron:        c,
		bot:         bot,
		store:       st,
		gmail:       gmailService,
		ai:          agent,
		lastCheck:   make(map[int64]time.Time),
		lastDigest:  make(map[string]string),
		initialized: make(map[int64]bool),
	}
	if _, err := c.AddFunc("0 * * * * *", s.runMinuteTasks); err != nil {
		log.Printf("register scheduler task failed: %v", err)
	}
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
		log.Printf("scheduler list users failed: %v", err)
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
	interval := user.CheckIntervalMin
	if interval <= 0 {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	last, ok := s.lastCheck[user.TgUserID]
	if !ok || now.Sub(last) >= time.Duration(interval)*time.Minute {
		s.lastCheck[user.TgUserID] = now
		return true
	}
	return false
}

// shouldDigest 支持多時間點，任一匹配且當天未發送過即觸發
func (s *Scheduler) shouldDigest(user store.User, now time.Time) bool {
	if len(user.DigestTimes) == 0 {
		return false
	}
	currentHHMM := now.Format("15:04")
	matched := false
	for _, t := range user.DigestTimes {
		if currentHHMM == t {
			matched = true
			break
		}
	}
	if !matched {
		return false
	}

	// key = userID + 時間點，防止同一時間點當天重複觸發
	key := fmt.Sprintf("%d_%s_%s", user.TgUserID, now.Format("2006-01-02"), currentHHMM)
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
		log.Printf("poll unread failed user=%d err=%v", user.TgUserID, err)
		return
	}
	if len(emails) == 0 {
		s.setInitialized(user.TgUserID)
		return
	}

	if !s.isInitialized(user.TgUserID) {
		for _, item := range emails {
			if markErr := s.store.MarkEmailSeen(ctx, user.TgUserID, item.ID); markErr != nil {
				log.Printf("mark baseline seen failed user=%d id=%s err=%v", user.TgUserID, item.ID, markErr)
			}
		}
		s.setInitialized(user.TgUserID)
		return
	}

	for i := len(emails) - 1; i >= 0; i-- {
		item := emails[i]
		seen, seenErr := s.store.IsEmailSeen(ctx, user.TgUserID, item.ID)
		if seenErr != nil {
			log.Printf("check seen failed user=%d id=%s err=%v", user.TgUserID, item.ID, seenErr)
			continue
		}
		if seen {
			continue
		}

		if user.AIPushEnabled {
			s.pushWithAIFilter(ctx, user, item)
		} else {
			s.pushEmailNotify(user.TgUserID, item, "")
		}

		if markErr := s.store.MarkEmailSeen(ctx, user.TgUserID, item.ID); markErr != nil {
			log.Printf("mark seen failed user=%d id=%s err=%v", user.TgUserID, item.ID, markErr)
		}
	}
}

// pushWithAIFilter 讓 AI 判斷是否重要，重要才推送，並附帶一句話摘要
func (s *Scheduler) pushWithAIFilter(ctx context.Context, user store.User, item gmail.EmailSummary) {
	aiCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	verdict, summary, err := s.ai.JudgeEmailImportance(aiCtx, user.TgUserID, item)
	if err != nil {
		log.Printf("ai judge failed user=%d id=%s err=%v, fallback to push", user.TgUserID, item.ID, err)
		s.pushEmailNotify(user.TgUserID, item, "")
		return
	}
	if !verdict {
		log.Printf("ai filtered email user=%d id=%s subject=%q", user.TgUserID, item.ID, item.Subject)
		return
	}
	s.pushEmailNotify(user.TgUserID, item, summary)
}

func (s *Scheduler) pushEmailNotify(tgUserID int64, item gmail.EmailSummary, aiSummary string) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("📬 *新邮件*\n*主题:* %s\n*发件人:* %s\nID: `%s`",
		item.Subject, item.From, item.ID))
	if aiSummary != "" {
		sb.WriteString("\n\n💡 " + aiSummary)
	}
	sb.WriteString(fmt.Sprintf("\n\n/read %s", item.ID))

	if _, err := s.bot.Send(&tele.User{ID: tgUserID}, sb.String(), tele.ModeMarkdown); err != nil {
		// 降級純文本
		plain := fmt.Sprintf("📬 新邮件\n主题: %s\n发件人: %s\nID: %s\n\n/read %s",
			item.Subject, item.From, item.ID, item.ID)
		if _, err2 := s.bot.Send(&tele.User{ID: tgUserID}, plain); err2 != nil {
			log.Printf("send notify failed user=%d id=%s err=%v", tgUserID, item.ID, err2)
		}
	}
}

func (s *Scheduler) pushDailyDigest(ctx context.Context, user store.User) {
	digest, err := s.ai.GenerateDailyDigest(ctx, user.TgUserID)
	if err != nil {
		log.Printf("generate digest failed user=%d err=%v", user.TgUserID, err)
		return
	}
	msg := "🗓 *每日邮件摘要*\n\n" + mdToTelegram(digest)
	if _, sendErr := s.bot.Send(&tele.User{ID: user.TgUserID}, msg, tele.ModeMarkdown); sendErr != nil {
		plain := "🗓 每日邮件摘要\n\n" + digest
		if _, err2 := s.bot.Send(&tele.User{ID: user.TgUserID}, plain); err2 != nil {
			log.Printf("send digest failed user=%d err=%v", user.TgUserID, err2)
		}
	}
}

func (s *Scheduler) isInitialized(userID int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized[userID]
}

func (s *Scheduler) setInitialized(userID int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized[userID] = true
}
