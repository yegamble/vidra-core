package setup

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"html/template"
	"log"
	"net/http"
	"sync"

	"athena/internal/sysinfo"
)

//go:embed templates/*.html
var templatesFS embed.FS

type Wizard struct {
	templates *template.Template
	config    *WizardConfig
	mu        sync.Mutex
}

type WizardConfig struct {
	PostgresMode string
	DatabaseURL  string
	CreateDB     bool

	RedisMode     string
	RedisURL      string
	EnableIPFS    bool
	IPFSMode      string
	IPFSAPIUrl    string
	EnableClamAV  bool
	EnableWhisper bool

	StoragePath     string
	BackupEnabled   bool
	BackupTarget    string
	BackupSchedule  string
	BackupRetention string
	BackupLocalPath string

	JWTSecret     string
	AdminUsername string
	AdminEmail    string

	NginxDomain   string
	NginxPort     int
	NginxProtocol string
	NginxTLSMode  string
	NginxEmail    string

	SystemResources *sysinfo.Resources
}

type TemplateData struct {
	Title           string
	CurrentStep     string
	ShowBreadcrumb  bool
	ShowActions     bool
	ShowBack        bool
	ShowContinue    bool
	DisableContinue bool
	BackURL         string
	CompletedSteps  map[string]bool
	Config          *WizardConfig
	SystemResources *sysinfo.Resources
	Recommendation  string
	Error           string
	Success         string
	MigrationCount  int
	Port            string
}

func NewWizard() *Wizard {
	tmpl := template.Must(template.ParseFS(templatesFS, "templates/*.html"))

	sysInfo := sysinfo.DetectResources()

	jwtSecret, err := GenerateJWTSecret()
	if err != nil {
		log.Fatalf("Failed to generate JWT secret: %v", err)
	}

	return &Wizard{
		templates: tmpl,
		config: &WizardConfig{
			PostgresMode:    "docker",
			RedisMode:       "docker",
			IPFSMode:        "docker",
			StoragePath:     "./data/storage",
			BackupEnabled:   true,
			BackupTarget:    "local",
			BackupSchedule:  "0 2 * * *",
			BackupRetention: "7",
			BackupLocalPath: "./backups",
			JWTSecret:       jwtSecret,
			SystemResources: sysInfo,
		},
	}
}

func (w *Wizard) HandleWelcome(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(rw, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sysInfo := sysinfo.DetectResources()
	rec := sysinfo.Recommend(sysInfo)

	data := &TemplateData{
		Title:           "Welcome",
		CurrentStep:     "welcome",
		ShowBreadcrumb:  true,
		ShowActions:     true,
		ShowBack:        false,
		ShowContinue:    true,
		Config:          w.config,
		SystemResources: sysInfo,
		Recommendation:  rec.Explanation,
	}

	w.renderTemplate(rw, "layout.html", "welcome.html", data)
}

func (w *Wizard) HandleDatabase(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.processDatabaseForm(rw, r)
		return
	}

	data := &TemplateData{
		Title:           "Database Configuration",
		CurrentStep:     "database",
		ShowBreadcrumb:  true,
		ShowActions:     true,
		ShowBack:        true,
		ShowContinue:    true,
		BackURL:         "/setup/welcome",
		Config:          w.config,
		SystemResources: w.config.SystemResources,
		CompletedSteps:  map[string]bool{"welcome": true},
	}

	w.renderTemplate(rw, "layout.html", "database.html", data)
}

func (w *Wizard) HandleServices(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.processServicesForm(rw, r)
		return
	}

	sysInfo := sysinfo.DetectResources()
	rec := sysinfo.Recommend(sysInfo)

	data := &TemplateData{
		Title:           "Services Configuration",
		CurrentStep:     "services",
		ShowBreadcrumb:  true,
		ShowActions:     true,
		ShowBack:        true,
		ShowContinue:    true,
		BackURL:         "/setup/database",
		Config:          w.config,
		SystemResources: sysInfo,
		Recommendation:  rec.Explanation,
		CompletedSteps:  map[string]bool{"welcome": true, "database": true},
	}

	w.renderTemplate(rw, "layout.html", "services.html", data)
}

func (w *Wizard) HandleNetworking(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.processNetworkingForm(rw, r)
		return
	}

	w.mu.Lock()
	if w.config.NginxDomain == "" {
		w.config.NginxDomain = "localhost"
	}
	if w.config.NginxPort == 0 {
		w.config.NginxPort = 80
	}
	if w.config.NginxProtocol == "" {
		w.config.NginxProtocol = "http"
	}
	w.mu.Unlock()

	data := &TemplateData{
		Title:           "Networking Configuration",
		CurrentStep:     "networking",
		ShowBreadcrumb:  true,
		ShowActions:     true,
		ShowBack:        true,
		ShowContinue:    true,
		BackURL:         "/setup/services",
		Config:          w.config,
		SystemResources: w.config.SystemResources,
		CompletedSteps:  map[string]bool{"welcome": true, "database": true, "services": true},
	}

	w.renderTemplate(rw, "layout.html", "networking.html", data)
}

func (w *Wizard) HandleStorage(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.processStorageForm(rw, r)
		return
	}

	data := &TemplateData{
		Title:           "Storage Configuration",
		CurrentStep:     "storage",
		ShowBreadcrumb:  true,
		ShowActions:     true,
		ShowBack:        true,
		ShowContinue:    true,
		BackURL:         "/setup/networking",
		Config:          w.config,
		SystemResources: w.config.SystemResources,
		CompletedSteps:  map[string]bool{"welcome": true, "database": true, "services": true, "networking": true},
	}

	w.renderTemplate(rw, "layout.html", "storage.html", data)
}

func (w *Wizard) HandleSecurity(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.processSecurityForm(rw, r)
		return
	}

	data := &TemplateData{
		Title:           "Security Configuration",
		CurrentStep:     "security",
		ShowBreadcrumb:  true,
		ShowActions:     true,
		ShowBack:        true,
		ShowContinue:    true,
		BackURL:         "/setup/storage",
		Config:          w.config,
		SystemResources: w.config.SystemResources,
		CompletedSteps:  map[string]bool{"welcome": true, "database": true, "services": true, "networking": true, "storage": true},
	}

	w.renderTemplate(rw, "layout.html", "security.html", data)
}

func (w *Wizard) HandleReview(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.processReviewForm(rw, r)
		return
	}

	data := &TemplateData{
		Title:           "Review Configuration",
		CurrentStep:     "review",
		ShowBreadcrumb:  true,
		ShowActions:     true,
		ShowBack:        true,
		ShowContinue:    true,
		BackURL:         "/setup/security",
		Config:          w.config,
		SystemResources: w.config.SystemResources,
		CompletedSteps:  map[string]bool{"welcome": true, "database": true, "services": true, "networking": true, "storage": true, "security": true},
	}

	w.renderTemplate(rw, "layout.html", "review.html", data)
}

func (w *Wizard) HandleComplete(rw http.ResponseWriter, r *http.Request) {
	data := &TemplateData{
		Title:           "Setup Complete",
		CurrentStep:     "complete",
		ShowBreadcrumb:  false,
		ShowActions:     false,
		Config:          w.config,
		SystemResources: w.config.SystemResources,
		MigrationCount:  61,
		Port:            "8080",
	}

	w.renderTemplate(rw, "layout.html", "complete.html", data)
}

func (w *Wizard) renderTemplate(rw http.ResponseWriter, layout, content string, data *TemplateData) {
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")

	var buf bytes.Buffer
	if err := w.templates.ExecuteTemplate(&buf, layout, data); err != nil {
		log.Printf("Template rendering error: %v", err)
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		return
	}

	if _, err := buf.WriteTo(rw); err != nil {
		log.Printf("Failed to write template response: %v", err)
	}
}

func GenerateJWTSecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}
