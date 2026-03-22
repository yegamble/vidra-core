package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type collaboratorChannelRepository interface {
	GetByHandle(ctx context.Context, handle string) (*domain.Channel, error)
	CheckOwnership(ctx context.Context, channelID, userID uuid.UUID) (bool, error)
}

type collaboratorUserRepository interface {
	GetByID(ctx context.Context, id string) (*domain.User, error)
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
}

type collaboratorRepository interface {
	ListByChannel(ctx context.Context, channelID uuid.UUID) ([]*domain.ChannelCollaborator, error)
	GetByChannelAndID(ctx context.Context, channelID, collaboratorID uuid.UUID) (*domain.ChannelCollaborator, error)
	GetByChannelAndUser(ctx context.Context, channelID, userID uuid.UUID) (*domain.ChannelCollaborator, error)
	UpsertInvite(ctx context.Context, collaborator *domain.ChannelCollaborator) error
	UpdateStatus(ctx context.Context, collaboratorID uuid.UUID, status domain.ChannelCollaboratorStatus) error
	Delete(ctx context.Context, collaboratorID uuid.UUID) error
}

type CollaboratorHandlers struct {
	channelRepo      collaboratorChannelRepository
	userRepo         collaboratorUserRepository
	collaboratorRepo collaboratorRepository
}

func NewCollaboratorHandlers(channelRepo collaboratorChannelRepository, userRepo collaboratorUserRepository, collaboratorRepo collaboratorRepository) *CollaboratorHandlers {
	return &CollaboratorHandlers{
		channelRepo:      channelRepo,
		userRepo:         userRepo,
		collaboratorRepo: collaboratorRepo,
	}
}

func (h *CollaboratorHandlers) ListCollaborators(w http.ResponseWriter, r *http.Request) {
	channel, userID, err := h.resolveChannelAndUser(r)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	if allowed, err := h.canManageOrView(r.Context(), channel.ID, userID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	} else if !allowed {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	collaborators, err := h.collaboratorRepo.ListByChannel(r.Context(), channel.ID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("list collaborators: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]any{
		"total": len(collaborators),
		"data":  collaborators,
	})
}

func (h *CollaboratorHandlers) InviteCollaborator(w http.ResponseWriter, r *http.Request) {
	channel, userID, err := h.resolveChannelAndUser(r)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	isOwner, err := h.channelRepo.CheckOwnership(r.Context(), channel.ID, userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("check channel ownership: %w", err))
		return
	}
	if !isOwner {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	var req struct {
		UserID      string `json:"userId"`
		Username    string `json:"username"`
		Account     string `json:"account"`
		AccountName string `json:"accountName"`
		Role        string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid request body"))
		return
	}

	invitee, err := h.resolveInvitee(r.Context(), req.UserID, req.Username, req.Account, req.AccountName)
	if err != nil {
		status := http.StatusInternalServerError
		if err == domain.ErrUserNotFound {
			status = http.StatusNotFound
		}
		shared.WriteError(w, status, err)
		return
	}

	inviteeID, err := uuid.Parse(invitee.ID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_USER_ID", "Invitee user ID is invalid"))
		return
	}
	if inviteeID == userID {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Channel owner is already a collaborator"))
		return
	}

	collaborator := &domain.ChannelCollaborator{
		ChannelID: channel.ID,
		UserID:    inviteeID,
		InvitedBy: userID,
		Role:      req.Role,
		Status:    domain.ChannelCollaboratorStatusPending,
	}
	if collaborator.Role == "" {
		collaborator.Role = "editor"
	}

	if err := h.collaboratorRepo.UpsertInvite(r.Context(), collaborator); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("invite collaborator: %w", err))
		return
	}

	refreshed, err := h.collaboratorRepo.GetByChannelAndUser(r.Context(), channel.ID, inviteeID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("reload collaborator invitation: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, refreshed)
}

func (h *CollaboratorHandlers) AcceptCollaborator(w http.ResponseWriter, r *http.Request) {
	h.respondToInvite(w, r, domain.ChannelCollaboratorStatusAccepted)
}

func (h *CollaboratorHandlers) RejectCollaborator(w http.ResponseWriter, r *http.Request) {
	h.respondToInvite(w, r, domain.ChannelCollaboratorStatusRejected)
}

func (h *CollaboratorHandlers) DeleteCollaborator(w http.ResponseWriter, r *http.Request) {
	channel, userID, err := h.resolveChannelAndUser(r)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	collaboratorID, err := uuid.Parse(chi.URLParam(r, "collaboratorId"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid collaborator ID"))
		return
	}

	collaborator, err := h.collaboratorRepo.GetByChannelAndID(r.Context(), channel.ID, collaboratorID)
	if err != nil {
		status := http.StatusInternalServerError
		if err == domain.ErrNotFound {
			status = http.StatusNotFound
		}
		shared.WriteError(w, status, err)
		return
	}

	isOwner, err := h.channelRepo.CheckOwnership(r.Context(), channel.ID, userID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("check channel ownership: %w", err))
		return
	}
	if !isOwner && collaborator.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	if err := h.collaboratorRepo.Delete(r.Context(), collaboratorID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("delete collaborator: %w", err))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *CollaboratorHandlers) respondToInvite(w http.ResponseWriter, r *http.Request, status domain.ChannelCollaboratorStatus) {
	channel, userID, err := h.resolveChannelAndUser(r)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	collaboratorID, err := uuid.Parse(chi.URLParam(r, "collaboratorId"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_ID", "Invalid collaborator ID"))
		return
	}

	collaborator, err := h.collaboratorRepo.GetByChannelAndID(r.Context(), channel.ID, collaboratorID)
	if err != nil {
		statusCode := http.StatusInternalServerError
		if err == domain.ErrNotFound {
			statusCode = http.StatusNotFound
		}
		shared.WriteError(w, statusCode, err)
		return
	}
	if collaborator.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	if err := h.collaboratorRepo.UpdateStatus(r.Context(), collaborator.ID, status); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("update collaborator status: %w", err))
		return
	}

	refreshed, err := h.collaboratorRepo.GetByChannelAndID(r.Context(), channel.ID, collaborator.ID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("reload collaborator: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, refreshed)
}

func (h *CollaboratorHandlers) resolveChannelAndUser(r *http.Request) (*domain.Channel, uuid.UUID, error) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		return nil, uuid.Nil, domain.ErrUnauthorized
	}

	channel, err := h.channelRepo.GetByHandle(r.Context(), chi.URLParam(r, "channelHandle"))
	if err != nil {
		return nil, uuid.Nil, err
	}

	return channel, userID, nil
}

func (h *CollaboratorHandlers) canManageOrView(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	isOwner, err := h.channelRepo.CheckOwnership(ctx, channelID, userID)
	if err != nil {
		return false, err
	}
	if isOwner {
		return true, nil
	}

	collaborator, err := h.collaboratorRepo.GetByChannelAndUser(ctx, channelID, userID)
	if err == domain.ErrNotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return collaborator.Status == domain.ChannelCollaboratorStatusAccepted, nil
}

func (h *CollaboratorHandlers) resolveInvitee(ctx context.Context, userID, username, account, accountName string) (*domain.User, error) {
	switch {
	case userID != "":
		return h.userRepo.GetByID(ctx, userID)
	case username != "":
		return h.userRepo.GetByUsername(ctx, username)
	case account != "":
		return h.userRepo.GetByUsername(ctx, account)
	case accountName != "":
		return h.userRepo.GetByUsername(ctx, accountName)
	default:
		return nil, domain.NewDomainError("INVALID_REQUEST", "An invitee identifier is required")
	}
}
