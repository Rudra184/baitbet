//go:build integration

package server

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rudra184/baitbet/internal/store"
)

// newTestStoreForServer mirrors internal/store's own test helper (each
// package owns its test fixtures; the duplication is small and keeps the
// two packages' tests independent of each other's internals).
func newTestStoreForServer(t *testing.T) *store.Store {
	t.Helper()
	adminURL := os.Getenv("SIM_TEST_DB_ADMIN_URL")
	if adminURL == "" {
		t.Skip("integration test: SIM_TEST_DB_ADMIN_URL not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Skipf("server integration test: cannot reach admin DB: %v", err)
	}
	if err := adminPool.Ping(ctx); err != nil {
		t.Skipf("server integration test: cannot ping admin DB: %v", err)
	}

	dbName := fmt.Sprintf("simtest_srv_%d", time.Now().UnixNano())
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	u, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse admin URL: %v", err)
	}
	u.Path = "/" + dbName

	st, err := store.Open(ctx, u.String())
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate test store: %v", err)
	}

	t.Cleanup(func() {
		st.Close()
		dropCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		adminPool.Exec(dropCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid <> pg_backend_pid()`, dbName)
		if _, err := adminPool.Exec(dropCtx, "DROP DATABASE IF EXISTS "+dbName); err != nil {
			t.Logf("warning: failed to drop test database %s: %v", dbName, err)
		}
		adminPool.Close()
	})

	return st
}
