package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

type Metrics struct {
	MessagesTotal  atomic.Int64
	ToolCallsTotal atomic.Int64
	ErrorsTotal    atomic.Int64
	ActiveUsers    sync.Map
	StartTime      time.Time
}

type Snapshot struct {
	MessagesTotal  int64     `json:"messages_total"`
	ToolCallsTotal int64     `json:"tool_calls_total"`
	ErrorsTotal    int64     `json:"errors_total"`
	ActiveUsers    int64     `json:"active_users"`
	StartTime      time.Time `json:"start_time"`
	UptimeSeconds  int64     `json:"uptime_seconds"`
}

var Default = New()

func New() *Metrics {
	return &Metrics{StartTime: time.Now()}
}

func (m *Metrics) MarkActiveUser(identity string) {
	if identity == "" {
		return
	}
	m.ActiveUsers.Store(identity, time.Now())
}

func (m *Metrics) Snapshot() Snapshot {
	var active int64
	m.ActiveUsers.Range(func(key, value any) bool {
		active++
		return true
	})
	return Snapshot{
		MessagesTotal:  m.MessagesTotal.Load(),
		ToolCallsTotal: m.ToolCallsTotal.Load(),
		ErrorsTotal:    m.ErrorsTotal.Load(),
		ActiveUsers:    active,
		StartTime:      m.StartTime,
		UptimeSeconds:  int64(time.Since(m.StartTime).Seconds()),
	}
}
