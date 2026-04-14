package setup

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
)

// validateCSRF checks that the request's CSRF token matches the wizard's token.
// Checks X-CSRF-Token header first (for JSON endpoints), then _csrf_token form value.
// Returns true if valid, false if invalid (and writes 403 to the response).
func (w *Wizard) validateCSRF(rw http.ResponseWriter, r *http.Request) bool {
	token := r.Header.Get("X-CSRF-Token")
	if token == "" {
		token = r.FormValue("_csrf_token")
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(w.csrfToken)) != 1 {
		http.Error(rw, "Forbidden - invalid CSRF token", http.StatusForbidden)
		return false
	}
	return true
}

// GenerateJWTSecret generates a cryptographically random JWT secret.
func GenerateJWTSecret() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(bytes), nil
}

func (w *Wizard) processDatabaseForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}
	if !w.validateCSRF(rw, r) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.config.PostgresMode = r.FormValue("POSTGRES_MODE")
	w.config.CreateDB = r.FormValue("create_db") == "true"

	if w.config.PostgresMode == "external" {
		// Parse individual PostgreSQL fields
		w.config.PostgresHost = r.FormValue("POSTGRES_HOST")
		portStr := r.FormValue("POSTGRES_PORT")
		if portStr != "" {
			port, err := strconv.Atoi(portStr)
			if err != nil || port <= 0 || port > 65535 {
				http.Error(rw, "Invalid PostgreSQL port: must be between 1 and 65535", http.StatusBadRequest)
				return
			}
			w.config.PostgresPort = port
		} else {
			w.config.PostgresPort = 5432
		}
		w.config.PostgresUser = r.FormValue("POSTGRES_USER")
		w.config.PostgresPassword = r.FormValue("POSTGRES_PASSWORD")
		dbName := r.FormValue("POSTGRES_DB")
		if dbName != "" {
			w.config.PostgresDB = dbName
		} else {
			w.config.PostgresDB = "vidra"
		}
		w.config.PostgresSSLMode = r.FormValue("POSTGRES_SSLMODE")
		if w.config.PostgresSSLMode == "" {
			w.config.PostgresSSLMode = "disable"
		}

		// Validate required fields
		if w.config.PostgresHost == "" {
			http.Error(rw, "PostgreSQL host is required for external mode", http.StatusBadRequest)
			return
		}
		if w.config.PostgresUser == "" {
			http.Error(rw, "PostgreSQL user is required for external mode", http.StatusBadRequest)
			return
		}
		if w.config.PostgresPassword == "" {
			http.Error(rw, "PostgreSQL password is required for external mode", http.StatusBadRequest)
			return
		}

		// Validate fields for shell metacharacters
		if containsShellMetachars(w.config.PostgresHost) {
			http.Error(rw, "PostgreSQL host contains invalid characters", http.StatusBadRequest)
			return
		}
		if containsShellMetachars(w.config.PostgresUser) {
			http.Error(rw, "PostgreSQL user contains invalid characters", http.StatusBadRequest)
			return
		}
		if containsShellMetachars(w.config.PostgresDB) {
			http.Error(rw, "PostgreSQL database name contains invalid characters", http.StatusBadRequest)
			return
		}

		// Construct DATABASE_URL using net/url.UserPassword
		u := &url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(w.config.PostgresUser, w.config.PostgresPassword),
			Host:   net.JoinHostPort(w.config.PostgresHost, strconv.Itoa(w.config.PostgresPort)),
			Path:   "/" + w.config.PostgresDB,
		}
		if w.config.PostgresSSLMode != "" {
			u.RawQuery = "sslmode=" + w.config.PostgresSSLMode
		}
		w.config.DatabaseURL = u.String()
	} else {
		// Docker mode - clear individual fields
		w.config.PostgresHost = ""
		w.config.PostgresPort = 5432
		w.config.PostgresUser = ""
		w.config.PostgresPassword = ""
		w.config.PostgresDB = "vidra"
		w.config.PostgresSSLMode = "disable"
		w.config.DatabaseURL = ""
	}

	http.Redirect(rw, r, "/setup/services", http.StatusSeeOther)
}

func (w *Wizard) processServicesForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}
	if !w.validateCSRF(rw, r) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.config.RedisMode = r.FormValue("REDIS_MODE")
	w.config.RedisURL = r.FormValue("REDIS_URL")
	w.config.EnableIPFS = r.FormValue("ENABLE_IPFS") == "true"
	w.config.IPFSMode = r.FormValue("IPFS_MODE")
	w.config.IPFSAPIUrl = r.FormValue("IPFS_API_URL")
	w.config.EnableClamAV = r.FormValue("ENABLE_CLAMAV") == "true"
	w.config.EnableWhisper = r.FormValue("ENABLE_WHISPER") == "true"

	w.config.EnableBitcoin = r.FormValue("ENABLE_BITCOIN") == "true"
	if w.config.EnableBitcoin {
		w.config.BTCPayServerURL = r.FormValue("BTCPAY_SERVER_URL")
		if w.config.BTCPayServerURL != "" {
			if err := ValidateBTCPayServerURL(w.config.BTCPayServerURL); err != nil {
				http.Error(rw, "Invalid BTCPay Server URL: "+err.Error(), http.StatusBadRequest)
				return
			}
		}
		w.config.BTCPayAPIKey = r.FormValue("BTCPAY_API_KEY")
		w.config.BTCPayStoreID = r.FormValue("BTCPAY_STORE_ID")
	}

	http.Redirect(rw, r, "/setup/email", http.StatusSeeOther)
}

func (w *Wizard) processEmailForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}
	if !w.validateCSRF(rw, r) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	smtpMode := r.FormValue("SMTP_MODE")
	w.config.SMTPMode = smtpMode
	w.config.EnableEmail = smtpMode != "disabled"

	switch smtpMode {
	case "disabled":
		http.Redirect(rw, r, "/setup/networking", http.StatusSeeOther)
		return
	case "docker":
		w.config.SMTPHost = "localhost"
		w.config.SMTPPort = 1025
		w.config.SMTPUsername = ""
		w.config.SMTPPassword = ""
		w.config.SMTPTLS = false
		w.config.SMTPDisableSTARTTLS = false
	case "external":
		w.config.SMTPHost = r.FormValue("SMTP_HOST")
		portStr := r.FormValue("SMTP_PORT")
		if portStr != "" {
			port, err := strconv.Atoi(portStr)
			if err != nil || port <= 0 || port > 65535 {
				http.Error(rw, "Invalid SMTP port number", http.StatusBadRequest)
				return
			}
			w.config.SMTPPort = port
		}
		w.config.SMTPUsername = r.FormValue("SMTP_USERNAME")
		w.config.SMTPPassword = r.FormValue("SMTP_PASSWORD")
		w.config.SMTPTLS = r.FormValue("SMTP_TLS") == "true"
		w.config.SMTPDisableSTARTTLS = r.FormValue("SMTP_DISABLE_STARTTLS") == "true"

		if w.config.SMTPHost == "" {
			http.Error(rw, "SMTP host is required for external mode", http.StatusBadRequest)
			return
		}
	}

	w.config.SMTPFromAddress = r.FormValue("SMTP_FROM_ADDRESS")
	w.config.SMTPFromName = r.FormValue("SMTP_FROM_NAME")

	if w.config.SMTPFromAddress == "" {
		http.Error(rw, "From address is required", http.StatusBadRequest)
		return
	}

	http.Redirect(rw, r, "/setup/networking", http.StatusSeeOther)
}

func (w *Wizard) processNetworkingForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}
	if !w.validateCSRF(rw, r) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	domain := r.FormValue("NGINX_DOMAIN")
	protocol := r.FormValue("NGINX_PROTOCOL")
	tlsMode := r.FormValue("NGINX_TLS_MODE")
	email := r.FormValue("NGINX_LETSENCRYPT_EMAIL")

	if protocol != "http" && protocol != "https" {
		http.Error(rw, "Invalid protocol: must be http or https", http.StatusBadRequest)
		return
	}
	if protocol == "https" && tlsMode != "self-signed" && tlsMode != "letsencrypt" {
		http.Error(rw, "Invalid TLS mode: must be self-signed or letsencrypt", http.StatusBadRequest)
		return
	}

	if err := ValidateDomain(domain); err != nil {
		http.Error(rw, "Invalid domain: "+err.Error(), http.StatusBadRequest)
		return
	}

	if tlsMode == "letsencrypt" && (domain == "localhost" || domain == "127.0.0.1" || domain == "::1") {
		http.Error(rw, "Let's Encrypt requires a real domain name, not localhost or loopback addresses", http.StatusBadRequest)
		return
	}

	var port int
	if _, err := fmt.Sscanf(r.FormValue("NGINX_PORT"), "%d", &port); err != nil {
		http.Error(rw, "Invalid port number", http.StatusBadRequest)
		return
	}
	if err := ValidatePort(port); err != nil {
		http.Error(rw, "Invalid port: "+err.Error(), http.StatusBadRequest)
		return
	}

	w.config.NginxDomain = domain
	w.config.NginxPort = port
	w.config.NginxProtocol = protocol
	w.config.NginxTLSMode = tlsMode
	w.config.NginxEmail = email

	http.Redirect(rw, r, "/setup/storage", http.StatusSeeOther)
}

func (w *Wizard) processStorageForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}
	if !w.validateCSRF(rw, r) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.config.StoragePath = r.FormValue("STORAGE_PATH")
	w.config.BackupEnabled = r.FormValue("BACKUP_ENABLED") == "true"
	w.config.BackupTarget = r.FormValue("BACKUP_TARGET")
	w.config.BackupSchedule = r.FormValue("BACKUP_SCHEDULE")
	w.config.BackupRetention = r.FormValue("BACKUP_RETENTION")
	w.config.BackupLocalPath = r.FormValue("BACKUP_LOCAL_PATH")

	http.Redirect(rw, r, "/setup/security", http.StatusSeeOther)
}

func (w *Wizard) processSecurityForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}
	if !w.validateCSRF(rw, r) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	customSecret := r.FormValue("JWT_SECRET_CUSTOM")
	if customSecret != "" {
		if err := ValidateJWTSecret(customSecret); err != nil {
			http.Error(rw, "Invalid JWT secret: "+err.Error(), http.StatusBadRequest)
			return
		}
		w.config.JWTSecret = customSecret
	}

	w.config.AdminUsername = r.FormValue("ADMIN_USERNAME")
	w.config.AdminEmail = r.FormValue("ADMIN_EMAIL")

	adminPassword := r.FormValue("ADMIN_PASSWORD")
	if adminPassword != "" && len(adminPassword) < 8 {
		http.Error(rw, "Admin password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	w.config.AdminPassword = adminPassword

	http.Redirect(rw, r, "/setup/review", http.StatusSeeOther)
}

func (w *Wizard) processReviewForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}
	if !w.validateCSRF(rw, r) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Admin password is saved to config by processSecurityForm
	adminPassword := w.config.AdminPassword
	if adminPassword == "" {
		http.Error(rw, "Admin password is required", http.StatusBadRequest)
		return
	}

	if w.config.PostgresMode == "external" && w.config.CreateDB {
		ctx := r.Context()
		if err := CreateDatabaseIfNotExists(ctx, w.config.DatabaseURL); err != nil {
			http.Error(rw, "Failed to create database: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if w.config.NginxProtocol == "http" || w.config.NginxProtocol == "https" {
		if err := GenerateNginxConfig(w.config, filepath.Join(w.OutputDir, "nginx/conf")); err != nil {
			http.Error(rw, "Failed to generate nginx config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// In Docker mode, skip CreateAdminUser (no database running during setup).
	// Admin credentials are persisted to .env and created on first startup.
	// In external mode, create admin user before writing .env to prevent partial state.
	if w.config.PostgresMode == "external" && w.config.AdminUsername != "" && w.config.AdminEmail != "" {
		ctx := r.Context()
		if err := CreateAdminUser(ctx, w.config.DatabaseURL, w.config.AdminUsername, w.config.AdminEmail, adminPassword); err != nil {
			http.Error(rw, "Failed to create admin user: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Write docker-compose.override.yml before .env to ensure Docker services are configured
	if err := WriteComposeOverride(filepath.Join(w.OutputDir, "docker-compose.override.yml"), w.config); err != nil {
		http.Error(rw, "Failed to write docker-compose override: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Write .env file last to prevent half-configured state
	if err := WriteEnvFile(filepath.Join(w.OutputDir, ".env"), w.config); err != nil {
		http.Error(rw, "Failed to write configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(rw, r, "/setup/complete", http.StatusSeeOther)
}

func (w *Wizard) processQuickInstallForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}
	if !w.validateCSRF(rw, r) {
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	// Get form values
	adminUsername := r.FormValue("ADMIN_USERNAME")
	adminEmail := r.FormValue("ADMIN_EMAIL")
	adminPassword := r.FormValue("ADMIN_PASSWORD")
	adminPasswordConfirm := r.FormValue("ADMIN_PASSWORD_CONFIRM")
	domain := r.FormValue("NGINX_DOMAIN")

	// Validate required fields
	if adminUsername == "" {
		http.Error(rw, "Admin username is required", http.StatusBadRequest)
		return
	}
	if containsShellMetachars(adminUsername) {
		http.Error(rw, "Admin username contains invalid characters", http.StatusBadRequest)
		return
	}
	if adminEmail == "" {
		http.Error(rw, "Admin email is required", http.StatusBadRequest)
		return
	}
	if !strings.Contains(adminEmail, "@") {
		http.Error(rw, "Admin email must be a valid email address", http.StatusBadRequest)
		return
	}
	if adminPassword == "" {
		http.Error(rw, "Admin password is required", http.StatusBadRequest)
		return
	}
	if len(adminPassword) < 8 {
		http.Error(rw, "Admin password must be at least 8 characters", http.StatusBadRequest)
		return
	}
	if adminPassword != adminPasswordConfirm {
		http.Error(rw, "Passwords do not match", http.StatusBadRequest)
		return
	}
	if domain == "" {
		http.Error(rw, "Domain is required", http.StatusBadRequest)
		return
	}

	// Validate domain
	if err := ValidateDomain(domain); err != nil {
		http.Error(rw, "Invalid domain: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Set config values
	w.config.AdminUsername = adminUsername
	w.config.AdminEmail = adminEmail
	w.config.AdminPassword = adminPassword
	w.config.NginxDomain = domain

	// Generate nginx config
	if w.config.NginxProtocol == "http" || w.config.NginxProtocol == "https" {
		if err := GenerateNginxConfig(w.config, filepath.Join(w.OutputDir, "nginx/conf")); err != nil {
			http.Error(rw, "Failed to generate nginx config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// In Docker mode, skip CreateAdminUser (no database running during setup).
	// Admin credentials are persisted to .env and created on first startup.
	// In external mode, create admin user before writing .env to prevent partial state.
	if w.config.PostgresMode == "external" && w.config.DatabaseURL != "" {
		ctx := r.Context()
		if err := CreateAdminUser(ctx, w.config.DatabaseURL, w.config.AdminUsername, w.config.AdminEmail, adminPassword); err != nil {
			http.Error(rw, "Failed to create admin user: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Write docker-compose.override.yml before .env to ensure Docker services are configured
	if err := WriteComposeOverride(filepath.Join(w.OutputDir, "docker-compose.override.yml"), w.config); err != nil {
		http.Error(rw, "Failed to write docker-compose override: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Write .env file last to prevent half-configured state
	if err := WriteEnvFile(filepath.Join(w.OutputDir, ".env"), w.config); err != nil {
		http.Error(rw, "Failed to write configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(rw, r, "/setup/complete", http.StatusSeeOther)
}
