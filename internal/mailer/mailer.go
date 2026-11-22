// Package mailer sends through an "approved mailer profile" - in this slice,
// one local SMTP sink. Stdlib net/smtp is enough for that; evaluating
// wneessen/go-mail for real TLS/timeout handling against an actual relay
// is still open, per PLAN.md §3's "Dependency choices to validate".
package mailer

import (
	"fmt"
	"net/smtp"
)

// Profile is an "approved SMTP profile" - one named mail route an operator
// can pick in the Test/Release steps. Nothing here validates or trusts
// content; it only moves bytes the caller already rendered.
type Profile struct {
	Name     string
	Host     string // host:port
	From     string
	NoAuth   bool // local sinks (mailpit/mailhog) take no auth
	Username string
	Password string
}

// Send returns a plain error on any SMTP-level rejection: callers record this
// as "send failed", distinct from "accepted" (no error) and distinct again
// from "verified delivered" (an outcome this slice cannot observe - that
// needs real mailbox evidence, per the Quality plan's "Separate SMTP
// acceptance from verified delivery").
func Send(p Profile, to, subject, htmlBody string) error {
	msg := buildMessage(p.From, to, subject, htmlBody)

	var auth smtp.Auth
	if !p.NoAuth {
		host := p.Host
		if i := indexColon(host); i >= 0 {
			host = host[:i]
		}
		auth = smtp.PlainAuth("", p.Username, p.Password, host)
	}
	if err := smtp.SendMail(p.Host, auth, p.From, []string{to}, msg); err != nil {
		return fmt.Errorf("mailer: send via %q: %w", p.Name, err)
	}
	return nil
}

func indexColon(s string) int {
	for i, c := range s {
		if c == ':' {
			return i
		}
	}
	return -1
}

func buildMessage(from, to, subject, htmlBody string) []byte {
	return fmt.Appendf(nil,
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/html; charset=\"UTF-8\"\r\n\r\n%s",
		from, to, subject, htmlBody,
	)
}
