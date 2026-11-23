// Package mailer sends through an approved mailer profile via stdlib net/smtp.
package mailer

import (
	"fmt"
	"net/smtp"
)

// Profile is one named SMTP route an operator can pick for Test/Release.
type Profile struct {
	Name     string
	Host     string // host:port
	From     string
	NoAuth   bool // local sinks (mailpit/mailhog) take no auth
	Username string
	Password string
}

// Send reports only "accepted by SMTP" or an error - never delivery confirmation.
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
