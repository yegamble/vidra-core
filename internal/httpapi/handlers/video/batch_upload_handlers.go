package video

import (
	"encoding/json"
	"errors"
	"net/http"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase/upload"
)

func BatchInitiateUploadHandler(uploadService upload.Service, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		var req domain.BatchUploadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}

		if len(req.Videos) == 0 {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("EMPTY_BATCH", "Batch must contain at least one video"))
			return
		}

		maxBatch := cfg.MaxBatchUploadSize
		if maxBatch <= 0 {
			maxBatch = 10
		}
		if len(req.Videos) > maxBatch {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BATCH_TOO_LARGE",
				"Batch size exceeds maximum allowed"))
			return
		}

		response, err := uploadService.InitiateBatchUpload(r.Context(), userID, &req)
		if err != nil {
			var domainErr domain.DomainError
			if errors.As(err, &domainErr) {
				shared.WriteError(w, http.StatusBadRequest, domainErr)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("BATCH_INITIATE_FAILED", "Failed to initiate batch upload"))
			return
		}

		shared.WriteJSON(w, http.StatusCreated, response)
	}
}

func GetBatchStatusHandler(uploadService upload.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		batchID := chi.URLParam(r, "batchId")
		if batchID == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_BATCH_ID", "Batch ID is required"))
			return
		}
		if _, err := uuid.Parse(batchID); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_BATCH_ID", "Invalid batch ID format"))
			return
		}

		status, err := uploadService.GetBatchStatus(r.Context(), batchID, userID)
		if err != nil {
			var domainErr domain.DomainError
			if errors.As(err, &domainErr) {
				if domainErr.Code == "BATCH_NOT_FOUND" {
					shared.WriteError(w, http.StatusNotFound, domainErr)
					return
				}
				shared.WriteError(w, http.StatusBadRequest, domainErr)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("BATCH_STATUS_FAILED", "Failed to get batch status"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, status)
	}
}
