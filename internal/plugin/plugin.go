// 插件系统框架
package plugin

import (
	"context"
	"gmailbot/internal/agent"
	"gmailbot/internal/event"
)

// Context 插件初始化上下文
type Context struct {
	Registry *agent.ToolRegistry
	Bus      *event.Bus
	Extra    map[string]any
}

// Command 插件注册的命令
type Command struct {
	Name        string
	Description string
	Handler     func(ctx context.Context, args []string) (string, error)
}

// EventSub 插件事件订阅
type EventSub struct {
	EventType string
	Handler   event.Handler
}

// Plugin 插件接口
type Plugin interface {
	Name() string
	Description() string
	Init(ctx *Context) error
	Shutdown() error
	Commands() []Command
	EventHandlers() []EventSub
}

// Info 插件信息
type Info struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Active      bool   `json:"active"`
	ToolCount   int    `json:"tool_count"`
}

// managedPlugin 内部包装，记录插件状态和注册的工具名
type managedPlugin struct {
	plugin    Plugin
	active    bool
	toolNames []string
}

// Manager 插件管理器
type Manager struct {
	plugins  []managedPlugin
	registry *agent.ToolRegistry
	bus      *event.Bus
	extra    map[string]any
}

// NewManager 创建插件管理器
func NewManager(registry *agent.ToolRegistry, bus *event.Bus, extra map[string]any) *Manager {
	return &Manager{
		registry: registry,
		bus:      bus,
		extra:    extra,
	}
}

// Register 初始化插件并记录其注册的工具
func (m *Manager) Register(p Plugin) error {
	before := map[string]struct{}{}
	for _, tool := range m.registry.AllTools() {
		before[tool.Name] = struct{}{}
	}
	ctx := &Context{
		Registry: m.registry,
		Bus:      m.bus,
		Extra:    m.extra,
	}
	if err := p.Init(ctx); err != nil {
		return err
	}
	var toolNames []string
	for _, tool := range m.registry.AllTools() {
		if _, exists := before[tool.Name]; !exists {
			toolNames = append(toolNames, tool.Name)
		}
	}
	m.plugins = append(m.plugins, managedPlugin{plugin: p, active: true, toolNames: toolNames})
	if m.bus != nil {
		for _, sub := range p.EventHandlers() {
			m.bus.Subscribe(sub.EventType, sub.Handler)
		}
	}
	return nil
}

// Shutdown 按注册倒序优雅关闭所有插件
func (m *Manager) Shutdown() {
	for i := len(m.plugins) - 1; i >= 0; i-- {
		_ = m.plugins[i].plugin.Shutdown()
	}
}

// List 返回所有插件名称
func (m *Manager) List() []string {
	names := make([]string, len(m.plugins))
	for i, entry := range m.plugins {
		names[i] = entry.plugin.Name()
	}
	return names
}

// Commands 返回所有已启用插件的命令
func (m *Manager) Commands() []Command {
	var commands []Command
	for _, entry := range m.plugins {
		if !entry.active {
			continue
		}
		commands = append(commands, entry.plugin.Commands()...)
	}
	return commands
}

// Info 返回所有插件的运行信息
func (m *Manager) Info() []Info {
	infos := make([]Info, 0, len(m.plugins))
	for _, entry := range m.plugins {
		infos = append(infos, Info{
			Name:        entry.plugin.Name(),
			Description: entry.plugin.Description(),
			Active:      entry.active,
			ToolCount:   len(entry.toolNames),
		})
	}
	return infos
}

// SetActive 启用或禁用插件及其工具
func (m *Manager) SetActive(name string, active bool) bool {
	for i := range m.plugins {
		if m.plugins[i].plugin.Name() != name {
			continue
		}
		m.plugins[i].active = active
		for _, toolName := range m.plugins[i].toolNames {
			m.registry.SetActive(toolName, active)
		}
		return true
	}
	return false
}
