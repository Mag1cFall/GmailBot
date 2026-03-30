// 命令路由
package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Command 可注册命令
type Command struct {
	Name        string
	Description string
	Handler     func(ctx context.Context, msg UnifiedMessage, args []string) (UnifiedResponse, error)
}

// CommandRouter 命令路由器
type CommandRouter struct {
	mu       sync.RWMutex
	commands map[string]Command
	order    []string
}

// NewCommandRouter 创建命令路由器
func NewCommandRouter() *CommandRouter {
	return &CommandRouter{
		commands: map[string]Command{},
	}
}

// Register 注册命令，命令名不能为空且 Handler 不能为 nil
func (r *CommandRouter) Register(cmd Command) error {
	name := normalizeCommandName(cmd.Name)
	if name == "" {
		return fmt.Errorf("command name is required")
	}
	if cmd.Handler == nil {
		return fmt.Errorf("command handler is required")
	}
	cmd.Name = name

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.commands[name]; !exists {
		r.order = append(r.order, name)
	}
	r.commands[name] = cmd
	sort.Strings(r.order)
	return nil
}

// Commands 返回所有已注册命令，按字母顺序
func (r *CommandRouter) Commands() []Command {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Command, 0, len(r.order))
	for _, name := range r.order {
		if cmd, ok := r.commands[name]; ok {
			out = append(out, cmd)
		}
	}
	return out
}

// Handle 匹配并执行命令
func (r *CommandRouter) Handle(ctx context.Context, msg UnifiedMessage) (UnifiedResponse, bool, error) {
	name, args, ok := parseCommand(msg.Text)
	if !ok {
		return UnifiedResponse{}, false, nil
	}

	r.mu.RLock()
	cmd, exists := r.commands[name]
	r.mu.RUnlock()
	if !exists {
		return UnifiedResponse{}, false, nil
	}

	resp, err := cmd.Handler(ctx, msg, args)
	return resp, true, err
}

// parseCommand 解析 /cmd args 格式的命令文本
func parseCommand(text string) (string, []string, bool) {
	text = strings.TrimSpace(text)
	if text == "" || !strings.HasPrefix(text, "/") {
		return "", nil, false
	}
	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "", nil, false
	}
	name := normalizeCommandName(parts[0])
	if name == "" {
		return "", nil, false
	}
	return name, parts[1:], true
}

// normalizeCommandName 解析并小写化命令名，去除 / 前缀和 @bot 后缀
func normalizeCommandName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	if index := strings.Index(name, "@"); index >= 0 {
		name = name[:index]
	}
	return strings.ToLower(strings.TrimSpace(name))
}
