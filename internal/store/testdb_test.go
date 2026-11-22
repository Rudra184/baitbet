//go:build integration

package store

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// newTestStore creates a throwaway database against the already-running
// local sim-db (see docker-compose.yml), migrates it, and drops it on
// cleanup. It skips (doesn't fail) the test if SIM_TEST_DB_ADMIN_URL isn't
// set or the DB isn't reachable - these are integration tests, not unit
// tests, and this repo's own test suite needs the local stack up to run
// them, matching docker-compose.yml's documented purpose.
func newTestStore(t *testing.T) *Store {
	t.Helper()
	adminURL := testAdminURL(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	adminPool, err := pgxpool.New(ctx, adminURL)
	if err != nil {
		t.Skipf("store integration test: cannot reach admin DB: %v", err)
	}
	// adminPool is closed at the end of t.Cleanup below, not here - it's
	// needed again after the test body runs, to drop the throwaway database.
	if err := adminPool.Ping(ctx); err != nil {
		t.Skipf("store integration test: cannot ping admin DB: %v", err)
	}

	dbName := fmt.Sprintf("simtest_%d", time.Now().UnixNano())
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatalf("create test database: %v", err)
	}

	testURL := withDatabase(t, adminURL, dbName)
	st, err := Open(ctx, testURL)
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
		// Terminate any lingering connections before dropping - pgxpool.Close
		// above should have released them, but DROP DATABASE fails loudly if
		// anything is still attached.
		adminPool.Exec(dropCtx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname=$1 AND pid <> pg_backend_pid()`, dbName)
		if _, err := adminPool.Exec(dropCtx, "DROP DATABASE IF EXISTS "+dbName); err != nil {
			t.Logf("warning: failed to drop test database %s: %v", dbName, err)
		}
		adminPool.Close()
	})

	return st
}

func testAdminURL(t *testing.T) string {
	t.Helper()
	v := os.Getenv("SIM_TEST_DB_ADMIN_URL")
	if v == "" {
		t.Skip("integration test: SIM_TEST_DB_ADMIN_URL not set")
	}
	return v
}

func withDatabase(t *testing.T, rawURL, dbName string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse admin URL: %v", err)
	}
	u.Path = "/" + dbName
	return u.String()
}
