package email

import (
	"bytes"
	"context"
	"net/smtp"
	"testing"

	"github.com/stretchr/testify/assert"
)

type fakeSender struct {
	plainCalled    bool
	tlsCalled      bool
	starttlsCalled bool
	addr           string
	host           string
	from           string
	to             []string
	msg            []byte
}

func (f *fakeSender) SendPlain(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	f.plainCalled = true
	f.addr, f.from, f.to, f.msg = addr, from, to, append([]byte(nil), msg...)
	return nil
}

func (f *fakeSender) SendTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, host string) error {
	f.tlsCalled = true
	f.addr, f.host, f.from, f.to, f.msg = addr, host, from, to, append([]byte(nil), msg...)
	return nil
}

func (f *fakeSender) SendSTARTTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte, host string) error {
	f.starttlsCalled = true
	f.addr, f.host, f.from, f.to, f.msg = addr, host, from, to, append([]byte(nil), msg...)
	return nil
}

var _ EmailSender = (*fakeSender)(nil)

func TestService_UsesTLS_On465(t *testing.T) {
	fs := &fakeSender{}
	cfg := &Config{SMTPHost: "smtp.example.com", SMTPPort: 465, FromAddress: "no-reply@example.com", FromName: "Athena", BaseURL: "https://app.example.com"}
	svc := NewServiceWithSender(cfg, fs)

	err := svc.SendVerificationEmail(context.Background(), "user@example.com", "alice", "tok", "123456")
	assert.NoError(t, err)
	assert.True(t, fs.tlsCalled)
	assert.False(t, fs.starttlsCalled)
	assert.False(t, fs.plainCalled)
	assert.Equal(t, "smtp.example.com:465", fs.addr)
	assert.Equal(t, "smtp.example.com", fs.host)
	assert.Equal(t, "no-reply@example.com", fs.from)
	assert.Equal(t, []string{"user@example.com"}, fs.to)
	assert.True(t, bytes.Contains(fs.msg, []byte("Subject: Verify Your Email Address")))
}

func TestService_UsesSTARTTLS_On587(t *testing.T) {
	fs := &fakeSender{}
	cfg := &Config{SMTPHost: "smtp.example.com", SMTPPort: 587, FromAddress: "no-reply@example.com", FromName: "Athena", BaseURL: "https://app.example.com"}
	svc := NewServiceWithSender(cfg, fs)

	err := svc.SendPasswordResetEmail(context.Background(), "user@example.com", "bob", "resettok")
	assert.NoError(t, err)
	assert.True(t, fs.starttlsCalled)
	assert.False(t, fs.tlsCalled)
	assert.False(t, fs.plainCalled)
	assert.Equal(t, "smtp.example.com:587", fs.addr)
	assert.Equal(t, "smtp.example.com", fs.host)
	assert.Equal(t, "no-reply@example.com", fs.from)
	assert.Equal(t, []string{"user@example.com"}, fs.to)
	assert.True(t, bytes.Contains(fs.msg, []byte("Subject: Reset Your Password")))
}

func TestService_UsesPlain_OtherPorts(t *testing.T) {
	fs := &fakeSender{}
	cfg := &Config{SMTPHost: "smtp.example.com", SMTPPort: 1025, FromAddress: "no-reply@example.com", FromName: "Athena", BaseURL: "https://app.example.com"}
	svc := NewServiceWithSender(cfg, fs)

	err := svc.SendVerificationEmail(context.Background(), "user@example.com", "alice", "tok", "123456")
	assert.NoError(t, err)
	assert.True(t, fs.plainCalled)
	assert.False(t, fs.tlsCalled)
	assert.False(t, fs.starttlsCalled)
	assert.Equal(t, "smtp.example.com:1025", fs.addr)
	assert.Equal(t, "no-reply@example.com", fs.from)
	assert.Equal(t, []string{"user@example.com"}, fs.to)
}

func TestService_ConfigDrivenTLS_ForceTLSOnNonStandardPort(t *testing.T) {
	fs := &fakeSender{}
	cfg := &Config{SMTPHost: "smtp.example.com", SMTPPort: 2525, TLS: true, FromAddress: "no-reply@example.com", FromName: "Athena", BaseURL: "https://app.example.com"}
	svc := NewServiceWithSender(cfg, fs)

	err := svc.SendVerificationEmail(context.Background(), "user@example.com", "alice", "tok", "123456")
	assert.NoError(t, err)
	assert.True(t, fs.tlsCalled, "TLS=true should force TLS regardless of port")
	assert.False(t, fs.starttlsCalled)
	assert.False(t, fs.plainCalled)
	assert.Equal(t, "smtp.example.com:2525", fs.addr)
}

func TestService_ConfigDrivenTLS_DisableSTARTTLSOn587(t *testing.T) {
	fs := &fakeSender{}
	cfg := &Config{SMTPHost: "smtp.example.com", SMTPPort: 587, DisableSTARTTLS: true, FromAddress: "no-reply@example.com", FromName: "Athena", BaseURL: "https://app.example.com"}
	svc := NewServiceWithSender(cfg, fs)

	err := svc.SendVerificationEmail(context.Background(), "user@example.com", "alice", "tok", "123456")
	assert.NoError(t, err)
	assert.True(t, fs.plainCalled, "DisableSTARTTLS=true on port 587 should use plain")
	assert.False(t, fs.tlsCalled)
	assert.False(t, fs.starttlsCalled)
}

func TestService_SendmailTransport_ReturnsError(t *testing.T) {
	fs := &fakeSender{}
	cfg := &Config{Transport: "sendmail", SendmailPath: "/usr/sbin/sendmail", FromAddress: "no-reply@example.com", FromName: "Athena", BaseURL: "https://app.example.com"}
	svc := NewServiceWithSender(cfg, fs)

	err := svc.SendVerificationEmail(context.Background(), "user@example.com", "alice", "tok", "123456")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "sendmail transport not implemented")
}

func TestService_BackwardCompat_Port465FallsBackToTLS(t *testing.T) {
	fs := &fakeSender{}
	cfg := &Config{SMTPHost: "smtp.example.com", SMTPPort: 465, TLS: false, DisableSTARTTLS: false, FromAddress: "no-reply@example.com", FromName: "Athena", BaseURL: "https://app.example.com"}
	svc := NewServiceWithSender(cfg, fs)

	err := svc.SendVerificationEmail(context.Background(), "user@example.com", "alice", "tok", "123456")
	assert.NoError(t, err)
	assert.True(t, fs.tlsCalled, "Port 465 with zero-value config should fall back to TLS")
}

func TestService_BackwardCompat_Port2525FallsBackToPlain(t *testing.T) {
	fs := &fakeSender{}
	cfg := &Config{SMTPHost: "smtp.example.com", SMTPPort: 2525, TLS: false, DisableSTARTTLS: false, FromAddress: "no-reply@example.com", FromName: "Athena", BaseURL: "https://app.example.com"}
	svc := NewServiceWithSender(cfg, fs)

	err := svc.SendVerificationEmail(context.Background(), "user@example.com", "alice", "tok", "123456")
	assert.NoError(t, err)
	assert.True(t, fs.plainCalled, "Non-standard port with zero-value config should fall back to plain")
}
