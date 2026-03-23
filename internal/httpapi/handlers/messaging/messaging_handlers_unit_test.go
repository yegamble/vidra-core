package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func withChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withChiParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withAuth(r *http.Request, userID uuid.UUID) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), middleware.UserIDKey, userID.String()))
}

func decodeBody(t *testing.T, rr *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	return body
}

type MockMessageService struct {
	mock.Mock
}

func (m *MockMessageService) SendMessage(ctx context.Context, senderID string, req *domain.SendMessageRequest) (*domain.Message, error) {
	args := m.Called(ctx, senderID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Message), args.Error(1)
}

func (m *MockMessageService) GetMessages(ctx context.Context, userID string, req *domain.GetMessagesRequest) (*domain.MessagesResponse, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.MessagesResponse), args.Error(1)
}

func TestChatHandlers_RemoveModerator_Success(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	modToRemove := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)
	mockChatRepo.On("RemoveModerator", mock.Anything, streamID, modToRemove).Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": streamID.String(),
		"userId":   modToRemove.String(),
	})
	req = withAuth(req, ownerID)
	rr := httptest.NewRecorder()

	handlers.RemoveModerator(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockStreamRepo.AssertExpectations(t)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_RemoveModerator_InvalidStreamID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": "not-a-uuid",
		"userId":   uuid.NewString(),
	})
	rr := httptest.NewRecorder()

	handlers.RemoveModerator(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_RemoveModerator_InvalidUserID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": uuid.NewString(),
		"userId":   "not-a-uuid",
	})
	rr := httptest.NewRecorder()

	handlers.RemoveModerator(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_RemoveModerator_Unauthorized(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": uuid.NewString(),
		"userId":   uuid.NewString(),
	})
	rr := httptest.NewRecorder()

	handlers.RemoveModerator(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestChatHandlers_RemoveModerator_NotOwner(t *testing.T) {
	handlers, _, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	otherUser := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": streamID.String(),
		"userId":   uuid.NewString(),
	})
	req = withAuth(req, otherUser)
	rr := httptest.NewRecorder()

	handlers.RemoveModerator(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatHandlers_RemoveModerator_NotFound(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	modToRemove := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)
	mockChatRepo.On("RemoveModerator", mock.Anything, streamID, modToRemove).Return(domain.ErrNotFound)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": streamID.String(),
		"userId":   modToRemove.String(),
	})
	req = withAuth(req, ownerID)
	rr := httptest.NewRecorder()

	handlers.RemoveModerator(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
	mockStreamRepo.AssertExpectations(t)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_RemoveModerator_InternalError(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	modToRemove := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)
	mockChatRepo.On("RemoveModerator", mock.Anything, streamID, modToRemove).Return(errors.New("db error"))

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": streamID.String(),
		"userId":   modToRemove.String(),
	})
	req = withAuth(req, ownerID)
	rr := httptest.NewRecorder()

	handlers.RemoveModerator(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestChatHandlers_GetModerators_Success(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()

	mods := []*domain.ChatModerator{
		{StreamID: streamID, UserID: uuid.New()},
		{StreamID: streamID, UserID: uuid.New()},
	}
	mockChatRepo.On("GetModerators", mock.Anything, streamID).Return(mods, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	rr := httptest.NewRecorder()

	handlers.GetModerators(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_GetModerators_InvalidStreamID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", "bad-uuid")
	rr := httptest.NewRecorder()

	handlers.GetModerators(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_GetModerators_InternalError(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	mockChatRepo.On("GetModerators", mock.Anything, streamID).Return(nil, errors.New("db error"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	rr := httptest.NewRecorder()

	handlers.GetModerators(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_UnbanUser_Success_AsOwner(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	bannedUser := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, ownerID).Return(false, nil)
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)
	mockChatRepo.On("UnbanUser", mock.Anything, streamID, bannedUser).Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": streamID.String(),
		"userId":   bannedUser.String(),
	})
	req = withAuth(req, ownerID)
	rr := httptest.NewRecorder()

	handlers.UnbanUser(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockChatRepo.AssertExpectations(t)
	mockStreamRepo.AssertExpectations(t)
}

func TestChatHandlers_UnbanUser_Success_AsModerator(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	modID := uuid.New()
	bannedUser := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, modID).Return(true, nil)
	mockChatRepo.On("UnbanUser", mock.Anything, streamID, bannedUser).Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": streamID.String(),
		"userId":   bannedUser.String(),
	})
	req = withAuth(req, modID)
	rr := httptest.NewRecorder()

	handlers.UnbanUser(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_UnbanUser_InvalidStreamID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": "bad",
		"userId":   uuid.NewString(),
	})
	rr := httptest.NewRecorder()

	handlers.UnbanUser(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_UnbanUser_InvalidUserID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": uuid.NewString(),
		"userId":   "bad",
	})
	rr := httptest.NewRecorder()

	handlers.UnbanUser(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_UnbanUser_Unauthorized(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": uuid.NewString(),
		"userId":   uuid.NewString(),
	})
	rr := httptest.NewRecorder()

	handlers.UnbanUser(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestChatHandlers_UnbanUser_Forbidden(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	nonModUser := uuid.New()
	bannedUser := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, nonModUser).Return(false, nil)
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": streamID.String(),
		"userId":   bannedUser.String(),
	})
	req = withAuth(req, nonModUser)
	rr := httptest.NewRecorder()

	handlers.UnbanUser(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestChatHandlers_UnbanUser_BanNotFound(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	modID := uuid.New()
	bannedUser := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, modID).Return(true, nil)
	mockChatRepo.On("UnbanUser", mock.Anything, streamID, bannedUser).Return(domain.ErrNotFound)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": streamID.String(),
		"userId":   bannedUser.String(),
	})
	req = withAuth(req, modID)
	rr := httptest.NewRecorder()

	handlers.UnbanUser(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestChatHandlers_UnbanUser_InternalError(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	modID := uuid.New()
	bannedUser := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, modID).Return(true, nil)
	mockChatRepo.On("UnbanUser", mock.Anything, streamID, bannedUser).Return(errors.New("db error"))

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId": streamID.String(),
		"userId":   bannedUser.String(),
	})
	req = withAuth(req, modID)
	rr := httptest.NewRecorder()

	handlers.UnbanUser(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestChatHandlers_GetBans_Success(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	modID := uuid.New()

	bans := []*domain.ChatBan{
		{StreamID: streamID, UserID: uuid.New(), Reason: "spam"},
	}
	mockChatRepo.On("IsModerator", mock.Anything, streamID, modID).Return(true, nil)
	mockChatRepo.On("GetBans", mock.Anything, streamID).Return(bans, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	req = withAuth(req, modID)
	rr := httptest.NewRecorder()

	handlers.GetBans(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockChatRepo.AssertExpectations(t)
}

func TestChatHandlers_GetBans_InvalidStreamID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", "bad-uuid")
	rr := httptest.NewRecorder()

	handlers.GetBans(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_GetBans_Unauthorized(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", uuid.NewString())
	rr := httptest.NewRecorder()

	handlers.GetBans(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestChatHandlers_GetBans_Forbidden(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	nonModUser := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, nonModUser).Return(false, nil)
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:     streamID,
		UserID: ownerID,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	req = withAuth(req, nonModUser)
	rr := httptest.NewRecorder()

	handlers.GetBans(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)
}

func TestChatHandlers_GetBans_InternalError(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	modID := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, modID).Return(true, nil)
	mockChatRepo.On("GetBans", mock.Anything, streamID).Return(nil, errors.New("db error"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	req = withAuth(req, modID)
	rr := httptest.NewRecorder()

	handlers.GetBans(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestChatHandlers_VerifyModeratorOrOwner_IsModCheckError(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	userID := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, userID).Return(false, errors.New("db error"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	req = withAuth(req, userID)
	rr := httptest.NewRecorder()

	handlers.GetBans(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestChatHandlers_VerifyModeratorOrOwner_StreamNotFound(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	userID := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, userID).Return(false, nil)
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(nil, domain.ErrNotFound)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	req = withAuth(req, userID)
	rr := httptest.NewRecorder()

	handlers.GetBans(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestChatHandlers_VerifyModeratorOrOwner_StreamGetError(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	userID := uuid.New()

	mockChatRepo.On("IsModerator", mock.Anything, streamID, userID).Return(false, nil)
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(nil, errors.New("db error"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	req = withAuth(req, userID)
	rr := httptest.NewRecorder()

	handlers.GetBans(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestChatHandlers_GetChatStats_InvalidStreamID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", "bad-uuid")
	rr := httptest.NewRecorder()

	handlers.GetChatStats(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_GetChatStats_InternalError(t *testing.T) {
	handlers, mockChatRepo, _, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	mockChatRepo.On("GetStreamStats", mock.Anything, streamID).Return(nil, errors.New("db error"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	rr := httptest.NewRecorder()

	handlers.GetChatStats(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestChatHandlers_DeleteMessage_InvalidStreamID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId":  "bad-uuid",
		"messageId": uuid.NewString(),
	})
	rr := httptest.NewRecorder()

	handlers.DeleteMessage(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_DeleteMessage_InvalidMessageID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId":  uuid.NewString(),
		"messageId": "bad-uuid",
	})
	rr := httptest.NewRecorder()

	handlers.DeleteMessage(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_DeleteMessage_Unauthorized(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withChiParams(req, map[string]string{
		"streamId":  uuid.NewString(),
		"messageId": uuid.NewString(),
	})
	rr := httptest.NewRecorder()

	handlers.DeleteMessage(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestChatHandlers_AddModerator_InvalidStreamID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParam(req, "streamId", "bad-uuid")
	rr := httptest.NewRecorder()

	handlers.AddModerator(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_AddModerator_Unauthorized(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParam(req, "streamId", uuid.NewString())
	rr := httptest.NewRecorder()

	handlers.AddModerator(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestChatHandlers_BanUser_InvalidStreamID(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParam(req, "streamId", "bad-uuid")
	rr := httptest.NewRecorder()

	handlers.BanUser(rr, req)
	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestChatHandlers_BanUser_Unauthorized(t *testing.T) {
	handlers, _, _, _, _ := setupChatHandlerTest(t)

	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = withChiParam(req, "streamId", uuid.NewString())
	rr := httptest.NewRecorder()

	handlers.BanUser(rr, req)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestChatHandlers_GetChatMessages_StreamNotFound(t *testing.T) {
	handlers, _, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(nil, domain.ErrNotFound)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	rr := httptest.NewRecorder()

	handlers.GetChatMessages(rr, req)
	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestChatHandlers_GetChatMessages_StreamGetError(t *testing.T) {
	handlers, _, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(nil, errors.New("db error"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	rr := httptest.NewRecorder()

	handlers.GetChatMessages(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestChatHandlers_GetChatMessages_PrivateStreamForbidden(t *testing.T) {
	handlers, _, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	ownerID := uuid.New()
	otherUser := uuid.New()
	channelID := uuid.New()

	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:        streamID,
		UserID:    ownerID,
		Privacy:   "private",
		ChannelID: channelID,
	}, nil)

	mockSubscriptionRepo := new(MockSubscriptionRepository)
	mockSubscriptionRepo.On("IsSubscribed", mock.Anything, otherUser, channelID).Return(false, nil)
	handlers.subscriptionRepo = mockSubscriptionRepo

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	req = withAuth(req, otherUser)
	rr := httptest.NewRecorder()

	handlers.GetChatMessages(rr, req)
	assert.Equal(t, http.StatusForbidden, rr.Code)

	mockSubscriptionRepo.AssertExpectations(t)
}

func TestChatHandlers_GetChatMessages_MessagesError(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:      streamID,
		Privacy: "public",
	}, nil)
	mockChatRepo.On("GetMessages", mock.Anything, streamID, 50, 0).Return(nil, errors.New("db error"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	rr := httptest.NewRecorder()

	handlers.GetChatMessages(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestChatHandlers_GetChatMessages_CountError(t *testing.T) {
	handlers, mockChatRepo, mockStreamRepo, _, _ := setupChatHandlerTest(t)

	streamID := uuid.New()
	mockStreamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
		ID:      streamID,
		Privacy: "public",
	}, nil)
	mockChatRepo.On("GetMessages", mock.Anything, streamID, 50, 0).Return([]*domain.ChatMessage{}, nil)
	mockChatRepo.On("GetMessageCount", mock.Anything, streamID).Return(0, errors.New("db error"))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req = withChiParam(req, "streamId", streamID.String())
	rr := httptest.NewRecorder()

	handlers.GetChatMessages(rr, req)
	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestNotificationHandlers_GetUnreadCount_Success(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications/unread-count", h.GetUnreadCount)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeBody(t, rr)
	assert.True(t, body["success"].(bool))
}

func TestNotificationHandlers_GetUnreadCount_Unauthorized(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications/unread-count", h.GetUnreadCount)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNotificationHandlers_GetUnreadCount_InvalidUserID(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "not-a-uuid"))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications/unread-count", h.GetUnreadCount)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestNotificationHandlers_GetNotificationStats_Success(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/stats", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications/stats", h.GetNotificationStats)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	body := decodeBody(t, rr)
	assert.True(t, body["success"].(bool))
}

func TestNotificationHandlers_GetNotificationStats_Unauthorized(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/stats", nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications/stats", h.GetNotificationStats)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNotificationHandlers_GetNotificationStats_InvalidUserID(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/stats", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "not-a-uuid"))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications/stats", h.GetNotificationStats)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestNotificationHandlers_MarkAsRead_Unauthorized(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/notifications/"+uuid.NewString()+"/read", nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/notifications/{id}/read", h.MarkAsRead)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNotificationHandlers_MarkAsRead_InvalidNotificationID(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/notifications/not-a-uuid/read", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/notifications/{id}/read", h.MarkAsRead)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestNotificationHandlers_MarkAsRead_NotFound(t *testing.T) {
	svc := &mockNotificationServiceErr{markReadErr: domain.ErrNotificationNotFound}
	h := NewNotificationHandlers(svc)

	nid := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/notifications/"+nid.String()+"/read", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/notifications/{id}/read", h.MarkAsRead)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestNotificationHandlers_MarkAsRead_InternalError(t *testing.T) {
	svc := &mockNotificationServiceErr{markReadErr: errors.New("db error")}
	h := NewNotificationHandlers(svc)

	nid := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/notifications/"+nid.String()+"/read", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/notifications/{id}/read", h.MarkAsRead)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestNotificationHandlers_MarkAllAsRead_Unauthorized(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/notifications/read-all", nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/notifications/read-all", h.MarkAllAsRead)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNotificationHandlers_MarkAllAsRead_InvalidUserID(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/notifications/read-all", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "not-a-uuid"))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/notifications/read-all", h.MarkAllAsRead)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestNotificationHandlers_MarkAllAsRead_InternalError(t *testing.T) {
	svc := &mockNotificationServiceErr{markAllErr: errors.New("db error")}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/notifications/read-all", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/notifications/read-all", h.MarkAllAsRead)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestNotificationHandlers_DeleteNotification_Unauthorized(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/notifications/"+uuid.NewString(), nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Delete("/api/v1/notifications/{id}", h.DeleteNotification)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNotificationHandlers_DeleteNotification_InvalidNotificationID(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/notifications/not-a-uuid", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Delete("/api/v1/notifications/{id}", h.DeleteNotification)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestNotificationHandlers_DeleteNotification_NotFound(t *testing.T) {
	svc := &mockNotificationServiceErr{deleteErr: domain.ErrNotificationNotFound}
	h := NewNotificationHandlers(svc)

	nid := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/notifications/"+nid.String(), nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Delete("/api/v1/notifications/{id}", h.DeleteNotification)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestNotificationHandlers_DeleteNotification_InternalError(t *testing.T) {
	svc := &mockNotificationServiceErr{deleteErr: errors.New("db error")}
	h := NewNotificationHandlers(svc)

	nid := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/notifications/"+nid.String(), nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Delete("/api/v1/notifications/{id}", h.DeleteNotification)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestGetUserIDFromContext(t *testing.T) {
	result := GetUserIDFromContext(context.Background())
	assert.Equal(t, "", result)

	userID := uuid.New()
	ctx := context.WithValue(context.Background(), middleware.UserIDKey, userID.String())
	result = GetUserIDFromContext(ctx)
	assert.Equal(t, userID.String(), result)
}

func TestGetClientIP_XForwardedFor_Ignored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:9999"
	req.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	assert.Equal(t, "192.0.2.1", GetClientIP(req))
}

func TestGetClientIP_XForwardedFor_Single_Ignored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:9999"
	req.Header.Set("X-Forwarded-For", "10.0.0.1")
	assert.Equal(t, "192.0.2.1", GetClientIP(req))
}

func TestGetClientIP_XRealIP_Ignored(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.0.2.1:9999"
	req.Header.Set("X-Real-IP", "9.8.7.6")
	assert.Equal(t, "192.0.2.1", GetClientIP(req))
}

func TestGetClientIP_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1:12345"
	assert.Equal(t, "192.168.1.1", GetClientIP(req))
}

func TestGetClientIP_RemoteAddr_NoPort(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "192.168.1.1"
	assert.Equal(t, "192.168.1.1", GetClientIP(req))
}

func TestWriteJSONResponse(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteJSONResponse(rr, http.StatusOK, map[string]string{"key": "value"})

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body map[string]string
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, "value", body["key"])
}

func TestWriteErrorResponse(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteErrorResponse(rr, http.StatusBadRequest, "invalid_input", "Field is required")

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	errObj := body["error"].(map[string]interface{})
	assert.Equal(t, "invalid_input", errObj["code"])
	assert.Equal(t, "Field is required", errObj["message"])
}

func TestWriteValidationErrorResponse(t *testing.T) {
	v := validator.New()
	type testReq struct {
		Name  string `validate:"required"`
		Email string `validate:"required,email"`
	}
	err := v.Struct(&testReq{})
	require.Error(t, err)

	rr := httptest.NewRecorder()
	WriteValidationErrorResponse(rr, err)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	errObj := body["error"].(map[string]interface{})
	assert.Equal(t, "validation_failed", errObj["code"])
	details := errObj["details"].([]interface{})
	assert.Len(t, details, 2)
}

func TestWriteValidationErrorResponse_NonValidationError(t *testing.T) {
	rr := httptest.NewRecorder()
	WriteValidationErrorResponse(rr, errors.New("generic error"))

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var body map[string]interface{}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	errObj := body["error"].(map[string]interface{})
	assert.Equal(t, "validation_failed", errObj["code"])
	assert.Nil(t, errObj["details"])
}

func TestGetValidationErrorMessage_AllTags(t *testing.T) {
	v := validator.New()

	type reqRequired struct {
		Name string `validate:"required"`
	}
	type reqMin struct {
		Name string `validate:"min=5"`
	}
	type reqMax struct {
		Name string `validate:"max=2"`
	}
	type reqEmail struct {
		Email string `validate:"email"`
	}
	type reqUUID struct {
		ID string `validate:"uuid"`
	}
	type reqOther struct {
		Name string `validate:"alpha"`
	}

	tests := []struct {
		name     string
		input    interface{}
		contains string
	}{
		{"required", &reqRequired{}, "required"},
		{"min", &reqMin{Name: "ab"}, "Minimum length"},
		{"max", &reqMax{Name: "abcdef"}, "Maximum length"},
		{"email", &reqEmail{Email: "bad"}, "Invalid email"},
		{"uuid", &reqUUID{ID: "bad"}, "Invalid UUID"},
		{"other", &reqOther{Name: "12345"}, "Invalid value"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.Struct(tt.input)
			require.Error(t, err)
			var ve validator.ValidationErrors
			require.True(t, errors.As(err, &ve))
			msg := getValidationErrorMessage(ve[0])
			assert.Contains(t, msg, tt.contains)
		})
	}
}

func TestGetUnreadCountHandler_Unauthorized(t *testing.T) {
	handler := GetUnreadCountHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/conversations/unread-count", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestGetMessagesHandler_MissingConversationWith(t *testing.T) {
	handler := GetMessagesHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/messages", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.NewString()))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestGetConversationsHandler_Unauthorized(t *testing.T) {
	handler := GetConversationsHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/conversations", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestSendMessageHandler_Unauthorized(t *testing.T) {
	handler := SendMessageHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/messages", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestSendMessageHandler_InvalidJSON(t *testing.T) {
	mockService := &MockMessageService{}
	handler := SendMessageHandler(mockService)

	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString("invalid json"))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.NewString()))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestSendMessageHandler_ValidationError(t *testing.T) {
	mockService := &MockMessageService{}
	handler := SendMessageHandler(mockService)

	body := `{"recipient_id":"","content":"test"}`
	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.NewString()))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestSendMessageHandler_ServiceError(t *testing.T) {
	mockService := &MockMessageService{}
	mockService.On("SendMessage", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Return(nil, errors.New("service error"))
	handler := SendMessageHandler(mockService)

	recipientID := uuid.New().String()
	body := fmt.Sprintf(`{"recipient_id":"%s","content":"test"}`, recipientID)
	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.NewString()))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockService.AssertExpectations(t)
}

func TestSendMessageHandler_Success(t *testing.T) {
	mockService := &MockMessageService{}
	messageID := uuid.New()
	senderID := uuid.New().String()
	recipientID := uuid.New().String()

	mockService.On("SendMessage", mock.Anything, senderID, mock.Anything).
		Return(&domain.Message{
			ID:          messageID.String(),
			SenderID:    senderID,
			RecipientID: recipientID,
			Content:     strPtr("test"),
		}, nil)
	handler := SendMessageHandler(mockService)

	body := fmt.Sprintf(`{"recipient_id":"%s","content":"test"}`, recipientID)
	req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, senderID))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusCreated, rr.Code)
	mockService.AssertExpectations(t)
}

func TestGetMessagesHandler_Unauthorized(t *testing.T) {
	handler := GetMessagesHandler(nil)

	req := httptest.NewRequest(http.MethodGet, "/messages?conversation_with="+uuid.NewString(), nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestGetMessagesHandler_ServiceError(t *testing.T) {
	mockService := &MockMessageService{}
	mockService.On("GetMessages", mock.Anything, mock.AnythingOfType("string"), mock.Anything).
		Return(nil, errors.New("service error"))
	handler := GetMessagesHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/messages?conversation_with="+uuid.NewString(), nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, uuid.NewString()))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
	mockService.AssertExpectations(t)
}

func TestGetMessagesHandler_Success(t *testing.T) {
	mockService := &MockMessageService{}
	userID := uuid.New().String()
	conversationWith := uuid.New().String()

	mockService.On("GetMessages", mock.Anything, userID, mock.Anything).
		Return(&domain.MessagesResponse{
			Messages: []domain.Message{
				{ID: uuid.NewString(), SenderID: userID, RecipientID: conversationWith, Content: strPtr("test")},
			},
			Total:   1,
			HasMore: false,
		}, nil)
	handler := GetMessagesHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/messages?conversation_with="+conversationWith, nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	mockService.AssertExpectations(t)
}

func TestGetUserID_UUIDType(t *testing.T) {
	userID := uuid.New()
	ctx := context.WithValue(context.Background(), middleware.UserIDKey, userID)
	result := getUserID(ctx)
	assert.Equal(t, userID.String(), result)
}

func TestGetUserID_EmptyString(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.UserIDKey, "")
	result := getUserID(ctx)
	assert.Equal(t, "", result)
}

func TestGetUserID_NilUUID(t *testing.T) {
	ctx := context.WithValue(context.Background(), middleware.UserIDKey, uuid.Nil)
	result := getUserID(ctx)
	assert.Equal(t, "", result)
}

func TestGetUserID_BackwardCompatibleFallback(t *testing.T) {
	ctx := context.WithValue(context.Background(), "userID", "legacy-user-id")
	result := getUserID(ctx)
	assert.Equal(t, "legacy-user-id", result)
}

func TestNotificationHandlers_GetNotifications_Unauthorized(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications", h.GetNotifications)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestNotificationHandlers_GetNotifications_InvalidUserID(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "not-a-uuid"))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications", h.GetNotifications)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestNotificationHandlers_GetNotifications_InvalidType(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	userID := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications?types=invalid_type", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications", h.GetNotifications)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestNotificationHandlers_GetNotifications_ServiceError(t *testing.T) {
	svc := &mockNotificationServiceErr{getNotificationsErr: errors.New("db error")}
	h := NewNotificationHandlers(svc)

	userID := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications", h.GetNotifications)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestNotificationHandlers_GetUnreadCount_ServiceError(t *testing.T) {
	svc := &mockNotificationServiceErr{getUnreadCountErr: errors.New("db error")}
	h := NewNotificationHandlers(svc)

	userID := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/unread-count", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications/unread-count", h.GetUnreadCount)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestNotificationHandlers_GetNotificationStats_ServiceError(t *testing.T) {
	svc := &mockNotificationServiceErr{getStatsErr: errors.New("db error")}
	h := NewNotificationHandlers(svc)

	userID := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/stats", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications/stats", h.GetNotificationStats)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusInternalServerError, rr.Code)
}

type mockNotificationServiceErr struct {
	markReadErr         error
	markAllErr          error
	deleteErr           error
	getNotificationsErr error
	getUnreadCountErr   error
	getStatsErr         error
}

func (m *mockNotificationServiceErr) CreateVideoNotificationForSubscribers(context.Context, *domain.Video, string) error {
	return nil
}
func (m *mockNotificationServiceErr) CreateMessageNotification(context.Context, *domain.Message, string) error {
	return nil
}
func (m *mockNotificationServiceErr) CreateMessageReadNotification(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (m *mockNotificationServiceErr) GetUserNotifications(_ context.Context, _ uuid.UUID, _ domain.NotificationFilter) ([]domain.Notification, error) {
	return nil, m.getNotificationsErr
}
func (m *mockNotificationServiceErr) MarkAsRead(_ context.Context, _, _ uuid.UUID) error {
	return m.markReadErr
}
func (m *mockNotificationServiceErr) MarkAllAsRead(_ context.Context, _ uuid.UUID) error {
	return m.markAllErr
}
func (m *mockNotificationServiceErr) DeleteNotification(_ context.Context, _, _ uuid.UUID) error {
	return m.deleteErr
}
func (m *mockNotificationServiceErr) GetUnreadCount(context.Context, uuid.UUID) (int, error) {
	return 0, m.getUnreadCountErr
}
func (m *mockNotificationServiceErr) GetStats(context.Context, uuid.UUID) (*domain.NotificationStats, error) {
	if m.getStatsErr != nil {
		return nil, m.getStatsErr
	}
	return &domain.NotificationStats{TotalCount: 0}, nil
}
