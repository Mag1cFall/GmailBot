package store

import (
	"context"
	"testing"
	"time"
)

func TestReminderLifecycle(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	remindAt := time.Now().UTC().Add(time.Minute)

	created, err := st.CreateReminder(ctx, Reminder{
		Platform: "telegram",
		UserID:   "100",
		Content:  "pay rent",
		RemindAt: remindAt,
	})
	if err != nil {
		t.Fatalf("create reminder failed: %v", err)
	}
	if created.ID == "" {
		t.Fatal("expected reminder id")
	}
	due, err := st.ListDueReminders(ctx, remindAt.Add(time.Second))
	if err != nil {
		t.Fatalf("list reminders failed: %v", err)
	}
	if len(due) != 1 || due[0].Content != "pay rent" {
		t.Fatalf("unexpected due reminders: %#v", due)
	}
	if err := st.MarkReminderSent(ctx, created.ID); err != nil {
		t.Fatalf("mark reminder sent failed: %v", err)
	}
	due, err = st.ListDueReminders(ctx, remindAt.Add(time.Second))
	if err != nil {
		t.Fatalf("list reminders failed: %v", err)
	}
	if len(due) != 0 {
		t.Fatalf("expected no due reminders after sending, got %#v", due)
	}
}
