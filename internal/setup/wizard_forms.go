package setup

import (
	"fmt"
	"net/http"
	"strconv"
)

func (w *Wizard) processDatabaseForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	w.config.PostgresMode = r.FormValue("POSTGRES_MODE")
	w.config.DatabaseURL = r.FormValue("DATABASE_URL")
	w.config.CreateDB = r.FormValue("create_db") == "true"

	if w.config.PostgresMode == "external" {
		if err := ValidateDatabaseURL(w.config.DatabaseURL); err != nil {
			http.Error(rw, "Invalid database URL: "+err.Error(), http.StatusBadRequest)
			return
		}
	}

	http.Redirect(rw, r, "/setup/services", http.StatusSeeOther)
}

func (w *Wizard) processServicesForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
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

	http.Redirect(rw, r, "/setup/email", http.StatusSeeOther)
}

func (w *Wizard) processEmailForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
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

	http.Redirect(rw, r, "/setup/review", http.StatusSeeOther)
}

func (w *Wizard) processReviewForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	adminPassword := r.FormValue("ADMIN_PASSWORD")
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
		if err := GenerateNginxConfig(w.config, "nginx/conf"); err != nil {
			http.Error(rw, "Failed to generate nginx config: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	if err := WriteEnvFile(".env", w.config); err != nil {
		http.Error(rw, "Failed to write configuration: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if w.config.AdminUsername != "" && w.config.AdminEmail != "" {
		ctx := r.Context()
		if err := CreateAdminUser(ctx, w.config.DatabaseURL, w.config.AdminUsername, w.config.AdminEmail, adminPassword); err != nil {
			http.Error(rw, "Failed to create admin user: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	http.Redirect(rw, r, "/setup/complete", http.StatusSeeOther)
}
