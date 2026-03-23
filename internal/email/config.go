package email

import "vidra-core/internal/config"

func NewConfigFromAppConfig(cfg *config.Config) *Config {
	return &Config{
		Transport:       cfg.SMTPTransport,
		SendmailPath:    cfg.SMTPSendmailPath,
		SMTPHost:        cfg.SMTPHost,
		SMTPPort:        cfg.SMTPPort,
		SMTPUsername:    cfg.SMTPUsername,
		SMTPPassword:    cfg.SMTPPassword,
		TLS:             cfg.SMTPTLS,
		DisableSTARTTLS: cfg.SMTPDisableSTARTTLS,
		CAFile:          cfg.SMTPCAFile,
		FromAddress:     cfg.SMTPFromAddress,
		FromName:        cfg.SMTPFromName,
		BaseURL:         cfg.PublicBaseURL,
	}
}
