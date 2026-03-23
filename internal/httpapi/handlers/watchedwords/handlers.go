package watchedwords

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	ucww "athena/internal/usecase/watched_words"
)

// Handlers handles HTTP requests for watched word lists.
type Handlers struct {
	service *ucww.Service
}

// NewHandlers creates a new Handlers instance.
func NewHandlers(service *ucww.Service) *Handlers {
	return &Handlers{service: service}
}

// ListAccountWatchedWords handles GET /api/v1/watched-words/accounts/{accountName}/lists
func (h *Handlers) ListAccountWatchedWords(w http.ResponseWriter, r *http.Request) {
	accountName := chi.URLParam(r, "accountName")
	if accountName == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("account name required"))
		return
	}

	lists, err := h.service.ListByAccount(r.Context(), &accountName)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to list watched words"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, lists)
}

// CreateAccountWatchedWordList handles POST /api/v1/watched-words/accounts/{accountName}/lists
func (h *Handlers) CreateAccountWatchedWordList(w http.ResponseWriter, r *http.Request) {
	accountName := chi.URLParam(r, "accountName")
	if accountName == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("account name required"))
		return
	}

	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	var req domain.CreateWatchedWordListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	list, err := h.service.Create(r.Context(), &accountName, &req)
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			shared.WriteError(w, http.StatusBadRequest, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to create watched word list"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, list)
}

// UpdateAccountWatchedWordList handles PUT /api/v1/watched-words/accounts/{accountName}/lists/{listId}
func (h *Handlers) UpdateAccountWatchedWordList(w http.ResponseWriter, r *http.Request) {
	listID, err := strconv.ParseInt(chi.URLParam(r, "listId"), 10, 64)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid list ID"))
		return
	}

	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	var req domain.UpdateWatchedWordListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	list, err := h.service.Update(r.Context(), listID, &req)
	if err != nil {
		if errors.Is(err, domain.ErrWatchedWordListNotFound) {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update watched word list"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, list)
}

// DeleteAccountWatchedWordList handles DELETE /api/v1/watched-words/accounts/{accountName}/lists/{listId}
func (h *Handlers) DeleteAccountWatchedWordList(w http.ResponseWriter, r *http.Request) {
	listID, err := strconv.ParseInt(chi.URLParam(r, "listId"), 10, 64)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid list ID"))
		return
	}

	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	if err := h.service.Delete(r.Context(), listID); err != nil {
		if errors.Is(err, domain.ErrWatchedWordListNotFound) {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to delete watched word list"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListServerWatchedWords handles GET /api/v1/watched-words/server/lists
func (h *Handlers) ListServerWatchedWords(w http.ResponseWriter, r *http.Request) {
	lists, err := h.service.ListByAccount(r.Context(), nil)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to list server watched words"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, lists)
}

// CreateServerWatchedWordList handles POST /api/v1/watched-words/server/lists
func (h *Handlers) CreateServerWatchedWordList(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	var req domain.CreateWatchedWordListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	list, err := h.service.Create(r.Context(), nil, &req)
	if err != nil {
		if errors.Is(err, domain.ErrValidation) {
			shared.WriteError(w, http.StatusBadRequest, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to create server watched word list"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, list)
}

// UpdateServerWatchedWordList handles PUT /api/v1/watched-words/server/lists/{listId}
func (h *Handlers) UpdateServerWatchedWordList(w http.ResponseWriter, r *http.Request) {
	listID, err := strconv.ParseInt(chi.URLParam(r, "listId"), 10, 64)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid list ID"))
		return
	}

	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	var req domain.UpdateWatchedWordListRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	list, err := h.service.Update(r.Context(), listID, &req)
	if err != nil {
		if errors.Is(err, domain.ErrWatchedWordListNotFound) {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update server watched word list"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, list)
}

// DeleteServerWatchedWordList handles DELETE /api/v1/watched-words/server/lists/{listId}
func (h *Handlers) DeleteServerWatchedWordList(w http.ResponseWriter, r *http.Request) {
	listID, err := strconv.ParseInt(chi.URLParam(r, "listId"), 10, 64)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid list ID"))
		return
	}

	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	if err := h.service.Delete(r.Context(), listID); err != nil {
		if errors.Is(err, domain.ErrWatchedWordListNotFound) {
			shared.WriteError(w, http.StatusNotFound, err)
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to delete server watched word list"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
