package backup

import (
	"encoding/json"
	"log"
	"net/http"

	"athena/internal/backup"
	backupUsecase "athena/internal/usecase/backup"

	"github.com/go-chi/chi/v5"
)

type Handler struct {
	service *backupUsecase.Service
}

func NewHandler(service *backupUsecase.Service) *Handler {
	return &Handler{
		service: service,
	}
}

func (h *Handler) ListBackups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	backups, err := h.service.ListBackups(ctx)
	if err != nil {
		log.Printf("ListBackups error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"backups": backups,
	})
}

func (h *Handler) TriggerBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.service.TriggerBackup(ctx); err != nil {
		log.Printf("TriggerBackup error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"status": "backup started",
	})
}

func (h *Handler) DeleteBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	backupID, ok := h.extractBackupID(r)
	if !ok {
		http.Error(w, "backup ID required", http.StatusBadRequest)
		return
	}

	if err := h.service.DeleteBackup(ctx, backupID); err != nil {
		log.Printf("DeleteBackup error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"status": "deleted",
	})
}

func (h *Handler) RestoreBackup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	backupID, ok := h.extractBackupID(r)
	if !ok {
		http.Error(w, "backup ID required", http.StatusBadRequest)
		return
	}

	opts := backup.RestoreOptions{
		BackupPath:      backupID,
		CreatePreBackup: true,
		RunMigrations:   true,
	}

	progressChan, err := h.service.StartRestore(ctx, opts)
	if err != nil {
		log.Printf("RestoreBackup error: %v", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	go func() {
		for range progressChan {
		}
	}()

	h.respondJSON(w, http.StatusAccepted, map[string]interface{}{
		"status": "restore started",
	})
}

func (h *Handler) GetRestoreStatus(w http.ResponseWriter, r *http.Request) {
	h.respondJSON(w, http.StatusOK, map[string]interface{}{
		"status": "no active restore",
	})
}

func (h *Handler) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) extractBackupID(r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if id == "" {
		return "", false
	}
	return id, true
}
