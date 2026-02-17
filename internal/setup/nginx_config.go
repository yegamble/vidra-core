package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"athena/nginx"
)

type nginxTemplateData struct {
	Domain       string
	Port         int
	UpstreamAddr string
}

func GenerateNginxConfig(config *WizardConfig, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	data := nginxTemplateData{
		Domain:       config.NginxDomain,
		Port:         config.NginxPort,
		UpstreamAddr: "app:8080",
	}

	var templateName string
	switch {
	case config.NginxProtocol == "http":
		templateName = "templates/nginx-http.conf.tmpl"
	case config.NginxProtocol == "https" && config.NginxTLSMode == "self-signed":
		templateName = "templates/nginx-https-selfsigned.conf.tmpl"
	case config.NginxProtocol == "https" && config.NginxTLSMode == "letsencrypt":
		templateName = "templates/nginx-https-letsencrypt.conf.tmpl"
	default:
		return fmt.Errorf("unknown nginx protocol/TLS mode: %s/%s", config.NginxProtocol, config.NginxTLSMode)
	}

	tmpl, err := template.ParseFS(nginx.TemplatesFS, templateName)
	if err != nil {
		return fmt.Errorf("parsing template %s: %w", templateName, err)
	}

	mainConfPath := filepath.Join(outputDir, "default.conf")
	mainConfFile, err := os.Create(mainConfPath)
	if err != nil {
		return fmt.Errorf("creating main config file: %w", err)
	}
	defer mainConfFile.Close()

	if err := tmpl.Execute(mainConfFile, data); err != nil {
		return fmt.Errorf("executing template: %w", err)
	}

	includes := map[string]string{
		"templates/common-security.conf": "security.conf",
		"templates/common-proxy.conf":    "proxy.conf",
	}

	for srcName, dstName := range includes {
		content, err := nginx.TemplatesFS.ReadFile(srcName)
		if err != nil {
			return fmt.Errorf("reading include file %s: %w", srcName, err)
		}

		dstPath := filepath.Join(outputDir, dstName)
		if err := os.WriteFile(dstPath, content, 0644); err != nil {
			return fmt.Errorf("writing include file %s: %w", dstName, err)
		}
	}

	return nil
}
