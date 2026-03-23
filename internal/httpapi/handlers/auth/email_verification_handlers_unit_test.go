package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockEmailVerificationService struct {
	verifyEmailWithTokenFunc    func(ctx context.Context, token string) error
	verifyEmailWithCodeFunc     func(ctx context.Context, code, userID string) error
	resendVerificationEmailFunc func(ctx context.Context, email string) error
}

func (m *mockEmailVerificationService) VerifyEmailWithToken(ctx context.Context, token string) error {
	if m.verifyEmailWithTokenFunc != nil {
		return m.verifyEmailWithTokenFunc(ctx, token)
	}
	return nil
}

func (m *mockEmailVerificationService) VerifyEmailWithCode(ctx context.Context, code, userID string) error {
	if m.verifyEmailWithCodeFunc != nil {
		return m.verifyEmailWithCodeFunc(ctx, code, userID)
	}
	return nil
}

func (m *mockEmailVerificationService) ResendVerificationEmail(ctx context.Context, email string) error {
	if m.resendVerificationEmailFunc != nil {
		return m.resendVerificationEmailFunc(ctx, email)
	}
	return nil
}

func TestVerifyEmail_WithToken_Success(t *testing.T) {
	mockService := &mockEmailVerificationService{
		verifyEmailWithTokenFunc: func(ctx context.Context, token string) error {
			assert.Equal(t, "valid-token-123", token)
			return nil
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"token": "valid-token-123"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	data, ok := response["data"].(map[string]interface{})
	require.True(t, ok, "response should have data field")
	assert.Equal(t, "Email verified successfully", data["message"])
	assert.Equal(t, true, data["success"])
}

func TestVerifyEmail_WithToken_InvalidToken(t *testing.T) {
	mockService := &mockEmailVerificationService{
		verifyEmailWithTokenFunc: func(ctx context.Context, token string) error {
			return domain.ErrInvalidVerificationToken
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"token": "invalid-token"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerifyEmail_WithToken_ExpiredToken(t *testing.T) {
	mockService := &mockEmailVerificationService{
		verifyEmailWithTokenFunc: func(ctx context.Context, token string) error {
			return domain.ErrVerificationTokenExpired
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"token": "expired-token"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerifyEmail_WithToken_ServiceError(t *testing.T) {
	mockService := &mockEmailVerificationService{
		verifyEmailWithTokenFunc: func(ctx context.Context, token string) error {
			return assert.AnError
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"token": "some-token"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVerifyEmail_WithCode_Success(t *testing.T) {
	mockService := &mockEmailVerificationService{
		verifyEmailWithCodeFunc: func(ctx context.Context, code, userID string) error {
			assert.Equal(t, "123456", code)
			assert.Equal(t, "user-123", userID)
			return nil
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"code": "123456"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	data, ok := response["data"].(map[string]interface{})
	require.True(t, ok, "response should have data field")
	assert.Equal(t, true, data["success"])
}

func TestVerifyEmail_WithCode_Unauthorized(t *testing.T) {
	mockService := &mockEmailVerificationService{}
	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"code": "123456"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVerifyEmail_WithCode_InvalidCode(t *testing.T) {
	mockService := &mockEmailVerificationService{
		verifyEmailWithCodeFunc: func(ctx context.Context, code, userID string) error {
			return domain.ErrInvalidVerificationCode
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"code": "wrong-code"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerifyEmail_WithCode_ExpiredCode(t *testing.T) {
	mockService := &mockEmailVerificationService{
		verifyEmailWithCodeFunc: func(ctx context.Context, code, userID string) error {
			return domain.ErrVerificationTokenExpired
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"code": "expired-code"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerifyEmail_WithCode_ServiceError(t *testing.T) {
	mockService := &mockEmailVerificationService{
		verifyEmailWithCodeFunc: func(ctx context.Context, code, userID string) error {
			return assert.AnError
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"code": "some-code"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestVerifyEmail_InvalidJSON(t *testing.T) {
	mockService := &mockEmailVerificationService{}
	handler := NewEmailVerificationHandlers(mockService)

	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader([]byte("invalid-json")))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestVerifyEmail_MissingCredentials(t *testing.T) {
	mockService := &mockEmailVerificationService{}
	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/verify", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.VerifyEmail(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestResendVerification_Success(t *testing.T) {
	mockService := &mockEmailVerificationService{
		resendVerificationEmailFunc: func(ctx context.Context, email string) error {
			assert.Equal(t, "user@example.com", email)
			return nil
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"email": "user@example.com"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/resend", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ResendVerification(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	data, ok := response["data"].(map[string]interface{})
	require.True(t, ok, "response should have data field")
	assert.Equal(t, "Verification email sent successfully", data["message"])
}

func TestResendVerification_AlreadyVerified(t *testing.T) {
	mockService := &mockEmailVerificationService{
		resendVerificationEmailFunc: func(ctx context.Context, email string) error {
			return domain.ErrEmailAlreadyVerified
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"email": "verified@example.com"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/resend", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ResendVerification(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestResendVerification_UserNotFound(t *testing.T) {
	mockService := &mockEmailVerificationService{
		resendVerificationEmailFunc: func(ctx context.Context, email string) error {
			return domain.ErrUserNotFound
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"email": "nonexistent@example.com"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/resend", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ResendVerification(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	data, ok := response["data"].(map[string]interface{})
	require.True(t, ok, "response should have data field")
	assert.Equal(t, "If the email exists, a verification email has been sent", data["message"])
}

func TestResendVerification_ServiceError(t *testing.T) {
	mockService := &mockEmailVerificationService{
		resendVerificationEmailFunc: func(ctx context.Context, email string) error {
			return assert.AnError
		},
	}

	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"email": "user@example.com"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/resend", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ResendVerification(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestResendVerification_InvalidJSON(t *testing.T) {
	mockService := &mockEmailVerificationService{}
	handler := NewEmailVerificationHandlers(mockService)

	req := httptest.NewRequest(http.MethodPost, "/resend", bytes.NewReader([]byte("invalid")))
	rec := httptest.NewRecorder()

	handler.ResendVerification(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestResendVerification_MissingEmail(t *testing.T) {
	mockService := &mockEmailVerificationService{}
	handler := NewEmailVerificationHandlers(mockService)

	reqBody := map[string]string{"email": ""}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/resend", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	handler.ResendVerification(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetVerificationStatus_Success(t *testing.T) {
	mockService := &mockEmailVerificationService{}
	handler := NewEmailVerificationHandlers(mockService)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-123"))
	rec := httptest.NewRecorder()

	handler.GetVerificationStatus(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	var response map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	data, ok := response["data"].(map[string]interface{})
	require.True(t, ok, "response should have data field")
	assert.Contains(t, data, "email_verified")
}

func TestGetVerificationStatus_Unauthorized(t *testing.T) {
	mockService := &mockEmailVerificationService{}
	handler := NewEmailVerificationHandlers(mockService)

	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	rec := httptest.NewRecorder()

	handler.GetVerificationStatus(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}
