package email

import (
	"testing"

	"athena/internal/config"

	"github.com/stretchr/testify/assert"
)

func TestNewConfigFromAppConfig(t *testing.T) {
	appCfg := &config.Config{
		EnableEmail:         true,
		SMTPTransport:       "smtp",
		SMTPSendmailPath:    "/usr/sbin/sendmail",
		SMTPHost:            "smtp.example.com",
		SMTPPort:            587,
		SMTPUsername:        "user@example.com",
		SMTPPassword:        "password123",
		SMTPTLS:             false,
		SMTPDisableSTARTTLS: false,
		SMTPCAFile:          "/etc/ssl/ca.pem",
		SMTPFromAddress:     "no-reply@example.com",
		SMTPFromName:        "Athena",
		PublicBaseURL:       "https://athena.example.com",
	}

	emailCfg := NewConfigFromAppConfig(appCfg)

	assert.NotNil(t, emailCfg)
	assert.Equal(t, "smtp", emailCfg.Transport)
	assert.Equal(t, "/usr/sbin/sendmail", emailCfg.SendmailPath)
	assert.Equal(t, "smtp.example.com", emailCfg.SMTPHost)
	assert.Equal(t, 587, emailCfg.SMTPPort)
	assert.Equal(t, "user@example.com", emailCfg.SMTPUsername)
	assert.Equal(t, "password123", emailCfg.SMTPPassword)
	assert.Equal(t, false, emailCfg.TLS)
	assert.Equal(t, false, emailCfg.DisableSTARTTLS)
	assert.Equal(t, "/etc/ssl/ca.pem", emailCfg.CAFile)
	assert.Equal(t, "no-reply@example.com", emailCfg.FromAddress)
	assert.Equal(t, "Athena", emailCfg.FromName)
	assert.Equal(t, "https://athena.example.com", emailCfg.BaseURL)
}

func TestNewConfigFromAppConfig_AllFieldsMapped(t *testing.T) {
	appCfg := &config.Config{
		SMTPTransport:       "smtp",
		SMTPSendmailPath:    "/usr/sbin/sendmail",
		SMTPHost:            "mail.test.com",
		SMTPPort:            465,
		SMTPUsername:        "test",
		SMTPPassword:        "pass",
		SMTPTLS:             true,
		SMTPDisableSTARTTLS: true,
		SMTPCAFile:          "/ca.pem",
		SMTPFromAddress:     "from@test.com",
		SMTPFromName:        "Test",
		PublicBaseURL:       "http://localhost",
	}

	emailCfg := NewConfigFromAppConfig(appCfg)

	assert.Equal(t, "smtp", emailCfg.Transport)
	assert.Equal(t, "/usr/sbin/sendmail", emailCfg.SendmailPath)
	assert.Equal(t, "mail.test.com", emailCfg.SMTPHost)
	assert.Equal(t, 465, emailCfg.SMTPPort)
	assert.Equal(t, "test", emailCfg.SMTPUsername)
	assert.Equal(t, "pass", emailCfg.SMTPPassword)
	assert.Equal(t, true, emailCfg.TLS)
	assert.Equal(t, true, emailCfg.DisableSTARTTLS)
	assert.Equal(t, "/ca.pem", emailCfg.CAFile)
	assert.Equal(t, "from@test.com", emailCfg.FromAddress)
	assert.Equal(t, "Test", emailCfg.FromName)
	assert.Equal(t, "http://localhost", emailCfg.BaseURL)
}
