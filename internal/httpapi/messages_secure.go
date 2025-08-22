package httpapi

import (
	"encoding/json"
	"net/http"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// SendSecureMessageHandler accepts an already-encrypted message and detached signature.
func SendSecureMessageHandler(svc *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}
		var req domain.SendSecureMessageRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}
		if err := validate.Struct(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_ERROR", err.Error()))
			return
		}
		msg, err := svc.SendSecureMessage(r.Context(), userID, &req)
		if err != nil {
			WriteError(w, MapDomainErrorToHTTP(err), domain.NewDomainError("SEND_SECURE_MESSAGE_FAILED", err.Error()))
			return
		}
		WriteJSON(w, http.StatusCreated, domain.MessageResponse{Message: *msg})
	}
}

// StartSecureConversationHandler ensures both users have PGP keys and creates a secure conversation record.
func StartSecureConversationHandler(svc *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}
		var req domain.StartSecureConversationRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}
		if err := validate.Struct(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_ERROR", err.Error()))
			return
		}
		conv, err := svc.StartSecureConversation(r.Context(), userID, &req)
		if err != nil {
			WriteError(w, MapDomainErrorToHTTP(err), domain.NewDomainError("START_SECURE_CONVERSATION_FAILED", err.Error()))
			return
		}
		WriteJSON(w, http.StatusCreated, conv)
	}
}

// SetPGPKeyHandler sets the current user's PGP public key.
func SetPGPKeyHandler(svc *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}
		var req domain.SetPGPKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}
		if err := validate.Struct(&req); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_ERROR", err.Error()))
			return
		}
		if err := svc.SetPGPPublicKey(r.Context(), userID, &req); err != nil {
			WriteError(w, MapDomainErrorToHTTP(err), domain.NewDomainError("SET_PGP_KEY_FAILED", err.Error()))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// RemovePGPKeyHandler removes the current user's PGP public key.
func RemovePGPKeyHandler(svc *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}
		if err := svc.RemovePGPPublicKey(r.Context(), userID); err != nil {
			WriteError(w, MapDomainErrorToHTTP(err), domain.NewDomainError("REMOVE_PGP_KEY_FAILED", err.Error()))
			return
		}
		WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// GetUserPGPKeyHandler returns whether a user has a PGP key and optionally the key (public info).
func GetUserPGPKeyHandler(svc *usecase.MessageService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID := chi.URLParam(r, "id")
		if userID == "" {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_USER_ID", "User ID is required"))
			return
		}
		resp, err := svc.GetUserPGPPublicKey(r.Context(), userID)
		if err != nil {
			WriteError(w, MapDomainErrorToHTTP(err), domain.NewDomainError("GET_PGP_KEY_FAILED", err.Error()))
			return
		}
		WriteJSON(w, http.StatusOK, resp)
	}
}

// GeneratePGPKeyHandler generates a keypair for the authenticated user and stores the public key + fingerprint.
func GeneratePGPKeyHandler(svc *usecase.MessageService) http.HandlerFunc {
	type reqBody struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}
		var body reqBody
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}
		pub, priv, fp, err := svc.GeneratePGPKey(r.Context(), userID, body.Name, body.Email)
		if err != nil {
			WriteError(w, MapDomainErrorToHTTP(err), domain.NewDomainError("GENERATE_PGP_KEY_FAILED", err.Error()))
			return
		}
		WriteJSON(w, http.StatusCreated, map[string]string{
			"public_key":  pub,
			"private_key": priv,
			"fingerprint": fp,
		})
	}
}
