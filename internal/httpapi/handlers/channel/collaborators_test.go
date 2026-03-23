package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type stubCollaboratorChannelRepo struct {
	channel *domain.Channel
	ownerID uuid.UUID
}

func (s *stubCollaboratorChannelRepo) GetByHandle(_ context.Context, handle string) (*domain.Channel, error) {
	if s.channel == nil || s.channel.Handle != handle {
		return nil, domain.ErrNotFound
	}
	return s.channel, nil
}

func (s *stubCollaboratorChannelRepo) CheckOwnership(_ context.Context, _ uuid.UUID, userID uuid.UUID) (bool, error) {
	return userID == s.ownerID, nil
}

type stubCollaboratorUserRepo struct {
	users map[string]*domain.User
}

func (s *stubCollaboratorUserRepo) GetByID(_ context.Context, id string) (*domain.User, error) {
	if user, ok := s.users[id]; ok {
		return user, nil
	}
	return nil, domain.ErrUserNotFound
}

func (s *stubCollaboratorUserRepo) GetByUsername(_ context.Context, username string) (*domain.User, error) {
	for _, user := range s.users {
		if user.Username == username {
			return user, nil
		}
	}
	return nil, domain.ErrUserNotFound
}

type stubCollaboratorRepo struct {
	items map[uuid.UUID]*domain.ChannelCollaborator
}

func (s *stubCollaboratorRepo) ListByChannel(_ context.Context, channelID uuid.UUID) ([]*domain.ChannelCollaborator, error) {
	items := []*domain.ChannelCollaborator{}
	for _, item := range s.items {
		if item.ChannelID == channelID {
			items = append(items, item)
		}
	}
	return items, nil
}

func (s *stubCollaboratorRepo) GetByChannelAndID(_ context.Context, channelID, collaboratorID uuid.UUID) (*domain.ChannelCollaborator, error) {
	item, ok := s.items[collaboratorID]
	if !ok || item.ChannelID != channelID {
		return nil, domain.ErrNotFound
	}
	return item, nil
}

func (s *stubCollaboratorRepo) GetByChannelAndUser(_ context.Context, channelID, userID uuid.UUID) (*domain.ChannelCollaborator, error) {
	for _, item := range s.items {
		if item.ChannelID == channelID && item.UserID == userID {
			return item, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (s *stubCollaboratorRepo) UpsertInvite(_ context.Context, collaborator *domain.ChannelCollaborator) error {
	if collaborator.ID == uuid.Nil {
		collaborator.ID = uuid.New()
	}
	copy := *collaborator
	s.items[copy.ID] = &copy
	return nil
}

func (s *stubCollaboratorRepo) UpdateStatus(_ context.Context, collaboratorID uuid.UUID, status domain.ChannelCollaboratorStatus) error {
	item, ok := s.items[collaboratorID]
	if !ok {
		return domain.ErrNotFound
	}
	item.Status = status
	return nil
}

func (s *stubCollaboratorRepo) Delete(_ context.Context, collaboratorID uuid.UUID) error {
	delete(s.items, collaboratorID)
	return nil
}

func TestCollaboratorHandlers_InviteCollaborator(t *testing.T) {
	ownerID := uuid.New()
	channelID := uuid.New()
	inviteeID := uuid.New()

	handler := NewCollaboratorHandlers(
		&stubCollaboratorChannelRepo{
			channel: &domain.Channel{ID: channelID, Handle: "owner-channel"},
			ownerID: ownerID,
		},
		&stubCollaboratorUserRepo{
			users: map[string]*domain.User{
				inviteeID.String(): {ID: inviteeID.String(), Username: "invitee"},
			},
		},
		&stubCollaboratorRepo{items: map[uuid.UUID]*domain.ChannelCollaborator{}},
	)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/video-channels/owner-channel/collaborators/invite", strings.NewReader(`{"username":"invitee"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, ownerID.String()))
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, routeContext(map[string]string{"channelHandle": "owner-channel"})))
	rr := httptest.NewRecorder()

	handler.InviteCollaborator(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())

	var envelope struct {
		Data domain.ChannelCollaborator `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &envelope))
	require.Equal(t, inviteeID, envelope.Data.UserID)
	require.Equal(t, domain.ChannelCollaboratorStatusPending, envelope.Data.Status)
}

func routeContext(params map[string]string) *chi.Context {
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	return rctx
}
