package plugin

import (
	"context"
	"encoding/json"
	"testing"

	"gmailbot/internal/agent"
	"gmailbot/internal/event"
)

func TestManagerTogglesPluginToolsAndCommands(t *testing.T) {
	registry := agent.NewToolRegistry()
	bus := event.NewBus()
	mgr := NewManager(registry, bus, map[string]any{})

	alpha := &toolPlugin{name: "alpha", toolName: "alpha_tool", commandName: "alpha_cmd"}
	beta := &toolPlugin{name: "beta", toolName: "beta_tool", commandName: "beta_cmd"}
	if err := mgr.Register(alpha); err != nil {
		t.Fatalf("register alpha failed: %v", err)
	}
	if err := mgr.Register(beta); err != nil {
		t.Fatalf("register beta failed: %v", err)
	}

	if len(mgr.Commands()) != 2 {
		t.Fatalf("expected 2 commands, got %#v", mgr.Commands())
	}
	if tool, ok := registry.Get("alpha_tool"); !ok || !tool.Active {
		t.Fatalf("expected alpha tool active, got %#v %v", tool, ok)
	}

	if !mgr.SetActive("alpha", false) {
		t.Fatal("expected alpha plugin to toggle off")
	}
	if len(mgr.Commands()) != 1 || mgr.Commands()[0].Name != "beta_cmd" {
		t.Fatalf("unexpected commands after toggle: %#v", mgr.Commands())
	}
	if tool, ok := registry.Get("alpha_tool"); !ok || tool.Active {
		t.Fatalf("expected alpha tool disabled, got %#v %v", tool, ok)
	}

	if !mgr.SetActive("alpha", true) {
		t.Fatal("expected alpha plugin to toggle on")
	}
	if len(mgr.Commands()) != 2 {
		t.Fatalf("expected commands to return after re-enable, got %#v", mgr.Commands())
	}
	if tool, ok := registry.Get("alpha_tool"); !ok || !tool.Active {
		t.Fatalf("expected alpha tool re-enabled, got %#v %v", tool, ok)
	}

	if infos := mgr.Info(); len(infos) != 2 || infos[0].ToolCount != 1 && infos[1].ToolCount != 1 {
		t.Fatalf("unexpected plugin info: %#v", infos)
	}
	result, err := registry.Execute(&agent.ToolContext{}, "alpha_tool", `{"value":"ok"}`)
	if err != nil {
		t.Fatalf("execute alpha tool failed: %v", err)
	}
	if result != `{"plugin":"alpha","value":"ok"}` {
		t.Fatalf("unexpected alpha tool result: %s", result)
	}
}

type toolPlugin struct {
	name        string
	toolName    string
	commandName string
}

func (p *toolPlugin) Name() string        { return p.name }
func (p *toolPlugin) Description() string { return p.name }
func (p *toolPlugin) Shutdown() error     { return nil }

func (p *toolPlugin) Init(ctx *Context) error {
	ctx.Registry.Register(&agent.ToolDef{
		Name:        p.toolName,
		Description: p.toolName,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"value":{"type":"string"}}}`),
		Active:      true,
		Handler: func(tc *agent.ToolContext, raw json.RawMessage) (string, error) {
			var req struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(raw, &req); err != nil {
				return "", err
			}
			return agent.ToJSON(map[string]any{"plugin": p.name, "value": req.Value})
		},
	})
	return nil
}

func (p *toolPlugin) Commands() []Command {
	return []Command{{Name: p.commandName, Description: p.commandName, Handler: func(ctx context.Context, args []string) (string, error) {
		return p.commandName, nil
	}}}
}

func (p *toolPlugin) EventHandlers() []EventSub { return nil }
