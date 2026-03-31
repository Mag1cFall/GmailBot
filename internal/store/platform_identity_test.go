package store

import (
	"context"
	"testing"
)

func TestResolvePlatformUserKeyKeepsTelegramIDs(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	key, err := st.ResolvePlatformUserKey(ctx, "telegram", "12345")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != 12345 {
		t.Fatalf("expected telegram key 12345, got %d", key)
	}

	user, err := st.GetUser(ctx, 12345)
	if err != nil {
		t.Fatalf("expected telegram user row: %v", err)
	}
	if user.Platform != "telegram" || user.UserID != "12345" {
		t.Fatalf("unexpected identity: %#v", user)
	}
}

func TestResolvePlatformUserKeyPersistsNonTelegramMapping(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	first, err := st.ResolvePlatformUserKey(ctx, "discord", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	second, err := st.ResolvePlatformUserKey(ctx, "discord", "user-1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if first != second {
		t.Fatalf("expected stable mapping, got %d and %d", first, second)
	}
	if first >= 0 {
		t.Fatalf("expected synthetic non-telegram key to be negative, got %d", first)
	}
}

func TestGetOrCreateActiveSessionByIdentityStoresPlatformFields(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	session, err := st.GetOrCreateActiveSessionByIdentity(ctx, "discord", "user-2")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session.Platform != "discord" {
		t.Fatalf("expected platform discord, got %s", session.Platform)
	}
	if session.UserID != "user-2" {
		t.Fatalf("expected user id user-2, got %s", session.UserID)
	}
	if session.TgUserID >= 0 {
		t.Fatalf("expected synthetic key, got %d", session.TgUserID)
	}
}
