// Package render turns a content.Pack into self-contained HTML (email,
// debrief page): deterministic, html/template-escaped, no remote URLs or
// <script> - see render_test.go.
package render

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"
	"strings"

	"github.com/rudra184/baitbet/internal/clone"
	"github.com/rudra184/baitbet/internal/content"
)

//go:embed templates/debrief.html.tmpl templates/email.html.tmpl templates/email_delivery.html.tmpl templates/landing.html.tmpl templates/form_fragment.html.tmpl
var templateFS embed.FS

//go:embed assets/mark.svg
var markSVG []byte

var (
	debriefTmpl       = template.Must(template.ParseFS(templateFS, "templates/debrief.html.tmpl"))
	emailTmpl         = template.Must(template.ParseFS(templateFS, "templates/email.html.tmpl"))
	emailDeliveryTmpl = template.Must(template.ParseFS(templateFS, "templates/email_delivery.html.tmpl"))
	landingTmpl       = template.Must(template.ParseFS(templateFS, "templates/landing.html.tmpl", "templates/form_fragment.html.tmpl"))
	// formFragmentTmpl renders the "form" block alone, for splicing into a
	// cloned page at LandingFromClone's fallback (no preserved form found).
	formFragmentTmpl = template.Must(template.New("form").ParseFS(templateFS, "templates/form_fragment.html.tmpl"))
)

type debriefView struct {
	Title       string
	Intro       string
	NextSteps   []string
	ClosingNote string
	// template.URL, not string: it's a data: URI we built ourselves
	// (localAssetDataURI), never pack-authored text, so html/template's
	// default "unknown dynamic URL" sanitizing doesn't apply.
	LogoDataURI template.URL
}

type emailView struct {
	Subject string
	// template.HTML, not string: an operator writing a custom email template
	// is deliberately trusted to author real markup (links, formatting,
	// a look-alike layout) - see Email's doc comment. Subject and every
	// other pack field stay plain strings (auto-escaped).
	BodyHTML    template.HTML
	ButtonLabel string
}

type emailDeliveryView struct {
	Subject     string
	BodyHTML    template.HTML
	ButtonLabel string
	// Both server-built, never pack-authored - same template.URL reason as LogoDataURI.
	ButtonHref   template.URL
	OpenPixelSrc template.URL
}

// Debrief renders the form-free landing page.
func Debrief(pack content.Pack) (string, error) {
	if err := pack.Validate(); err != nil {
		return "", err
	}
	view := debriefView{
		Title:       pack.Debrief.Title,
		Intro:       pack.Debrief.Intro,
		NextSteps:   pack.Debrief.NextSteps,
		ClosingNote: pack.Debrief.ClosingNote,
	}
	if pack.Debrief.LogoAssetKey != "" {
		uri, err := localAssetDataURI(pack.Debrief.LogoAssetKey)
		if err != nil {
			return "", err
		}
		view.LogoDataURI = template.URL(uri)
	}
	var buf bytes.Buffer
	if err := debriefTmpl.Execute(&buf, view); err != nil {
		return "", fmt.Errorf("render debrief: %w", err)
	}
	return buf.String(), nil
}

// Email renders the recipient-facing email body. BodyHTML is rendered
// as trusted markup (template.HTML) - an operator building a campaign
// template is expected to author real HTML (formatting, links, a
// convincing layout), not plain text - so callers must never pass
// through end-user/recipient input here, only operator-authored
// template content from the admin workspace.
func Email(pack content.Pack) (string, error) {
	if err := pack.Validate(); err != nil {
		return "", err
	}
	view := emailView{
		Subject:     pack.Email.Subject,
		BodyHTML:    template.HTML(pack.Email.BodyHTML),
		ButtonLabel: pack.Email.ButtonLabel,
	}
	var buf bytes.Buffer
	if err := emailTmpl.Execute(&buf, view); err != nil {
		return "", fmt.Errorf("render email: %w", err)
	}
	return buf.String(), nil
}

// EmailDelivery is Email plus a real tracking link and open-tracking
// pixel. trackingURL must be this service's own link, never pack input.
func EmailDelivery(pack content.Pack, trackingURL string) (string, error) {
	if err := pack.Validate(); err != nil {
		return "", err
	}
	view := emailDeliveryView{
		Subject:      pack.Email.Subject,
		BodyHTML:     template.HTML(pack.Email.BodyHTML),
		ButtonLabel:  pack.Email.ButtonLabel,
		ButtonHref:   template.URL(trackingURL),
		OpenPixelSrc: template.URL(trackingURL + "/open.gif"),
	}
	var buf bytes.Buffer
	if err := emailDeliveryTmpl.Execute(&buf, view); err != nil {
		return "", fmt.Errorf("render email delivery: %w", err)
	}
	return buf.String(), nil
}

// LandingPage is rendered instead of the debrief when a campaign's landing
// page captures a username/password - the one reversal of this package's
// form-free design. The password value itself is never read back into Go:
// the browser posts it to SubmitAction, and store.RecordSubmission must
// never persist it.
type LandingPage struct {
	Title           string
	BrandColor      string
	LogoAssetKey    string
	CaptureUsername bool
	CapturePassword bool
	SubmitAction    string // this service's own /t/{token}/submit URL
}

type landingView struct {
	Title           string
	BrandColor      string
	LogoDataURI     template.URL
	CaptureUsername bool
	CapturePassword bool
	SubmitAction    template.URL
}

// Landing renders the credential-capture-mimicking page.
func Landing(p LandingPage) (string, error) {
	view := landingView{
		Title:           p.Title,
		BrandColor:      p.BrandColor,
		CaptureUsername: p.CaptureUsername,
		CapturePassword: p.CapturePassword,
		SubmitAction:    template.URL(p.SubmitAction),
	}
	if p.LogoAssetKey != "" {
		uri, err := localAssetDataURI(p.LogoAssetKey)
		if err != nil {
			return "", err
		}
		view.LogoDataURI = template.URL(uri)
	}
	if view.BrandColor == "" {
		view.BrandColor = "#eef1f4"
	}
	var buf bytes.Buffer
	if err := landingTmpl.Execute(&buf, view); err != nil {
		return "", fmt.Errorf("render landing: %w", err)
	}
	return buf.String(), nil
}

// LandingFromClone wires this service's capture into a browser-cloned
// page: the real <form> in place if clone preserved one (matching the
// cloned site's own styling), else the generic fragment spliced at the
// marker or appended before </body>. Either way it posts to the same
// SubmitAction as Landing, and the password-never-persisted invariant holds.
func LandingFromClone(clonedHTML string, p LandingPage) (string, error) {
	if out, ok := spliceIntoPreservedForm(clonedHTML, p); ok {
		return out, nil
	}

	var buf bytes.Buffer
	if err := formFragmentTmpl.Execute(&buf, landingView{
		CaptureUsername: p.CaptureUsername,
		CapturePassword: p.CapturePassword,
		SubmitAction:    template.URL(p.SubmitAction),
	}); err != nil {
		return "", fmt.Errorf("render landing from clone: render form: %w", err)
	}
	form := buf.String()

	if span := findMarker(clonedHTML); span != nil {
		return clonedHTML[:span.start] + form + clonedHTML[span.end:], nil
	}
	lower := strings.ToLower(clonedHTML)
	if i := strings.LastIndex(lower, "</body>"); i >= 0 {
		return clonedHTML[:i] + form + clonedHTML[i:], nil
	}
	return clonedHTML + form, nil
}

// spliceIntoPreservedForm rewrites clone's sentinels into the real submit
// action/field names, removing (not renaming) a field the campaign
// doesn't capture. ok is false if clonedHTML has no such form.
func spliceIntoPreservedForm(docHTML string, p LandingPage) (out string, ok bool) {
	if !strings.Contains(docHTML, clone.SubmitActionSentinel) {
		return "", false
	}
	out = docHTML

	if p.CapturePassword {
		out = strings.ReplaceAll(out, clone.PasswordFieldSentinel, "password")
	} else {
		out = removeSentinelInput(out, clone.PasswordFieldSentinel)
	}
	if p.CaptureUsername {
		out = strings.ReplaceAll(out, clone.UsernameFieldSentinel, "username")
	} else {
		out = removeSentinelInput(out, clone.UsernameFieldSentinel)
	}
	out = strings.ReplaceAll(out, clone.SubmitActionSentinel, p.SubmitAction)
	return out, true
}

// removeSentinelInput drops the whole <input> (void element, no closing tag).
func removeSentinelInput(docHTML, sentinel string) string {
	needle := `name="` + sentinel + `"`
	idx := strings.Index(docHTML, needle)
	if idx < 0 {
		return docHTML
	}
	start := strings.LastIndex(docHTML[:idx], "<input")
	end := strings.Index(docHTML[idx:], ">")
	if start < 0 || end < 0 {
		return docHTML
	}
	end = idx + end + 1
	return docHTML[:start] + docHTML[end:]
}

type markerSpan struct{ start, end int }

// findMarker locates the marker div's full span so it can be replaced whole.
func findMarker(docHTML string) *markerSpan {
	needle := `id="` + clone.FormMarkerID + `"`
	idx := strings.Index(docHTML, needle)
	if idx < 0 {
		return nil
	}
	start := strings.LastIndex(docHTML[:idx], "<div")
	if start < 0 {
		return nil
	}
	closeIdx := strings.Index(docHTML[idx:], "</div>") // marker div has no nested elements
	if closeIdx < 0 {
		return nil
	}
	end := idx + closeIdx + len("</div>")
	return &markerSpan{start: start, end: end}
}

// localAssetDataURI rejects unknown keys rather than falling back to a network fetch.
func localAssetDataURI(key string) (string, error) {
	switch key {
	case "mark":
		return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(markSVG), nil
	default:
		return "", fmt.Errorf("render: unknown local asset key %q", key)
	}
}
