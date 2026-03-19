package persona

import (
	"context"
	"path/filepath"
	"testing"

	"gmailbot/internal/store"
)

func TestManagerResolvesDefaultAndSessionPersona(t *testing.T) {
	st := newTestStore(t)
	mgr := NewManager(st, "all-tools")
	mgr.Register(Persona{Name: "all-tools", SystemPrompt: "all", Tools: []string{"a", "b"}})
	mgr.Register(Persona{Name: "gmail", SystemPrompt: "gmail", Tools: []string{"gmail_tool"}})

	defaultPersona := mgr.Default()
	if defaultPersona.Name != "all-tools" {
		t.Fatalf("unexpected default persona: %#v", defaultPersona)
	}
	if _, err := st.GetOrCreateActiveSessionByIdentity(context.Background(), "telegram", "1"); err != nil {
		t.Fatalf("create session failed: %v", err)
	}
	active, err := mgr.ActivePersona(context.Background(), "telegram", "1")
	if err != nil {
		t.Fatalf("get active persona failed: %v", err)
	}
	t.Logf("default persona: %s", active.Name)
	if active.Name != "all-tools" {
		t.Fatalf("unexpected active persona: %#v", active)
	}
	selected, err := mgr.SetActiveSessionPersona(context.Background(), "telegram", "1", "gmail")
	if err != nil {
		t.Fatalf("set active persona failed: %v", err)
	}
	if selected.Name != "gmail" {
		t.Fatalf("unexpected selected persona: %#v", selected)
	}
	active, err = mgr.ActivePersona(context.Background(), "telegram", "1")
	if err != nil {
		t.Fatalf("get active persona failed: %v", err)
	}
	t.Logf("switched persona: %s", active.Name)
	if active.Name != "gmail" {
		t.Fatalf("unexpected active persona after switch: %#v", active)
	}
}

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Init(filepath.Join(t.TempDir(), "persona.db"))
	if err != nil {
		t.Fatalf("init store failed: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}
