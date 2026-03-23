package messaging

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

type mockE2EEService struct {
	registerIdentityKeyFn    func(ctx context.Context, userID, publicIdentityKey, publicSigningKey, clientIP, userAgent string) error
	getPublicKeysFn          func(ctx context.Context, userID string) (*domain.PublicKeyBundle, error)
	getE2EEStatusFn          func(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error)
	initiateKeyExchangeFn    func(ctx context.Context, senderID, recipientID, senderPublicKey, clientIP, userAgent string) (*domain.KeyExchangeMessage, error)
	acceptKeyExchangeFn      func(ctx context.Context, keyExchangeID, userID, recipientPublicKey, clientIP, userAgent string) error
	getPendingKeyExchangesFn func(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error)
	storeEncryptedMessageFn  func(ctx context.Context, senderID string, req *domain.StoreEncryptedMessageRequest, clientIP, userAgent string) (*domain.Message, error)
	getEncryptedMessagesFn   func(ctx context.Context, conversationID, userID string, limit, offset int) ([]*domain.Message, error)
}

func (m *mockE2EEService) RegisterIdentityKey(ctx context.Context, userID, publicIdentityKey, publicSigningKey, clientIP, userAgent string) error {
	return m.registerIdentityKeyFn(ctx, userID, publicIdentityKey, publicSigningKey, clientIP, userAgent)
}

func (m *mockE2EEService) GetPublicKeys(ctx context.Context, userID string) (*domain.PublicKeyBundle, error) {
	return m.getPublicKeysFn(ctx, userID)
}

func (m *mockE2EEService) GetE2EEStatus(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error) {
	return m.getE2EEStatusFn(ctx, userID)
}

func (m *mockE2EEService) InitiateKeyExchange(ctx context.Context, senderID, recipientID, senderPublicKey, clientIP, userAgent string) (*domain.KeyExchangeMessage, error) {
	return m.initiateKeyExchangeFn(ctx, senderID, recipientID, senderPublicKey, clientIP, userAgent)
}

func (m *mockE2EEService) AcceptKeyExchange(ctx context.Context, keyExchangeID, userID, recipientPublicKey, clientIP, userAgent string) error {
	return m.acceptKeyExchangeFn(ctx, keyExchangeID, userID, recipientPublicKey, clientIP, userAgent)
}

func (m *mockE2EEService) GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error) {
	return m.getPendingKeyExchangesFn(ctx, userID)
}

func (m *mockE2EEService) StoreEncryptedMessage(ctx context.Context, senderID string, req *domain.StoreEncryptedMessageRequest, clientIP, userAgent string) (*domain.Message, error) {
	return m.storeEncryptedMessageFn(ctx, senderID, req, clientIP, userAgent)
}

func (m *mockE2EEService) GetEncryptedMessages(ctx context.Context, conversationID, userID string, limit, offset int) ([]*domain.Message, error) {
	return m.getEncryptedMessagesFn(ctx, conversationID, userID, limit, offset)
}

func newTestHandler(svc *mockE2EEService) *SecureMessagesHandler {
	return NewSecureMessagesHandler(svc, validator.New())
}

func addAuthContext(r *http.Request, userID string) *http.Request {
	ctx := middleware.WithUserID(r.Context(), uuid.MustParse(userID))
	return r.WithContext(ctx)
}

func addChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestRegisterIdentityKey_Success(t *testing.T) {
	userID := uuid.New().String()
	svc := &mockE2EEService{
		registerIdentityKeyFn: func(_ context.Context, uid, ik, sk, _, _ string) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, "base64-x25519-pubkey", ik)
			assert.Equal(t, "base64-ed25519-pubkey", sk)
			return nil
		},
	}
	body, _ := json.Marshal(domain.RegisterIdentityKeyRequest{
		PublicIdentityKey: "base64-x25519-pubkey",
		PublicSigningKey:  "base64-ed25519-pubkey",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/keys", bytes.NewReader(body))
	req = addAuthContext(req, userID)
	rec := httptest.NewRecorder()

	newTestHandler(svc).RegisterIdentityKey(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestRegisterIdentityKey_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/e2ee/keys", bytes.NewReader([]byte("bad json")))
	req = addAuthContext(req, uuid.New().String())
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).RegisterIdentityKey(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestRegisterIdentityKey_Conflict(t *testing.T) {
	svc := &mockE2EEService{
		registerIdentityKeyFn: func(_ context.Context, _, _, _, _, _ string) error {
			return domain.ErrConflict
		},
	}
	body, _ := json.Marshal(domain.RegisterIdentityKeyRequest{
		PublicIdentityKey: "base64-x25519-pubkey",
		PublicSigningKey:  "base64-ed25519-pubkey",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/keys", bytes.NewReader(body))
	req = addAuthContext(req, uuid.New().String())
	rec := httptest.NewRecorder()

	newTestHandler(svc).RegisterIdentityKey(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestGetPublicKeys_Success(t *testing.T) {
	targetID := uuid.New().String()
	svc := &mockE2EEService{
		getPublicKeysFn: func(_ context.Context, uid string) (*domain.PublicKeyBundle, error) {
			assert.Equal(t, targetID, uid)
			return &domain.PublicKeyBundle{
				PublicIdentityKey: "x25519-key",
				PublicSigningKey:  "ed25519-key",
				KeyVersion:        1,
			}, nil
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/e2ee/keys/"+targetID, nil)
	req = addAuthContext(req, uuid.New().String())
	req = addChiParam(req, "userId", targetID)
	rec := httptest.NewRecorder()

	newTestHandler(svc).GetPublicKeys(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var resp domain.PublicKeyBundle
	_ = json.NewDecoder(rec.Body).Decode(&resp)
	assert.Equal(t, "x25519-key", resp.PublicIdentityKey)
}

func TestGetPublicKeys_NotFound(t *testing.T) {
	svc := &mockE2EEService{
		getPublicKeysFn: func(_ context.Context, _ string) (*domain.PublicKeyBundle, error) {
			return nil, domain.ErrNotFound
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/e2ee/keys/"+uuid.New().String(), nil)
	req = addAuthContext(req, uuid.New().String())
	req = addChiParam(req, "userId", uuid.New().String())
	rec := httptest.NewRecorder()

	newTestHandler(svc).GetPublicKeys(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetE2EEStatus_Success(t *testing.T) {
	userID := uuid.New().String()
	svc := &mockE2EEService{
		getE2EEStatusFn: func(_ context.Context, uid string) (*domain.E2EEStatusResponse, error) {
			assert.Equal(t, userID, uid)
			return &domain.E2EEStatusResponse{HasIdentityKey: true, KeyVersion: 2}, nil
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/e2ee/status", nil)
	req = addAuthContext(req, userID)
	rec := httptest.NewRecorder()

	newTestHandler(svc).GetE2EEStatus(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestInitiateKeyExchange_Success(t *testing.T) {
	senderID := uuid.New().String()
	recipientID := uuid.New().String()
	now := time.Now()

	svc := &mockE2EEService{
		initiateKeyExchangeFn: func(_ context.Context, sid, rid, pk, _, _ string) (*domain.KeyExchangeMessage, error) {
			assert.Equal(t, senderID, sid)
			assert.Equal(t, recipientID, rid)
			assert.Equal(t, "sender-pub-key", pk)
			return &domain.KeyExchangeMessage{
				ID:           uuid.New().String(),
				SenderID:     sid,
				RecipientID:  rid,
				ExchangeType: domain.KeyExchangeTypeOffer,
				PublicKey:    pk,
				CreatedAt:    now,
				ExpiresAt:    now.Add(time.Hour),
			}, nil
		},
	}
	body, _ := json.Marshal(domain.InitiateKeyExchangeRequest{
		RecipientID:     recipientID,
		SenderPublicKey: "sender-pub-key",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/exchange", bytes.NewReader(body))
	req = addAuthContext(req, senderID)
	rec := httptest.NewRecorder()

	newTestHandler(svc).InitiateKeyExchange(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestInitiateKeyExchange_SelfMessage(t *testing.T) {
	userID := uuid.New().String()
	body, _ := json.Marshal(domain.InitiateKeyExchangeRequest{
		RecipientID:     userID,
		SenderPublicKey: "some-key",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/exchange", bytes.NewReader(body))
	req = addAuthContext(req, userID)
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).InitiateKeyExchange(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestInitiateKeyExchange_Conflict(t *testing.T) {
	svc := &mockE2EEService{
		initiateKeyExchangeFn: func(_ context.Context, _, _, _, _, _ string) (*domain.KeyExchangeMessage, error) {
			return nil, domain.ErrConflict
		},
	}
	body, _ := json.Marshal(domain.InitiateKeyExchangeRequest{
		RecipientID:     uuid.New().String(),
		SenderPublicKey: "key",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/exchange", bytes.NewReader(body))
	req = addAuthContext(req, uuid.New().String())
	rec := httptest.NewRecorder()

	newTestHandler(svc).InitiateKeyExchange(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
}

func TestAcceptKeyExchange_Success(t *testing.T) {
	userID := uuid.New().String()
	kexID := uuid.New().String()

	svc := &mockE2EEService{
		acceptKeyExchangeFn: func(_ context.Context, kid, uid, pk, _, _ string) error {
			assert.Equal(t, kexID, kid)
			assert.Equal(t, userID, uid)
			assert.Equal(t, "recipient-pub-key", pk)
			return nil
		},
	}
	body, _ := json.Marshal(domain.AcceptKeyExchangeRequest{
		KeyExchangeID: kexID,
		PublicKey:     "recipient-pub-key",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/exchange/accept", bytes.NewReader(body))
	req = addAuthContext(req, userID)
	rec := httptest.NewRecorder()

	newTestHandler(svc).AcceptKeyExchange(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAcceptKeyExchange_Forbidden(t *testing.T) {
	svc := &mockE2EEService{
		acceptKeyExchangeFn: func(_ context.Context, _, _, _, _, _ string) error {
			return domain.ErrForbidden
		},
	}
	body, _ := json.Marshal(domain.AcceptKeyExchangeRequest{
		KeyExchangeID: uuid.New().String(),
		PublicKey:     "key",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/exchange/accept", bytes.NewReader(body))
	req = addAuthContext(req, uuid.New().String())
	rec := httptest.NewRecorder()

	newTestHandler(svc).AcceptKeyExchange(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestAcceptKeyExchange_InvalidJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/e2ee/exchange/accept", bytes.NewReader([]byte("bad")))
	req = addAuthContext(req, uuid.New().String())
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).AcceptKeyExchange(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestStoreEncryptedMessage_Success(t *testing.T) {
	senderID := uuid.New().String()
	recipientID := uuid.New().String()
	now := time.Now()

	svc := &mockE2EEService{
		storeEncryptedMessageFn: func(_ context.Context, sid string, req *domain.StoreEncryptedMessageRequest, _, _ string) (*domain.Message, error) {
			assert.Equal(t, senderID, sid)
			assert.Equal(t, "base64ciphertext", req.EncryptedContent)
			encrypted := "base64ciphertext"
			nonce := "nonce123"
			sig := "sig456"
			return &domain.Message{
				ID:               uuid.New().String(),
				SenderID:         sid,
				RecipientID:      req.RecipientID,
				MessageType:      domain.MessageTypeSecure,
				IsEncrypted:      true,
				EncryptedContent: &encrypted,
				ContentNonce:     &nonce,
				PGPSignature:     &sig,
				CreatedAt:        now,
				UpdatedAt:        now,
			}, nil
		},
	}
	body, _ := json.Marshal(domain.StoreEncryptedMessageRequest{
		RecipientID:      recipientID,
		EncryptedContent: "base64ciphertext",
		ContentNonce:     "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef",
		Signature:        "sig456",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/messages", bytes.NewReader(body))
	req = addAuthContext(req, senderID)
	rec := httptest.NewRecorder()

	newTestHandler(svc).StoreEncryptedMessage(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestStoreEncryptedMessage_SelfMessage(t *testing.T) {
	userID := uuid.New().String()
	body, _ := json.Marshal(domain.StoreEncryptedMessageRequest{
		RecipientID:      userID,
		EncryptedContent: "ct",
		ContentNonce:     "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef",
		Signature:        "s",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/messages", bytes.NewReader(body))
	req = addAuthContext(req, userID)
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).StoreEncryptedMessage(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestStoreEncryptedMessage_KeyExchangeNotComplete_412(t *testing.T) {
	senderID := uuid.New().String()
	recipientID := uuid.New().String()
	svc := &mockE2EEService{
		storeEncryptedMessageFn: func(_ context.Context, _ string, _ *domain.StoreEncryptedMessageRequest, _, _ string) (*domain.Message, error) {
			return nil, domain.ErrKeyExchangeNotComplete
		},
	}
	body, _ := json.Marshal(domain.StoreEncryptedMessageRequest{
		RecipientID: recipientID, EncryptedContent: "ct", ContentNonce: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef", Signature: "s",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/messages", bytes.NewReader(body))
	req = addAuthContext(req, senderID)
	rec := httptest.NewRecorder()
	newTestHandler(svc).StoreEncryptedMessage(rec, req)
	assert.Equal(t, http.StatusPreconditionFailed, rec.Code)
}

func TestGetEncryptedMessages_Success(t *testing.T) {
	userID := uuid.New().String()
	convID := uuid.New().String()

	svc := &mockE2EEService{
		getEncryptedMessagesFn: func(_ context.Context, cid, uid string, limit, offset int) ([]*domain.Message, error) {
			assert.Equal(t, convID, cid)
			assert.Equal(t, userID, uid)
			return []*domain.Message{}, nil
		},
	}
	req := httptest.NewRequest(http.MethodGet, "/e2ee/conversations/"+convID+"/messages", nil)
	req = addAuthContext(req, userID)
	req = addChiParam(req, "conversationId", convID)
	rec := httptest.NewRecorder()

	newTestHandler(svc).GetEncryptedMessages(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetEncryptedMessages_Forbidden(t *testing.T) {
	svc := &mockE2EEService{
		getEncryptedMessagesFn: func(_ context.Context, _, _ string, _, _ int) ([]*domain.Message, error) {
			return nil, domain.ErrForbidden
		},
	}
	convID := uuid.New().String()
	req := httptest.NewRequest(http.MethodGet, "/e2ee/conversations/"+convID+"/messages", nil)
	req = addAuthContext(req, uuid.New().String())
	req = addChiParam(req, "conversationId", convID)
	rec := httptest.NewRecorder()

	newTestHandler(svc).GetEncryptedMessages(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestGetClientIP_PublicRemoteAddr_IgnoresForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	req.Header.Set("X-Forwarded-For", "9.9.9.9")

	ip := GetClientIP(req)

	assert.Equal(t, "1.2.3.4", ip)
}

func TestGetClientIP_PrivateRemoteAddr_TrustsXForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "127.0.0.1:9999"
	req.Header.Set("X-Forwarded-For", "203.0.113.5, 10.0.0.1")

	ip := GetClientIP(req)

	assert.Equal(t, "203.0.113.5", ip)
}

func TestGetClientIP_PrivateRemoteAddr_TrustsXRealIP(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:8080"
	req.Header.Set("X-Real-IP", "198.51.100.42")

	ip := GetClientIP(req)

	assert.Equal(t, "198.51.100.42", ip)
}

func TestGetClientIP_PrivateRemoteAddr_NoForwardedHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "172.17.0.2:8080"

	ip := GetClientIP(req)

	assert.Equal(t, "172.17.0.2", ip)
}

func TestAcceptKeyExchange_Expired_410(t *testing.T) {
	svc := &mockE2EEService{
		acceptKeyExchangeFn: func(_ context.Context, _, _, _, _, _ string) error {
			return domain.ErrKeyExchangeExpired
		},
	}
	body, _ := json.Marshal(domain.AcceptKeyExchangeRequest{
		KeyExchangeID: uuid.New().String(),
		PublicKey:     "pub-key",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/key-exchange/accept", bytes.NewReader(body))
	req = addAuthContext(req, uuid.New().String())
	rec := httptest.NewRecorder()

	newTestHandler(svc).AcceptKeyExchange(rec, req)

	assert.Equal(t, http.StatusGone, rec.Code)
}

func TestRegisterIdentityKey_NoAuth_401(t *testing.T) {
	body, _ := json.Marshal(domain.RegisterIdentityKeyRequest{
		PublicIdentityKey: "base64-x25519-pubkey",
		PublicSigningKey:  "base64-ed25519-pubkey",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/keys", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).RegisterIdentityKey(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGetE2EEStatus_NoAuth_401(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/e2ee/status", nil)
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).GetE2EEStatus(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestInitiateKeyExchange_NoAuth_401(t *testing.T) {
	body, _ := json.Marshal(domain.InitiateKeyExchangeRequest{
		RecipientID:     uuid.New().String(),
		SenderPublicKey: "pub-key",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/key-exchange/initiate", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).InitiateKeyExchange(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestAcceptKeyExchange_NoAuth_401(t *testing.T) {
	body, _ := json.Marshal(domain.AcceptKeyExchangeRequest{
		KeyExchangeID: uuid.New().String(),
		PublicKey:     "pub-key",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/key-exchange/accept", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).AcceptKeyExchange(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGetPendingKeyExchanges_NoAuth_401(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/e2ee/key-exchange/pending", nil)
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).GetPendingKeyExchanges(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestStoreEncryptedMessage_NoAuth_401(t *testing.T) {
	body, _ := json.Marshal(domain.StoreEncryptedMessageRequest{
		RecipientID:      uuid.New().String(),
		EncryptedContent: "ciphertext",
		ContentNonce:     "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef",
		Signature:        "sig",
	})
	req := httptest.NewRequest(http.MethodPost, "/e2ee/messages", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).StoreEncryptedMessage(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGetEncryptedMessages_NoAuth_401(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/e2ee/messages/conv-123", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("conversationId", uuid.New().String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).GetEncryptedMessages(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGetPublicKeys_InvalidUUID_400(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/e2ee/keys/not-a-uuid", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("userId", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	rec := httptest.NewRecorder()

	newTestHandler(&mockE2EEService{}).GetPublicKeys(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	var resp map[string]interface{}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	errObj := resp["error"].(map[string]interface{})
	assert.Equal(t, "invalid_user_id", errObj["code"])
}
