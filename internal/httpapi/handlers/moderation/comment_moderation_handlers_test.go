package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/middleware"
)

// mockCommentRepo implements port.CommentRepository methods needed for testing.
type mockCommentRepo struct {
	approveErr      error
	listAllComments []*domain.CommentWithUser
	listAllTotal    int64
	listAllErr      error
	bulkRemoveCount int64
	bulkRemoveErr   error
}

func (m *mockCommentRepo) Approve(_ context.Context, _ uuid.UUID) error {
	return m.approveErr
}

func (m *mockCommentRepo) ListAll(_ context.Context, _ domain.AdminCommentListOptions) ([]*domain.CommentWithUser, int64, error) {
	return m.listAllComments, m.listAllTotal, m.listAllErr
}

func (m *mockCommentRepo) BulkRemoveByAccount(_ context.Context, _ string) (int64, error) {
	return m.bulkRemoveCount, m.bulkRemoveErr
}

// Stub methods required by the port.CommentRepository interface.
func (m *mockCommentRepo) Create(_ context.Context, _ *domain.Comment) error { return nil }
func (m *mockCommentRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.Comment, error) {
	return nil, nil
}
func (m *mockCommentRepo) GetByIDWithUser(_ context.Context, _ uuid.UUID) (*domain.CommentWithUser, error) {
	return nil, nil
}
func (m *mockCommentRepo) Update(_ context.Context, _ uuid.UUID, _ string) error { return nil }
func (m *mockCommentRepo) Delete(_ context.Context, _ uuid.UUID) error           { return nil }
func (m *mockCommentRepo) ListByVideo(_ context.Context, _ domain.CommentListOptions) ([]*domain.CommentWithUser, error) {
	return nil, nil
}
func (m *mockCommentRepo) ListReplies(_ context.Context, _ uuid.UUID, _, _ int) ([]*domain.CommentWithUser, error) {
	return nil, nil
}
func (m *mockCommentRepo) ListRepliesBatch(_ context.Context, _ []uuid.UUID, _ int) (map[uuid.UUID][]*domain.CommentWithUser, error) {
	return nil, nil
}
func (m *mockCommentRepo) CountByVideo(_ context.Context, _ uuid.UUID, _ bool) (int, error) {
	return 0, nil
}
func (m *mockCommentRepo) FlagComment(_ context.Context, _ *domain.CommentFlag) error { return nil }
func (m *mockCommentRepo) UnflagComment(_ context.Context, _, _ uuid.UUID) error      { return nil }
func (m *mockCommentRepo) GetFlags(_ context.Context, _ uuid.UUID) ([]*domain.CommentFlag, error) {
	return nil, nil
}
func (m *mockCommentRepo) UpdateStatus(_ context.Context, _ uuid.UUID, _ domain.CommentStatus) error {
	return nil
}
func (m *mockCommentRepo) IsOwner(_ context.Context, _, _ uuid.UUID) (bool, error) { return false, nil }

func withCommentUserCtx(r *http.Request) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.UserIDKey, "550e8400-e29b-41d4-a716-446655440000")
	return r.WithContext(ctx)
}

func withCommentChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestApproveComment(t *testing.T) {
	tests := []struct {
		name       string
		commentID  string
		withAuth   bool
		repoErr    error
		wantStatus int
	}{
		{
			name:       "success",
			commentID:  uuid.New().String(),
			withAuth:   true,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "not found",
			commentID:  uuid.New().String(),
			withAuth:   true,
			repoErr:    domain.ErrNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "invalid UUID",
			commentID:  "not-a-uuid",
			withAuth:   true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "no auth",
			commentID:  uuid.New().String(),
			withAuth:   false,
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockCommentRepo{approveErr: tt.repoErr}
			h := NewCommentModerationHandlers(repo)

			req := httptest.NewRequest(http.MethodPost, "/", nil)
			req = withCommentChiParam(req, "commentId", tt.commentID)
			if tt.withAuth {
				req = withCommentUserCtx(req)
			}
			rec := httptest.NewRecorder()

			h.ApproveComment(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestListAllComments(t *testing.T) {
	commentID := uuid.New()
	userID := uuid.New()

	tests := []struct {
		name       string
		query      string
		comments   []*domain.CommentWithUser
		total      int64
		repoErr    error
		wantStatus int
		wantTotal  int64
	}{
		{
			name: "success with results",
			comments: []*domain.CommentWithUser{
				{Comment: domain.Comment{ID: commentID, UserID: userID, Body: "test"}, Username: "user1"},
			},
			total:      1,
			wantStatus: http.StatusOK,
			wantTotal:  1,
		},
		{
			name:       "success empty",
			comments:   []*domain.CommentWithUser{},
			total:      0,
			wantStatus: http.StatusOK,
			wantTotal:  0,
		},
		{
			name:       "with query params",
			query:      "?limit=10&offset=5&status=active&heldForReview=true&search=test",
			comments:   []*domain.CommentWithUser{},
			total:      0,
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockCommentRepo{
				listAllComments: tt.comments,
				listAllTotal:    tt.total,
				listAllErr:      tt.repoErr,
			}
			h := NewCommentModerationHandlers(repo)

			req := httptest.NewRequest(http.MethodGet, "/"+tt.query, nil)
			rec := httptest.NewRecorder()

			h.ListAllComments(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantStatus == http.StatusOK {
				var resp map[string]interface{}
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				assert.True(t, resp["success"].(bool))
			}
		})
	}
}

func TestBulkRemoveComments(t *testing.T) {
	tests := []struct {
		name       string
		body       interface{}
		withAuth   bool
		removed    int64
		repoErr    error
		wantStatus int
	}{
		{
			name:       "success",
			body:       domain.BulkRemoveCommentsRequest{AccountName: "spammer", Scope: "instance"},
			withAuth:   true,
			removed:    5,
			wantStatus: http.StatusOK,
		},
		{
			name:       "no auth",
			body:       domain.BulkRemoveCommentsRequest{AccountName: "spammer", Scope: "instance"},
			withAuth:   false,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "empty account name",
			body:       domain.BulkRemoveCommentsRequest{AccountName: "", Scope: "instance"},
			withAuth:   true,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "invalid body",
			body:       "not json",
			withAuth:   true,
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &mockCommentRepo{
				bulkRemoveCount: tt.removed,
				bulkRemoveErr:   tt.repoErr,
			}
			h := NewCommentModerationHandlers(repo)

			var bodyBytes []byte
			if str, ok := tt.body.(string); ok {
				bodyBytes = []byte(str)
			} else {
				bodyBytes, _ = json.Marshal(tt.body)
			}

			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			if tt.withAuth {
				req = withCommentUserCtx(req)
			}
			rec := httptest.NewRecorder()

			h.BulkRemoveComments(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)

			if tt.wantStatus == http.StatusOK {
				var resp map[string]interface{}
				err := json.Unmarshal(rec.Body.Bytes(), &resp)
				require.NoError(t, err)
				data := resp["data"].(map[string]interface{})
				assert.Equal(t, float64(tt.removed), data["removed"])
			}
		})
	}
}
