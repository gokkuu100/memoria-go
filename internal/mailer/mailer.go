// Package mailer sends transactional email. Production uses SMTP (Gmail SMTP
// at launch); development without SMTP credentials falls back to logging the
// message so OTP codes are readable in `make logs`.
package mailer

import (
	"context"
	"fmt"
	"log/slog"
	"net/smtp"
)

type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

type SMTP struct {
	Host     string
	Port     string
	Username string
	Password string
	From     string
}

// Send delivers via SMTP with STARTTLS (net/smtp upgrades automatically when
// the server advertises it, which Gmail on port 587 does).
func (s *SMTP) Send(_ context.Context, to, subject, body string) error {
	addr := s.Host + ":" + s.Port
	auth := smtp.PlainAuth("", s.Username, s.Password, s.Host)
	msg := []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"utf-8\"\r\n\r\n%s\r\n",
		s.From, to, subject, body,
	))
	if err := smtp.SendMail(addr, auth, s.From, []string{to}, msg); err != nil {
		return fmt.Errorf("smtp send to %s: %w", to, err)
	}
	return nil
}

// Log is the development fallback used when SMTP is not configured.
type Log struct{}

func (Log) Send(_ context.Context, to, subject, body string) error {
	slog.Info("mailer (dev log mode)", "to", to, "subject", subject, "body", body)
	return nil
}
