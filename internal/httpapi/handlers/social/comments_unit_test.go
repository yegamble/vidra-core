package social

import (
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockCommentService struct {
	createCommentFunc   func(ctx context.Context, userID uuid.UUID, req *domain.CreateCommentRequest) (*domain.Comment, error)
	getCommentFunc      func(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error)
	listCommentsFunc    func(ctx context.Context, videoID uuid.UUID, parentID *uuid.UUID, limit, offset int, orderBy string) ([]*domain.CommentWithUser, error)
	updateCommentFunc   func(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, req *domain.UpdateCommentRequest) error
	deleteCommentFunc   func(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, isAdmin bool) error
	flagCommentFunc     func(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, req *domain.FlagCommentRequest) error
	unflagCommentFunc   func(ctx context.Context, userID uuid.UUID, commentID uuid.UUID) error
	moderateCommentFunc func(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, status domain.CommentStatus, isAdmin bool) error
}

func (m *mockCommentService) CreateComment(ctx context.Context, userID uuid.UUID, req *domain.CreateCommentRequest) (*domain.Comment, error) {
	if m.createCommentFunc != nil {
		return m.createCommentFunc(ctx, userID, req)
	}
	return nil, errors.New("not implemented")
}

func (m *mockCommentService) GetComment(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
	if m.getCommentFunc != nil {
		return m.getCommentFunc(ctx, id)
	}
	return nil, errors.New("not implemented")
}

func (m *mockCommentService) ListComments(ctx context.Context, videoID uuid.UUID, parentID *uuid.UUID, limit, offset int, orderBy string) ([]*domain.CommentWithUser, error) {
	if m.listCommentsFunc != nil {
		return m.listCommentsFunc(ctx, videoID, parentID, limit, offset, orderBy)
	}
	return nil, errors.New("not implemented")
}

func (m *mockCommentService) UpdateComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, req *domain.UpdateCommentRequest) error {
	if m.updateCommentFunc != nil {
		return m.updateCommentFunc(ctx, userID, commentID, req)
	}
	return errors.New("not implemented")
}

func (m *mockCommentService) DeleteComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, isAdmin bool) error {
	if m.deleteCommentFunc != nil {
		return m.deleteCommentFunc(ctx, userID, commentID, isAdmin)
	}
	return errors.New("not implemented")
}

func (m *mockCommentService) FlagComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, req *domain.FlagCommentRequest) error {
	if m.flagCommentFunc != nil {
		return m.flagCommentFunc(ctx, userID, commentID, req)
	}
	return errors.New("not implemented")
}

func (m *mockCommentService) UnflagComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID) error {
	if m.unflagCommentFunc != nil {
		return m.unflagCommentFunc(ctx, userID, commentID)
	}
	return errors.New("not implemented")
}

func (m *mockCommentService) ModerateComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, status domain.CommentStatus, isAdmin bool) error {
	if m.moderateCommentFunc != nil {
		return m.moderateCommentFunc(ctx, userID, commentID, status, isAdmin)
	}
	return errors.New("not implemented")
}

func TestGetComment_InvalidID(t *testing.T) {
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/comments/invalid-uuid", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetComment_NotFound(t *testing.T) {
	commentID := uuid.New()
	mockService := &mockCommentService{
		getCommentFunc: func(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
			return nil, domain.ErrNotFound
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/comments/"+commentID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetComment(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetComment_ServiceError(t *testing.T) {
	commentID := uuid.New()
	mockService := &mockCommentService{
		getCommentFunc: func(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
			return nil, errors.New("database error")
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/comments/"+commentID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetComment(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetComment_Success(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()
	videoID := uuid.New()

	expectedComment := &domain.CommentWithUser{
		Comment: domain.Comment{
			ID:      commentID,
			VideoID: videoID,
			UserID:  userID,
			Body:    "Test comment",
		},
		Username: "testuser",
	}

	mockService := &mockCommentService{
		getCommentFunc: func(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
			return expectedComment, nil
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/comments/"+commentID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetComment(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper shared.Response
	err := json.NewDecoder(rec.Body).Decode(&wrapper)
	require.NoError(t, err)

	commentBytes, err := json.Marshal(wrapper.Data)
	require.NoError(t, err)

	var response domain.CommentWithUser
	err = json.Unmarshal(commentBytes, &response)
	require.NoError(t, err)
	assert.Equal(t, commentID, response.ID)
	assert.Equal(t, "Test comment", response.Body)
}

func TestUpdateComment_Unauthorized(t *testing.T) {
	commentID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.UpdateCommentRequest{Body: "Updated comment"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/comments/"+commentID.String(), bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.UpdateComment(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUpdateComment_InvalidJSON(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/comments/"+commentID.String(), bytes.NewReader([]byte("invalid json")))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.UpdateComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateComment_InvalidBody(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.UpdateCommentRequest{Body: ""}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/comments/"+commentID.String(), bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.UpdateComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateComment_NotFound(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		updateCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, req *domain.UpdateCommentRequest) error {
			return domain.ErrNotFound
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.UpdateCommentRequest{Body: "Updated comment"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/comments/"+commentID.String(), bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.UpdateComment(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUpdateComment_Forbidden(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		updateCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, req *domain.UpdateCommentRequest) error {
			return domain.ErrUnauthorized
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.UpdateCommentRequest{Body: "Updated comment"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/comments/"+commentID.String(), bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.UpdateComment(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestUpdateComment_Success(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		updateCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, req *domain.UpdateCommentRequest) error {
			return nil
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.UpdateCommentRequest{Body: "Updated comment"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/comments/"+commentID.String(), bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.UpdateComment(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestDeleteComment_Unauthorized(t *testing.T) {
	commentID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/"+commentID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.DeleteComment(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDeleteComment_InvalidID(t *testing.T) {
	userID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/invalid-uuid", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.DeleteComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDeleteComment_NotFound(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		deleteCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, isAdmin bool) error {
			return domain.ErrNotFound
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/"+commentID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.DeleteComment(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDeleteComment_Forbidden(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		deleteCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, isAdmin bool) error {
			return domain.ErrUnauthorized
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/"+commentID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.DeleteComment(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDeleteComment_Success(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		deleteCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, isAdmin bool) error {
			return nil
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/"+commentID.String(), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.DeleteComment(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestCreateComment_InvalidVideoID(t *testing.T) {
	userID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.CreateCommentRequest{Body: "Test comment"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/invalid-uuid/comments", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.CreateComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateComment_Unauthorized(t *testing.T) {
	videoID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.CreateCommentRequest{Body: "Test comment"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/comments", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.CreateComment(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreateComment_InvalidJSON(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/comments", bytes.NewReader([]byte("invalid json")))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.CreateComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateComment_EmptyBody(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.CreateCommentRequest{Body: ""}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/comments", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.CreateComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateComment_VideoNotFound(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		createCommentFunc: func(ctx context.Context, uid uuid.UUID, req *domain.CreateCommentRequest) (*domain.Comment, error) {
			return nil, domain.ErrNotFound
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.CreateCommentRequest{Body: "Test comment"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/comments", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.CreateComment(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestCreateComment_Success(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	commentID := uuid.New()

	expectedComment := &domain.Comment{
		ID:      commentID,
		VideoID: videoID,
		UserID:  userID,
		Body:    "Test comment",
	}

	mockService := &mockCommentService{
		createCommentFunc: func(ctx context.Context, uid uuid.UUID, req *domain.CreateCommentRequest) (*domain.Comment, error) {
			return expectedComment, nil
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.CreateCommentRequest{Body: "Test comment"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/comments", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.CreateComment(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestGetComments_InvalidVideoID(t *testing.T) {
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/invalid-uuid/comments", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetComments(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetComments_InvalidParentID(t *testing.T) {
	videoID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/comments?parentId=invalid-uuid", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetComments(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetComments_NotFound(t *testing.T) {
	videoID := uuid.New()

	mockService := &mockCommentService{
		listCommentsFunc: func(ctx context.Context, vid uuid.UUID, parentID *uuid.UUID, limit, offset int, orderBy string) ([]*domain.CommentWithUser, error) {
			return nil, domain.ErrNotFound
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/comments", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetComments(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetComments_Success(t *testing.T) {
	videoID := uuid.New()

	comments := []*domain.CommentWithUser{
		{
			Comment: domain.Comment{
				ID:      uuid.New(),
				VideoID: videoID,
				Body:    "Comment 1",
			},
			Username: "user1",
		},
		{
			Comment: domain.Comment{
				ID:      uuid.New(),
				VideoID: videoID,
				Body:    "Comment 2",
			},
			Username: "user2",
		},
	}

	mockService := &mockCommentService{
		listCommentsFunc: func(ctx context.Context, vid uuid.UUID, parentID *uuid.UUID, limit, offset int, orderBy string) ([]*domain.CommentWithUser, error) {
			return comments, nil
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/comments", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoId", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.GetComments(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestFlagComment_InvalidCommentID(t *testing.T) {
	userID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.FlagCommentRequest{Reason: "spam"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/invalid-uuid/flag", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.FlagComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFlagComment_Unauthorized(t *testing.T) {
	commentID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.FlagCommentRequest{Reason: "spam"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/"+commentID.String()+"/flag", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.FlagComment(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestFlagComment_InvalidReason(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.FlagCommentRequest{Reason: "invalid_reason"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/"+commentID.String()+"/flag", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.FlagComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestFlagComment_NotFound(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		flagCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, req *domain.FlagCommentRequest) error {
			return domain.ErrNotFound
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.FlagCommentRequest{Reason: "spam"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/"+commentID.String()+"/flag", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.FlagComment(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestFlagComment_Success(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		flagCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, req *domain.FlagCommentRequest) error {
			return nil
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := domain.FlagCommentRequest{Reason: "spam"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/"+commentID.String()+"/flag", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.FlagComment(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestUnflagComment_InvalidCommentID(t *testing.T) {
	userID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/invalid-uuid/flag", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.UnflagComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnflagComment_Unauthorized(t *testing.T) {
	commentID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/"+commentID.String()+"/flag", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.UnflagComment(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnflagComment_NotFound(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		unflagCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID) error {
			return domain.ErrNotFound
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/"+commentID.String()+"/flag", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.UnflagComment(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestUnflagComment_Success(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		unflagCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID) error {
			return nil
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/"+commentID.String()+"/flag", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.UnflagComment(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}

func TestModerateComment_InvalidCommentID(t *testing.T) {
	userID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := map[string]string{"status": "hidden"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/invalid-uuid/moderate", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.ModerateComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestModerateComment_Unauthorized(t *testing.T) {
	commentID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := map[string]string{"status": "hidden"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/"+commentID.String()+"/moderate", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()
	handler.ModerateComment(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestModerateComment_InvalidStatus(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()
	mockService := &mockCommentService{}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := map[string]string{"status": "invalid_status"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/"+commentID.String()+"/moderate", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.ModerateComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestModerateComment_NotFound(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		moderateCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, status domain.CommentStatus, isAdmin bool) error {
			return domain.ErrNotFound
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := map[string]string{"status": "hidden"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/"+commentID.String()+"/moderate", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.ModerateComment(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestModerateComment_Forbidden(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		moderateCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, status domain.CommentStatus, isAdmin bool) error {
			return domain.ErrUnauthorized
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := map[string]string{"status": "hidden"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/"+commentID.String()+"/moderate", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.ModerateComment(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestModerateComment_Success(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	mockService := &mockCommentService{
		moderateCommentFunc: func(ctx context.Context, uid uuid.UUID, cid uuid.UUID, status domain.CommentStatus, isAdmin bool) error {
			return nil
		},
	}
	handler := &CommentHandlers{commentService: mockService}

	reqBody := map[string]string{"status": "hidden"}
	bodyBytes, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/"+commentID.String()+"/moderate", bytes.NewReader(bodyBytes))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("commentId", commentID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, userID.String()))

	rec := httptest.NewRecorder()
	handler.ModerateComment(rec, req)

	assert.Equal(t, http.StatusNoContent, rec.Code)
}
