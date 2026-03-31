package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	mysql "github.com/go-sql-driver/mysql"
)

var storeTestDBCounter atomic.Uint64

func newTestStore(t *testing.T) *Store {
	t.Helper()
	rawDSN := strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	if rawDSN == "" {
		t.Fatal("TEST_DB_DSN is required")
	}
	cfg, err := mysql.ParseDSN(rawDSN)
	if err != nil {
		t.Fatalf("parse TEST_DB_DSN failed: %v", err)
	}
	if strings.TrimSpace(cfg.DBName) == "" {
		t.Fatal("TEST_DB_DSN must include database name")
	}

	adminCfg := *cfg
	adminCfg.DBName = ""
	adminDB, err := sql.Open("mysql", adminCfg.FormatDSN())
	if err != nil {
		t.Fatalf("open mysql admin connection failed: %v", err)
	}
	t.Cleanup(func() {
		_ = adminDB.Close()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := adminDB.PingContext(ctx); err != nil {
		t.Fatalf("ping mysql admin connection failed: %v", err)
	}

	dbName := uniqueStoreTestDBName(cfg.DBName)
	if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE `"+dbName+"` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatalf("create test database failed: %v", err)
	}

	testCfg := *cfg
	testCfg.DBName = dbName
	st, err := Init(testCfg.FormatDSN())
	if err != nil {
		_, _ = adminDB.ExecContext(context.Background(), "DROP DATABASE IF EXISTS `"+dbName+"`")
		t.Fatalf("init test store failed: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
		dropCtx, dropCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer dropCancel()
		_, _ = adminDB.ExecContext(dropCtx, "DROP DATABASE IF EXISTS `"+dbName+"`")
	})
	return st
}

func uniqueStoreTestDBName(base string) string {
	base = sanitizeStoreTestDBName(base)
	seq := storeTestDBCounter.Add(1)
	name := fmt.Sprintf("%s_%d_%d", base, time.Now().UnixNano(), seq)
	if len(name) <= 64 {
		return name
	}
	return name[:64]
}

func sanitizeStoreTestDBName(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return "gmailbot_test"
	}
	var b strings.Builder
	for _, r := range input {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "gmailbot_test"
	}
	if len(out) > 40 {
		return out[:40]
	}
	return out
}
