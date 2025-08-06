package service

import (
    "fmt"
)

// Mailer defines a simple interface for sending emails. In production,
// implement Send to deliver email via SMTP or an external provider.
type Mailer struct {
    Host     string
    Port     int
    Username string
    Password string
    From     string
}

// Send sends an email to the specified recipient with subject and body.
// Currently this is a stub that prints to stdout. For a real
// implementation, you can use net/smtp or a third-party library.
func (m *Mailer) Send(to, subject, body string) error {
    // TODO: implement real email sending
    fmt.Printf("Sending email to %s with subject %q:\n%s\n", to, subject, body)
    return nil
}