// MySQL 数据库初始化和迁移
package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
)

// Store MySQL 数据存储
type Store struct {
	db *sql.DB
}

// Init 初始化数据库并执行迁移
func Init(dsn string) (*Store, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("db dsn is required")
	}
	db, err := sql.Open("mysql", strings.TrimSpace(dsn))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close 关闭数据库连接
func (s *Store) Close() error {
	return s.db.Close()
}

// migrate 执行建表和字段迭代升级
func (s *Store) migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS users (
			tg_user_id BIGINT PRIMARY KEY,
			platform VARCHAR(32) NOT NULL DEFAULT 'telegram',
			user_id VARCHAR(255) NOT NULL DEFAULT '',
			gmail_address VARCHAR(255) NOT NULL DEFAULT '',
			access_token LONGTEXT NULL,
			refresh_token LONGTEXT NULL,
			token_expiry VARCHAR(64) NOT NULL DEFAULT '',
			digest_time VARCHAR(255) NOT NULL DEFAULT '',
			check_interval_min INT NOT NULL DEFAULT 5,
			ai_push_enabled TINYINT(1) NOT NULL DEFAULT 0,
			created_at VARCHAR(64) NOT NULL DEFAULT ''
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id VARCHAR(64) PRIMARY KEY,
			tg_user_id BIGINT NOT NULL,
			platform VARCHAR(32) NOT NULL DEFAULT 'telegram',
			user_id VARCHAR(255) NOT NULL DEFAULT '',
			persona_name VARCHAR(255) NOT NULL DEFAULT '',
			title VARCHAR(255) NOT NULL DEFAULT '',
			messages LONGTEXT NOT NULL,
			created_at VARCHAR(64) NOT NULL DEFAULT '',
			last_active VARCHAR(64) NOT NULL DEFAULT '',
			is_active TINYINT(1) NOT NULL DEFAULT 0,
			INDEX idx_sessions_user_active (tg_user_id, is_active, last_active),
			INDEX idx_sessions_platform_user (platform, user_id, last_active)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS seen_emails (
			tg_user_id BIGINT NOT NULL,
			email_id VARCHAR(255) NOT NULL,
			notified_at VARCHAR(64) NOT NULL DEFAULT '',
			PRIMARY KEY (tg_user_id, email_id),
			INDEX idx_seen_emails_user (tg_user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS user_identities (
			platform VARCHAR(32) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			user_key BIGINT NOT NULL UNIQUE,
			created_at VARCHAR(64) NOT NULL DEFAULT '',
			PRIMARY KEY (platform, user_id)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`CREATE TABLE IF NOT EXISTS reminders (
			id VARCHAR(64) PRIMARY KEY,
			user_key BIGINT NOT NULL,
			platform VARCHAR(32) NOT NULL,
			user_id VARCHAR(255) NOT NULL,
			content LONGTEXT NOT NULL,
			remind_at VARCHAR(64) NOT NULL,
			created_at VARCHAR(64) NOT NULL,
			sent_at VARCHAR(64) NOT NULL DEFAULT '',
			INDEX idx_reminders_due (sent_at, remind_at)
		) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS platform VARCHAR(32) NOT NULL DEFAULT 'telegram'`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS user_id VARCHAR(255) NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN IF NOT EXISTS ai_push_enabled TINYINT(1) NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS platform VARCHAR(32) NOT NULL DEFAULT 'telegram'`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS user_id VARCHAR(255) NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN IF NOT EXISTS persona_name VARCHAR(255) NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions MODIFY COLUMN messages LONGTEXT NOT NULL`,
		`UPDATE users SET platform = 'telegram' WHERE platform IS NULL OR TRIM(platform) = ''`,
		`UPDATE users SET user_id = CAST(tg_user_id AS CHAR) WHERE user_id IS NULL OR TRIM(user_id) = ''`,
		`UPDATE users SET created_at = ? WHERE created_at IS NULL OR TRIM(created_at) = ''`,
		`UPDATE sessions SET platform = 'telegram' WHERE platform IS NULL OR TRIM(platform) = ''`,
		`UPDATE sessions SET user_id = CAST(tg_user_id AS CHAR) WHERE user_id IS NULL OR TRIM(user_id) = ''`,
		`UPDATE sessions SET created_at = ? WHERE created_at IS NULL OR TRIM(created_at) = ''`,
		`UPDATE sessions SET last_active = created_at WHERE last_active IS NULL OR TRIM(last_active) = ''`,
		`UPDATE seen_emails SET notified_at = ? WHERE notified_at IS NULL OR TRIM(notified_at) = ''`,
		`UPDATE user_identities SET created_at = ? WHERE created_at IS NULL OR TRIM(created_at) = ''`,
		`UPDATE reminders SET sent_at = '' WHERE sent_at IS NULL`,
		`INSERT IGNORE INTO user_identities (platform, user_id, user_key, created_at)
		 SELECT 'telegram', CAST(tg_user_id AS CHAR), tg_user_id, created_at FROM users`,
	}
	now := time.Now().UTC().Format(time.RFC3339)
	for _, stmt := range statements {
		query := strings.TrimSpace(stmt)
		if query == "" {
			continue
		}
		var err error
		switch query {
		case `UPDATE users SET created_at = ? WHERE created_at IS NULL OR TRIM(created_at) = ''`,
			`UPDATE sessions SET created_at = ? WHERE created_at IS NULL OR TRIM(created_at) = ''`,
			`UPDATE seen_emails SET notified_at = ? WHERE notified_at IS NULL OR TRIM(notified_at) = ''`,
			`UPDATE user_identities SET created_at = ? WHERE created_at IS NULL OR TRIM(created_at) = ''`:
			_, err = s.db.ExecContext(ctx, query, now)
		default:
			_, err = s.db.ExecContext(ctx, query)
		}
		if err != nil {
			return err
		}
	}
	return nil
}
