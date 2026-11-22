// Package content defines the versioned content pack the renderer consumes.
// A pack holds only typed text fields and references to local, embedded
// assets - never forms, remote URLs or executable content - so the same
// pack can be validated once and then rendered identically in preview and
// in delivery (see render.Render* and the "Author once" step in the
// standalone-service plan).
package content

// Pack is one versioned, tenant-scoped training scenario: the email a
// recipient receives and the debrief page the tracking link resolves to.
type Pack struct {
	// Version identifies this exact content revision. Approval and release
	// in the operator workflow are tied to one Version, never to "latest".
	Version string

	Scenario string // e.g. "network_reauth" - matches the platform's existing scenario_type

	Email   EmailContent
	Debrief DebriefContent
}

type EmailContent struct {
	FromName    string
	Subject     string
	BodyHTML    string // short rich-text body; rendered as escaped data, never raw HTML
	ButtonLabel string
}

type DebriefContent struct {
	Title        string
	Intro        string
	NextSteps    []string
	ClosingNote  string
	LogoAssetKey string // key into the embedded local asset set; never a URL
}

// Validate rejects a pack that isn't safe to render: every field here ends
// up as escaped template data, but a pack with no content or an asset key
// that isn't local would silently produce a broken or misleading page.
func (p Pack) Validate() error {
	switch {
	case p.Version == "":
		return errRequired("Version")
	case p.Scenario == "":
		return errRequired("Scenario")
	case p.Debrief.Title == "":
		return errRequired("Debrief.Title")
	case p.Email.Subject == "":
		return errRequired("Email.Subject")
	}
	return nil
}

type validationError string

func (e validationError) Error() string { return "content: missing required field " + string(e) }

func errRequired(field string) error { return validationError(field) }
