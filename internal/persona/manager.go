// 人设管理，支持多人设切换
package persona

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"gmailbot/internal/store"
)

// Manager 人设管理器
type Manager struct {
	mu          sync.RWMutex
	store       *store.Store
	personas    map[string]Persona
	defaultName string
}

// NewManager 创建人设管理器，并注册内置人设
func NewManager(st *store.Store, defaultName string) *Manager {
	mgr := &Manager{
		store:       st,
		personas:    map[string]Persona{},
		defaultName: strings.TrimSpace(defaultName),
	}
	mgr.Register(Persona{
		Name:         "gmail",
		SystemPrompt: "你是用户的私人 Gmail 管理助手，擅长查信、读信、写信、整理标签和生成摘要。",
		Tools: []string{
			"list_emails", "get_email", "get_labels", "summarize_emails", "send_email", "reply_email", "forward_email", "create_label", "delete_label", "modify_labels",
			"memory_read", "memory_write", "memory_search", "memory_list", "memory_delete",
			"web_search", "read_url", "get_current_time", "run_calculation", "set_reminder",
			"handoff_to_email_writer", "handoff_to_email_searcher",
		},
	})
	mgr.Register(Persona{
		Name:         "gmail-only",
		SystemPrompt: "你是专注 Gmail 的助手，只处理邮件相关任务。",
		Tools: []string{
			"list_emails", "get_email", "get_labels", "summarize_emails", "send_email", "reply_email", "forward_email", "create_label", "delete_label", "modify_labels",
		},
	})
	mgr.Register(Persona{
		Name:         "all-tools",
		SystemPrompt: "你是通用 AI Agent 助手，可以在用户许可范围内使用全部工具完成任务。",
	})
	mgr.Register(Persona{
		Name:         "research",
		SystemPrompt: "你是搜索与知识助手，优先查找网页和知识库内容并组织答案。",
		Tools:        []string{"web_search", "read_url", "knowledge_search", "knowledge_list", "memory_read", "memory_write", "memory_search", "memory_list", "memory_delete", "get_current_time", "run_calculation"},
	})
	if mgr.defaultName == "" {
		mgr.defaultName = "gmail"
	}
	return mgr
}

// Register 注册人设，name 为空则忽略
func (m *Manager) Register(persona Persona) {
	if strings.TrimSpace(persona.Name) == "" {
		return
	}
	persona.Name = strings.TrimSpace(persona.Name)
	m.mu.Lock()
	m.personas[persona.Name] = persona
	m.mu.Unlock()
}

// List 返回所有已注册人设，按名称升序
func (m *Manager) List() []Persona {
	m.mu.RLock()
	defer m.mu.RUnlock()
	personas := make([]Persona, 0, len(m.personas))
	for _, persona := range m.personas {
		personas = append(personas, persona)
	}
	sort.Slice(personas, func(i, j int) bool { return personas[i].Name < personas[j].Name })
	return personas
}

// Get 按名称查找人设
func (m *Manager) Get(name string) (Persona, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	persona, ok := m.personas[strings.TrimSpace(name)]
	return persona, ok
}

// Default 返回默认人设
func (m *Manager) Default() Persona {
	if persona, ok := m.Get(m.defaultName); ok {
		return persona
	}
	if persona, ok := m.Get("gmail"); ok {
		return persona
	}
	return Persona{Name: "default", SystemPrompt: "你是一个乐于助人的 AI 助手。"}
}

// ActivePersona 获取用户当前会话的人设
func (m *Manager) ActivePersona(ctx context.Context, platformName, userID string) (Persona, error) {
	session, err := m.store.GetOrCreateActiveSessionByIdentity(ctx, platformName, userID)
	if err != nil {
		return Persona{}, err
	}
	if strings.TrimSpace(session.PersonaName) == "" {
		return m.Default(), nil
	}
	if persona, ok := m.Get(session.PersonaName); ok {
		return persona, nil
	}
	return m.Default(), nil
}

// SetActiveSessionPersona 切换用户当前会话的人设
func (m *Manager) SetActiveSessionPersona(ctx context.Context, platformName, userID, personaName string) (Persona, error) {
	persona, ok := m.Get(personaName)
	if !ok {
		return Persona{}, fmt.Errorf("persona %s not found", personaName)
	}
	if err := m.store.SetActiveSessionPersonaByIdentity(ctx, platformName, userID, persona.Name); err != nil {
		return Persona{}, err
	}
	return persona, nil
}
