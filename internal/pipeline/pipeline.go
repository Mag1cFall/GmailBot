package pipeline

import (
	"context"
	"strconv"
	"strings"
	"sync"

	"gmailbot/internal/platform"
)

type Event struct {
	Message  platform.UnifiedMessage
	Response platform.UnifiedResponse
	Aborted  bool
	AbortMsg string
	Extra    map[string]any
}

func (e *Event) TelegramUserID() int64 {
	if e == nil || e.Message.Platform != "telegram" {
		return 0
	}
	v, _ := strconv.ParseInt(e.Message.UserID, 10, 64)
	return v
}

type Stage interface {
	Name() string
	Process(ctx context.Context, evt *Event, next func(context.Context, *Event) error) error
}

type Pipeline struct {
	mu     sync.RWMutex
	stages []Stage
}

func New() *Pipeline {
	return &Pipeline{}
}

func (p *Pipeline) AddStage(s Stage) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stages = append(p.stages, s)
}

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

func (p *Pipeline) Execute(ctx context.Context, evt *Event) error {
	p.mu.RLock()
	stages := make([]Stage, len(p.stages))
	copy(stages, p.stages)
	p.mu.RUnlock()

	return p.runStage(ctx, evt, stages, 0)
}

func (p *Pipeline) runStage(ctx context.Context, evt *Event, stages []Stage, idx int) error {
	if idx >= len(stages) || evt.Aborted {
		return nil
	}
	stage := stages[idx]
	return stage.Process(ctx, evt, func(innerCtx context.Context, innerEvt *Event) error {
		return p.runStage(innerCtx, innerEvt, stages, idx+1)
	})
}

func (p *Pipeline) StageNames() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	names := make([]string, len(p.stages))
	for i, s := range p.stages {
		names[i] = s.Name()
	}
	return names
}

type AuthCheckStage struct {
	CheckFunc func(ctx context.Context, msg platform.UnifiedMessage) error
}

func (s *AuthCheckStage) Name() string { return "auth_check" }

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

type RateLimitStage struct {
	mu     sync.Mutex
	counts map[string][]int64
	PerMin int
}

func NewRateLimitStage(perMin int) *RateLimitStage {
	return &RateLimitStage{
		counts: make(map[string][]int64),
		PerMin: perMin,
	}
}

func (s *RateLimitStage) Name() string { return "rate_limit" }

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

type ctxKeyNow struct{}

type AIProcessStage struct {
	HandleFunc func(ctx context.Context, msg platform.UnifiedMessage) (platform.UnifiedResponse, error)
}

func (s *AIProcessStage) Name() string { return "ai_process" }

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

type SafetyFilterStage struct {
	Patterns []string
}

func (s *SafetyFilterStage) Name() string { return "safety_filter" }

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

func containsInsensitive(s, substr string) bool {
	return len(substr) > 0 && len(s) >= len(substr) &&
		indexOf(toLower(s), toLower(substr)) >= 0
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

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
