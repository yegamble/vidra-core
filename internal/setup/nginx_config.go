package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

type nginxTemplateData struct {
	Domain       string
	Port         int
	UpstreamAddr string
}

func findProjectRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for {
		templatesDir := filepath.Join(dir, "nginx", "templates")
		if _, err := os.Stat(templatesDir); err == nil {
			return dir, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find project root")
		}
		dir = parent
	}
}

func GenerateNginxConfig(config *WizardConfig, outputDir string) error {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}

	root, err := findProjectRoot()
	if err != nil {
		return fmt.Errorf("finding project root: %w", err)
	}

	data := nginxTemplateData{
		Domain:       config.NginxDomain,
		Port:         config.NginxPort,
		UpstreamAddr: "app:8080",
	}

	var templateName string
	switch {
	case config.NginxProtocol == "http":
		templateName = "nginx-http.conf.tmpl"
	case config.NginxProtocol == "https" && config.NginxTLSMode == "self-signed":
		templateName = "nginx-https-selfsigned.conf.tmpl"
	case config.NginxProtocol == "https" && config.NginxTLSMode == "letsencrypt":
		templateName = "nginx-https-letsencrypt.conf.tmpl"
	default:
		return fmt.Errorf("unknown nginx protocol/TLS mode: %s/%s", config.NginxProtocol, config.NginxTLSMode)
	}

	templatePath := filepath.Join(root, "nginx", "templates", templateName)
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		return fmt.Errorf("parsing template %s: %w", templatePath, err)
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
		"common-security.conf": "security.conf",
		"common-proxy.conf":    "proxy.conf",
	}

	for srcName, dstName := range includes {
		srcPath := filepath.Join(root, "nginx", "templates", srcName)
		content, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("reading include file %s: %w", srcPath, err)
		}

		dstPath := filepath.Join(outputDir, dstName)
		if err := os.WriteFile(dstPath, content, 0644); err != nil {
			return fmt.Errorf("writing include file %s: %w", dstName, err)
		}
	}

	return nil
}
