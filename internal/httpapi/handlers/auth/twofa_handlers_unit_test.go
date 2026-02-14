package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockTwoFAService struct {
	generateSecretErr        error
	verifySetupErr           error
	disableErr               error
	regenerateBackupCodesErr error
	getStatusErr             error
	setup                    *domain.TwoFASetupResponse
}

func (m *mockTwoFAService) GenerateSecret(ctx context.Context, userID string) (*domain.TwoFASetupResponse, error) {
	if m.generateSecretErr != nil {
		return nil, m.generateSecretErr
	}
	if m.setup != nil {
		return m.setup, nil
	}
	return &domain.TwoFASetupResponse{
		Secret:      "test-secret",
		QRCodeURI:   "otpauth://totp/test@example.com?secret=test-secret",
		BackupCodes: []string{"code1", "code2", "code3"},
	}, nil
}

func (m *mockTwoFAService) VerifySetup(ctx context.Context, userID, code string) error {
	return m.verifySetupErr
}

func (m *mockTwoFAService) Disable(ctx context.Context, userID, password, code string) error {
	return m.disableErr
}

func (m *mockTwoFAService) RegenerateBackupCodes(ctx context.Context, userID, code string) ([]string, error) {
	if m.regenerateBackupCodesErr != nil {
		return nil, m.regenerateBackupCodesErr
	}
	return []string{"newcode1", "newcode2", "newcode3"}, nil
}

func (m *mockTwoFAService) GetStatus(ctx context.Context, userID string) (*domain.TwoFAStatusResponse, error) {
	if m.getStatusErr != nil {
		return nil, m.getStatusErr
	}
	return &domain.TwoFAStatusResponse{Enabled: false}, nil
}

func TestSetupTwoFA_Success(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.SetupTwoFA(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper struct {
		Data    domain.TwoFASetupResponse `json:"data"`
		Success bool                      `json:"success"`
	}
	err := json.Unmarshal(rec.Body.Bytes(), &wrapper)
	require.NoError(t, err)

	assert.True(t, wrapper.Success)
	assert.NotEmpty(t, wrapper.Data.Secret)
	assert.NotEmpty(t, wrapper.Data.QRCodeURI)
	assert.Len(t, wrapper.Data.BackupCodes, 3)
}

func TestSetupTwoFA_Unauthorized(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	rec := httptest.NewRecorder()

	h.SetupTwoFA(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "UNAUTHORIZED")
}

func TestSetupTwoFA_AlreadyEnabled(t *testing.T) {
	service := &mockTwoFAService{
		generateSecretErr: domain.ErrTwoFAAlreadyEnabled,
	}
	h := &TwoFAHandlers{twoFAService: service}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.SetupTwoFA(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "TWOFA_ALREADY_ENABLED")
}

func TestSetupTwoFA_ServiceError(t *testing.T) {
	service := &mockTwoFAService{
		generateSecretErr: errors.New("database error"),
	}
	h := &TwoFAHandlers{twoFAService: service}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/setup", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.SetupTwoFA(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "INTERNAL_ERROR")
}

func TestVerifyTwoFASetup_Success(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFAVerifySetupRequest{Code: "123456"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.VerifyTwoFASetup(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestVerifyTwoFASetup_Unauthorized(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFAVerifySetupRequest{Code: "123456"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", bytes.NewReader(body))
	rec := httptest.NewRecorder()

	h.VerifyTwoFASetup(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestVerifyTwoFASetup_InvalidJSON(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", bytes.NewReader([]byte("invalid json")))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.VerifyTwoFASetup(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "INVALID_JSON")
}

func TestVerifyTwoFASetup_MissingCode(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFAVerifySetupRequest{Code: ""}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.VerifyTwoFASetup(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "MISSING_CODE")
}

func TestVerifyTwoFASetup_InvalidCode(t *testing.T) {
	service := &mockTwoFAService{
		verifySetupErr: domain.ErrTwoFAInvalidCode,
	}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFAVerifySetupRequest{Code: "wrong-code"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.VerifyTwoFASetup(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "INVALID_CODE")
}

func TestVerifyTwoFASetup_AlreadyEnabled(t *testing.T) {
	service := &mockTwoFAService{
		verifySetupErr: domain.ErrTwoFAAlreadyEnabled,
	}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFAVerifySetupRequest{Code: "123456"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.VerifyTwoFASetup(rec, req)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "TWOFA_ALREADY_ENABLED")
}

func TestVerifyTwoFASetup_SetupIncomplete(t *testing.T) {
	service := &mockTwoFAService{
		verifySetupErr: domain.ErrTwoFASetupIncomplete,
	}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFAVerifySetupRequest{Code: "123456"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/verify-setup", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.VerifyTwoFASetup(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "TWOFA_SETUP_INCOMPLETE")
}

func TestDisableTwoFA_Success(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFADisableRequest{Password: "password123", Code: "123456"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/disable", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.DisableTwoFA(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestDisableTwoFA_Unauthorized(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/disable", nil)
	rec := httptest.NewRecorder()

	h.DisableTwoFA(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDisableTwoFA_NotEnabled(t *testing.T) {
	service := &mockTwoFAService{
		disableErr: domain.ErrTwoFANotEnabled,
	}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFADisableRequest{Password: "password123", Code: "123456"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/disable", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.DisableTwoFA(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "TWOFA_NOT_ENABLED")
}

func TestGenerateBackupCodes_Success(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/backup-codes", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.RegenerateBackupCodes(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var response domain.TwoFARegenerateBackupCodesResponse
	err := json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Len(t, response.BackupCodes, 3)
}

func TestRegenerateBackupCodes_Unauthorized(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/backup-codes", nil)
	rec := httptest.NewRecorder()

	h.RegenerateBackupCodes(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestRegenerateBackupCodes_NotEnabled(t *testing.T) {
	service := &mockTwoFAService{
		regenerateBackupCodesErr: domain.ErrTwoFANotEnabled,
	}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFARegenerateBackupCodesRequest{Code: "123456"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/backup-codes", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.RegenerateBackupCodes(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "TWOFA_NOT_ENABLED")
}

func TestRegenerateBackupCodes_ServiceError(t *testing.T) {
	service := &mockTwoFAService{
		regenerateBackupCodesErr: errors.New("database error"),
	}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFARegenerateBackupCodesRequest{Code: "123456"}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/backup-codes", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.RegenerateBackupCodes(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestRegenerateBackupCodes_MissingCode(t *testing.T) {
	service := &mockTwoFAService{}
	h := &TwoFAHandlers{twoFAService: service}

	reqBody := domain.TwoFARegenerateBackupCodesRequest{Code: ""}
	body, _ := json.Marshal(reqBody)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/2fa/backup-codes", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "test-user-id"))
	rec := httptest.NewRecorder()

	h.RegenerateBackupCodes(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "MISSING_CODE")
}
