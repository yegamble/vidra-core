package config

type CSPConfig struct {
	Enabled bool

	ReportOnly bool

	ReportURI string
}

func loadCSPConfig() CSPConfig {
	return CSPConfig{
		Enabled:    getBoolEnv("CSP_ENABLED", true),
		ReportOnly: getBoolEnv("CSP_REPORT_ONLY", false),
		ReportURI:  getEnvOrDefault("CSP_REPORT_URI", ""),
	}
}
