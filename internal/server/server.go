// Package server is the operator-facing admin workspace and the public
// recipient-tracking surface for one local run of the simulation service.
// It implements the Draft -> Preview -> Test -> Approve -> Release -> Close
// workflow from the Quality and Workflow Plan v2, scoped to what's provable
// locally: real DB-backed state, real SMTP send through a local sink, a
// real tracking link, real event recording.
package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/rudra184/baitbet/internal/authn"
	"github.com/rudra184/baitbet/internal/content"
	"github.com/rudra184/baitbet/internal/mailer"
	"github.com/rudra184/baitbet/internal/render"
	"github.com/rudra184/baitbet/internal/store"
)

type Server struct {
	Store       *store.Store
	MailProfile mailer.Profile
	PublicBase  string // e.g. http://127.0.0.1:8765 - used to build tracking links

	// HandoffSecret, when set, requires every admin route to carry a valid
	// signed handoff token (internal/authn) minted by a host application
	// after its own auth/entitlement checks already passed (see
	// ARCHITECTURE.md). When empty, the service runs standalone: every
	// admin request is treated as belonging to DefaultOrgID, no token
	// required. The two modes share every other code path - standalone
	// mode is not a separate, less-tested branch.
	HandoffSecret string
	DefaultOrgID  string

	tmpl *template.Template
}

func New(st *store.Store, mp mailer.Profile, publicBase, handoffSecret, defaultOrgID string) *Server {
	return &Server{
		Store: st, MailProfile: mp, PublicBase: publicBase,
		HandoffSecret: handoffSecret, DefaultOrgID: defaultOrgID,
		tmpl: parseTemplates(),
	}
}

type ctxKey int

const orgIDCtxKey ctxKey = 0

func orgIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(orgIDCtxKey).(string)
	return id
}

// withOrg resolves the org this request is allowed to act as - from a
// verified handoff token in integrated mode, or DefaultOrgID in standalone
// mode - before the wrapped handler runs. It is the ONLY place that trusts
// a request's claimed identity; every handler below reads the org back out
// of the request context rather than a shared Server field, so one
// process can safely serve more than one organization.
func (s *Server) withOrg(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		orgID := s.DefaultOrgID
		if s.HandoffSecret != "" {
			claims, err := authn.FromHeader(r, s.HandoffSecret)
			if err != nil {
				http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
				return
			}
			orgID = claims.OrgID
		}
		next(w, r.WithContext(context.WithValue(r.Context(), orgIDCtxKey, orgID)))
	}
}

// ownedCampaign loads a campaign and confirms it belongs to orgID. A
// mismatch is reported as "not found", not "forbidden" - a client token
// scoped to one org must not be able to learn that a campaign ID exists
// in a different org.
func (s *Server) ownedCampaign(ctx context.Context, orgID, id string) (store.Campaign, error) {
	c, err := s.Store.GetCampaign(ctx, id)
	if err != nil {
		return store.Campaign{}, err
	}
	if c.OrganizationID != orgID {
		return store.Campaign{}, errNotFound
	}
	return c, nil
}

var errNotFound = errors.New("not found")

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.withOrg(s.handleIndex))
	mux.HandleFunc("GET /campaigns/new", s.withOrg(s.handleNewForm))
	mux.HandleFunc("POST /campaigns", s.withOrg(s.handleCreate))
	mux.HandleFunc("GET /campaigns/{id}", s.withOrg(s.handleWorkspace))
	mux.HandleFunc("POST /campaigns/{id}/draft", s.withOrg(s.handleSaveDraft))
	mux.HandleFunc("GET /campaigns/{id}/preview/email", s.withOrg(s.handlePreviewEmail))
	mux.HandleFunc("GET /campaigns/{id}/preview/debrief", s.withOrg(s.handlePreviewDebrief))
	mux.HandleFunc("POST /campaigns/{id}/test-send", s.withOrg(s.handleTestSend))
	mux.HandleFunc("POST /campaigns/{id}/approve", s.withOrg(s.handleApprove))
	mux.HandleFunc("POST /campaigns/{id}/release", s.withOrg(s.handleRelease))
	mux.HandleFunc("POST /campaigns/{id}/close", s.withOrg(s.handleCloseCampaign))
	mux.HandleFunc("GET /campaigns/{id}/report.csv", s.withOrg(s.handleReportCSV))

	// Called by the embedding platform before/alongside a user's first admin
	// request for an org baitbet has never seen - campaigns.organization_id
	// has a hard FK to organizations(id), so a token naming an org with no
	// row here would otherwise fail every write with a constraint violation.
	// Idempotent; cheap enough to call on every request rather than caching
	// "already synced" state, so a later org rename/enable change is picked
	// up without any extra invalidation logic.
	mux.HandleFunc("POST /internal/sync-org", s.withOrg(s.handleSyncOrg))

	// Recipient-facing, public, no admin auth, no forms, no handoff token -
	// a recipient never has one; the tracking token itself is the credential.
	mux.HandleFunc("GET /t/{token}", s.handleTrack)

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	return mux
}

func (s *Server) handleSyncOrg(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); s.fail(w, err) {
		return
	}
	if body.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	// orgIDFrom, not the request body - a caller can only sync the org its
	// own token names, never an arbitrary id.
	if err := s.Store.UpsertOrganization(r.Context(), orgIDFrom(r.Context()), body.Name, body.Enabled); s.fail(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.Ping(r.Context()); err != nil {
		http.Error(w, "db unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Write([]byte("ok"))
}

// --- Operator workspace ---

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	campaigns, err := s.Store.ListCampaigns(r.Context(), orgIDFrom(r.Context()))
	if s.fail(w, err) {
		return
	}
	s.render(w, "index.html.tmpl", map[string]any{"Campaigns": campaigns})
}

func (s *Server) handleNewForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, "new.html.tmpl", nil)
}

func (s *Server) handleCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); s.fail(w, err) {
		return
	}
	id := newID("camp")
	c := campaignFromForm(r)
	c.ID = id
	c.OrganizationID = orgIDFrom(r.Context())
	c.ContentVersion = newVersion()
	c.Status = "draft"
	if err := s.Store.CreateCampaign(r.Context(), c); s.fail(w, err) {
		return
	}
	http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
}

func (s *Server) handleWorkspace(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id)
	if s.failNotFound(w, err) {
		return
	}
	recipients, err := s.Store.ListRecipients(r.Context(), id)
	if s.fail(w, err) {
		return
	}
	counts, err := s.Store.EventCountsForCampaign(r.Context(), id)
	if s.fail(w, err) {
		return
	}
	unresolved := unresolvedChecks(c, recipients)
	s.render(w, "workspace.html.tmpl", map[string]any{
		"C": c, "Recipients": recipients, "Counts": counts, "Unresolved": unresolved,
	})
}

// unresolvedChecks is the "short list of unresolved checks" the workflow
// plan asks every screen to carry, instead of hiding what's blocking
// progress to the next step.
func unresolvedChecks(c store.Campaign, recipients []store.Recipient) []string {
	var out []string
	if c.Status == "draft" {
		hasTest := false
		for _, r := range recipients {
			if r.IsTest {
				hasTest = true
			}
		}
		if !hasTest {
			out = append(out, "No named test recipients added yet")
		}
		out = append(out, "Not yet sent to a test mailbox")
	}
	if c.Status == "tested" {
		out = append(out, "Awaiting approval")
	}
	if c.Status == "approved" {
		out = append(out, "Awaiting release")
	}
	return out
}

func (s *Server) handleSaveDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); s.fail(w, err) {
		return
	}
	existing, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id)
	if s.failNotFound(w, err) {
		return
	}
	if existing.Status != "draft" {
		http.Error(w, "campaign is not in draft; content is locked once tested", http.StatusConflict)
		return
	}
	c := campaignFromForm(r)
	if err := s.Store.UpdateDraftContent(r.Context(), id, c, newVersion()); s.fail(w, err) {
		return
	}
	http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
}

func campaignFromForm(r *http.Request) store.Campaign {
	return store.Campaign{
		Name:             r.FormValue("name"),
		Scenario:         r.FormValue("scenario"),
		EmailSubject:     r.FormValue("email_subject"),
		EmailBodyHTML:    r.FormValue("email_body"),
		EmailButtonLabel: r.FormValue("email_button_label"),
		EmailFromName:    r.FormValue("email_from_name"),
		DebriefTitle:     r.FormValue("debrief_title"),
		DebriefIntro:     r.FormValue("debrief_intro"),
		DebriefNextSteps: r.FormValue("debrief_next_steps"),
		DebriefClosing:   r.FormValue("debrief_closing"),
		MailerProfile:    r.FormValue("mailer_profile"),
	}
}

func packFromCampaign(c store.Campaign) content.Pack {
	var steps []string
	for _, line := range strings.Split(c.DebriefNextSteps, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			steps = append(steps, t)
		}
	}
	return content.Pack{
		Version:  c.ContentVersion,
		Scenario: c.Scenario,
		Email: content.EmailContent{
			FromName:    c.EmailFromName,
			Subject:     c.EmailSubject,
			BodyHTML:    c.EmailBodyHTML,
			ButtonLabel: c.EmailButtonLabel,
		},
		Debrief: content.DebriefContent{
			Title:        c.DebriefTitle,
			Intro:        c.DebriefIntro,
			NextSteps:    steps,
			ClosingNote:  c.DebriefClosing,
			LogoAssetKey: "mark",
		},
	}
}

func (s *Server) handlePreviewEmail(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), r.PathValue("id"))
	if s.failNotFound(w, err) {
		return
	}
	out, err := render.Email(packFromCampaign(c))
	if s.fail(w, err) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

func (s *Server) handlePreviewDebrief(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), r.PathValue("id"))
	if s.failNotFound(w, err) {
		return
	}
	out, err := render.Debrief(packFromCampaign(c))
	if s.fail(w, err) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

// handleTestSend sends to named test mailboxes only (never real recipients)
// and leaves the campaign in status "tested" - the gate Approve requires.
func (s *Server) handleTestSend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); s.fail(w, err) {
		return
	}
	c, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id)
	if s.failNotFound(w, err) {
		return
	}
	pack := packFromCampaign(c)

	addrs := strings.Fields(strings.ReplaceAll(r.FormValue("test_recipients"), ",", " "))
	if len(addrs) == 0 {
		http.Error(w, "at least one named test recipient is required", http.StatusBadRequest)
		return
	}
	for _, addr := range addrs {
		rec := store.Recipient{ID: newID("rec"), CampaignID: id, Email: addr, IsTest: true, Token: newToken()}
		if err := s.Store.AddRecipient(r.Context(), rec); s.fail(w, err) {
			return
		}
		link := fmt.Sprintf("%s/t/%s", s.PublicBase, rec.Token)
		html, err := render.EmailDelivery(pack, link)
		if s.fail(w, err) {
			return
		}
		sendErr := mailer.Send(s.MailProfile, addr, pack.Email.Subject, html)
		_ = s.Store.MarkSent(r.Context(), rec.ID, sendErr)
		if sendErr != nil {
			log.Printf("test-send to %s failed: %v", addr, sendErr)
		}
	}
	if err := s.Store.SetStatus(r.Context(), id, "tested"); s.fail(w, err) {
		return
	}
	http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
}

func (s *Server) handleApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.failNotFound(w, err) {
		return
	}
	if err := s.Store.Approve(r.Context(), id); s.failConflict(w, err) {
		return
	}
	http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
}

// handleRelease sends to the real named recipient list and is guarded
// server-side against double release (store.Release only succeeds once,
// from status "approved").
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); s.fail(w, err) {
		return
	}
	c, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id)
	if s.failNotFound(w, err) {
		return
	}
	org, err := s.Store.GetOrganization(r.Context(), c.OrganizationID)
	if s.fail(w, err) {
		return
	}
	if !org.Enabled {
		http.Error(w, "organization has this module disabled; cannot release", http.StatusForbidden)
		return
	}

	expires := time.Now().Add(14 * 24 * time.Hour)
	if err := s.Store.Release(r.Context(), id, expires); s.failConflict(w, err) {
		return
	}

	pack := packFromCampaign(c)
	addrs := strings.Fields(strings.ReplaceAll(r.FormValue("recipients"), ",", " "))
	for _, addr := range addrs {
		rec := store.Recipient{ID: newID("rec"), CampaignID: id, Email: addr, IsTest: false, Token: newToken()}
		if err := s.Store.AddRecipient(r.Context(), rec); s.fail(w, err) {
			return
		}
		link := fmt.Sprintf("%s/t/%s", s.PublicBase, rec.Token)
		html, err := render.EmailDelivery(pack, link)
		if s.fail(w, err) {
			return
		}
		sendErr := mailer.Send(s.MailProfile, addr, pack.Email.Subject, html)
		_ = s.Store.MarkSent(r.Context(), rec.ID, sendErr)
		if sendErr == nil {
			_ = s.Store.RecordEvent(r.Context(), rec.ID, "mail_accepted", "")
		} else {
			log.Printf("release send to %s failed: %v", addr, sendErr)
		}
	}
	http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
}

func (s *Server) handleCloseCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.failNotFound(w, err) {
		return
	}
	if err := s.Store.CloseCampaign(r.Context(), id); s.failConflict(w, err) {
		return
	}
	http.Redirect(w, r, "/campaigns/"+id, http.StatusSeeOther)
}

func (s *Server) handleReportCSV(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.failNotFound(w, err) {
		return
	}
	recipients, err := s.Store.ListRecipients(r.Context(), id)
	if s.fail(w, err) {
		return
	}
	w.Header().Set("Content-Type", "text/csv")
	w.Header().Set("Content-Disposition", "attachment; filename=report.csv")
	fmt.Fprintln(w, "email,is_test,sent_at,send_error,token")
	for _, rec := range recipients {
		sentAt := ""
		if rec.SentAt != nil {
			sentAt = rec.SentAt.Format(time.RFC3339)
		}
		errText := ""
		if rec.SendError != nil {
			errText = *rec.SendError
		}
		fmt.Fprintf(w, "%s,%t,%s,%s,%s\n", rec.Email, rec.IsTest, sentAt, errText, rec.Token)
	}
}

// --- Public recipient tracking ---

// handleTrack is the only unauthenticated, public endpoint. It resolves a
// token, checks the owning org and campaign are still active, records an
// event, and serves the form-free debrief - or a neutral closed response
// that reveals nothing and records nothing.
func (s *Server) handleTrack(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	rec, err := s.Store.GetRecipientByToken(r.Context(), token)
	if err != nil {
		serveNeutralClosed(w)
		return
	}
	c, err := s.Store.GetCampaign(r.Context(), rec.CampaignID)
	if err != nil {
		serveNeutralClosed(w)
		return
	}
	org, err := s.Store.GetOrganization(r.Context(), c.OrganizationID)
	if err != nil || !org.Enabled {
		serveNeutralClosed(w)
		return
	}
	if c.Status != "released" && c.Status != "closed" {
		serveNeutralClosed(w)
		return
	}
	if c.ExpiresAt != nil && time.Now().After(*c.ExpiresAt) {
		serveNeutralClosed(w)
		return
	}

	_ = s.Store.RecordEvent(r.Context(), rec.ID, "debrief_viewed", "")
	out, err := render.Debrief(packFromCampaign(c))
	if err != nil {
		serveNeutralClosed(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

func serveNeutralClosed(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Page closed</title></head>` +
		`<body style="font-family:sans-serif;max-width:480px;margin:80px auto;text-align:center;color:#444;">` +
		`<p>This page is no longer active.</p></body></html>`))
}

// --- helpers ---

func (s *Server) fail(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	log.Printf("error: %v", err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return true
}

// failNotFound reports any error from ownedCampaign as 404 - including a
// real "no such row" and a cross-org ownership mismatch alike, since the
// two must be indistinguishable to the caller (see ownedCampaign's doc).
func (s *Server) failNotFound(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	http.Error(w, "not found", http.StatusNotFound)
	return true
}

// failConflict is for store methods whose only error mode is "campaign
// wasn't in the required status" (Approve/Release/CloseCampaign) - an
// expected rejection of a duplicate or out-of-order action, not a server
// fault, so it reports 409 rather than 500.
func (s *Server) failConflict(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	http.Error(w, err.Error(), http.StatusConflict)
	return true
}

func (s *Server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func newID(prefix string) string { return prefix + "_" + randHex(8) }
func newToken() string           { return randHex(16) }
func newVersion() string         { return time.Now().UTC().Format("20060102T150405.000000000") }

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failing means the process environment is broken
	}
	return hex.EncodeToString(b)
}
