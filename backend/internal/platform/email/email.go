// Package email sends plain-text email over SMTP. Locally that's Mailpit (docker compose), which
// catches every message at http://localhost:8025 instead of delivering it.
package email

import (
	"context"
	"fmt"
	"mime"
	"net/mail"
	"net/smtp"
	"strings"
	"time"
)

type Message struct {
	To      string
	Subject string
	Text    string
}

// Sender sends email. SMTP is the real one; tests use a fake.
type Sender interface {
	Send(ctx context.Context, msg Message) error
}

// SMTP sends through an SMTP server such as Mailpit or a provider's relay.
type SMTP struct {
	Addr     string // host:port
	From     string // e.g. "NMS <no-reply@example.com>"
	Username string // optional; when set, also Password
	Password string
}

func (s SMTP) Send(_ context.Context, msg Message) error {
	from, err := mail.ParseAddress(s.From)
	if err != nil {
		return fmt.Errorf("parse from address: %w", err)
	}
	to, err := mail.ParseAddress(msg.To)
	if err != nil {
		return fmt.Errorf("parse to address: %w", err)
	}

	var auth smtp.Auth
	if s.Username != "" {
		host, _, _ := strings.Cut(s.Addr, ":")
		auth = smtp.PlainAuth("", s.Username, s.Password, host)
	}

	headers := []string{
		"From: " + from.String(),
		"To: " + to.String(),
		"Subject: " + mimeHeader(msg.Subject),
		"Date: " + time.Now().Format(time.RFC1123Z),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
	}
	body := strings.Join(headers, "\r\n") + "\r\n\r\n" + strings.ReplaceAll(msg.Text, "\n", "\r\n")

	if err := smtp.SendMail(s.Addr, auth, from.Address, []string{to.Address}, []byte(body)); err != nil {
		return fmt.Errorf("send email to %s: %w", to.Address, err)
	}
	return nil
}

// mimeHeader encodes a header value so non-ASCII text (e.g. "Café Plants") survives, and line
// breaks can't sneak extra headers in (organization names come from users).
func mimeHeader(value string) string {
	return mime.QEncoding.Encode("utf-8", value)
}
