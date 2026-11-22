// Package render turns a content.Pack into self-contained HTML: the
// recipient email and the debrief landing page.
//
// Three properties are load-bearing and covered by render_test.go:
//   - deterministic: the same Pack always produces byte-identical HTML, so
//     preview and delivery can be proven to show the same content version.
//   - escaped: all Pack text goes through html/template, which
//     context-aware-escapes it as data - a scenario author cannot inject
//     markup, scripts or attributes into the page.
//   - self-contained: the only asset reference is a data: URI built from an
//     embedded local file. There is no <form>, no remote http(s):// URL and
//     no <script>, so the page needs no browser engine and no network.
package render

import (
	"bytes"
	"embed"
	"encoding/base64"
	"fmt"
	"html/template"

	"github.com/rudra184/baitbet/internal/content"
)

//go:embed templates/debrief.html.tmpl templates/email.html.tmpl templates/email_delivery.html.tmpl
var templateFS embed.FS

//go:embed assets/mark.svg
var markSVG []byte

var (
	debriefTmpl       = template.Must(template.ParseFS(templateFS, "templates/debrief.html.tmpl"))
	emailTmpl         = template.Must(template.ParseFS(templateFS, "templates/email.html.tmpl"))
	emailDeliveryTmpl = template.Must(template.ParseFS(templateFS, "templates/email_delivery.html.tmpl"))
)

type debriefView struct {
	Title       string
	Intro       string
	NextSteps   []string
	ClosingNote string
	// LogoDataURI is template.URL, not string: html/template's URL sanitizer
	// otherwise treats any dynamic value in a src attribute as untrusted and
	// replaces it with "#ZgotmplZ". That's the correct default for anything
	// derived from pack content; this value never is - it's built in
	// localAssetDataURI from our own embedded asset bytes, keyed by a
	// hardcoded switch, never from pack-authored text.
	LogoDataURI template.URL
}

type emailView struct {
	Subject     string
	BodyHTML    string
	ButtonLabel string
}

type emailDeliveryView struct {
	Subject     string
	BodyHTML    string
	ButtonLabel string
	// ButtonHref is template.URL for the same reason LogoDataURI is: it is
	// built by server code (EmailDelivery's caller passes a URL this service
	// itself constructed from a recipient token), never from pack-authored
	// text, so html/template's default "unknown dynamic string in a URL
	// context is untrusted" stance doesn't apply to it.
	ButtonHref template.URL
}

// Debrief renders the form-free landing page for pack.
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

// Email renders the recipient-facing email body for pack.
func Email(pack content.Pack) (string, error) {
	if err := pack.Validate(); err != nil {
		return "", err
	}
	view := emailView{
		Subject:     pack.Email.Subject,
		BodyHTML:    pack.Email.BodyHTML,
		ButtonLabel: pack.Email.ButtonLabel,
	}
	var buf bytes.Buffer
	if err := emailTmpl.Execute(&buf, view); err != nil {
		return "", fmt.Errorf("render email: %w", err)
	}
	return buf.String(), nil
}

// EmailDelivery renders the recipient email for an actual send: same subject
// and body as Email, plus a real tracking link. trackingURL must already be
// this service's own link (built in the campaign/server layer from a
// recipient's token), never attacker- or pack-controlled input.
func EmailDelivery(pack content.Pack, trackingURL string) (string, error) {
	if err := pack.Validate(); err != nil {
		return "", err
	}
	view := emailDeliveryView{
		Subject:     pack.Email.Subject,
		BodyHTML:    pack.Email.BodyHTML,
		ButtonLabel: pack.Email.ButtonLabel,
		ButtonHref:  template.URL(trackingURL),
	}
	var buf bytes.Buffer
	if err := emailDeliveryTmpl.Execute(&buf, view); err != nil {
		return "", fmt.Errorf("render email delivery: %w", err)
	}
	return buf.String(), nil
}

// localAssetDataURI resolves a pack's asset key to an embedded file and
// returns it as a data: URI. The only key currently implemented is "mark"
// (internal/render/assets/mark.svg); any other key is rejected rather than
// silently falling back to a network fetch.
func localAssetDataURI(key string) (string, error) {
	switch key {
	case "mark":
		return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(markSVG), nil
	default:
		return "", fmt.Errorf("render: unknown local asset key %q", key)
	}
}
