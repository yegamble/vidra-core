package httpapi

import (
    "bytes"
    "context"
    "encoding/json"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"
    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"

    "athena/internal/domain"
    "athena/internal/middleware"
    "athena/internal/repository"
    "athena/internal/testutil"
    "athena/internal/usecase"
)

// Helper to decode response envelope and return raw data
func decodeEnvelope(t *testing.T, rr *httptest.ResponseRecorder) Response {
    t.Helper()
    var env Response
    err := json.Unmarshal(rr.Body.Bytes(), &env)
    if err != nil {
        t.Fatalf("decode envelope: %v (body=%s)", err, rr.Body.String())
    }
    return env
}

func TestSendMessageHandler_SuccessAndSafety(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil { // skipped due to unavailable services
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    userRepo := repository.NewUserRepository(td.DB)
    msgRepo := repository.NewMessageRepository(td.DB)
    svc := usecase.NewMessageService(msgRepo, userRepo)

    ctx := context.Background()
    // Seed two users
    alice := createTestUser(t, userRepo, ctx, "alice_msg", "alice_msg@example.com")
    bob := createTestUser(t, userRepo, ctx, "bob_msg", "bob_msg@example.com")

    handler := SendMessageHandler(svc)

    // Content including characters commonly seen in injection/XSS attempts
    contents := []string{
        "Hello, Bob!",
        `Bob's quote: ", '; --`,
        "<script>alert('xss');</script>",
    }

    for _, c := range contents {
        body := map[string]any{
            "recipient_id": bob.ID,
            "content":      c,
        }
        b, _ := json.Marshal(body)
        req := httptest.NewRequest(http.MethodPost, "/api/v1/messages", bytes.NewReader(b))
        req.Header.Set("Content-Type", "application/json")
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, alice.ID))

        rr := httptest.NewRecorder()
        handler.ServeHTTP(rr, req)

        assert.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
        env := decodeEnvelope(t, rr)
        require.True(t, env.Success)

        var mr domain.MessageResponse
        dataBytes, _ := json.Marshal(env.Data)
        require.NoError(t, json.Unmarshal(dataBytes, &mr))
        assert.Equal(t, alice.ID, mr.Message.SenderID)
        assert.Equal(t, bob.ID, mr.Message.RecipientID)
        assert.Equal(t, c, mr.Message.Content)
        assert.False(t, mr.Message.IsRead)
        assert.Equal(t, domain.MessageTypeText, mr.Message.MessageType)
    }
}

func TestSendMessageHandler_Unauthorized_InvalidJSON_Validation(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil {
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    userRepo := repository.NewUserRepository(td.DB)
    msgRepo := repository.NewMessageRepository(td.DB)
    svc := usecase.NewMessageService(msgRepo, userRepo)

    ctx := context.Background()
    u := createTestUser(t, userRepo, ctx, "sender", "sender@example.com")

    // Unauthorized
    {
        body := map[string]any{"recipient_id": uuid.NewString(), "content": "hi"}
        b, _ := json.Marshal(body)
        req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b))
        req.Header.Set("Content-Type", "application/json")
        rr := httptest.NewRecorder()
        SendMessageHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusUnauthorized, rr.Code)
    }

    // Invalid JSON
    {
        req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewBufferString("{\"recipient_id\": \""))
        req.Header.Set("Content-Type", "application/json")
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))
        rr := httptest.NewRecorder()
        SendMessageHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusBadRequest, rr.Code)
        env := decodeEnvelope(t, rr)
        require.NotNil(t, env.Error)
        assert.Contains(t, env.Error.Code, "INVALID_JSON")
    }

    // Validation error: bad UUID and missing content
    {
        body := map[string]any{"recipient_id": "not-a-uuid"}
        b, _ := json.Marshal(body)
        req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b))
        req.Header.Set("Content-Type", "application/json")
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))
        rr := httptest.NewRecorder()
        SendMessageHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusBadRequest, rr.Code)
        env := decodeEnvelope(t, rr)
        require.NotNil(t, env.Error)
        assert.Contains(t, env.Error.Code, "VALIDATION_ERROR")
    }

    // Self-messaging rejected and long content rejected
    v := createTestUser(t, userRepo, ctx, "recipient", "recipient@example.com")
    {
        body := map[string]any{"recipient_id": u.ID, "content": "hi"}
        b, _ := json.Marshal(body)
        req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b))
        req.Header.Set("Content-Type", "application/json")
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))
        rr := httptest.NewRecorder()
        SendMessageHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusBadRequest, rr.Code)
        env := decodeEnvelope(t, rr)
        require.NotNil(t, env.Error)
        // Handler wraps domain error into SEND_MESSAGE_FAILED for service errors
        assert.Contains(t, env.Error.Code, "SEND_MESSAGE_FAILED")
        assert.Contains(t, env.Error.Message, "cannot send message to yourself")
    }
    {
        long := bytes.Repeat([]byte("x"), 2001)
        body := map[string]any{"recipient_id": v.ID, "content": string(long)}
        b, _ := json.Marshal(body)
        req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b))
        req.Header.Set("Content-Type", "application/json")
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))
        rr := httptest.NewRecorder()
        SendMessageHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusBadRequest, rr.Code)
        env := decodeEnvelope(t, rr)
        require.NotNil(t, env.Error)
        assert.Contains(t, env.Error.Code, "SEND_MESSAGE_FAILED")
        assert.Contains(t, env.Error.Message, "too long")
    }
}

func TestGetMessages_MarkRead_Delete(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil {
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    userRepo := repository.NewUserRepository(td.DB)
    msgRepo := repository.NewMessageRepository(td.DB)
    svc := usecase.NewMessageService(msgRepo, userRepo)

    ctx := context.Background()
    alice := createTestUser(t, userRepo, ctx, "alice2", "alice2@example.com")
    bob := createTestUser(t, userRepo, ctx, "bob2", "bob2@example.com")

    // Seed a few messages via handler
    send := func(sender, recipient *domain.User, content string) domain.Message {
        body := map[string]any{"recipient_id": recipient.ID, "content": content}
        b, _ := json.Marshal(body)
        req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b))
        req.Header.Set("Content-Type", "application/json")
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, sender.ID))
        rr := httptest.NewRecorder()
        SendMessageHandler(svc).ServeHTTP(rr, req)
        if rr.Code != http.StatusCreated {
            t.Fatalf("send failed: %d %s", rr.Code, rr.Body.String())
        }
        env := decodeEnvelope(t, rr)
        var out domain.MessageResponse
        dataBytes, _ := json.Marshal(env.Data)
        _ = json.Unmarshal(dataBytes, &out)
        return out.Message
    }

    m1 := send(alice, bob, "hello 1")
    _ = send(bob, alice, "reply 1")
    m3 := send(alice, bob, "hello 2")

    // Get messages as Alice (should include her messages and Bob's reply)
    {
        req := httptest.NewRequest(http.MethodGet, "/messages?conversation_with="+bob.ID, nil)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, alice.ID))
        rr := httptest.NewRecorder()
        GetMessagesHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
        env := decodeEnvelope(t, rr)
        var list domain.MessagesResponse
        dataBytes, _ := json.Marshal(env.Data)
        require.NoError(t, json.Unmarshal(dataBytes, &list))
        assert.Equal(t, 3, list.Total)
        // Messages are ordered desc by created_at
        require.Len(t, list.Messages, 3)
        assert.Equal(t, m3.ID, list.Messages[0].ID)
        assert.Equal(t, m1.ID, list.Messages[2].ID)
    }

    // Mark latest Alice->Bob as read by Bob
    {
        req := httptest.NewRequest(http.MethodPut, "/messages/"+m3.ID+"/read", nil)
        rctx := chi.NewRouteContext()
        rctx.URLParams.Add("messageId", m3.ID)
        req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, bob.ID))
        rr := httptest.NewRecorder()
        MarkMessageReadHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
    }

    // Mark as read by non-recipient should 404
    {
        req := httptest.NewRequest(http.MethodPut, "/messages/"+m1.ID+"/read", nil)
        rctx := chi.NewRouteContext()
        rctx.URLParams.Add("messageId", m1.ID)
        req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, alice.ID))
        rr := httptest.NewRecorder()
        MarkMessageReadHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusNotFound, rr.Code)
        env := decodeEnvelope(t, rr)
        require.NotNil(t, env.Error)
        assert.Contains(t, env.Error.Code, "MARK_READ_FAILED")
    }

    // Delete oldest Alice->Bob as Alice (soft delete). Alice should no longer see it, Bob should.
    {
        req := httptest.NewRequest(http.MethodDelete, "/messages/"+m1.ID, nil)
        rctx := chi.NewRouteContext()
        rctx.URLParams.Add("messageId", m1.ID)
        req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, alice.ID))
        rr := httptest.NewRecorder()
        DeleteMessageHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
    }

    // Alice fetches: should be 2 now (m1 hidden)
    {
        req := httptest.NewRequest(http.MethodGet, "/messages?conversation_with="+bob.ID, nil)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, alice.ID))
        rr := httptest.NewRecorder()
        GetMessagesHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusOK, rr.Code)
        env := decodeEnvelope(t, rr)
        var list domain.MessagesResponse
        dataBytes, _ := json.Marshal(env.Data)
        _ = json.Unmarshal(dataBytes, &list)
        assert.Equal(t, 2, list.Total)
    }

    // Bob fetches: should still see 3
    {
        req := httptest.NewRequest(http.MethodGet, "/messages?conversation_with="+alice.ID, nil)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, bob.ID))
        rr := httptest.NewRecorder()
        GetMessagesHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusOK, rr.Code)
        env := decodeEnvelope(t, rr)
        var list domain.MessagesResponse
        dataBytes, _ := json.Marshal(env.Data)
        _ = json.Unmarshal(dataBytes, &list)
        assert.Equal(t, 3, list.Total)
    }
}

func TestConversations_AndUnreadCount(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil {
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    userRepo := repository.NewUserRepository(td.DB)
    msgRepo := repository.NewMessageRepository(td.DB)
    svc := usecase.NewMessageService(msgRepo, userRepo)

    ctx := context.Background()
    u1 := createTestUser(t, userRepo, ctx, "u1msg", "u1msg@example.com")
    u2 := createTestUser(t, userRepo, ctx, "u2msg", "u2msg@example.com")
    u3 := createTestUser(t, userRepo, ctx, "u3msg", "u3msg@example.com")

    // Seed messages
    send := func(sender, recipient *domain.User, content string) {
        body := map[string]any{"recipient_id": recipient.ID, "content": content}
        b, _ := json.Marshal(body)
        req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b))
        req.Header.Set("Content-Type", "application/json")
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, sender.ID))
        rr := httptest.NewRecorder()
        SendMessageHandler(svc).ServeHTTP(rr, req)
        if rr.Code != http.StatusCreated {
            t.Fatalf("send failed: %d %s", rr.Code, rr.Body.String())
        }
    }

    send(u1, u2, "u1->u2 #1")
    time.Sleep(10 * time.Millisecond)
    send(u2, u1, "u2->u1 #1")
    time.Sleep(10 * time.Millisecond)
    send(u1, u3, "u1->u3 #1")

    // Unread count for u1 should be 1 (message from u2)
    {
        req := httptest.NewRequest(http.MethodGet, "/conversations/unread-count", nil)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u1.ID))
        rr := httptest.NewRecorder()
        GetUnreadCountHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusOK, rr.Code)
        env := decodeEnvelope(t, rr)
        m := map[string]int{}
        dataBytes, _ := json.Marshal(env.Data)
        _ = json.Unmarshal(dataBytes, &m)
        assert.Equal(t, 1, m["unread_count"]) 
    }

    // Conversations for u1 should include two threads ordered by last message
    {
        req := httptest.NewRequest(http.MethodGet, "/conversations", nil)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u1.ID))
        rr := httptest.NewRecorder()
        GetConversationsHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
        env := decodeEnvelope(t, rr)
        var cr domain.ConversationsResponse
        dataBytes, _ := json.Marshal(env.Data)
        _ = json.Unmarshal(dataBytes, &cr)
        assert.Equal(t, 2, cr.Total)
        require.Len(t, cr.Conversations, 2)
        // The most recent last_message should be from u1->u3
        assert.NotNil(t, cr.Conversations[0].LastMessage)
        assert.Equal(t, u1.ID, cr.Conversations[0].LastMessage.SenderID)
    }
}

func TestMessagesHandlers_ValidationAndMissingParam(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil {
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    userRepo := repository.NewUserRepository(td.DB)
    msgRepo := repository.NewMessageRepository(td.DB)
    svc := usecase.NewMessageService(msgRepo, userRepo)

    ctx := context.Background()
    u := createTestUser(t, userRepo, ctx, "valuser", "valuser@example.com")

    // GetMessages: unauthorized
    {
        req := httptest.NewRequest(http.MethodGet, "/messages?conversation_with="+uuid.NewString(), nil)
        rr := httptest.NewRecorder()
        GetMessagesHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusUnauthorized, rr.Code)
    }

    // GetMessages: invalid uuid
    {
        req := httptest.NewRequest(http.MethodGet, "/messages?conversation_with=not-a-uuid", nil)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))
        rr := httptest.NewRecorder()
        GetMessagesHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusBadRequest, rr.Code)
        env := decodeEnvelope(t, rr)
        require.NotNil(t, env.Error)
        assert.Contains(t, env.Error.Code, "VALIDATION_ERROR")
    }

    // MarkMessageRead: missing path param
    {
        req := httptest.NewRequest(http.MethodPut, "/messages//read", nil)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))
        rr := httptest.NewRecorder()
        MarkMessageReadHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusBadRequest, rr.Code)
        env := decodeEnvelope(t, rr)
        require.NotNil(t, env.Error)
        assert.Contains(t, env.Error.Code, "MISSING_PARAMETER")
    }

    // DeleteMessage: missing path param
    {
        req := httptest.NewRequest(http.MethodDelete, "/messages/", nil)
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, u.ID))
        rr := httptest.NewRecorder()
        DeleteMessageHandler(svc).ServeHTTP(rr, req)
        assert.Equal(t, http.StatusBadRequest, rr.Code)
        env := decodeEnvelope(t, rr)
        require.NotNil(t, env.Error)
        assert.Contains(t, env.Error.Code, "MISSING_PARAMETER")
    }
}

func TestSendMessage_WithInvalidParentReference_ReturnsNotFound(t *testing.T) {
    td := testutil.SetupTestDB(t)
    if td == nil {
        return
    }
    td.TruncateTables(t, "messages", "conversations", "users")

    userRepo := repository.NewUserRepository(td.DB)
    msgRepo := repository.NewMessageRepository(td.DB)
    svc := usecase.NewMessageService(msgRepo, userRepo)

    ctx := context.Background()
    alice := createTestUser(t, userRepo, ctx, "alice3", "alice3@example.com")
    bob := createTestUser(t, userRepo, ctx, "bob3", "bob3@example.com")
    carol := createTestUser(t, userRepo, ctx, "carol3", "carol3@example.com")

    // Carol -> Bob creates a message
    {
        body := map[string]any{"recipient_id": bob.ID, "content": "hello from carol"}
        b, _ := json.Marshal(body)
        req := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b))
        req.Header.Set("Content-Type", "application/json")
        req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, carol.ID))
        rr := httptest.NewRecorder()
        SendMessageHandler(svc).ServeHTTP(rr, req)
        if rr.Code != http.StatusCreated {
            t.Fatalf("seed send failed: %d %s", rr.Code, rr.Body.String())
        }
        env := decodeEnvelope(t, rr)
        var mr domain.MessageResponse
        dataBytes, _ := json.Marshal(env.Data)
        _ = json.Unmarshal(dataBytes, &mr)

        // Alice tries to reply to Carol's message using parent_message_id
        body2 := map[string]any{"recipient_id": bob.ID, "content": "alice replies", "parent_message_id": mr.Message.ID}
        b2, _ := json.Marshal(body2)
        req2 := httptest.NewRequest(http.MethodPost, "/messages", bytes.NewReader(b2))
        req2.Header.Set("Content-Type", "application/json")
        req2 = req2.WithContext(context.WithValue(req2.Context(), middleware.UserIDKey, alice.ID))
        rr2 := httptest.NewRecorder()
        SendMessageHandler(svc).ServeHTTP(rr2, req2)
        // Parent not in Alice's conversation should be treated as not found
        assert.Equal(t, http.StatusNotFound, rr2.Code)
        env2 := decodeEnvelope(t, rr2)
        require.NotNil(t, env2.Error)
        assert.Contains(t, env2.Error.Code, "SEND_MESSAGE_FAILED")
    }
}
