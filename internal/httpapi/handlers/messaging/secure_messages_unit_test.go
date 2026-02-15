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
)

type mockE2EEService struct {
	setupE2EEFn              func(ctx context.Context, userID string, password string, clientIP string, userAgent string) error
	unlockE2EEFn             func(ctx context.Context, userID string, password string, clientIP string, userAgent string) error
	lockE2EEFn               func(ctx context.Context, userID string)
	getE2EEStatusFn          func(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error)
	initiateKeyExchangeFn    func(ctx context.Context, senderID string, recipientID string, clientIP string, userAgent string) (*domain.KeyExchangeMessage, error)
	acceptKeyExchangeFn      func(ctx context.Context, keyExchangeID string, userID string, clientIP string, userAgent string) error
	getPendingKeyExchangesFn func(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error)
	encryptMessageFn         func(ctx context.Context, senderID string, recipientID string, encryptedContent string, clientIP string, userAgent string) (*domain.Message, error)
	saveSecureMessageFn      func(ctx context.Context, message *domain.Message) error
	getMessageFn             func(ctx context.Context, messageID string) (*domain.Message, error)
	decryptMessageFn         func(ctx context.Context, message *domain.Message, userID string, clientIP string, userAgent string) (string, error)
}

func (m *mockE2EEService) SetupE2EE(ctx context.Context, userID string, password string, clientIP string, userAgent string) error {
	if m.setupE2EEFn != nil {
		return m.setupE2EEFn(ctx, userID, password, clientIP, userAgent)
	}
	return nil
}

func (m *mockE2EEService) UnlockE2EE(ctx context.Context, userID string, password string, clientIP string, userAgent string) error {
	if m.unlockE2EEFn != nil {
		return m.unlockE2EEFn(ctx, userID, password, clientIP, userAgent)
	}
	return nil
}

func (m *mockE2EEService) LockE2EE(ctx context.Context, userID string) {
	if m.lockE2EEFn != nil {
		m.lockE2EEFn(ctx, userID)
	}
}

func (m *mockE2EEService) GetE2EEStatus(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error) {
	if m.getE2EEStatusFn != nil {
		return m.getE2EEStatusFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockE2EEService) InitiateKeyExchange(ctx context.Context, senderID string, recipientID string, clientIP string, userAgent string) (*domain.KeyExchangeMessage, error) {
	if m.initiateKeyExchangeFn != nil {
		return m.initiateKeyExchangeFn(ctx, senderID, recipientID, clientIP, userAgent)
	}
	return nil, nil
}

func (m *mockE2EEService) AcceptKeyExchange(ctx context.Context, keyExchangeID string, userID string, clientIP string, userAgent string) error {
	if m.acceptKeyExchangeFn != nil {
		return m.acceptKeyExchangeFn(ctx, keyExchangeID, userID, clientIP, userAgent)
	}
	return nil
}

func (m *mockE2EEService) GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error) {
	if m.getPendingKeyExchangesFn != nil {
		return m.getPendingKeyExchangesFn(ctx, userID)
	}
	return nil, nil
}

func (m *mockE2EEService) EncryptMessage(ctx context.Context, senderID string, recipientID string, encryptedContent string, clientIP string, userAgent string) (*domain.Message, error) {
	if m.encryptMessageFn != nil {
		return m.encryptMessageFn(ctx, senderID, recipientID, encryptedContent, clientIP, userAgent)
	}
	return nil, nil
}

func (m *mockE2EEService) SaveSecureMessage(ctx context.Context, message *domain.Message) error {
	if m.saveSecureMessageFn != nil {
		return m.saveSecureMessageFn(ctx, message)
	}
	return nil
}

func (m *mockE2EEService) GetMessage(ctx context.Context, messageID string) (*domain.Message, error) {
	if m.getMessageFn != nil {
		return m.getMessageFn(ctx, messageID)
	}
	return nil, nil
}

func (m *mockE2EEService) DecryptMessage(ctx context.Context, message *domain.Message, userID string, clientIP string, userAgent string) (string, error) {
	if m.decryptMessageFn != nil {
		return m.decryptMessageFn(ctx, message, userID, clientIP, userAgent)
	}
	return "", nil
}

func TestSetupE2EE_Success(t *testing.T) {
	mockService := &mockE2EEService{
		setupE2EEFn: func(ctx context.Context, userID string, password string, clientIP string, userAgent string) error {
			assert.Equal(t, "test-password", password)
			return nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := `{"password":"test-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/setup", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.SetupE2EE(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestSetupE2EE_InvalidJSON(t *testing.T) {
	handler := NewSecureMessagesHandler(&mockE2EEService{}, validator.New())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/setup", bytes.NewBufferString("invalid json"))
	rec := httptest.NewRecorder()

	handler.SetupE2EE(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSetupE2EE_AlreadySetup(t *testing.T) {
	mockService := &mockE2EEService{
		setupE2EEFn: func(ctx context.Context, userID string, password string, clientIP string, userAgent string) error {
			return errors.New("user already has E2EE setup")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := `{"password":"test-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/setup", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.SetupE2EE(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestUnlockE2EE_Success(t *testing.T) {
	mockService := &mockE2EEService{
		unlockE2EEFn: func(ctx context.Context, userID string, password string, clientIP string, userAgent string) error {
			return nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := `{"password":"test-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/unlock", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.UnlockE2EE(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestUnlockE2EE_InvalidPassword(t *testing.T) {
	mockService := &mockE2EEService{
		unlockE2EEFn: func(ctx context.Context, userID string, password string, clientIP string, userAgent string) error {
			return errors.New("invalid password")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := `{"password":"wrong-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/unlock", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.UnlockE2EE(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestLockE2EE_Success(t *testing.T) {
	mockService := &mockE2EEService{
		lockE2EEFn: func(ctx context.Context, userID string) {
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/lock", nil)
	rec := httptest.NewRecorder()

	handler.LockE2EE(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetE2EEStatus_Success(t *testing.T) {
	mockService := &mockE2EEService{
		getE2EEStatusFn: func(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error) {
			return &domain.E2EEStatusResponse{
				HasMasterKey: true,
				IsUnlocked:   true,
			}, nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/e2ee/status", nil)
	rec := httptest.NewRecorder()

	handler.GetE2EEStatus(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestInitiateKeyExchange_Success(t *testing.T) {
	senderID := uuid.New().String()
	recipientID := uuid.New().String()

	mockService := &mockE2EEService{
		initiateKeyExchangeFn: func(ctx context.Context, sid string, rid string, clientIP string, userAgent string) (*domain.KeyExchangeMessage, error) {
			assert.Equal(t, senderID, sid)
			assert.Equal(t, recipientID, rid)
			return &domain.KeyExchangeMessage{
				ID:           "exchange-id",
				SenderID:     sid,
				RecipientID:  rid,
				ExchangeType: "initiate",
			}, nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := fmt.Sprintf(`{"recipient_id":"%s","public_key":"key","signature":"sig"}`, recipientID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/key-exchange/initiate", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, senderID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.InitiateKeyExchange(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestInitiateKeyExchange_SelfMessage(t *testing.T) {
	handler := NewSecureMessagesHandler(&mockE2EEService{}, validator.New())

	body := `{"recipient_id":"user-id-placeholder","public_key":"key","signature":"sig"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/key-exchange/initiate", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.InitiateKeyExchange(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAcceptKeyExchange_Success(t *testing.T) {
	userID := uuid.New().String()
	keyExchangeID := uuid.New().String()

	mockService := &mockE2EEService{
		acceptKeyExchangeFn: func(ctx context.Context, keid string, uid string, clientIP string, userAgent string) error {
			assert.Equal(t, keyExchangeID, keid)
			assert.Equal(t, userID, uid)
			return nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := fmt.Sprintf(`{"key_exchange_id":"%s","public_key":"key","signature":"sig"}`, keyExchangeID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/key-exchange/accept", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.AcceptKeyExchange(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetPendingKeyExchanges_Success(t *testing.T) {
	userID := uuid.New().String()

	mockService := &mockE2EEService{
		getPendingKeyExchangesFn: func(ctx context.Context, uid string) ([]*domain.KeyExchangeMessage, error) {
			assert.Equal(t, userID, uid)
			return []*domain.KeyExchangeMessage{
				{ID: "exchange-1"},
				{ID: "exchange-2"},
			}, nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/e2ee/key-exchanges", nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.GetPendingKeyExchanges(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, float64(2), response["total"])
}

func TestSendSecureMessage_Success(t *testing.T) {
	senderID := uuid.New().String()
	recipientID := uuid.New().String()

	mockService := &mockE2EEService{
		encryptMessageFn: func(ctx context.Context, sid string, rid string, encryptedContent string, clientIP string, userAgent string) (*domain.Message, error) {
			assert.Equal(t, senderID, sid)
			assert.Equal(t, recipientID, rid)
			return &domain.Message{
				ID:          "msg-123",
				SenderID:    sid,
				RecipientID: rid,
				Content:     encryptedContent,
			}, nil
		},
		saveSecureMessageFn: func(ctx context.Context, message *domain.Message) error {
			return nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := fmt.Sprintf(`{"recipient_id":"%s","encrypted_content":"encrypted","pgp_signature":"sig"}`, recipientID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/secure", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, senderID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.SendSecureMessage(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestDecryptMessage_Success(t *testing.T) {
	recipientID := uuid.New().String()

	mockService := &mockE2EEService{
		getMessageFn: func(ctx context.Context, messageID string) (*domain.Message, error) {
			return &domain.Message{
				ID:          messageID,
				SenderID:    uuid.New().String(),
				RecipientID: recipientID,
				IsEncrypted: true,
			}, nil
		},
		decryptMessageFn: func(ctx context.Context, message *domain.Message, userID string, clientIP string, userAgent string) (string, error) {
			assert.Equal(t, recipientID, userID)
			return "decrypted content", nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/msg-123/decrypt", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("messageId", "msg-123")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, recipientID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()

	handler.DecryptMessage(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestDecryptMessage_NotEncrypted(t *testing.T) {
	recipientID := uuid.New().String()

	mockService := &mockE2EEService{
		getMessageFn: func(ctx context.Context, messageID string) (*domain.Message, error) {
			return &domain.Message{
				ID:          messageID,
				SenderID:    uuid.New().String(),
				RecipientID: recipientID,
				IsEncrypted: false,
			}, nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/msg-123/decrypt", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("messageId", "msg-123")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, recipientID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()

	handler.DecryptMessage(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAcceptKeyExchange_InvalidJSON(t *testing.T) {
	handler := NewSecureMessagesHandler(&mockE2EEService{}, validator.New())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/key-exchange/accept", bytes.NewBufferString("invalid json"))
	rec := httptest.NewRecorder()

	handler.AcceptKeyExchange(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAcceptKeyExchange_MissingKeyExchangeID(t *testing.T) {
	userID := uuid.New().String()
	handler := NewSecureMessagesHandler(&mockE2EEService{}, validator.New())

	body := `{"public_key":"key","signature":"sig"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/key-exchange/accept", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.AcceptKeyExchange(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAcceptKeyExchange_ServiceError(t *testing.T) {
	userID := uuid.New().String()
	keyExchangeID := uuid.New().String()

	mockService := &mockE2EEService{
		acceptKeyExchangeFn: func(ctx context.Context, keid string, uid string, clientIP string, userAgent string) error {
			return errors.New("key exchange not found")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := fmt.Sprintf(`{"key_exchange_id":"%s","public_key":"key","signature":"sig"}`, keyExchangeID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/key-exchange/accept", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.AcceptKeyExchange(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSendSecureMessage_InvalidJSON(t *testing.T) {
	handler := NewSecureMessagesHandler(&mockE2EEService{}, validator.New())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/secure", bytes.NewBufferString("invalid json"))
	rec := httptest.NewRecorder()

	handler.SendSecureMessage(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSendSecureMessage_MissingRecipient(t *testing.T) {
	senderID := uuid.New().String()
	handler := NewSecureMessagesHandler(&mockE2EEService{}, validator.New())

	body := `{"encrypted_content":"encrypted","pgp_signature":"sig"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/secure", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, senderID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.SendSecureMessage(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestSendSecureMessage_EncryptionError(t *testing.T) {
	senderID := uuid.New().String()
	recipientID := uuid.New().String()

	mockService := &mockE2EEService{
		encryptMessageFn: func(ctx context.Context, sid string, rid string, encryptedContent string, clientIP string, userAgent string) (*domain.Message, error) {
			return nil, errors.New("encryption key not found")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := fmt.Sprintf(`{"recipient_id":"%s","encrypted_content":"encrypted","pgp_signature":"sig"}`, recipientID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/secure", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, senderID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.SendSecureMessage(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestSendSecureMessage_SaveError(t *testing.T) {
	senderID := uuid.New().String()
	recipientID := uuid.New().String()

	mockService := &mockE2EEService{
		encryptMessageFn: func(ctx context.Context, sid string, rid string, encryptedContent string, clientIP string, userAgent string) (*domain.Message, error) {
			return &domain.Message{
				ID:          "msg-123",
				SenderID:    sid,
				RecipientID: rid,
				Content:     encryptedContent,
			}, nil
		},
		saveSecureMessageFn: func(ctx context.Context, message *domain.Message) error {
			return errors.New("database error")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := fmt.Sprintf(`{"recipient_id":"%s","encrypted_content":"encrypted","pgp_signature":"sig"}`, recipientID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/messages/secure", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, senderID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.SendSecureMessage(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestDecryptMessage_MessageNotFound(t *testing.T) {
	recipientID := uuid.New().String()

	mockService := &mockE2EEService{
		getMessageFn: func(ctx context.Context, messageID string) (*domain.Message, error) {
			return nil, domain.ErrNotFound
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/msg-123/decrypt", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("messageId", "msg-123")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, recipientID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()

	handler.DecryptMessage(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDecryptMessage_Unauthorized(t *testing.T) {
	recipientID := uuid.New().String()
	wrongUserID := uuid.New().String()

	mockService := &mockE2EEService{
		getMessageFn: func(ctx context.Context, messageID string) (*domain.Message, error) {
			return &domain.Message{
				ID:          messageID,
				SenderID:    uuid.New().String(),
				RecipientID: recipientID,
				IsEncrypted: true,
			}, nil
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/msg-123/decrypt", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("messageId", "msg-123")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, wrongUserID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()

	handler.DecryptMessage(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDecryptMessage_DecryptionError(t *testing.T) {
	recipientID := uuid.New().String()

	mockService := &mockE2EEService{
		getMessageFn: func(ctx context.Context, messageID string) (*domain.Message, error) {
			return &domain.Message{
				ID:          messageID,
				SenderID:    uuid.New().String(),
				RecipientID: recipientID,
				IsEncrypted: true,
			}, nil
		},
		decryptMessageFn: func(ctx context.Context, message *domain.Message, userID string, clientIP string, userAgent string) (string, error) {
			return "", errors.New("decryption failed")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/messages/msg-123/decrypt", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("messageId", "msg-123")
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, recipientID)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()

	handler.DecryptMessage(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestInitiateKeyExchange_InvalidJSON(t *testing.T) {
	handler := NewSecureMessagesHandler(&mockE2EEService{}, validator.New())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/key-exchange/initiate", bytes.NewBufferString("invalid json"))
	rec := httptest.NewRecorder()

	handler.InitiateKeyExchange(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestInitiateKeyExchange_MissingRecipient(t *testing.T) {
	senderID := uuid.New().String()
	handler := NewSecureMessagesHandler(&mockE2EEService{}, validator.New())

	body := `{"public_key":"key","signature":"sig"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/key-exchange/initiate", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, senderID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.InitiateKeyExchange(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestInitiateKeyExchange_ServiceError(t *testing.T) {
	senderID := uuid.New().String()
	recipientID := uuid.New().String()

	mockService := &mockE2EEService{
		initiateKeyExchangeFn: func(ctx context.Context, sid string, rid string, clientIP string, userAgent string) (*domain.KeyExchangeMessage, error) {
			return nil, errors.New("recipient not found")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := fmt.Sprintf(`{"recipient_id":"%s","public_key":"key","signature":"sig"}`, recipientID)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/key-exchange/initiate", bytes.NewBufferString(body))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, senderID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.InitiateKeyExchange(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnlockE2EE_InvalidJSON(t *testing.T) {
	handler := NewSecureMessagesHandler(&mockE2EEService{}, validator.New())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/unlock", bytes.NewBufferString("invalid json"))
	rec := httptest.NewRecorder()

	handler.UnlockE2EE(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnlockE2EE_ServiceError(t *testing.T) {
	mockService := &mockE2EEService{
		unlockE2EEFn: func(ctx context.Context, userID string, password string, clientIP string, userAgent string) error {
			return errors.New("database error")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	body := `{"password":"test-password"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/e2ee/unlock", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.UnlockE2EE(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetE2EEStatus_ServiceError(t *testing.T) {
	mockService := &mockE2EEService{
		getE2EEStatusFn: func(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/e2ee/status", nil)
	rec := httptest.NewRecorder()

	handler.GetE2EEStatus(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetPendingKeyExchanges_ServiceError(t *testing.T) {
	userID := uuid.New().String()

	mockService := &mockE2EEService{
		getPendingKeyExchangesFn: func(ctx context.Context, uid string) ([]*domain.KeyExchangeMessage, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewSecureMessagesHandler(mockService, validator.New())

	req := httptest.NewRequest(http.MethodGet, "/api/v1/e2ee/key-exchanges", nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.GetPendingKeyExchanges(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}
