package setup

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"

	"athena/internal/email"
	"athena/internal/security"
	"athena/internal/sysinfo"
)

//go:embed templates/*.html
var templatesFS embed.FS

type Wizard struct {
	templates map[string]*template.Template
	config    *WizardConfig
	mu        sync.Mutex

	// RateLimit controls test connection request throttling.
	// Defaults to in-memory. Set to NewRedisRateLimiter() for persistence across restarts.
	RateLimit RateLimiter

	// URLValidator is used by test connection handlers for SSRF protection.
	// Defaults to security.NewURLValidator() (blocks private IPs).
	// Override with security.NewURLValidatorAllowPrivate() in integration tests.
	URLValidator *security.URLValidator

	// OutputDir is the base directory for generated files (.env, docker-compose.override.yml, nginx/conf).
	// Defaults to "" (current working directory).
	// Override with t.TempDir() in tests to avoid writing to the real project directory.
	OutputDir string
}

type WizardConfig struct {
	PostgresMode     string
	DatabaseURL      string
	CreateDB         bool
	PostgresHost     string
	PostgresPort     int
	PostgresUser     string
	PostgresPassword string
	PostgresDB       string
	PostgresSSLMode  string

	RedisMode     string
	RedisURL      string
	EnableIPFS    bool
	IPFSMode      string
	IPFSAPIUrl    string
	EnableClamAV  bool
	EnableWhisper bool

	EnableIOTA  bool
	IOTAMode    string
	IOTANodeURL string
	IOTANetwork string

	EnableEmail         bool
	SMTPMode            string
	SMTPHost            string
	SMTPPort            int
	SMTPUsername        string
	SMTPPassword        string
	SMTPFromAddress     string
	SMTPFromName        string
	SMTPTLS             bool
	SMTPDisableSTARTTLS bool

	StoragePath     string
	BackupEnabled   bool
	BackupTarget    string
	BackupSchedule  string
	BackupRetention string
	BackupLocalPath string

	JWTSecret     string
	AdminUsername string
	AdminEmail    string
	AdminPassword string

	NginxEnabled  bool
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
	// Parse each page template individually with the layout to avoid
	// {{define "content"}} collision (last alphabetical file would win)
	templates := make(map[string]*template.Template)
	pages := []string{"welcome", "quickinstall", "database", "services", "email", "networking", "storage", "security", "review", "complete"}
	for _, page := range pages {
		t := template.Must(template.ParseFS(templatesFS, "templates/layout.html", "templates/"+page+".html"))
		templates[page] = t
	}

	sysInfo := sysinfo.DetectResources()

	jwtSecret, err := GenerateJWTSecret()
	if err != nil {
		log.Fatalf("Failed to generate JWT secret: %v", err)
	}

	return &Wizard{
		templates: templates,
		RateLimit: NewMemoryRateLimiter(),
		URLValidator:   security.NewURLValidator(),
		OutputDir:      "",
		config: &WizardConfig{
			PostgresMode:    "docker",
			PostgresPort:    5432,
			PostgresUser:    "athena",
			PostgresDB:      "athena",
			PostgresSSLMode: "disable",
			RedisMode:       "docker",
			IPFSMode:        "docker",
			IOTAMode:        "docker",
			IOTANetwork:     "testnet",
			EnableEmail:     true,
			SMTPMode:        "docker",
			SMTPHost:        "localhost",
			SMTPPort:        1025,
			SMTPFromAddress: "noreply@localhost",
			SMTPFromName:    "Athena",
			StoragePath:     "./data/storage",
			BackupEnabled:   true,
			BackupTarget:    "local",
			BackupSchedule:  "0 2 * * *",
			BackupRetention: "7",
			BackupLocalPath: "./backups",
			JWTSecret:       jwtSecret,
			NginxEnabled:    true,
			NginxDomain:     "localhost",
			NginxPort:       80,
			NginxProtocol:   "http",
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

func (w *Wizard) HandleEmail(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.processEmailForm(rw, r)
		return
	}

	data := &TemplateData{
		Title:           "Email Configuration",
		CurrentStep:     "email",
		ShowBreadcrumb:  true,
		ShowActions:     true,
		ShowBack:        true,
		ShowContinue:    true,
		BackURL:         "/setup/services",
		Config:          w.config,
		SystemResources: w.config.SystemResources,
		CompletedSteps:  map[string]bool{"welcome": true, "database": true, "services": true},
	}

	w.renderTemplate(rw, "layout.html", "email.html", data)
}

func (w *Wizard) HandleNetworking(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.processNetworkingForm(rw, r)
		return
	}

	data := &TemplateData{
		Title:           "Networking Configuration",
		CurrentStep:     "networking",
		ShowBreadcrumb:  true,
		ShowActions:     true,
		ShowBack:        true,
		ShowContinue:    true,
		BackURL:         "/setup/email",
		Config:          w.config,
		SystemResources: w.config.SystemResources,
		CompletedSteps:  map[string]bool{"welcome": true, "database": true, "services": true, "email": true},
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
		CompletedSteps:  map[string]bool{"welcome": true, "database": true, "services": true, "email": true, "networking": true},
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
		CompletedSteps:  map[string]bool{"welcome": true, "database": true, "services": true, "email": true, "networking": true, "storage": true},
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
		CompletedSteps:  map[string]bool{"welcome": true, "database": true, "services": true, "email": true, "networking": true, "storage": true, "security": true},
	}

	w.renderTemplate(rw, "layout.html", "review.html", data)
}

func (w *Wizard) HandleTestEmail(rw http.ResponseWriter, r *http.Request) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var req struct {
		Email string `json:"email"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(rw, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.Email == "" || !strings.Contains(req.Email, "@") {
		http.Error(rw, "Valid email address required", http.StatusBadRequest)
		return
	}

	clientIP, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		clientIP = r.RemoteAddr
	}

	if !w.getRateLimiter().CheckRateLimit(clientIP) {
		http.Error(rw, "Too many test emails. Please wait 5 minutes.", http.StatusTooManyRequests)
		return
	}

	emailConfig := &email.Config{
		SMTPHost:        w.config.SMTPHost,
		SMTPPort:        w.config.SMTPPort,
		SMTPUsername:    w.config.SMTPUsername,
		SMTPPassword:    w.config.SMTPPassword,
		TLS:             w.config.SMTPTLS,
		DisableSTARTTLS: w.config.SMTPDisableSTARTTLS,
		FromAddress:     w.config.SMTPFromAddress,
		FromName:        w.config.SMTPFromName,
	}

	emailService := email.NewService(emailConfig)
	sendErr := emailService.SendTestEmail(r.Context(), req.Email)

	response := make(map[string]interface{})
	if sendErr != nil {
		log.Printf("SMTP test email failed: %v", sendErr)
		response["success"] = false
		response["message"] = "Failed to send test email. Check your SMTP settings."
	} else {
		response["success"] = true
		if w.config.SMTPMode == "docker" {
			response["message"] = "Test email sent! Check Mailpit UI at http://localhost:8025"
		} else {
			response["message"] = "Test email sent successfully to " + req.Email
		}
	}

	rw.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(rw).Encode(response)
}

func (w *Wizard) HandleQuickInstall(rw http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		w.processQuickInstallForm(rw, r)
		return
	}

	data := &TemplateData{
		Title:           "Quick Install (Docker)",
		CurrentStep:     "quickinstall",
		ShowBreadcrumb:  false,
		ShowActions:     false,
		Config:          w.config,
		SystemResources: w.config.SystemResources,
	}

	w.renderTemplate(rw, "layout.html", "quickinstall.html", data)
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

	// Extract page name from content (e.g., "database.html" → "database")
	pageName := strings.TrimSuffix(content, ".html")
	tmpl, ok := w.templates[pageName]
	if !ok {
		log.Printf("Template not found for page: %s", pageName)
		http.Error(rw, "Internal server error", http.StatusInternalServerError)
		return
	}

	var buf bytes.Buffer
	if err := tmpl.ExecuteTemplate(&buf, layout, data); err != nil {
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
