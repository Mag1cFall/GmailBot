// 业务指标统计
package metrics

import (
	"sync"
	"sync/atomic"
	"time"
)

// Metrics 运行时指标
type Metrics struct {
	MessagesTotal  atomic.Int64
	ToolCallsTotal atomic.Int64
	ErrorsTotal    atomic.Int64
	ActiveUsers    sync.Map
	StartTime      time.Time
}

// Snapshot 指标快照
type Snapshot struct {
	MessagesTotal  int64     `json:"messages_total"`
	ToolCallsTotal int64     `json:"tool_calls_total"`
	ErrorsTotal    int64     `json:"errors_total"`
	ActiveUsers    int64     `json:"active_users"`
	StartTime      time.Time `json:"start_time"`
	UptimeSeconds  int64     `json:"uptime_seconds"`
}

var Default = New()

// New 创建指标实例
func New() *Metrics {
	return &Metrics{StartTime: time.Now()}
}

// MarkActiveUser 记录活跃用户，用于统计独立用户数
func (m *Metrics) MarkActiveUser(identity string) {
	if identity == "" {
		return
	}
	m.ActiveUsers.Store(identity, time.Now())
}

// Snapshot 生成当前指标快照
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
