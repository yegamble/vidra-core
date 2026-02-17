package email

import (
	"crypto/rand"
	"fmt"
	"html"
	"strings"
)

func composeVerificationEmail(cfg *Config, username, token, code string) (string, string, string) {
	subject := "Verify Your Email Address"
	verificationLink := fmt.Sprintf("%s/verify-email?token=%s", cfg.BaseURL, token)

	safeUsername := html.EscapeString(username)
	safeLink := html.EscapeString(verificationLink)
	safeCode := html.EscapeString(code)

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
    `, safeUsername, safeLink, safeCode, safeLink, safeLink)

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

func composePasswordResetEmail(cfg *Config, username, token string) (string, string, string) {
	subject := "Reset Your Password"
	resetLink := fmt.Sprintf("%s/reset-password?token=%s", cfg.BaseURL, token)

	safeUsername := html.EscapeString(username)
	safeLink := html.EscapeString(resetLink)

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
    `, safeUsername, safeLink, safeLink, safeLink)

	plainBody := fmt.Sprintf(`
Password Reset Request

Hi %s,

We received a request to reset your password. Visit this link to set a new password:
%s

This link will expire in 1 hour. If you didn't request a password reset, you can safely ignore this email.
    `, username, resetLink)

	return subject, plainBody, htmlBody
}

func composeResendVerificationEmail(cfg *Config, username, token, code string) (string, string, string) {
	subject := "New Verification Code"
	verificationLink := fmt.Sprintf("%s/verify-email?token=%s", cfg.BaseURL, token)

	safeUsername := html.EscapeString(username)
	safeLink := html.EscapeString(verificationLink)
	safeCode := html.EscapeString(code)

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
    `, safeUsername, safeLink, safeCode, safeLink, safeLink)

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

func generateBoundary() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("boundary_%x", b)
}

func buildMIMEMessage(from, to, subject, plainBody, htmlBody string) []byte {
	boundary := generateBoundary()
	parts := []string{
		fmt.Sprintf("From: %s", from),
		fmt.Sprintf("To: %s", to),
		fmt.Sprintf("Subject: %s", subject),
		"MIME-Version: 1.0",
		fmt.Sprintf("Content-Type: multipart/alternative; boundary=\"%s\"", boundary),
		"",
		"--" + boundary,
		"Content-Type: text/plain; charset=\"UTF-8\"",
		"",
		strings.TrimSpace(plainBody),
		"",
		"--" + boundary,
		"Content-Type: text/html; charset=\"UTF-8\"",
		"",
		strings.TrimSpace(htmlBody),
		"",
		"--" + boundary + "--",
	}
	return []byte(strings.Join(parts, "\r\n"))
}
