package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
)

type Config struct {
	Transport       string
	SendmailPath    string
	SMTPHost        string
	SMTPPort        int
	SMTPUsername    string
	SMTPPassword    string
	TLS             bool
	DisableSTARTTLS bool
	CAFile          string
	FromAddress     string
	FromName        string
	BaseURL         string
}

type Service struct {
	config *Config
	sender EmailSender
}

func NewService(config *Config) *Service {
	return NewServiceWithSender(config, &smtpSender{})
}

func NewServiceWithSender(config *Config, sender EmailSender) *Service {
	return &Service{config: config, sender: sender}
}

type EmailSender interface {
	SendPlain(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
	SendTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, host string) error
	SendSTARTTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, host string) error
}

type smtpSender struct{}

func (s *smtpSender) SendPlain(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	return smtp.SendMail(addr, auth, from, to, msg)
}

func (s *smtpSender) SendTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, host string) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: false,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer func() { _ = conn.Close() }()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer func() { _ = client.Close() }()

	if err = client.Auth(auth); err != nil {
		return fmt.Errorf("failed to authenticate: %w", err)
	}

	if err = client.Mail(from); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	for _, recipient := range to {
		if err = client.Rcpt(recipient); err != nil {
			return fmt.Errorf("failed to set recipient: %w", err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	if _, err = w.Write(msg); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("failed to close data writer: %w", err)
	}

	return client.Quit()
}

func (s *smtpSender) SendSTARTTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, host string) error {
	c, err := smtp.Dial(addr)
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer func() { _ = c.Close() }()

	if err = c.Hello("localhost"); err != nil {
		return fmt.Errorf("failed to send HELO: %w", err)
	}

	if ok, _ := c.Extension("STARTTLS"); ok {
		config := &tls.Config{
			ServerName:         host,
			MinVersion:         tls.VersionTLS12,
			InsecureSkipVerify: false,
		}
		if err = c.StartTLS(config); err != nil {
			return fmt.Errorf("failed to start TLS: %w", err)
		}
	} else {
		return fmt.Errorf("STARTTLS not supported by server on port 587 - refusing to send over insecure connection")
	}

	if auth != nil {
		if err = c.Auth(auth); err != nil {
			return fmt.Errorf("failed to authenticate: %w", err)
		}
	}

	if err = c.Mail(from); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	for _, recipient := range to {
		if err = c.Rcpt(recipient); err != nil {
			return fmt.Errorf("failed to set recipient %s: %w", recipient, err)
		}
	}

	w, err := c.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}
	if _, err = w.Write(msg); err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("failed to close data writer: %w", err)
	}

	return c.Quit()
}

func (s *Service) SendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	subject, plainBody, htmlBody := composeVerificationEmail(s.config, username, token, code)
	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}

func (s *Service) SendPasswordResetEmail(ctx context.Context, toEmail, username, token string) error {
	subject, plainBody, htmlBody := composePasswordResetEmail(s.config, username, token)
	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}

func (s *Service) sendEmail(to, subject, plainBody, htmlBody string) error {
	if s.config.Transport == "sendmail" {
		return fmt.Errorf("sendmail transport not implemented")
	}

	from := fmt.Sprintf("%s <%s>", s.config.FromName, s.config.FromAddress)
	msg := buildMIMEMessage(from, to, subject, plainBody, htmlBody)

	auth := smtp.PlainAuth("", s.config.SMTPUsername, s.config.SMTPPassword, s.config.SMTPHost)

	addr := fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort)

	if s.config.TLS {
		return s.sender.SendTLS(addr, auth, s.config.FromAddress, []string{to}, msg, s.config.SMTPHost)
	}

	if s.config.DisableSTARTTLS {
		return s.sender.SendPlain(addr, auth, s.config.FromAddress, []string{to}, msg)
	}

	if s.config.SMTPPort == 465 {
		return s.sender.SendTLS(addr, auth, s.config.FromAddress, []string{to}, msg, s.config.SMTPHost)
	}

	if s.config.SMTPPort == 587 {
		return s.sender.SendSTARTTLS(addr, auth, s.config.FromAddress, []string{to}, msg, s.config.SMTPHost)
	}

	return s.sender.SendPlain(addr, auth, s.config.FromAddress, []string{to}, msg)
}

func (s *Service) SendResendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	subject, plainBody, htmlBody := composeResendVerificationEmail(s.config, username, token, code)
	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}

func (s *Service) SendTestEmail(ctx context.Context, toEmail string) error {
	subject := "Test Email"
	plainBody := "This is a test email to verify your SMTP configuration is working correctly."
	htmlBody := `
        <html>
        <body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
            <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
                <h2 style="color: #2c3e50;">Test Email</h2>
                <p>This is a test email to verify your SMTP configuration is working correctly.</p>
                <p style="color: #666; font-size: 14px; margin-top: 30px;">
                    If you received this email, your SMTP settings are configured properly.
                </p>
            </div>
        </body>
        </html>
    `
	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}
