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

	CREATE INDEX IF NOT EXISTS idx_sessions_user_active ON sessions (tg_user_id, is_active, last_active);
	CREATE INDEX IF NOT EXISTS idx_seen_emails_user ON seen_emails (tg_user_id);
	`)
	if err != nil {
		return err
	}
	// 新欄位：已存在時 SQLite 會報錯，直接忽略
	_, _ = s.db.ExecContext(ctx, `ALTER TABLE users ADD COLUMN ai_push_enabled INTEGER DEFAULT 0`)
	return nil
}
