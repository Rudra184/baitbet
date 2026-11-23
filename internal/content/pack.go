// Package content defines the versioned content pack the renderer
// consumes: only typed text and local embedded asset keys, never forms,
// remote URLs or executable content.
package content

// Pack is one versioned, tenant-scoped training scenario.
type Pack struct {
	Version  string // approval/release are tied to one Version, never "latest"
	Scenario string

	Email   EmailContent
	Debrief DebriefContent
}

type EmailContent struct {
	FromName    string
	Subject     string
	BodyHTML    string
	ButtonLabel string
}

type DebriefContent struct {
	Title        string
	Intro        string
	NextSteps    []string
	ClosingNote  string
	LogoAssetKey string // key into the embedded local asset set; never a URL
}

// Validate rejects a pack missing fields that would render as broken/misleading.
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

// IsValidationError reports whether err came from Pack.Validate (400, not 500).
func IsValidationError(err error) bool {
	_, ok := err.(validationError)
	return ok
}
