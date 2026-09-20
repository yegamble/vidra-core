package mailconfig

import (
	"strings"

	"github.com/vidra/vidra-core/internal/mail"
)

// Input is the PUT body: the same shape as Document, with a WRITE-ONLY secret
// where each block's *_set flag is.
//
// Every secret is a *string with three distinct meanings, and the distinction
// is the whole reason the field is a pointer:
//
//	absent / null — keep the stored credential. Only valid when the transport
//	                is unchanged; switching transport means the stored secret
//	                belongs to the old provider, so a new one is REQUIRED.
//	""            — clear the credential. Only valid for smtp.password, where
//	                an anonymous relay is a real shape; a provider with no API
//	                key is not a configuration, it is a broken one.
//	a value       — replace it.
//
// A plain string could not express "leave it alone", so a panel that rendered
// a masked field would save the mask.
type Input struct {
	Transport   string         `json:"transport"`
	FromAddress string         `json:"from_address"`
	FromName    string         `json:"from_name"`
	ReplyTo     string         `json:"reply_to"`
	SMTP        *SMTPInput     `json:"smtp"`
	Mailgun     *MailgunInput  `json:"mailgun"`
	Resend      *APIKeyInput   `json:"resend"`
	Brevo       *APIKeyInput   `json:"brevo"`
	Postmark    *PostmarkInput `json:"postmark"`
}

// SMTPInput is a generic relay's settings plus its write-only password.
type SMTPInput struct {
	Host       string  `json:"host"`
	Port       int     `json:"port"`
	Username   string  `json:"username"`
	Encryption string  `json:"encryption"`
	Password   *string `json:"password"`
}

// MailgunInput is the sending domain, its region, and the write-only key.
type MailgunInput struct {
	Domain string  `json:"domain"`
	Region string  `json:"region"`
	APIKey *string `json:"api_key"`
}

// APIKeyInput is a provider whose only setting is its write-only key.
type APIKeyInput struct {
	APIKey *string `json:"api_key"`
}

// PostmarkInput is the message stream plus the write-only server token.
type PostmarkInput struct {
	MessageStream string  `json:"message_stream"`
	ServerToken   *string `json:"server_token"`
}

// settings projects the block matching kind onto the stored shape. A missing
// block yields zero settings, which mail.NewTransport then rejects with the
// dotted field names the admin needs — rather than this layer inventing a
// second, possibly disagreeing, "you forgot the smtp block" message.
func (in Input) settings(kind string) storedSettings {
	var s storedSettings
	switch kind {
	case mail.KindSMTP:
		if in.SMTP != nil {
			s.Host = strings.TrimSpace(in.SMTP.Host)
			s.Port = in.SMTP.Port
			s.Username = strings.TrimSpace(in.SMTP.Username)
			s.Encryption = strings.TrimSpace(in.SMTP.Encryption)
		}
	case mail.KindMailgun:
		if in.Mailgun != nil {
			s.Domain = strings.TrimSpace(in.Mailgun.Domain)
			s.Region = strings.TrimSpace(in.Mailgun.Region)
		}
	case mail.KindPostmark:
		if in.Postmark != nil {
			s.MessageStream = strings.TrimSpace(in.Postmark.MessageStream)
		}
	}
	return s
}

// secret returns the credential the caller supplied for kind, and whether they
// supplied one at all (see the Input doc comment for the three meanings).
func (in Input) secret(kind string) (value string, present bool) {
	var p *string
	switch kind {
	case mail.KindSMTP:
		if in.SMTP != nil {
			p = in.SMTP.Password
		}
	case mail.KindMailgun:
		if in.Mailgun != nil {
			p = in.Mailgun.APIKey
		}
	case mail.KindResend:
		if in.Resend != nil {
			p = in.Resend.APIKey
		}
	case mail.KindBrevo:
		if in.Brevo != nil {
			p = in.Brevo.APIKey
		}
	case mail.KindPostmark:
		if in.Postmark != nil {
			p = in.Postmark.ServerToken
		}
	}
	if p == nil {
		return "", false
	}
	return *p, true
}

// validateIdentity checks the fields that are common to every transport and
// that all end up in a MESSAGE HEADER. They are rejected rather than sanitized:
// a silently mangled From address is a deliverability failure an operator
// cannot see, and there is no legitimate reason for a line break or a list
// separator in any of them.
//
// The address rule is mail.IsAddrSpec — the SAME predicate the transports apply
// at the wire, not a local copy of it. A second implementation is exactly how
// "a configuration that saves is a configuration that resolves" stops being
// true: this package accepted `Vidra <no-reply@example.org>` (net/mail parses
// it) while the transport hands that string straight to MAIL FROM, so the save
// returned 200 and every message afterwards failed. A display name belongs in
// from_name, which is the field that renders one.
func validateIdentity(in Input) []mail.FieldError {
	var bad []mail.FieldError
	from := strings.TrimSpace(in.FromAddress)
	switch {
	case from == "":
		bad = append(bad, mail.FieldError{Field: "from_address", Msg: "required"})
	case !mail.IsAddrSpec(from):
		bad = append(bad, mail.FieldError{Field: "from_address", Msg: "must be one email address, with no display name"})
	}
	if strings.ContainsAny(in.FromName, "\r\n") {
		bad = append(bad, mail.FieldError{Field: "from_name", Msg: "must not contain line breaks"})
	}
	if reply := strings.TrimSpace(in.ReplyTo); reply != "" && !mail.IsAddrSpec(reply) {
		bad = append(bad, mail.FieldError{Field: "reply_to", Msg: "must be one email address, with no display name"})
	}
	return bad
}

// smtpUsername is the relay username the caller supplied, or "" when the SMTP
// block is absent.
func smtpUsername(in Input) string {
	if in.SMTP == nil {
		return ""
	}
	return in.SMTP.Username
}
