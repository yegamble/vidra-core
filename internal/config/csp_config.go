package config

type CSPConfig struct {
	Enabled bool

	ReportOnly bool

	ReportURI string
}

func loadCSPConfig() CSPConfig {
	return CSPConfig{
		Enabled:    GetBoolEnv("CSP_ENABLED", true),
		ReportOnly: GetBoolEnv("CSP_REPORT_ONLY", false),
		ReportURI:  GetEnvOrDefault("CSP_REPORT_URI", ""),
	}
}
