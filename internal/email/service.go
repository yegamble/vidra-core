package email

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"
)

// Config holds email configuration
type Config struct {
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	FromAddress  string
	FromName     string
	BaseURL      string
}

// Service handles email sending
type Service struct {
	config *Config
	sender EmailSender
}

// NewService creates a new email service
func NewService(config *Config) *Service {
	return NewServiceWithSender(config, &smtpSender{})
}

// NewServiceWithSender allows injecting a custom sender implementation
func NewServiceWithSender(config *Config, sender EmailSender) *Service {
	return &Service{config: config, sender: sender}
}

// EmailSender abstracts SMTP sending so it can be mocked in tests
type EmailSender interface {
	// SendPlain sends using a plain connection (e.g., port 25 or local dev servers)
	SendPlain(addr string, auth smtp.Auth, from string, to []string, msg []byte) error
	// SendTLS sends using implicit TLS (port 465)
	SendTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, host string) error
	// SendSTARTTLS sends using STARTTLS (port 587)
	SendSTARTTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, host string) error
}

// smtpSender is the default EmailSender using net/smtp
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
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("failed to create SMTP client: %w", err)
	}
	defer client.Close()

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
	defer c.Close()

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

// SendVerificationEmail sends an email with verification link and code
func (s *Service) SendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	subject, plainBody, htmlBody := composeVerificationEmail(s.config, username, token, code)
	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}

// SendPasswordResetEmail sends a password reset email
func (s *Service) SendPasswordResetEmail(ctx context.Context, toEmail, username, token string) error {
	subject, plainBody, htmlBody := composePasswordResetEmail(s.config, username, token)
	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}

// sendEmail sends an email with both plain text and HTML versions
func (s *Service) sendEmail(to, subject, plainBody, htmlBody string) error {
	from := fmt.Sprintf("%s <%s>", s.config.FromName, s.config.FromAddress)
	msg := buildMIMEMessage(from, to, subject, plainBody, htmlBody)

	// Set up authentication
	auth := smtp.PlainAuth("", s.config.SMTPUsername, s.config.SMTPPassword, s.config.SMTPHost)

	// Connect to the SMTP server
	addr := fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort)

	// For TLS connections (port 465)
	if s.config.SMTPPort == 465 {
		return s.sender.SendTLS(addr, auth, s.config.FromAddress, []string{to}, msg, s.config.SMTPHost)
	}

	// For STARTTLS connections (port 587)
	if s.config.SMTPPort == 587 {
		return s.sender.SendSTARTTLS(addr, auth, s.config.FromAddress, []string{to}, msg, s.config.SMTPHost)
	}

	// For plain connections (port 25) - not recommended
	return s.sender.SendPlain(addr, auth, s.config.FromAddress, []string{to}, msg)
}

// SendResendVerificationEmail sends a new verification email
func (s *Service) SendResendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	subject, plainBody, htmlBody := composeResendVerificationEmail(s.config, username, token, code)
	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}

// composeVerificationEmail returns the subject and bodies for a verification email
func composeVerificationEmail(cfg *Config, username, token, code string) (string, string, string) {
	subject := "Verify Your Email Address"
	verificationLink := fmt.Sprintf("%s/verify-email?token=%s", cfg.BaseURL, token)

	htmlBody := fmt.Sprintf(`
        <html>
        <body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
            <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
                <h2 style="color: #2c3e50;">Welcome to Athena, %s!</h2>
                
                <p>Thank you for registering. Please verify your email address to complete your registration.</p>
                
                <div style="margin: 30px 0;">
                    <a href="%s" style="background-color: #3498db; color: white; padding: 12px 30px; text-decoration: none; border-radius: 5px; display: inline-block;">
                        Verify Email Address
                    </a>
                </div>
                
                <p>Or you can enter this verification code manually:</p>
                <div style="background-color: #f8f9fa; padding: 15px; border-radius: 5px; font-size: 24px; font-weight: bold; text-align: center; letter-spacing: 3px;">
                    %s
                </div>
                
                <p style="color: #666; font-size: 14px; margin-top: 30px;">
                    This verification link and code will expire in 24 hours. If you didn't create an account, you can safely ignore this email.
                </p>
                
                <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
                
                <p style="color: #999; font-size: 12px;">
                    If you're having trouble clicking the button, copy and paste this URL into your browser:<br>
                    <a href="%s" style="color: #3498db;">%s</a>
                </p>
            </div>
        </body>
        </html>
    `, username, verificationLink, code, verificationLink, verificationLink)

	plainBody := fmt.Sprintf(`
Welcome to Athena, %s!

Thank you for registering. Please verify your email address to complete your registration.

Click this link to verify your email:
%s

Or enter this verification code manually:
%s

This verification link and code will expire in 24 hours. If you didn't create an account, you can safely ignore this email.
    `, username, verificationLink, code)

	return subject, plainBody, htmlBody
}

// composePasswordResetEmail returns the subject and bodies for a password reset
func composePasswordResetEmail(cfg *Config, username, token string) (string, string, string) {
	subject := "Reset Your Password"
	resetLink := fmt.Sprintf("%s/reset-password?token=%s", cfg.BaseURL, token)

	htmlBody := fmt.Sprintf(`
        <html>
        <body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
            <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
                <h2 style="color: #2c3e50;">Password Reset Request</h2>
                
                <p>Hi %s,</p>
                
                <p>We received a request to reset your password. Click the button below to set a new password:</p>
                
                <div style="margin: 30px 0;">
                    <a href="%s" style="background-color: #e74c3c; color: white; padding: 12px 30px; text-decoration: none; border-radius: 5px; display: inline-block;">
                        Reset Password
                    </a>
                </div>
                
                <p style="color: #666; font-size: 14px;">
                    This link will expire in 1 hour. If you didn't request a password reset, you can safely ignore this email.
                </p>
                
                <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
                
                <p style="color: #999; font-size: 12px;">
                    If you're having trouble clicking the button, copy and paste this URL into your browser:<br>
                    <a href="%s" style="color: #3498db;">%s</a>
                </p>
            </div>
        </body>
        </html>
    `, username, resetLink, resetLink, resetLink)

	plainBody := fmt.Sprintf(`
Password Reset Request

Hi %s,

We received a request to reset your password. Visit this link to set a new password:
%s

This link will expire in 1 hour. If you didn't request a password reset, you can safely ignore this email.
    `, username, resetLink)

	return subject, plainBody, htmlBody
}

// composeResendVerificationEmail returns subject and bodies for resend verification
func composeResendVerificationEmail(cfg *Config, username, token, code string) (string, string, string) {
	subject := "New Verification Code"
	verificationLink := fmt.Sprintf("%s/verify-email?token=%s", cfg.BaseURL, token)

	htmlBody := fmt.Sprintf(`
        <html>
        <body style="font-family: Arial, sans-serif; line-height: 1.6; color: #333;">
            <div style="max-width: 600px; margin: 0 auto; padding: 20px;">
                <h2 style="color: #2c3e50;">New Verification Code</h2>
                
                <p>Hi %s,</p>
                
                <p>You requested a new verification code. Here's your new code to verify your email address:</p>
                
                <div style="margin: 30px 0;">
                    <a href="%s" style="background-color: #27ae60; color: white; padding: 12px 30px; text-decoration: none; border-radius: 5px; display: inline-block;">
                        Verify Email Address
                    </a>
                </div>
                
                <p>Or enter this new verification code:</p>
                <div style="background-color: #f8f9fa; padding: 15px; border-radius: 5px; font-size: 24px; font-weight: bold; text-align: center; letter-spacing: 3px;">
                    %s
                </div>
                
                <p style="color: #666; font-size: 14px; margin-top: 30px;">
                    This verification link and code will expire in 24 hours.
                </p>
                
                <hr style="border: none; border-top: 1px solid #eee; margin: 30px 0;">
                
                <p style="color: #999; font-size: 12px;">
                    If you're having trouble clicking the button, copy and paste this URL into your browser:<br>
                    <a href="%s" style="color: #3498db;">%s</a>
                </p>
            </div>
        </body>
        </html>
    `, username, verificationLink, code, verificationLink, verificationLink)

	plainBody := fmt.Sprintf(`
New Verification Code

Hi %s,

You requested a new verification code. Here's your new code to verify your email address:

Click this link to verify:
%s

Or enter this verification code:
%s

This verification link and code will expire in 24 hours.
    `, username, verificationLink, code)

	return subject, plainBody, htmlBody
}

// buildMIMEMessage constructs a multipart/alternative MIME message with a fixed boundary
func buildMIMEMessage(from, to, subject, plainBody, htmlBody string) []byte {
	parts := []string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=\"boundary123\"",
		"",
		"--boundary123",
		"Content-Type: text/plain; charset=\"UTF-8\"",
		"",
		strings.TrimSpace(plainBody),
		"",
		"--boundary123",
		"Content-Type: text/html; charset=\"UTF-8\"",
		"",
		strings.TrimSpace(htmlBody),
		"",
		"--boundary123--",
	}
	return []byte(strings.Join(parts, "\r\n"))
}
