// Package server is the operator-facing admin workspace and public
// recipient-tracking surface, implementing the Draft -> Preview -> Test ->
// Approve -> Release -> Close workflow.
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
	"github.com/rudra184/baitbet/internal/scoring"
	"github.com/rudra184/baitbet/internal/secrets"
	"github.com/rudra184/baitbet/internal/store"
)

type Server struct {
	Store       *store.Store
	MailProfile mailer.Profile // the standing fallback profile, used when a campaign names no org mailer profile or names one that can't be found
	Secrets     *secrets.Box   // encrypts/decrypts mailer_profiles.smtp_password_encrypted
	PublicBase  string         // e.g. http://127.0.0.1:8765 - used to build tracking links

	// HandoffSecret, when set, requires every admin route to carry a valid
	// signed handoff token (internal/authn) minted by the host app. Empty
	// means standalone: every request belongs to DefaultOrgID, no token needed.
	HandoffSecret string
	DefaultOrgID  string

	// BrowserWSURL is the headless Chrome devtools endpoint
	// internal/browserclone renders through. Empty disables "Clone a
	// webpage" with a clear error instead of crashing.
	BrowserWSURL string

	tmpl *template.Template
}

func New(st *store.Store, mp mailer.Profile, sec *secrets.Box, publicBase, handoffSecret, defaultOrgID, browserWSURL string) *Server {
	return &Server{
		Store: st, MailProfile: mp, Secrets: sec, PublicBase: publicBase,
		HandoffSecret: handoffSecret, DefaultOrgID: defaultOrgID,
		BrowserWSURL: browserWSURL,
		tmpl:         parseTemplates(),
	}
}

type ctxKey int

const orgIDCtxKey ctxKey = 0

func orgIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(orgIDCtxKey).(string)
	return id
}

// withOrg is the ONLY place that trusts a request's claimed org identity;
// every handler reads it back from the context, never a shared field, so
// one process can safely serve more than one organization.
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

// ownedCampaign reports a cross-org mismatch as "not found", not
// "forbidden" - a token scoped to one org must not learn another org's campaign exists.
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

	// Ensures organizations(id) exists before a token naming it is used
	// elsewhere (campaigns.organization_id has a hard FK). Idempotent.
	mux.HandleFunc("POST /internal/sync-org", s.withOrg(s.handleSyncOrg))

	// Public, no admin auth - the tracking token itself is the credential.
	mux.HandleFunc("GET /t/{token}", s.handleTrack)
	mux.HandleFunc("POST /t/{token}/submit", s.handleTrackSubmit)
	mux.HandleFunc("GET /t/{token}/open.gif", s.handleTrackOpen)
	mux.HandleFunc("GET /t/{token}/report", s.handleTrackReport)

	mux.HandleFunc("GET /healthz", s.handleHealthz)

	s.apiRoutes(mux)

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
	// orgIDFrom, not the request body - a caller can't name an arbitrary org id.
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

// unresolvedChecks surfaces what's blocking progress to the next step.
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

// resolveMailerProfile falls back to the server's default when the
// campaign names no profile, it can't be found, or no Secrets box is configured.
func (s *Server) resolveMailerProfile(ctx context.Context, orgID, name string) mailer.Profile {
	if name == "" || s.Secrets == nil {
		return s.MailProfile
	}
	mp, err := s.Store.GetMailerProfile(ctx, orgID, name)
	if err != nil {
		return s.MailProfile
	}
	password, err := s.Secrets.Decrypt(mp.SMTPPasswordEnc)
	if err != nil {
		log.Printf("mailer profile %q: decrypt failed, falling back to default: %v", name, err)
		return s.MailProfile
	}
	return mailer.Profile{
		Name: mp.Name, Host: mp.SMTPHost, From: mp.FromEmail,
		NoAuth: mp.NoAuth, Username: mp.SMTPUsername, Password: password,
	}
}

// sendTo is shared between the HTML and JSON test-send/release handlers
// so the two encodings can't drift in behavior.
func (s *Server) sendTo(r *http.Request, orgID, campaignID, mailerProfileName, addr string, pack content.Pack, isTest bool) error {
	rec, found, err := s.Store.FindRecipientByEmail(r.Context(), campaignID, addr)
	if err != nil {
		return err
	}
	if !found || isTest {
		rec = store.Recipient{ID: newID("rec"), CampaignID: campaignID, Email: addr, IsTest: isTest, Token: newToken()}
		if err := s.Store.AddRecipient(r.Context(), rec); err != nil {
			return err
		}
	}
	link := fmt.Sprintf("%s/t/%s", s.PublicBase, rec.Token)
	html, err := render.EmailDelivery(pack, link)
	if err != nil {
		return err
	}
	profile := s.resolveMailerProfile(r.Context(), orgID, mailerProfileName)
	sendErr := mailer.Send(profile, addr, pack.Email.Subject, html)
	_ = s.Store.MarkSent(r.Context(), rec.ID, sendErr)
	if sendErr != nil {
		log.Printf("send to %s failed: %v", addr, sendErr)
	} else if !isTest {
		_ = s.Store.RecordEvent(r.Context(), rec.ID, "mail_accepted", "")
	}
	return nil
}

func releaseExpiry() time.Time { return time.Now().Add(14 * 24 * time.Hour) }

// releaseAudience sends to extraAddrs (registering each as a new audience
// row) plus every unsent pre-registered audience row - supports both a
// direct caller's inline recipient list and a UI's register-then-release flow.
func (s *Server) releaseAudience(r *http.Request, c store.Campaign, extraAddrs []string) error {
	pack := packFromCampaign(c)
	for _, addr := range extraAddrs {
		if err := s.sendTo(r, c.OrganizationID, c.ID, c.MailerProfile, addr, pack, false); err != nil {
			return err
		}
	}
	// Queried after extraAddrs is sent, so any overlap is excluded rather than sent twice.
	pending, err := s.Store.ListUnsentAudience(r.Context(), c.ID)
	if err != nil {
		return err
	}
	for _, rec := range pending {
		if err := s.sendTo(r, c.OrganizationID, c.ID, c.MailerProfile, rec.Email, pack, false); err != nil {
			return err
		}
	}
	return nil
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
		if err := s.sendTo(r, c.OrganizationID, id, c.MailerProfile, addr, pack, true); s.fail(w, err) {
			return
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

	if err := s.Store.Release(r.Context(), id, releaseExpiry()); s.failConflict(w, err) {
		return
	}

	addrs := strings.Fields(strings.ReplaceAll(r.FormValue("recipients"), ",", " "))
	if err := s.releaseAudience(r, c, addrs); s.fail(w, err) {
		return
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
	fmt.Fprintln(w, "email,full_name,department,location,is_test,sent_at,clicked,submitted,risk_level,send_error,token")
	for _, rec := range recipients {
		sentAt := ""
		if rec.SentAt != nil {
			sentAt = rec.SentAt.Format(time.RFC3339)
		}
		errText := ""
		if rec.SendError != nil {
			errText = *rec.SendError
		}
		fmt.Fprintf(w, "%s,%s,%s,%s,%t,%s,%t,%t,%s,%s,%s\n",
			rec.Email, rec.FullName, rec.Department, rec.Location, rec.IsTest, sentAt,
			rec.ClickCount > 0, rec.SubmissionCount > 0, rec.RiskLevel, errText, rec.Token)
	}
}

// --- Public recipient tracking ---

// resolveActiveToken: org enabled, campaign past "draft", not expired. Any
// failure reports identically (a neutral closed response), so a dead or
// expired token looks the same as one that never existed.
func (s *Server) resolveActiveToken(ctx context.Context, token string) (store.Recipient, store.Campaign, bool) {
	rec, err := s.Store.GetRecipientByToken(ctx, token)
	if err != nil {
		return store.Recipient{}, store.Campaign{}, false
	}
	c, err := s.Store.GetCampaign(ctx, rec.CampaignID)
	if err != nil {
		return store.Recipient{}, store.Campaign{}, false
	}
	org, err := s.Store.GetOrganization(ctx, c.OrganizationID)
	if err != nil || !org.Enabled {
		return store.Recipient{}, store.Campaign{}, false
	}
	if c.Status == "draft" {
		return store.Recipient{}, store.Campaign{}, false
	}
	if c.ExpiresAt != nil && time.Now().After(*c.ExpiresAt) {
		return store.Recipient{}, store.Campaign{}, false
	}
	return rec, c, true
}

// clientIP prefers X-Forwarded-For (behind a TLS-terminating proxy).
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return strings.TrimSpace(strings.SplitN(xff, ",", 2)[0])
	}
	return r.RemoteAddr
}

// deviceFromUA is a coarse classification for the Live Timeline's device column.
func deviceFromUA(ua string) string {
	lower := strings.ToLower(ua)
	switch {
	case strings.Contains(lower, "ipad") || strings.Contains(lower, "tablet"):
		return "Tablet"
	case strings.Contains(lower, "mobi") || strings.Contains(lower, "android") || strings.Contains(lower, "iphone"):
		return "Mobile"
	case ua == "":
		return ""
	default:
		return "Desktop"
	}
}

// recordTrackingEvent records one event, then recomputes risk so User Risk stays current.
func (s *Server) recordTrackingEvent(r *http.Request, rec store.Recipient, eventType string) {
	ctx := r.Context()
	ua := r.UserAgent()
	var secs float64
	hasSecs := rec.SentAt != nil
	if hasSecs {
		secs = time.Since(*rec.SentAt).Seconds()
	}
	isScanner := scoring.IsScanner(ua, secs, hasSecs, rec.ClickCount > 0 || rec.SubmissionCount > 0)
	confidence := 1.0
	if isScanner {
		confidence = 0.5
	}
	_ = s.Store.RecordEventDetailed(ctx, rec.ID, eventType, "", clientIP(r), ua, deviceFromUA(ua), "", isScanner, confidence)
	s.recomputeRisk(ctx, rec.ID)
}

// recomputeRisk is called after every event this service records.
func (s *Server) recomputeRisk(ctx context.Context, recipientID string) {
	rec, err := s.Store.GetRecipient(ctx, recipientID)
	if err != nil {
		return
	}
	storeEvents, err := s.Store.RecipientEvents(ctx, recipientID)
	if err != nil {
		return
	}
	events := make([]scoring.Event, len(storeEvents))
	for i, e := range storeEvents {
		events[i] = scoring.Event{EventType: e.EventType, IsScannerEvent: e.IsScannerEvent}
	}
	trainingCompleted := false
	remediations, err := s.Store.RemediationsForRecipient(ctx, recipientID)
	if err == nil {
		for _, rem := range remediations {
			if rem.Type == "training" && rem.Status == "completed" {
				trainingCompleted = true
			}
		}
	}
	score, band := scoring.ComputeRecipientRisk(events, rec.SubmissionCount, rec.Reported, trainingCompleted)
	_ = s.Store.UpdateRisk(ctx, recipientID, score, band)
}

func (s *Server) handleTrack(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	rec, c, ok := s.resolveActiveToken(r.Context(), token)
	if !ok {
		serveNeutralClosed(w)
		return
	}

	_ = s.Store.IncrementClickCount(r.Context(), rec.ID)
	s.recordTrackingEvent(r, rec, "link_clicked")

	if c.LandingPageID != nil {
		lp, err := s.Store.GetLandingPage(r.Context(), *c.LandingPageID)
		if err == nil && (lp.CaptureUsername || lp.CapturePassword) {
			submitAction := fmt.Sprintf("%s/t/%s/submit", s.PublicBase, token)
			var out string
			if lp.HTML != "" {
				// Browser-cloned page: splice the real form into its stored markup.
				out, err = render.LandingFromClone(lp.HTML, render.LandingPage{
					CaptureUsername: lp.CaptureUsername, CapturePassword: lp.CapturePassword,
					SubmitAction: submitAction,
				})
			} else {
				out, err = render.Landing(render.LandingPage{
					Title: lp.Name, BrandColor: lp.BrandColor, LogoAssetKey: lp.LogoAssetKey,
					CaptureUsername: lp.CaptureUsername, CapturePassword: lp.CapturePassword,
					SubmitAction: submitAction,
				})
			}
			if err == nil {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write([]byte(out))
				return
			}
		}
	}

	s.serveDebrief(w, r, rec, c)
}

func (s *Server) serveDebrief(w http.ResponseWriter, r *http.Request, rec store.Recipient, c store.Campaign) {
	s.recordTrackingEvent(r, rec, "debrief_viewed")
	out, err := render.Debrief(packFromCampaign(c))
	if err != nil {
		serveNeutralClosed(w)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(out))
}

// handleTrackSubmit never persists a password value - only whether one was
// present ("password_field_used"). Redirects to redirect_url if set, else the debrief.
func (s *Server) handleTrackSubmit(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	rec, c, ok := s.resolveActiveToken(r.Context(), token)
	if !ok {
		serveNeutralClosed(w)
		return
	}
	if err := r.ParseForm(); err != nil {
		serveNeutralClosed(w)
		return
	}

	var lp store.LandingPage
	if c.LandingPageID != nil {
		lp, _ = s.Store.GetLandingPage(r.Context(), *c.LandingPageID)
	}

	var capturedUsername *string
	if lp.CaptureUsername {
		if v := r.FormValue("username"); v != "" {
			capturedUsername = &v
		}
	}
	_ = s.Store.RecordSubmission(r.Context(), rec.ID, capturedUsername)
	s.recordTrackingEvent(r, rec, "form_submitted")
	if lp.CapturePassword && r.FormValue("password") != "" {
		s.recordTrackingEvent(r, rec, "password_field_used")
	}

	if lp.RedirectURL != "" && (strings.HasPrefix(lp.RedirectURL, "http://") || strings.HasPrefix(lp.RedirectURL, "https://")) {
		http.Redirect(w, r, lp.RedirectURL, http.StatusSeeOther)
		return
	}
	s.serveDebrief(w, r, rec, c)
}

// handleTrackOpen's pixel is served even for a dead/expired token (records
// nothing), so a broken link can't be distinguished from a real one.
var transparentGIF = []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00, 0x80, 0x00, 0x00,
	0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0x21, 0xf9, 0x04, 0x01, 0x00, 0x00, 0x00, 0x00, 0x2c, 0x00, 0x00,
	0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0x02, 0x02, 0x44, 0x01, 0x00, 0x3b}

func (s *Server) handleTrackOpen(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if rec, _, ok := s.resolveActiveToken(r.Context(), token); ok {
		_ = s.Store.MarkOpened(r.Context(), rec.ID)
		s.recordTrackingEvent(r, rec, "email_opened")
	}
	w.Header().Set("Content-Type", "image/gif")
	w.Write(transparentGIF)
}

// handleTrackReport lets a recipient self-report, lowering their risk score.
func (s *Server) handleTrackReport(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	rec, _, ok := s.resolveActiveToken(r.Context(), token)
	if !ok {
		serveNeutralClosed(w)
		return
	}
	_ = s.Store.MarkReported(r.Context(), rec.ID)
	s.recordTrackingEvent(r, rec, "user_reported_email")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8"><title>Thank you</title></head>` +
		`<body style="font-family:sans-serif;max-width:480px;margin:80px auto;text-align:center;color:#444;">` +
		`<p>Thanks for reporting this - that's exactly the right call.</p></body></html>`))
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

// failNotFound reports any ownedCampaign error as 404, so a real "no such
// row" and a cross-org mismatch are indistinguishable to the caller.
func (s *Server) failNotFound(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	http.Error(w, "not found", http.StatusNotFound)
	return true
}

// failConflict is for an expected status-guard rejection, not a server fault - 409, not 500.
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
