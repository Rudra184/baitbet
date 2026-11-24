// JSON mirror of the HTML admin routes in server.go, for a host app
// building its own UI against baitbet. Stays a plain mirror of the
// store's own primitives - any shaping or UI-specific decision belongs in
// the caller, not here, so this service stays usable by more than one host app.
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rudra184/baitbet/internal/browserclone"
	"github.com/rudra184/baitbet/internal/content"
	"github.com/rudra184/baitbet/internal/store"
)

func (s *Server) apiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/campaigns", s.withOrg(s.apiListCampaigns))
	mux.HandleFunc("POST /api/campaigns", s.withOrg(s.apiCreateCampaign))
	mux.HandleFunc("GET /api/campaigns/{id}", s.withOrg(s.apiGetCampaign))
	mux.HandleFunc("DELETE /api/campaigns/{id}", s.withOrg(s.apiDeleteCampaign))
	mux.HandleFunc("PUT /api/campaigns/{id}/draft", s.withOrg(s.apiUpdateDraft))
	mux.HandleFunc("GET /api/campaigns/{id}/recipients", s.withOrg(s.apiListRecipients))
	mux.HandleFunc("POST /api/campaigns/{id}/recipients", s.withOrg(s.apiAddRecipients))
	mux.HandleFunc("DELETE /api/campaigns/{id}/recipients/{rid}", s.withOrg(s.apiDeleteRecipient))
	mux.HandleFunc("GET /api/campaigns/{id}/events", s.withOrg(s.apiEventCounts))
	mux.HandleFunc("GET /api/campaigns/{id}/stats", s.withOrg(s.apiStats))
	mux.HandleFunc("GET /api/campaigns/{id}/timeline", s.withOrg(s.apiTimeline))
	mux.HandleFunc("GET /api/campaigns/{id}/remediations", s.withOrg(s.apiListRemediations))
	mux.HandleFunc("POST /api/campaigns/{id}/remediations", s.withOrg(s.apiCreateRemediation))
	mux.HandleFunc("PATCH /api/campaigns/{id}/remediations/{rid}", s.withOrg(s.apiUpdateRemediation))
	mux.HandleFunc("POST /api/campaigns/{id}/test-send", s.withOrg(s.apiTestSend))
	mux.HandleFunc("POST /api/campaigns/{id}/approve", s.withOrg(s.apiApprove))
	mux.HandleFunc("POST /api/campaigns/{id}/release", s.withOrg(s.apiRelease))
	mux.HandleFunc("POST /api/campaigns/{id}/close", s.withOrg(s.apiClose))

	mux.HandleFunc("GET /api/templates", s.withOrg(s.apiListTemplates))
	mux.HandleFunc("POST /api/templates", s.withOrg(s.apiCreateTemplate))
	mux.HandleFunc("PUT /api/templates/{id}", s.withOrg(s.apiUpdateTemplate))
	mux.HandleFunc("DELETE /api/templates/{id}", s.withOrg(s.apiDeleteTemplate))

	mux.HandleFunc("GET /api/landing-pages", s.withOrg(s.apiListLandingPages))
	mux.HandleFunc("POST /api/landing-pages", s.withOrg(s.apiCreateLandingPage))
	mux.HandleFunc("PUT /api/landing-pages/{id}", s.withOrg(s.apiUpdateLandingPage))
	mux.HandleFunc("DELETE /api/landing-pages/{id}", s.withOrg(s.apiDeleteLandingPage))
	mux.HandleFunc("POST /api/landing-pages/clone", s.withOrg(s.apiCloneLandingPage))

	mux.HandleFunc("GET /api/mailer-profiles", s.withOrg(s.apiListMailerProfiles))
	mux.HandleFunc("PUT /api/mailer-profiles/{name}", s.withOrg(s.apiUpsertMailerProfile))
	mux.HandleFunc("DELETE /api/mailer-profiles/{name}", s.withOrg(s.apiDeleteMailerProfile))
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) apiFail(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	if content.IsValidationError(err) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return true
	}
	if constraint := store.ForeignKeyConstraintName(err); constraint != "" {
		msg := "a related record is missing or still referenced"
		switch constraint {
		case "campaigns_landing_page_id_fkey", "campaigns_template_id_fkey":
			msg = "still in use by one or more campaigns"
		case "landing_pages_organization_id_fkey", "templates_organization_id_fkey",
			"mailer_profiles_organization_id_fkey", "campaigns_organization_id_fkey":
			msg = "organization not yet synced with this service - retry in a moment"
		}
		writeJSON(w, http.StatusConflict, map[string]string{"error": msg})
		return true
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
	return true
}

func (s *Server) apiFailNotFound(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	return true
}

func (s *Server) apiFailConflict(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	return true
}

func (s *Server) apiListCampaigns(w http.ResponseWriter, r *http.Request) {
	campaigns, err := s.Store.ListCampaigns(r.Context(), orgIDFrom(r.Context()))
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, campaigns)
}

type campaignRequest struct {
	Name             string   `json:"name"`
	Scenario         string   `json:"scenario"`
	EmailSubject     string   `json:"email_subject"`
	EmailBodyHTML    string   `json:"email_body_html"`
	EmailButtonLabel string   `json:"email_button_label"`
	EmailFromName    string   `json:"email_from_name"`
	DebriefTitle     string   `json:"debrief_title"`
	DebriefIntro     string   `json:"debrief_intro"`
	DebriefNextSteps []string `json:"debrief_next_steps"`
	DebriefClosing   string   `json:"debrief_closing"`
	MailerProfile    string   `json:"mailer_profile"`
	TemplateID       *string  `json:"template_id,omitempty"`
	LandingPageID    *string  `json:"landing_page_id,omitempty"`
	Objective        string   `json:"objective"`
	BusinessUnit     string   `json:"business_unit"`
}

func (b campaignRequest) toCampaign() store.Campaign {
	return store.Campaign{
		Name: b.Name, Scenario: b.Scenario,
		EmailSubject: b.EmailSubject, EmailBodyHTML: b.EmailBodyHTML,
		EmailButtonLabel: b.EmailButtonLabel, EmailFromName: b.EmailFromName,
		DebriefTitle: b.DebriefTitle, DebriefIntro: b.DebriefIntro,
		DebriefNextSteps: strings.Join(b.DebriefNextSteps, "\n"),
		DebriefClosing:   b.DebriefClosing, MailerProfile: b.MailerProfile,
		TemplateID: b.TemplateID, LandingPageID: b.LandingPageID,
		Objective: b.Objective, BusinessUnit: b.BusinessUnit,
	}
}

func (s *Server) apiCreateCampaign(w http.ResponseWriter, r *http.Request) {
	var body campaignRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	c := body.toCampaign()
	c.ID = newID("camp")
	c.OrganizationID = orgIDFrom(r.Context())
	c.ContentVersion = newVersion()
	c.Status = "draft"
	if err := s.Store.CreateCampaign(r.Context(), c); s.apiFail(w, err) {
		return
	}
	created, err := s.Store.GetCampaign(r.Context(), c.ID)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) apiGetCampaign(w http.ResponseWriter, r *http.Request) {
	c, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), r.PathValue("id"))
	if s.apiFailNotFound(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, c)
}

func (s *Server) apiUpdateDraft(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	var body campaignRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	if err := s.Store.UpdateDraftContent(r.Context(), id, body.toCampaign(), newVersion()); s.apiFailConflict(w, err) {
		return
	}
	updated, err := s.Store.GetCampaign(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) apiListRecipients(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	recipients, err := s.Store.ListRecipients(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, recipients)
}

func (s *Server) apiEventCounts(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	counts, err := s.Store.EventCountsForCampaign(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, counts)
}

type recipientsRequest struct {
	Recipients []string `json:"recipients"`
}

// apiTestSend mirrors handleTestSend, JSON instead of form post/redirect.
func (s *Server) apiTestSend(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id)
	if s.apiFailNotFound(w, err) {
		return
	}
	var body recipientsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	if len(body.Recipients) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at least one named test recipient is required"})
		return
	}
	pack := packFromCampaign(c)
	for _, addr := range body.Recipients {
		if err := s.sendTo(r, c.OrganizationID, id, c.MailerProfile, addr, pack, true); s.apiFail(w, err) {
			return
		}
	}
	if err := s.Store.SetStatus(r.Context(), id, "tested"); s.apiFail(w, err) {
		return
	}
	updated, err := s.Store.GetCampaign(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) apiApprove(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	if err := s.Store.Approve(r.Context(), id); s.apiFailConflict(w, err) {
		return
	}
	updated, err := s.Store.GetCampaign(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

// apiRelease mirrors handleRelease, including the org-enabled check.
func (s *Server) apiRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id)
	if s.apiFailNotFound(w, err) {
		return
	}
	org, err := s.Store.GetOrganization(r.Context(), c.OrganizationID)
	if s.apiFail(w, err) {
		return
	}
	if !org.Enabled {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "organization has this module disabled; cannot release"})
		return
	}
	var body recipientsRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}

	if err := s.Store.Release(r.Context(), id, releaseExpiry()); s.apiFailConflict(w, err) {
		return
	}
	if err := s.releaseAudience(r, c, body.Recipients); s.apiFail(w, err) {
		return
	}
	updated, err := s.Store.GetCampaign(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) apiClose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	if err := s.Store.CloseCampaign(r.Context(), id); s.apiFailConflict(w, err) {
		return
	}
	updated, err := s.Store.GetCampaign(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) apiDeleteCampaign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	if err := s.Store.DeleteCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Audience (pre-launch recipient registration) ---

type audienceRecipient struct {
	Email      string `json:"email"`
	FullName   string `json:"full_name"`
	Department string `json:"department"`
	Role       string `json:"role"`
	Location   string `json:"location"`
}

type audienceRequest struct {
	Recipients []audienceRecipient `json:"recipients"`
}

// apiAddRecipients registers audience rows; locked once a campaign has launched.
func (s *Server) apiAddRecipients(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id)
	if s.apiFailNotFound(w, err) {
		return
	}
	if c.Status == "released" || c.Status == "closed" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "recipients are locked once a campaign is launched"})
		return
	}
	var body audienceRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	for _, rr := range body.Recipients {
		rec := store.Recipient{
			ID: newID("rec"), CampaignID: id, Email: rr.Email, Token: newToken(),
			FullName: rr.FullName, Department: rr.Department, Role: rr.Role, Location: rr.Location,
		}
		if err := s.Store.AddRecipient(r.Context(), rec); s.apiFail(w, err) {
			return
		}
	}
	recipients, err := s.Store.ListRecipients(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, recipients)
}

func (s *Server) apiDeleteRecipient(w http.ResponseWriter, r *http.Request) {
	id, rid := r.PathValue("id"), r.PathValue("rid")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	if err := s.Store.DeleteRecipient(r.Context(), id, rid); s.apiFailConflict(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Stats, timeline, remediations ---

func (s *Server) apiStats(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	stats, err := s.Store.ComputeStats(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) apiTimeline(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	entries, err := s.Store.Timeline(r.Context(), id, 500)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

func (s *Server) apiListRemediations(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	remediations, err := s.Store.RemediationsForCampaign(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, remediations)
}

type remediationRequest struct {
	RecipientID string  `json:"recipient_id"`
	Type        string  `json:"type"`
	Assignee    string  `json:"assignee"`
	DueDate     *string `json:"due_date,omitempty"`
	Notes       string  `json:"notes"`
}

// apiCreateRemediation creates a pending record only; the host app performs
// the real action and PATCHes the status.
func (s *Server) apiCreateRemediation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	var body remediationRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	rem := store.Remediation{
		ID: newID("rem"), CampaignID: id, RecipientID: body.RecipientID,
		Type: body.Type, Status: "pending", Assignee: body.Assignee, Notes: body.Notes,
	}
	if err := s.Store.CreateRemediation(r.Context(), rem); s.apiFail(w, err) {
		return
	}
	created, err := s.Store.GetRemediation(r.Context(), rem.ID)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

type remediationStatusRequest struct {
	Status         string `json:"status"`
	VerifiedSource string `json:"verified_source"`
	ExternalRef    string `json:"external_ref"`
	Notes          string `json:"notes"`
}

func (s *Server) apiUpdateRemediation(w http.ResponseWriter, r *http.Request) {
	id, rid := r.PathValue("id"), r.PathValue("rid")
	if _, err := s.ownedCampaign(r.Context(), orgIDFrom(r.Context()), id); s.apiFailNotFound(w, err) {
		return
	}
	var body remediationStatusRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	if err := s.Store.UpdateRemediationStatus(r.Context(), rid, body.Status, body.VerifiedSource, body.ExternalRef, body.Notes); s.apiFail(w, err) {
		return
	}
	updated, err := s.Store.GetRemediation(r.Context(), rid)
	if s.apiFail(w, err) {
		return
	}
	// A completed training remediation changes recipient risk - recompute.
	s.recomputeRisk(r.Context(), updated.RecipientID)
	writeJSON(w, http.StatusOK, updated)
}

// --- Template library ---

type templateRequest struct {
	Name        string `json:"name"`
	Scenario    string `json:"scenario"`
	Subject     string `json:"subject"`
	SenderName  string `json:"sender_name"`
	SenderEmail string `json:"sender_email"`
	BodyHTML    string `json:"body_html"`
	CTAText     string `json:"cta_text"`
}

func (s *Server) apiListTemplates(w http.ResponseWriter, r *http.Request) {
	templates, err := s.Store.ListTemplates(r.Context(), orgIDFrom(r.Context()))
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, templates)
}

func (s *Server) apiCreateTemplate(w http.ResponseWriter, r *http.Request) {
	var body templateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	orgID := orgIDFrom(r.Context())
	t := store.Template{
		ID: newID("tmpl"), OrganizationID: &orgID, Name: body.Name, Scenario: body.Scenario,
		Subject: body.Subject, SenderName: body.SenderName, SenderEmail: body.SenderEmail,
		BodyHTML: body.BodyHTML, CTAText: body.CTAText,
	}
	if err := s.Store.CreateTemplate(r.Context(), t); s.apiFail(w, err) {
		return
	}
	created, err := s.Store.GetTemplate(r.Context(), t.ID)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) apiUpdateTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body templateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	orgID := orgIDFrom(r.Context())
	t := store.Template{
		Name: body.Name, Scenario: body.Scenario, Subject: body.Subject,
		SenderName: body.SenderName, SenderEmail: body.SenderEmail, BodyHTML: body.BodyHTML, CTAText: body.CTAText,
	}
	if err := s.Store.UpdateTemplate(r.Context(), id, orgID, t); s.apiFail(w, err) {
		return
	}
	updated, err := s.Store.GetTemplate(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) apiDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.DeleteTemplate(r.Context(), id, orgIDFrom(r.Context())); s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// --- Landing-page library ---

type landingPageRequest struct {
	Name            string `json:"name"`
	PageType        string `json:"page_type"`
	HTML            string `json:"html"`
	CaptureUsername bool   `json:"capture_username"`
	CapturePassword bool   `json:"capture_password"`
	RedirectURL     string `json:"redirect_url"`
	LogoAssetKey    string `json:"logo_asset_key"`
	BrandColor      string `json:"brand_color"`
}

func (s *Server) apiListLandingPages(w http.ResponseWriter, r *http.Request) {
	pages, err := s.Store.ListLandingPages(r.Context(), orgIDFrom(r.Context()))
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, pages)
}

func (s *Server) apiCreateLandingPage(w http.ResponseWriter, r *http.Request) {
	var body landingPageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	orgID := orgIDFrom(r.Context())
	p := store.LandingPage{
		ID: newID("lp"), OrganizationID: &orgID, Name: body.Name, PageType: body.PageType, HTML: body.HTML,
		CaptureUsername: body.CaptureUsername, CapturePassword: body.CapturePassword,
		RedirectURL: body.RedirectURL, LogoAssetKey: body.LogoAssetKey, BrandColor: body.BrandColor,
	}
	if err := s.Store.CreateLandingPage(r.Context(), p); s.apiFail(w, err) {
		return
	}
	created, err := s.Store.GetLandingPage(r.Context(), p.ID)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) apiUpdateLandingPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body landingPageRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	orgID := orgIDFrom(r.Context())
	p := store.LandingPage{
		Name: body.Name, PageType: body.PageType, HTML: body.HTML,
		CaptureUsername: body.CaptureUsername, CapturePassword: body.CapturePassword,
		RedirectURL: body.RedirectURL, LogoAssetKey: body.LogoAssetKey, BrandColor: body.BrandColor,
	}
	if err := s.Store.UpdateLandingPage(r.Context(), id, orgID, p); s.apiFail(w, err) {
		return
	}
	updated, err := s.Store.GetLandingPage(r.Context(), id)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) apiDeleteLandingPage(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Store.DeleteLandingPage(r.Context(), id, orgIDFrom(r.Context())); s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type cloneRequest struct {
	URL string `json:"url"`
}

// apiCloneLandingPage renders via headless browser (internal/browserclone)
// so the preview and the served page both match the real target. Returned
// HTML is already sanitized/inlined; saving it as-is is what
// render.LandingFromClone renders at serve time.
func (s *Server) apiCloneLandingPage(w http.ResponseWriter, r *http.Request) {
	var body cloneRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	if s.BrowserWSURL == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "page cloning is not configured on this deployment (no browser service available)",
		})
		return
	}
	html, _, err := browserclone.FetchRendered(r.Context(), s.BrowserWSURL, body.URL)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"html": html})
}

// --- Mailer profiles ---

type mailerProfileRequest struct {
	FromName     string `json:"from_name"`
	FromEmail    string `json:"from_email"`
	ReplyTo      string `json:"reply_to"`
	SMTPHost     string `json:"smtp_host"`
	SMTPUsername string `json:"smtp_username"`
	SMTPPassword string `json:"smtp_password"` // write-only; never echoed back
	NoAuth       bool   `json:"no_auth"`
}

func (s *Server) apiListMailerProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.Store.ListMailerProfiles(r.Context(), orgIDFrom(r.Context()))
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, profiles)
}

func (s *Server) apiUpsertMailerProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body mailerProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&body); s.apiFail(w, err) {
		return
	}
	encPassword := body.SMTPPassword
	if s.Secrets != nil && body.SMTPPassword != "" {
		enc, err := s.Secrets.Encrypt(body.SMTPPassword)
		if s.apiFail(w, err) {
			return
		}
		encPassword = enc
	}
	orgID := orgIDFrom(r.Context())
	m := store.MailerProfile{
		ID: newID("mp"), OrganizationID: orgID, Name: name, FromName: body.FromName, FromEmail: body.FromEmail,
		ReplyTo: body.ReplyTo, SMTPHost: body.SMTPHost, SMTPUsername: body.SMTPUsername,
		SMTPPasswordEnc: encPassword, NoAuth: body.NoAuth,
	}
	if err := s.Store.UpsertMailerProfile(r.Context(), m); s.apiFail(w, err) {
		return
	}
	updated, err := s.Store.GetMailerProfile(r.Context(), orgID, name)
	if s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) apiDeleteMailerProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.Store.DeleteMailerProfile(r.Context(), orgIDFrom(r.Context()), name); s.apiFail(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}
