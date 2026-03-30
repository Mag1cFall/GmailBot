// 定时调度：新邮件检查、每日摘要、数据清理
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

// SendFunc 发送响应到指定用户的函数类型
type SendFunc func(ctx context.Context, platformName, userID string, resp platform.UnifiedResponse) error

// schedulerMailService 调度器依赖的邮件接口
type schedulerMailService interface {
	ListEmails(ctx context.Context, tgUserID int64, n int, query string) ([]gmail.EmailSummary, error)
}

// schedulerAgent 调度器依赖的 AI Agent 接口
type schedulerAgent interface {
	JudgeEmailImportance(ctx context.Context, tgUserID int64, subject, from, snippet string) (bool, string, error)
	GenerateDailyDigest(ctx context.Context, tgUserID int64) (string, error)
}

// Scheduler 定时任务调度器
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

// NewScheduler 创建调度器，注册每分钟任务和每日清理
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

// Start 启动定时器
func (s *Scheduler) Start() {
	s.cron.Start()
}

// Stop 优雅关闭定时器，等待进行中的任务完成
func (s *Scheduler) Stop() {
	stopCtx := s.cron.Stop()
	select {
	case <-stopCtx.Done():
	case <-time.After(3 * time.Second):
	}
}

// runMinuteTasks 每分钒执行，遍历所有已授权用户判断时否应检查邮件或推送摘要
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

// shouldCheck 判断是否到了检查新邮件的时间
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

// shouldDigest 判断当前时间是否匹配摘要时间并防重复推送
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

// pollNewEmails 检查新邮件并推送通知
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

// pushWithAIFilter AI 判断邮件重要性后决定是否推送
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

// pushEmailNotify 构建邮件通知并发送
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

// pushDailyDigest 生成并推送每日摘要
func (s *Scheduler) pushDailyDigest(ctx context.Context, user store.User) {
	digest, err := s.ai.GenerateDailyDigest(ctx, user.TgUserID)
	if err != nil {
		return
	}
	if s.send != nil {
		_ = s.send(ctx, user.Platform, user.UserID, platform.UnifiedResponse{Text: "🗓 *每日邮件摘要*\n\n" + digest, Markdown: true})
	}
}

// isInitialized 返回用户是否已完成首次初始化
func (s *Scheduler) isInitialized(identity string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.initialized[identity]
}

// setInitialized 标记用户已初始化
func (s *Scheduler) setInitialized(identity string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initialized[identity] = true
}

// schedulerIdentity 生成用户唯一标识
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

// runCleanup 每日凌晨 3 点执行，清理旧已见记录和过期摘要状态
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
