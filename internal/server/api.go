// JSON mirror of the HTML admin routes in server.go, for a host
// application that wants to build its own UI against baitbet instead of
// embedding the server-rendered workspace. Every handler here delegates to
// the exact same Store methods as its HTML counterpart - this file only
// changes the request/response encoding, never the workflow rules
// (draft -> tested -> approved -> released -> closed, the duplicate-action
// guards, tenant isolation). It stays a plain mirror of the store's own
// primitives on purpose: any shaping, aggregation, or UI-specific decision
// belongs in the caller, not here - that's what keeps this service usable
// by more than one host application.
package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/rudra184/baitbet/internal/store"
)

func (s *Server) apiRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/campaigns", s.withOrg(s.apiListCampaigns))
	mux.HandleFunc("POST /api/campaigns", s.withOrg(s.apiCreateCampaign))
	mux.HandleFunc("GET /api/campaigns/{id}", s.withOrg(s.apiGetCampaign))
	mux.HandleFunc("PUT /api/campaigns/{id}/draft", s.withOrg(s.apiUpdateDraft))
	mux.HandleFunc("GET /api/campaigns/{id}/recipients", s.withOrg(s.apiListRecipients))
	mux.HandleFunc("GET /api/campaigns/{id}/events", s.withOrg(s.apiEventCounts))
	mux.HandleFunc("POST /api/campaigns/{id}/test-send", s.withOrg(s.apiTestSend))
	mux.HandleFunc("POST /api/campaigns/{id}/approve", s.withOrg(s.apiApprove))
	mux.HandleFunc("POST /api/campaigns/{id}/release", s.withOrg(s.apiRelease))
	mux.HandleFunc("POST /api/campaigns/{id}/close", s.withOrg(s.apiClose))
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
}

func (b campaignRequest) toCampaign() store.Campaign {
	return store.Campaign{
		Name: b.Name, Scenario: b.Scenario,
		EmailSubject: b.EmailSubject, EmailBodyHTML: b.EmailBodyHTML,
		EmailButtonLabel: b.EmailButtonLabel, EmailFromName: b.EmailFromName,
		DebriefTitle: b.DebriefTitle, DebriefIntro: b.DebriefIntro,
		DebriefNextSteps: strings.Join(b.DebriefNextSteps, "\n"),
		DebriefClosing:   b.DebriefClosing, MailerProfile: b.MailerProfile,
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

// apiTestSend mirrors handleTestSend exactly (same mailer/render calls,
// same "tested" status transition) with a JSON body/response instead of a
// form post/redirect.
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
		if err := s.sendTo(r, id, addr, pack, true); s.apiFail(w, err) {
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

// apiRelease mirrors handleRelease exactly, including the org-enabled check
// and the one-way release guard in store.Release.
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
	pack := packFromCampaign(c)
	for _, addr := range body.Recipients {
		if err := s.sendTo(r, id, addr, pack, false); s.apiFail(w, err) {
			return
		}
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
