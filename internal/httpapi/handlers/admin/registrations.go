package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
)

// RegistrationRepository is the minimal data access interface for registration management.
type RegistrationRepository interface {
	ListPending(ctx context.Context) ([]*domain.UserRegistration, error)
	GetByID(ctx context.Context, id uuid.UUID) (*domain.UserRegistration, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status, response string) error
	Delete(ctx context.Context, id uuid.UUID) error
}

// UserCreator provisions a user account from a registration.
type UserCreator interface {
	Create(ctx context.Context, user *domain.User, passwordHash string) error
}

// RegistrationHandlers handles admin user registration workflow endpoints.
type RegistrationHandlers struct {
	repo        RegistrationRepository
	userCreator UserCreator
}

// NewRegistrationHandlers creates a new RegistrationHandlers.
func NewRegistrationHandlers(repo RegistrationRepository, userCreator UserCreator) *RegistrationHandlers {
	return &RegistrationHandlers{repo: repo, userCreator: userCreator}
}

// ListRegistrations handles GET /api/v1/admin/registrations.
func (h *RegistrationHandlers) ListRegistrations(w http.ResponseWriter, r *http.Request) {
	registrations, err := h.repo.ListPending(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to list registrations: %w", err))
		return
	}
	if registrations == nil {
		registrations = []*domain.UserRegistration{}
	}
	shared.WriteJSON(w, http.StatusOK, registrations)
}

// AcceptRegistration handles POST /api/v1/admin/registrations/{id}/accept.
func (h *RegistrationHandlers) AcceptRegistration(w http.ResponseWriter, r *http.Request) {
	idStr := registrationIDParam(r)
	id, err := uuid.Parse(idStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid registration ID"))
		return
	}

	var req struct {
		ModeratorMessage string `json:"moderatorMessage"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	reg, err := h.repo.GetByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("registration not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to fetch registration: %w", err))
		return
	}

	if err := h.repo.UpdateStatus(r.Context(), id, "accepted", req.ModeratorMessage); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update registration: %w", err))
		return
	}

	if h.userCreator != nil {
		user := &domain.User{
			ID:          uuid.New().String(),
			Username:    reg.Username,
			Email:       reg.Email,
			DisplayName: reg.Username,
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		if err := h.userCreator.Create(r.Context(), user, ""); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to create user account: %w", err))
			return
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// RejectRegistration handles POST /api/v1/admin/registrations/{id}/reject.
func (h *RegistrationHandlers) RejectRegistration(w http.ResponseWriter, r *http.Request) {
	h.updateRegistrationStatus(w, r, "rejected")
}

// DeleteRegistration handles DELETE /api/v1/admin/registrations/{id}.
func (h *RegistrationHandlers) DeleteRegistration(w http.ResponseWriter, r *http.Request) {
	idStr := registrationIDParam(r)
	id, err := uuid.Parse(idStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid registration ID"))
		return
	}

	if err := h.repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("registration not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to delete registration: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *RegistrationHandlers) updateRegistrationStatus(w http.ResponseWriter, r *http.Request, status string) {
	idStr := registrationIDParam(r)
	id, err := uuid.Parse(idStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid registration ID"))
		return
	}

	var req struct {
		ModeratorMessage string `json:"moderatorMessage"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)

	if _, err := h.repo.GetByID(r.Context(), id); err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("registration not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to fetch registration: %w", err))
		return
	}

	if err := h.repo.UpdateStatus(r.Context(), id, status, req.ModeratorMessage); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update registration: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func registrationIDParam(r *http.Request) string {
	if registrationID := chi.URLParam(r, "registrationId"); registrationID != "" {
		return registrationID
	}

	return chi.URLParam(r, "id")
}
