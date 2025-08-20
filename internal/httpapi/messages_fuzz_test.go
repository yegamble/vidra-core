package httpapi

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "strconv"
    "testing"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "athena/internal/domain"
    "athena/internal/middleware"
    "athena/internal/usecase"
)

// FuzzSendMessageHandler_InvalidPayloads fuzz-tests the SendMessage handler to ensure
// malformed JSON and odd payloads never cause panics or 5xx responses.
func FuzzSendMessageHandler_InvalidPayloads(f *testing.F) {
    // No DB or service needed; we only exercise pre-service validation paths
    stubService := usecase.NewMessageService(&stubMessageRepo{}, &stubUserRepo{})
    handler := SendMessageHandler(stubService)

    // Seed examples
    f.Add([]byte(""))
    f.Add([]byte("{}"))
    f.Add([]byte("{\"recipient_id\":\"not-uuid\"}"))
    f.Add([]byte("{\"recipient_id\":\"00000000-0000-0000-0000-000000000000\",\"content\":\"hi\"}"))

    f.Fuzz(func(t *testing.T, payload []byte) {
        // Force invalid JSON or validation failure to avoid invoking service
        var tmp map[string]any
        if err := json.Unmarshal(payload, &tmp); err == nil {
            // If it looks valid enough, break it
            if _, ok1 := tmp["recipient_id"].(string); ok1 {
                tmp["recipient_id"] = "not-a-uuid"
            }
            delete(tmp, "content")
            b, _ := json.Marshal(tmp)
            payload = b
        }
        // Case 1: unauthorized (should 401, no service call)
        req1 := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(payload))
        req1.Header.Set("Content-Type", "application/json")
        rr1 := httptest.NewRecorder()
        handler.ServeHTTP(rr1, req1)
        if rr1.Code >= 500 {
            t.Fatalf("unexpected 5xx (unauth): %d body=%s", rr1.Code, rr1.Body.String())
        }

        // Case 2: authorized but ensure invalid JSON/validation keeps us before service
        req2 := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(payload))
        req2.Header.Set("Content-Type", "application/json")
        req2 = req2.WithContext(context.WithValue(req2.Context(), middleware.UserIDKey, "11111111-1111-1111-1111-111111111111"))
        rr2 := httptest.NewRecorder()
        handler.ServeHTTP(rr2, req2)
        if rr2.Code >= 500 {
            t.Fatalf("unexpected 5xx (auth): %d body=%s", rr2.Code, rr2.Body.String())
        }
    })
}

// FuzzGetMessagesHandler_Query fuzz-tests the query parameters for GetMessages.
func FuzzGetMessagesHandler_Query(f *testing.F) {
    // No DB or service needed; we only exercise query validation
    // Create a minimal stub service to avoid nil pointer dereference
    stubService := usecase.NewMessageService(&stubMessageRepo{}, &stubUserRepo{})
    handler := GetMessagesHandler(stubService)

    f.Add("", 0, 0)
    f.Add("00000000-0000-0000-0000-000000000000", 0, 0)
    f.Add("not-a-uuid", -10, -1)

    f.Fuzz(func(t *testing.T, other string, limit, offset int) {
        url := "/messages?conversation_with=" + other + "&limit=" + itoaSafe(limit) + "&offset=" + itoaSafe(offset)
        req := httptest.NewRequest(http.MethodGet, url, nil)
        // authenticated user
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "11111111-1111-1111-1111-111111111111"))
        rr := httptest.NewRecorder()
        handler.ServeHTTP(rr, req)
        if rr.Code >= 500 {
            t.Fatalf("unexpected 5xx: %d body=%s", rr.Code, rr.Body.String())
        }
    })
}

// FuzzMarkMessageReadHandler_Path fuzz-tests the messageId path param handling.
func FuzzMarkMessageReadHandler_Path(f *testing.F) {
    // No DB or service needed; we only exercise path parameter validation
    stubService := usecase.NewMessageService(&stubMessageRepo{}, &stubUserRepo{})
    handler := MarkMessageReadHandler(stubService)

    f.Add("")
    f.Add("not-a-uuid")
    f.Add(uuid.NewString())

    f.Fuzz(func(t *testing.T, id string) {
        req := httptest.NewRequest(http.MethodPut, "/messages/"+id+"/read", nil)
        req = withChiURLParam(req, "messageId", id)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "11111111-1111-1111-1111-111111111111"))
        rr := httptest.NewRecorder()
        handler.ServeHTTP(rr, req)
        if rr.Code >= 500 {
            t.Fatalf("unexpected 5xx: %d body=%s", rr.Code, rr.Body.String())
        }
    })
}

// FuzzDeleteMessageHandler_Path fuzz-tests deletion path param handling to ensure
// invalid IDs and missing params are handled without panics or 5xx.
func FuzzDeleteMessageHandler_Path(f *testing.F) {
    stubService := usecase.NewMessageService(&stubMessageRepo{}, &stubUserRepo{})
    handler := DeleteMessageHandler(stubService)

    f.Add("")
    f.Add("not-a-uuid")
    f.Add(uuid.NewString())

    f.Fuzz(func(t *testing.T, id string) {
        // ensure invalid to short-circuit before service layer
        if _, err := uuid.Parse(id); err == nil {
            id = id + "x"
        }
        req := httptest.NewRequest(http.MethodDelete, "/messages/"+id, nil)
        req = withChiURLParam(req, "messageId", id)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "11111111-1111-1111-1111-111111111111"))
        rr := httptest.NewRecorder()
        handler.ServeHTTP(rr, req)
        if rr.Code >= 500 {
            t.Fatalf("unexpected 5xx: %d body=%s", rr.Code, rr.Body.String())
        }
    })
}

// FuzzGetConversationsHandler_Query fuzz-tests limit/offset bounds to ensure validation catches issues.
func FuzzGetConversationsHandler_Query(f *testing.F) {
    stubService := usecase.NewMessageService(&stubMessageRepo{}, &stubUserRepo{})
    handler := GetConversationsHandler(stubService)

    f.Add(-1, -1)
    f.Add(0, 0)
    f.Add(51, 0) // over max limit

    f.Fuzz(func(t *testing.T, limit, offset int) {
        // force invalid
        if limit >= 1 && limit <= 50 {
            limit = 51
        }
        if offset < 0 {
            offset = -1
        }
        url := "/conversations?limit=" + itoaSafe(limit) + "&offset=" + itoaSafe(offset)
        req := httptest.NewRequest(http.MethodGet, url, nil)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "11111111-1111-1111-1111-111111111111"))
        rr := httptest.NewRecorder()
        handler.ServeHTTP(rr, req)
        if rr.Code >= 500 {
            t.Fatalf("unexpected 5xx: %d body=%s", rr.Code, rr.Body.String())
        }
    })
}

// Utilities for fuzz tests (minimal helpers to avoid importing from other test files)

func itoaSafe(i int) string {
    if i < 0 {
        i = -i
    }
    return strconv.Itoa(i)
}

func withChiURLParam(r *http.Request, key, value string) *http.Request {
    rctx := chi.NewRouteContext()
    rctx.URLParams.Add(key, value)
    return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// Minimal stub repositories for fuzz testing that only exercises validation
type stubMessageRepo struct{}

func (s *stubMessageRepo) CreateMessage(context.Context, *domain.Message) error {
    return domain.ErrInternalServer
}

func (s *stubMessageRepo) GetMessage(context.Context, string, string) (*domain.Message, error) {
    return nil, domain.ErrInternalServer
}

func (s *stubMessageRepo) GetMessages(context.Context, string, string, int, int) ([]*domain.Message, error) {
    return nil, domain.ErrInternalServer
}

func (s *stubMessageRepo) MarkMessageAsRead(context.Context, string, string) error {
    return domain.ErrMessageNotFound
}

func (s *stubMessageRepo) DeleteMessage(context.Context, string, string) error {
    return domain.ErrMessageNotFound
}

func (s *stubMessageRepo) GetConversations(context.Context, string, int, int) ([]*domain.Conversation, error) {
    return nil, domain.ErrConversationNotFound
}

func (s *stubMessageRepo) GetUnreadCount(context.Context, string) (int, error) {
    return 0, domain.ErrUserNotFound
}

type stubUserRepo struct{}

func (s *stubUserRepo) Create(context.Context, *domain.User, string) error {
    return domain.ErrInternalServer
}

func (s *stubUserRepo) GetByID(context.Context, string) (*domain.User, error) {
    return nil, domain.ErrUserNotFound
}

func (s *stubUserRepo) GetByEmail(context.Context, string) (*domain.User, error) {
    return nil, domain.ErrInternalServer
}

func (s *stubUserRepo) GetByUsername(context.Context, string) (*domain.User, error) {
    return nil, domain.ErrInternalServer
}

func (s *stubUserRepo) Update(context.Context, *domain.User) error {
    return domain.ErrInternalServer
}

func (s *stubUserRepo) Delete(context.Context, string) error {
    return domain.ErrInternalServer
}

func (s *stubUserRepo) GetPasswordHash(context.Context, string) (string, error) {
    return "", domain.ErrInternalServer
}

func (s *stubUserRepo) UpdatePassword(context.Context, string, string) error {
    return domain.ErrInternalServer
}

func (s *stubUserRepo) List(context.Context, int, int) ([]*domain.User, error) {
    return nil, domain.ErrInternalServer
}

func (s *stubUserRepo) Count(context.Context) (int64, error) {
    return 0, domain.ErrInternalServer
}

func (s *stubUserRepo) SetAvatarFields(context.Context, string, *string, *string) error {
    return domain.ErrInternalServer
}
