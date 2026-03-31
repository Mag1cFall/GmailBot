package system

import (
	"context"
	"strings"
	"testing"
	"time"

	"gmailbot/internal/agent"
	"gmailbot/internal/event"
	"gmailbot/internal/plugin"
	"gmailbot/internal/testutil"
)

func TestSetReminderToolStoresReminder(t *testing.T) {
	st := testutil.NewTestStore(t)
	registry := agent.NewToolRegistry()
	plg := NewPlugin()
	if err := plg.Init(&plugin.Context{Registry: registry, Bus: event.NewBus(), Extra: map[string]any{"store": st}}); err != nil {
		t.Fatalf("init plugin failed: %v", err)
	}
	defer plg.Shutdown()
	tool, ok := registry.Get("set_reminder")
	if !ok {
		t.Fatal("expected set_reminder tool")
	}
	result, err := tool.Handler(&agent.ToolContext{Platform: "telegram", UserID: "7"}, []byte(`{"content":"喝水","at":"in 1m"}`))
	if err != nil {
		t.Fatalf("call tool failed: %v", err)
	}
	t.Logf("set_reminder result: %s", result)
	reminders, err := st.ListDueReminders(context.Background(), time.Now().Add(2*time.Minute))
	if err != nil {
		t.Fatalf("list reminders failed: %v", err)
	}
	if len(reminders) != 1 || !strings.Contains(result, "喝水") {
		t.Fatalf("unexpected reminders: %#v result=%s", reminders, result)
	}
}
