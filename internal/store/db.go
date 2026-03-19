package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Init(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
	CREATE TABLE IF NOT EXISTS users (
		tg_user_id         INTEGER PRIMARY KEY,
		platform           TEXT DEFAULT 'telegram',
		user_id            TEXT DEFAULT '',
		gmail_address      TEXT,
		access_token       TEXT,
		refresh_token      TEXT,
		token_expiry       TEXT,
		digest_time        TEXT DEFAULT '',
		check_interval_min INTEGER DEFAULT 5,
		created_at         TEXT DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS sessions (
		id           TEXT PRIMARY KEY,
		tg_user_id   INTEGER NOT NULL,
		platform     TEXT DEFAULT 'telegram',
		user_id      TEXT DEFAULT '',
		persona_name TEXT DEFAULT '',
		title        TEXT DEFAULT '',
		messages     TEXT DEFAULT '[]',
		created_at   TEXT DEFAULT CURRENT_TIMESTAMP,
		last_active  TEXT DEFAULT CURRENT_TIMESTAMP,
		is_active    INTEGER DEFAULT 0
	);

	CREATE TABLE IF NOT EXISTS seen_emails (
		tg_user_id INTEGER NOT NULL,
		email_id   TEXT NOT NULL,
		notified_at TEXT DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (tg_user_id, email_id)
	);

	CREATE TABLE IF NOT EXISTS user_identities (
		platform   TEXT NOT NULL,
		user_id    TEXT NOT NULL,
		user_key   INTEGER NOT NULL UNIQUE,
		created_at TEXT DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (platform, user_id)
	);

	CREATE TABLE IF NOT EXISTS reminders (
		id         TEXT PRIMARY KEY,
		user_key   INTEGER NOT NULL,
		platform   TEXT NOT NULL,
		user_id    TEXT NOT NULL,
		content    TEXT NOT NULL,
		remind_at  TEXT NOT NULL,
		created_at TEXT DEFAULT CURRENT_TIMESTAMP,
		sent_at    TEXT DEFAULT ''
	);

	CREATE INDEX IF NOT EXISTS idx_sessions_user_active ON sessions (tg_user_id, is_active, last_active);
	CREATE INDEX IF NOT EXISTS idx_seen_emails_user ON seen_emails (tg_user_id);
	CREATE INDEX IF NOT EXISTS idx_sessions_platform_user ON sessions (platform, user_id, last_active);
	CREATE INDEX IF NOT EXISTS idx_reminders_due ON reminders (sent_at, remind_at);
	`)
	if err != nil {
		return err
	}
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN platform TEXT DEFAULT 'telegram'`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN user_id TEXT DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN platform TEXT DEFAULT 'telegram'`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN user_id TEXT DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE sessions ADD COLUMN persona_name TEXT DEFAULT ''`)
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN ai_push_enabled INTEGER DEFAULT 0`)
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET platform = 'telegram' WHERE platform IS NULL OR TRIM(platform) = ''`)
	_, _ = s.db.ExecContext(ctx, `UPDATE users SET user_id = CAST(tg_user_id AS TEXT) WHERE user_id IS NULL OR TRIM(user_id) = ''`)
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET platform = 'telegram' WHERE platform IS NULL OR TRIM(platform) = ''`)
	_, _ = s.db.ExecContext(ctx, `UPDATE sessions SET user_id = CAST(tg_user_id AS TEXT) WHERE user_id IS NULL OR TRIM(user_id) = ''`)
	_, _ = s.db.ExecContext(ctx, `INSERT INTO user_identities (platform, user_id, user_key) SELECT 'telegram', CAST(tg_user_id AS TEXT), tg_user_id FROM users ON CONFLICT(platform, user_id) DO NOTHING`)
	return nil
}
