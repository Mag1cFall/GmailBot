package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	"gmailbot/internal/agent"
	"gmailbot/internal/event"
	"gmailbot/internal/plugin"
	"gmailbot/internal/store"
)

type Plugin struct {
	bus    *event.Bus
	store  *store.Store
	stop   chan struct{}
	closed chan struct{}
	once   sync.Once
}

func NewPlugin() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Name() string                     { return "system" }
func (p *Plugin) Description() string              { return "系统时间、计算与提醒" }
func (p *Plugin) Commands() []plugin.Command       { return nil }
func (p *Plugin) EventHandlers() []plugin.EventSub { return nil }

func (p *Plugin) Init(ctx *plugin.Context) error {
	st, _ := ctx.Extra["store"].(*store.Store)
	if st == nil {
		return errors.New("system plugin requires store")
	}
	p.store = st
	p.bus = ctx.Bus
	p.stop = make(chan struct{})
	p.closed = make(chan struct{})
	p.registerCurrentTime(ctx.Registry)
	p.registerCalculation(ctx.Registry)
	p.registerReminder(ctx.Registry)
	go p.runReminderLoop()
	return nil
}

func (p *Plugin) Shutdown() error {
	p.once.Do(func() {
		if p.stop != nil {
			close(p.stop)
		}
		if p.closed != nil {
			<-p.closed
		}
	})
	return nil
}

func (p *Plugin) runReminderLoop() {
	defer close(p.closed)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.stop:
			return
		case now := <-ticker.C:
			p.flushDueReminders(now)
		}
	}
}

func (p *Plugin) flushDueReminders(now time.Time) {
	if p.bus == nil || p.store == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	reminders, err := p.store.ListDueReminders(ctx, now.UTC())
	if err != nil {
		return
	}
	for _, reminder := range reminders {
		p.bus.Publish(ctx, event.Event{
			Type:   "reminder.due",
			Source: p.Name(),
			Payload: map[string]any{
				"id":       reminder.ID,
				"platform": reminder.Platform,
				"user_id":  reminder.UserID,
				"content":  reminder.Content,
			},
		})
	}
}

func (p *Plugin) registerCurrentTime(registry *agent.ToolRegistry) {
	registry.Register(&agent.ToolDef{
		Name:        "get_current_time",
		Description: "获取当前时间",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"timezone":{"type":"string","description":"IANA 时区，例如 Asia/Taipei"}}}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				Timezone string `json:"timezone"`
			}
			_ = json.Unmarshal(raw, &req)
			loc := time.UTC
			if strings.TrimSpace(req.Timezone) != "" {
				loaded, err := time.LoadLocation(req.Timezone)
				if err != nil {
					return "", err
				}
				loc = loaded
			}
			now := time.Now().In(loc)
			return agent.ToJSON(map[string]any{
				"time":     now.Format(time.RFC3339),
				"timezone": now.Location().String(),
			})
		},
		Active:   true,
		Category: "system",
	})
}

func (p *Plugin) registerCalculation(registry *agent.ToolRegistry) {
	registry.Register(&agent.ToolDef{
		Name:        "run_calculation",
		Description: "执行数学表达式计算",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"expression":{"type":"string","description":"数学表达式，例如 (12+3)*4/5"}},"required":["expression"]}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				Expression string `json:"expression"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", err
			}
			value, err := evaluateExpression(req.Expression)
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"expression": req.Expression, "result": value})
		},
		Active:   true,
		Category: "system",
	})
}

func (p *Plugin) registerReminder(registry *agent.ToolRegistry) {
	registry.Register(&agent.ToolDef{
		Name:        "set_reminder",
		Description: "设置提醒并在到期后推送给用户",
		Parameters:  json.RawMessage(`{"type":"object","properties":{"content":{"type":"string","description":"提醒内容"},"at":{"type":"string","description":"提醒时间，支持 RFC3339、2006-01-02 15:04 或 10m / in 10m"}},"required":["content","at"]}`),
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				Content string `json:"content"`
				At      string `json:"at"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", err
			}
			remindAt, err := parseReminderTime(req.At, time.Now())
			if err != nil {
				return "", err
			}
			platformName := strings.TrimSpace(tc.Platform)
			if platformName == "" {
				platformName = "telegram"
			}
			userID := strings.TrimSpace(tc.UserID)
			if userID == "" {
				userID = strconv.FormatInt(tc.TgUserID, 10)
			}
			reminder, err := p.store.CreateReminder(context.Background(), store.Reminder{
				Platform: platformName,
				UserID:   userID,
				Content:  req.Content,
				RemindAt: remindAt,
			})
			if err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"id": reminder.ID, "content": reminder.Content, "remind_at": reminder.RemindAt.Format(time.RFC3339)})
		},
		Active:   true,
		Category: "system",
	})
}

func parseReminderTime(raw string, now time.Time) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, errors.New("reminder time is required")
	}
	if strings.HasPrefix(strings.ToLower(raw), "in ") {
		raw = strings.TrimSpace(raw[3:])
	}
	if duration, err := time.ParseDuration(raw); err == nil {
		return now.Add(duration).UTC(), nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04", "2006-01-02 15:04:05", "2006-01-02T15:04"} {
		if parsed, err := time.ParseInLocation(layout, raw, now.Location()); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported reminder time: %s", raw)
}

func evaluateExpression(expression string) (float64, error) {
	expr, err := parser.ParseExpr(strings.TrimSpace(expression))
	if err != nil {
		return 0, err
	}
	return evalNode(expr)
}

func evalNode(node ast.Expr) (float64, error) {
	switch value := node.(type) {
	case *ast.BasicLit:
		return strconv.ParseFloat(value.Value, 64)
	case *ast.UnaryExpr:
		operand, err := evalNode(value.X)
		if err != nil {
			return 0, err
		}
		switch value.Op {
		case token.ADD:
			return operand, nil
		case token.SUB:
			return -operand, nil
		default:
			return 0, fmt.Errorf("unsupported unary operator: %s", value.Op.String())
		}
	case *ast.BinaryExpr:
		left, err := evalNode(value.X)
		if err != nil {
			return 0, err
		}
		right, err := evalNode(value.Y)
		if err != nil {
			return 0, err
		}
		switch value.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, errors.New("division by zero")
			}
			return left / right, nil
		case token.REM:
			if right == 0 {
				return 0, errors.New("division by zero")
			}
			return math.Mod(left, right), nil
		default:
			return 0, fmt.Errorf("unsupported binary operator: %s", value.Op.String())
		}
	case *ast.ParenExpr:
		return evalNode(value.X)
	default:
		return 0, fmt.Errorf("unsupported expression type %T", node)
	}
}
