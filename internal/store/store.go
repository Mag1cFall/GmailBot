package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

const maxSessionMessages = 50

type User struct {
	TgUserID         int64
	GmailAddress     string
	AccessToken      string
	RefreshToken     string
	TokenExpiry      time.Time
	DigestTimes      []string
	CheckIntervalMin int
	AIPushEnabled    bool
	CreatedAt        time.Time
}

func (u User) IsAuthorized() bool {
	return strings.TrimSpace(u.AccessToken) != "" && strings.TrimSpace(u.RefreshToken) != ""
}

func (u User) DigestTimeRaw() string {
	return strings.Join(u.DigestTimes, ",")
}

type SessionMessage struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type Session struct {
	ID         string
	TgUserID   int64
	Title      string
	Messages   []SessionMessage
	CreatedAt  time.Time
	LastActive time.Time
	IsActive   bool
}

type SessionSummary struct {
	ID         string
	Title      string
	LastActive time.Time
	IsActive   bool
}

func (s *Store) EnsureUser(ctx context.Context, tgUserID int64) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO users (tg_user_id) VALUES (?) ON CONFLICT(tg_user_id) DO NOTHING`,
		tgUserID,
	)
	return err
}

func (s *Store) GetUser(ctx context.Context, tgUserID int64) (User, error) {
	var (
		u            User
		gmailAddr    sql.NullString
		accessToken  sql.NullString
		refreshToken sql.NullString
		tokenExpiry  sql.NullString
		digestTime   sql.NullString
		aiPush       sql.NullInt64
		createdAt    sql.NullString
	)

	err := s.db.QueryRowContext(
		ctx,
		`SELECT tg_user_id, gmail_address, access_token, refresh_token, token_expiry,
		        digest_time, check_interval_min, ai_push_enabled, created_at
		   FROM users WHERE tg_user_id = ?`,
		tgUserID,
	).Scan(
		&u.TgUserID,
		&gmailAddr,
		&accessToken,
		&refreshToken,
		&tokenExpiry,
		&digestTime,
		&u.CheckIntervalMin,
		&aiPush,
		&createdAt,
	)
	if err != nil {
		return User{}, err
	}

	u.GmailAddress = strings.TrimSpace(gmailAddr.String)
	u.AccessToken = strings.TrimSpace(accessToken.String)
	u.RefreshToken = strings.TrimSpace(refreshToken.String)
	u.DigestTimes = parseDigestTimes(digestTime.String)
	u.AIPushEnabled = aiPush.Int64 == 1
	u.TokenExpiry = parseSQLiteTime(tokenExpiry.String)
	u.CreatedAt = parseSQLiteTime(createdAt.String)
	return u, nil
}

func (s *Store) SaveUserTokens(
	ctx context.Context,
	tgUserID int64,
	gmailAddress string,
	accessToken string,
	refreshToken string,
	expiry time.Time,
) error {
	if err := s.EnsureUser(ctx, tgUserID); err != nil {
		return err
	}
	expiryText := ""
	if !expiry.IsZero() {
		expiryText = expiry.UTC().Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE users
		   SET gmail_address = ?,
		       access_token = ?,
		       refresh_token = CASE WHEN ? <> '' THEN ? ELSE refresh_token END,
		       token_expiry = ?
		 WHERE tg_user_id = ?`,
		strings.TrimSpace(gmailAddress),
		strings.TrimSpace(accessToken),
		strings.TrimSpace(refreshToken),
		strings.TrimSpace(refreshToken),
		expiryText,
		tgUserID,
	)
	return err
}

func (s *Store) ClearUserTokens(ctx context.Context, tgUserID int64) error {
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE users
		   SET gmail_address = '',
		       access_token = '',
		       refresh_token = '',
		       token_expiry = NULL
		 WHERE tg_user_id = ?`,
		tgUserID,
	)
	return err
}

func (s *Store) SetDigestTimes(ctx context.Context, tgUserID int64, times []string) error {
	if err := s.EnsureUser(ctx, tgUserID); err != nil {
		return err
	}
	raw := strings.Join(times, ",")
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE users SET digest_time = ? WHERE tg_user_id = ?`,
		raw,
		tgUserID,
	)
	return err
}

func (s *Store) SetAIPushEnabled(ctx context.Context, tgUserID int64, enabled bool) error {
	if err := s.EnsureUser(ctx, tgUserID); err != nil {
		return err
	}
	v := 0
	if enabled {
		v = 1
	}
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE users SET ai_push_enabled = ? WHERE tg_user_id = ?`,
		v,
		tgUserID,
	)
	return err
}

func (s *Store) SetCheckInterval(ctx context.Context, tgUserID int64, minutes int) error {
	if minutes < 0 {
		return errors.New("check interval must be >= 0")
	}
	if err := s.EnsureUser(ctx, tgUserID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(
		ctx,
		`UPDATE users SET check_interval_min = ? WHERE tg_user_id = ?`,
		minutes,
		tgUserID,
	)
	return err
}

func (s *Store) ListAuthorizedUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT tg_user_id, gmail_address, access_token, refresh_token, token_expiry,
		        digest_time, check_interval_min, ai_push_enabled, created_at
		   FROM users
		  WHERE access_token <> '' AND refresh_token <> ''`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var (
			u            User
			gmailAddr    sql.NullString
			accessToken  sql.NullString
			refreshToken sql.NullString
			tokenExpiry  sql.NullString
			digestTime   sql.NullString
			aiPush       sql.NullInt64
			createdAt    sql.NullString
		)
		if scanErr := rows.Scan(
			&u.TgUserID,
			&gmailAddr,
			&accessToken,
			&refreshToken,
			&tokenExpiry,
			&digestTime,
			&u.CheckIntervalMin,
			&aiPush,
			&createdAt,
		); scanErr != nil {
			return nil, scanErr
		}
		u.GmailAddress = strings.TrimSpace(gmailAddr.String)
		u.AccessToken = strings.TrimSpace(accessToken.String)
		u.RefreshToken = strings.TrimSpace(refreshToken.String)
		u.TokenExpiry = parseSQLiteTime(tokenExpiry.String)
		u.DigestTimes = parseDigestTimes(digestTime.String)
		u.AIPushEnabled = aiPush.Int64 == 1
		u.CreatedAt = parseSQLiteTime(createdAt.String)
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) MarkEmailSeen(ctx context.Context, tgUserID int64, emailID string) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO seen_emails (tg_user_id, email_id) VALUES (?, ?)
		 ON CONFLICT(tg_user_id, email_id) DO NOTHING`,
		tgUserID,
		strings.TrimSpace(emailID),
	)
	return err
}

func (s *Store) IsEmailSeen(ctx context.Context, tgUserID int64, emailID string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT 1 FROM seen_emails WHERE tg_user_id = ? AND email_id = ? LIMIT 1`,
		tgUserID,
		strings.TrimSpace(emailID),
	).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) CreateSession(ctx context.Context, tgUserID int64, title string) (Session, error) {
	if err := s.EnsureUser(ctx, tgUserID); err != nil {
		return Session{}, err
	}
	if strings.TrimSpace(title) == "" {
		title = "新会话 " + time.Now().Format("01-02 15:04")
	}
	now := time.Now().UTC().Format(time.RFC3339)
	sessionID := uuid.NewString()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, err
	}
	defer rollback(tx)

	if _, err = tx.ExecContext(ctx, `UPDATE sessions SET is_active = 0 WHERE tg_user_id = ?`, tgUserID); err != nil {
		return Session{}, err
	}
	if _, err = tx.ExecContext(
		ctx,
		`INSERT INTO sessions (id, tg_user_id, title, messages, created_at, last_active, is_active)
		 VALUES (?, ?, ?, '[]', ?, ?, 1)`,
		sessionID,
		tgUserID,
		strings.TrimSpace(title),
		now,
		now,
	); err != nil {
		return Session{}, err
	}
	if err = tx.Commit(); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, sessionID)
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (Session, error) {
	return s.getSessionBy(
		ctx,
		`SELECT id, tg_user_id, title, messages, created_at, last_active, is_active
		   FROM sessions WHERE id = ? LIMIT 1`,
		sessionID,
	)
}

func (s *Store) GetOrCreateActiveSession(ctx context.Context, tgUserID int64) (Session, error) {
	session, err := s.getSessionBy(
		ctx,
		`SELECT id, tg_user_id, title, messages, created_at, last_active, is_active
		   FROM sessions
		  WHERE tg_user_id = ? AND is_active = 1
		  ORDER BY last_active DESC
		  LIMIT 1`,
		tgUserID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return s.CreateSession(ctx, tgUserID, "默认会话")
	}
	return session, err
}

func (s *Store) ListSessions(ctx context.Context, tgUserID int64, limit int) ([]SessionSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, title, last_active, is_active
		   FROM sessions
		  WHERE tg_user_id = ?
		  ORDER BY last_active DESC
		  LIMIT ?`,
		tgUserID,
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionSummary
	for rows.Next() {
		var (
			item       SessionSummary
			lastActive sql.NullString
			isActive   int
		)
		if scanErr := rows.Scan(&item.ID, &item.Title, &lastActive, &isActive); scanErr != nil {
			return nil, scanErr
		}
		item.LastActive = parseSQLiteTime(lastActive.String)
		item.IsActive = isActive == 1
		sessions = append(sessions, item)
	}
	return sessions, rows.Err()
}

func (s *Store) ResolveSessionID(ctx context.Context, tgUserID int64, prefix string) (string, error) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return "", errors.New("empty session id")
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id FROM sessions WHERE tg_user_id = ? AND id LIKE ?`,
		tgUserID,
		prefix+"%",
	)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var matched []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			return "", scanErr
		}
		matched = append(matched, id)
	}
	if len(matched) == 0 {
		return "", sql.ErrNoRows
	}
	if len(matched) > 1 {
		return "", fmt.Errorf("multiple sessions matched prefix %q", prefix)
	}
	return matched[0], nil
}

func (s *Store) SwitchActiveSession(ctx context.Context, tgUserID int64, sessionID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer rollback(tx)

	if _, err = tx.ExecContext(ctx, `UPDATE sessions SET is_active = 0 WHERE tg_user_id = ?`, tgUserID); err != nil {
		return err
	}
	res, err := tx.ExecContext(
		ctx,
		`UPDATE sessions
		    SET is_active = 1, last_active = ?
		  WHERE tg_user_id = ? AND id = ?`,
		time.Now().UTC().Format(time.RFC3339),
		tgUserID,
		sessionID,
	)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) ClearActiveSessionMessages(ctx context.Context, tgUserID int64) error {
	session, err := s.GetOrCreateActiveSession(ctx, tgUserID)
	if err != nil {
		return err
	}
	return s.UpdateSessionMessages(ctx, session.ID, []SessionMessage{})
}

func (s *Store) AppendActiveSessionMessage(ctx context.Context, tgUserID int64, role string, content string) (Session, error) {
	session, err := s.GetOrCreateActiveSession(ctx, tgUserID)
	if err != nil {
		return Session{}, err
	}
	messages := append(session.Messages, SessionMessage{
		Role:      strings.TrimSpace(role),
		Content:   strings.TrimSpace(content),
		CreatedAt: time.Now().UTC(),
	})
	if len(messages) > maxSessionMessages {
		messages = messages[len(messages)-maxSessionMessages:]
	}
	if err := s.UpdateSessionMessages(ctx, session.ID, messages); err != nil {
		return Session{}, err
	}
	return s.GetSession(ctx, session.ID)
}

func (s *Store) UpdateSessionMessages(ctx context.Context, sessionID string, messages []SessionMessage) error {
	payload, err := json.Marshal(messages)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(
		ctx,
		`UPDATE sessions
		    SET messages = ?, last_active = ?
		  WHERE id = ?`,
		string(payload),
		time.Now().UTC().Format(time.RFC3339),
		sessionID,
	)
	return err
}

func (s *Store) getSessionBy(ctx context.Context, query string, args ...any) (Session, error) {
	var (
		item       Session
		messages   string
		createdAt  sql.NullString
		lastActive sql.NullString
		isActive   int
	)
	err := s.db.QueryRowContext(ctx, query, args...).Scan(
		&item.ID,
		&item.TgUserID,
		&item.Title,
		&messages,
		&createdAt,
		&lastActive,
		&isActive,
	)
	if err != nil {
		return Session{}, err
	}
	item.CreatedAt = parseSQLiteTime(createdAt.String)
	item.LastActive = parseSQLiteTime(lastActive.String)
	item.IsActive = isActive == 1
	item.Messages = decodeMessages(messages)
	return item, nil
}

func decodeMessages(raw string) []SessionMessage {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []SessionMessage{}
	}
	var messages []SessionMessage
	if err := json.Unmarshal([]byte(raw), &messages); err != nil {
		return []SessionMessage{}
	}
	if len(messages) > maxSessionMessages {
		return messages[len(messages)-maxSessionMessages:]
	}
	return messages
}

func parseDigestTimes(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	for _, t := range strings.Split(raw, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseSQLiteTime(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		if t, err := time.Parse(layout, raw); err == nil {
			return t
		}
	}
	return time.Time{}
}

func rollback(tx *sql.Tx) {
	if tx != nil {
		_ = tx.Rollback()
	}
}
