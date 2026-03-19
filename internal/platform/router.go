package platform

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type Command struct {
	Name        string
	Description string
	Handler     func(ctx context.Context, msg UnifiedMessage, args []string) (UnifiedResponse, error)
}

type CommandRouter struct {
	mu       sync.RWMutex
	commands map[string]Command
	order    []string
}

func NewCommandRouter() *CommandRouter {
	return &CommandRouter{
		commands: map[string]Command{},
	}
}

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

func normalizeCommandName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	if index := strings.Index(name, "@"); index >= 0 {
		name = name[:index]
	}
	return strings.ToLower(strings.TrimSpace(name))
}
