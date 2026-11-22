// Command server runs the simulation service's operator workspace and
// public tracking endpoints against a real Postgres database and a real
// (local, by default) SMTP sink.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/rudra184/baitbet/internal/mailer"
	"github.com/rudra184/baitbet/internal/server"
	"github.com/rudra184/baitbet/internal/store"
)

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	ctx := context.Background()

	dsn := env("SIM_DB_URL", "postgres://sim_user:sim_password@127.0.0.1:5434/simulation_service?sslmode=disable")
	st, err := store.Open(ctx, dsn)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer st.Close()

	if err := st.Migrate(ctx); err != nil {
		log.Fatalf("migrate: %v", err)
	}

	orgID := env("SIM_ORG_ID", "org_local_demo")
	if err := st.UpsertOrganization(ctx, orgID, env("SIM_ORG_NAME", "Local demo org"), true); err != nil {
		log.Fatalf("seed org: %v", err)
	}

	mp := mailer.Profile{
		Name:   env("SIM_MAILER_NAME", "local-sink"),
		Host:   env("SIM_SMTP_HOST", "127.0.0.1:1025"),
		From:   env("SIM_MAIL_FROM", "it-helpdesk@sim.local"),
		NoAuth: parseBool(env("SIM_SMTP_NO_AUTH", "true")),
	}

	publicBase := env("SIM_PUBLIC_BASE", "http://127.0.0.1:8766")
	addr := env("SIM_LISTEN_ADDR", "127.0.0.1:8766")

	// Empty SIM_HANDOFF_SECRET = standalone mode: every request is treated
	// as orgID, no token required. Set it to require every admin request to
	// carry a valid signed handoff token minted by whatever host application
	// is embedding this service (see internal/authn and ARCHITECTURE.md).
	handoffSecret := os.Getenv("SIM_HANDOFF_SECRET")

	srv := server.New(st, mp, publicBase, handoffSecret, orgID)

	mode := "standalone (no handoff token required)"
	if handoffSecret != "" {
		mode = "integrated (handoff token required on every admin route)"
	}
	log.Printf("baitbet listening on http://%s (org=%s, mailer=%s@%s, mode=%s)", addr, orgID, mp.Name, mp.Host, mode)
	log.Fatal(http.ListenAndServe(addr, srv.Routes()))
}

func parseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}
