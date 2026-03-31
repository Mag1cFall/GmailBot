// 消息处理管线，支持多阶段中间件链
package pipeline

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"gmailbot/internal/platform"
)

// Event 管线事件
type Event struct {
	Message  platform.UnifiedMessage
	Response platform.UnifiedResponse
	Aborted  bool
	AbortMsg string
	Extra    map[string]any
}

// TelegramUserID 提取消息中的 Telegram 用户 ID
func (e *Event) TelegramUserID() int64 {
	if e == nil || e.Message.Platform != "telegram" {
		return 0
	}
	v, _ := strconv.ParseInt(e.Message.UserID, 10, 64)
	return v
}

// Stage 管线阶段接口
type Stage interface {
	Name() string
	Process(ctx context.Context, evt *Event, next func(context.Context, *Event) error) error
}

// Pipeline 消息处理管线
type Pipeline struct {
	mu     sync.RWMutex
	stages []Stage
}

// New 创建空管线
func New() *Pipeline {
	return &Pipeline{}
}

// AddStage 尾添加阶段
func (p *Pipeline) AddStage(s Stage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stages = append(p.stages, s)
}

// InsertBefore 在指定阶段前插入，目标不存在则尾添加
func (p *Pipeline) InsertBefore(target string, s Stage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, existing := range p.stages {
		if existing.Name() == target {
			newStages := make([]Stage, 0, len(p.stages)+1)
			newStages = append(newStages, p.stages[:i]...)
			newStages = append(newStages, s)
			newStages = append(newStages, p.stages[i:]...)
			p.stages = newStages
			return
		}
	}
	p.stages = append(p.stages, s)
}

// InsertAfter 在指定阶段后插入，目标不存在则尾添加
func (p *Pipeline) InsertAfter(target string, s Stage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, existing := range p.stages {
		if existing.Name() == target {
			newStages := make([]Stage, 0, len(p.stages)+1)
			newStages = append(newStages, p.stages[:i+1]...)
			newStages = append(newStages, s)
			newStages = append(newStages, p.stages[i+1:]...)
			p.stages = newStages
			return
		}
	}
	p.stages = append(p.stages, s)
}

// RemoveStage 移除指定名称的阶段
func (p *Pipeline) RemoveStage(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, s := range p.stages {
		if s.Name() == name {
			p.stages = append(p.stages[:i], p.stages[i+1:]...)
			return
		}
	}
}

// Execute 执行管线
func (p *Pipeline) Execute(ctx context.Context, evt *Event) error {
	p.mu.RLock()
	stages := make([]Stage, len(p.stages))
	copy(stages, p.stages)
	p.mu.RUnlock()

	return p.runStage(ctx, evt, stages, 0)
}

// runStage 递归执行管线阶段
func (p *Pipeline) runStage(ctx context.Context, evt *Event, stages []Stage, idx int) error {
	if idx >= len(stages) || evt.Aborted {
		return nil
	}
	stage := stages[idx]
	slog.Debug("pipeline stage", "stage", stage.Name(), "platform", evt.Message.Platform, "user", evt.Message.UserID)
	err := stage.Process(ctx, evt, func(innerCtx context.Context, innerEvt *Event) error {
		return p.runStage(innerCtx, innerEvt, stages, idx+1)
	})
	if evt.Aborted {
		slog.Info("pipeline aborted", "stage", stage.Name(), "reason", evt.AbortMsg)
	}
	return err
}

// StageNames 返回所有阶段名称
func (p *Pipeline) StageNames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, len(p.stages))
	for i, s := range p.stages {
		names[i] = s.Name()
	}
	return names
}

// AuthCheckStage 认证检查阶段
type AuthCheckStage struct {
	CheckFunc func(ctx context.Context, msg platform.UnifiedMessage) error
}

func (s *AuthCheckStage) Name() string { return "auth_check" }

// Process 执行认证检查，失败则设置 Aborted
func (s *AuthCheckStage) Process(ctx context.Context, evt *Event, next func(context.Context, *Event) error) error {
	if s.CheckFunc != nil {
		if err := s.CheckFunc(ctx, evt.Message); err != nil {
			evt.Aborted = true
			evt.AbortMsg = strings.TrimSpace(err.Error())
			if evt.AbortMsg == "" {
				evt.AbortMsg = "认证检查失败"
			}
			return nil
		}
	}
	return next(ctx, evt)
}

// RateLimitStage 限流阶段
type RateLimitStage struct {
	mu     sync.Mutex
	counts map[string][]int64
	PerMin int
}

// NewRateLimitStage 创建限流阶段， perMin 为每分钒最大消息数
func NewRateLimitStage(perMin int) *RateLimitStage {
	return &RateLimitStage{
		counts: make(map[string][]int64),
		PerMin: perMin,
	}
}

func (s *RateLimitStage) Name() string { return "rate_limit" }

// Process 执行限流检查，超出限制则设置 Aborted
func (s *RateLimitStage) Process(ctx context.Context, evt *Event, next func(context.Context, *Event) error) error {
	if s.PerMin <= 0 {
		return next(ctx, evt)
	}

	s.mu.Lock()
	now := ctx.Value(ctxKeyNow{})
	var nowUnix int64
	if t, ok := now.(int64); ok {
		nowUnix = t
	} else {
		nowUnix = timeNow().Unix()
	}

	cutoff := nowUnix - 60
	identity := evt.Message.Platform + ":" + evt.Message.UserID
	existing := s.counts[identity]
	var filtered []int64
	for _, ts := range existing {
		if ts > cutoff {
			filtered = append(filtered, ts)
		}
	}

	if len(filtered) >= s.PerMin {
		s.mu.Unlock()
		evt.Aborted = true
		evt.AbortMsg = "消息频率过高，请稍后再试。"
		return nil
	}

	filtered = append(filtered, nowUnix)
	s.counts[identity] = filtered
	s.mu.Unlock()

	return next(ctx, evt)
}

// ctxKeyNow 用于测试时注入当前时间的 context key
type ctxKeyNow struct{}

// AIProcessStage AI 对话处理阶段
type AIProcessStage struct {
	HandleFunc func(ctx context.Context, msg platform.UnifiedMessage) (platform.UnifiedResponse, error)
}

func (s *AIProcessStage) Name() string { return "ai_process" }

// Process 调用 AI 处理函数并将结果写入 Event.Response
func (s *AIProcessStage) Process(ctx context.Context, evt *Event, next func(context.Context, *Event) error) error {
	if s.HandleFunc == nil {
		return next(ctx, evt)
	}
	reply, err := s.HandleFunc(ctx, evt.Message)
	if err != nil {
		return err
	}
	evt.Response = reply
	return next(ctx, evt)
}

// SafetyFilterStage 安全过滤阶段，重写敏感内容
type SafetyFilterStage struct {
	Patterns []string
}

func (s *SafetyFilterStage) Name() string { return "safety_filter" }

// Process 在下游处理完成后对输出进行敏感内容遇码
func (s *SafetyFilterStage) Process(ctx context.Context, evt *Event, next func(context.Context, *Event) error) error {
	err := next(ctx, evt)
	if err != nil {
		return err
	}
	for _, pattern := range s.Patterns {
		if containsInsensitive(evt.Response.Text, pattern) {
			evt.Response.Text = redact(evt.Response.Text, pattern)
		}
	}
	return nil
}

// containsInsensitive 大小写不敏感子串匹配
func containsInsensitive(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) &&
		indexOf(toLower(s), toLower(substr)) >= 0
}

// indexOf 在 s 中查找 substr 的位置
func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// toLower ASCII 小写转换
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// redact 将文本中所有敏感内容替换为星号
func redact(s, sensitive string) string {
	lower := toLower(s)
	lowerSens := toLower(sensitive)
	var result []byte
	i := 0
	for i < len(s) {
		pos := indexOf(lower[i:], lowerSens)
		if pos < 0 {
			result = append(result, s[i:]...)
			break
		}
		result = append(result, s[i:i+pos]...)
		for j := 0; j < len(sensitive); j++ {
			result = append(result, '*')
		}
		i += pos + len(sensitive)
	}
	return string(result)
}
