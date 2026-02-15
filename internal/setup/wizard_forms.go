package setup

import (
	"net/http"
)

func (w *Wizard) processDatabaseForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}

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

	w.config.RedisMode = r.FormValue("REDIS_MODE")
	w.config.RedisURL = r.FormValue("REDIS_URL")
	w.config.EnableIPFS = r.FormValue("ENABLE_IPFS") == "true"
	w.config.IPFSMode = r.FormValue("IPFS_MODE")
	w.config.IPFSAPIUrl = r.FormValue("IPFS_API_URL")
	w.config.EnableClamAV = r.FormValue("ENABLE_CLAMAV") == "true"
	w.config.EnableWhisper = r.FormValue("ENABLE_WHISPER") == "true"

	http.Redirect(rw, r, "/setup/storage", http.StatusSeeOther)
}

func (w *Wizard) processStorageForm(rw http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(rw, "Invalid form data", http.StatusBadRequest)
		return
	}

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
