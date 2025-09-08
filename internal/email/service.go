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
}

// NewService creates a new email service
func NewService(config *Config) *Service {
	return &Service{config: config}
}

// SendVerificationEmail sends an email with verification link and code
func (s *Service) SendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	subject := "Verify Your Email Address"

	verificationLink := fmt.Sprintf("%s/verify-email?token=%s", s.config.BaseURL, token)

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

	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}

// SendPasswordResetEmail sends a password reset email
func (s *Service) SendPasswordResetEmail(ctx context.Context, toEmail, username, token string) error {
	subject := "Reset Your Password"

	resetLink := fmt.Sprintf("%s/reset-password?token=%s", s.config.BaseURL, token)

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

	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}

// sendEmail sends an email with both plain text and HTML versions
func (s *Service) sendEmail(to, subject, plainBody, htmlBody string) error {
	from := fmt.Sprintf("%s <%s>", s.config.FromName, s.config.FromAddress)

	// Create message with proper MIME formatting
	message := []string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		"Content-Type: multipart/alternative; boundary=\"boundary123\"",
		"",
		"--boundary123",
		"Content-Type: text/plain; charset=\"UTF-8\"",
		"",
		plainBody,
		"",
		"--boundary123",
		"Content-Type: text/html; charset=\"UTF-8\"",
		"",
		htmlBody,
		"",
		"--boundary123--",
	}

	msg := []byte(strings.Join(message, "\r\n"))

	// Set up authentication
	auth := smtp.PlainAuth("", s.config.SMTPUsername, s.config.SMTPPassword, s.config.SMTPHost)

	// Connect to the SMTP server
	addr := fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort)

	// For TLS connections (port 465)
	if s.config.SMTPPort == 465 {
		return s.sendEmailTLS(addr, auth, s.config.FromAddress, []string{to}, msg)
	}

	// For STARTTLS connections (port 587) or plain (port 25)
	return smtp.SendMail(addr, auth, s.config.FromAddress, []string{to}, msg)
}

// sendEmailTLS sends email over TLS connection (port 465)
func (s *Service) sendEmailTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		ServerName: s.config.SMTPHost,
		MinVersion: tls.VersionTLS12,
	})
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, s.config.SMTPHost)
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

	_, err = w.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	err = w.Close()
	if err != nil {
		return fmt.Errorf("failed to close data writer: %w", err)
	}

	return client.Quit()
}

// SendResendVerificationEmail sends a new verification email
func (s *Service) SendResendVerificationEmail(ctx context.Context, toEmail, username, token, code string) error {
	subject := "New Verification Code"

	verificationLink := fmt.Sprintf("%s/verify-email?token=%s", s.config.BaseURL, token)

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

	return s.sendEmail(toEmail, subject, plainBody, htmlBody)
}
