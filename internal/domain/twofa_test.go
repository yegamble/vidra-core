package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestTwoFADomainErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"ErrTwoFAAlreadyEnabled", ErrTwoFAAlreadyEnabled, "two-factor authentication is already enabled"},
		{"ErrTwoFANotEnabled", ErrTwoFANotEnabled, "two-factor authentication is not enabled"},
		{"ErrTwoFAInvalidCode", ErrTwoFAInvalidCode, "invalid two-factor authentication code"},
		{"ErrTwoFASetupIncomplete", ErrTwoFASetupIncomplete, "two-factor authentication setup is incomplete"},
		{"ErrTwoFABackupCodeUsed", ErrTwoFABackupCodeUsed, "backup code has already been used"},
		{"ErrTwoFARequired", ErrTwoFARequired, "two-factor authentication code required"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotNil(t, tt.err)
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

func TestTwoFAErrorsAreDistinct(t *testing.T) {
	errors := []error{
		ErrTwoFAAlreadyEnabled,
		ErrTwoFANotEnabled,
		ErrTwoFAInvalidCode,
		ErrTwoFASetupIncomplete,
		ErrTwoFABackupCodeUsed,
		ErrTwoFARequired,
	}

	for i := 0; i < len(errors); i++ {
		for j := i + 1; j < len(errors); j++ {
			assert.NotEqual(t, errors[i].Error(), errors[j].Error(),
				"Errors at index %d and %d should have different messages", i, j)
		}
	}
}

func TestTwoFABackupCodeJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	backupCode := TwoFABackupCode{
		ID:        "backup-123",
		UserID:    "user-456",
		CodeHash:  "bcrypt-hash-value",
		CreatedAt: now,
	}

	data, err := json.Marshal(backupCode)
	assert.NoError(t, err)

	// Verify CodeHash is NOT in JSON output (json:"-" tag)
	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	assert.NoError(t, err)

	assert.Nil(t, raw["code_hash"], "CodeHash should not appear in JSON due to json:\"-\" tag")
	assert.Equal(t, "backup-123", raw["id"])
	assert.Equal(t, "user-456", raw["user_id"])
}

func TestTwoFASetupResponseJSON(t *testing.T) {
	resp := TwoFASetupResponse{
		Secret:      "JBSWY3DPEHPK3PXP",
		QRCodeURI:   "otpauth://totp/Athena:user@example.com?secret=JBSWY3DPEHPK3PXP&issuer=Athena",
		BackupCodes: []string{"code1", "code2", "code3", "code4", "code5"},
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded TwoFASetupResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, resp.Secret, decoded.Secret)
	assert.Equal(t, resp.QRCodeURI, decoded.QRCodeURI)
	assert.Len(t, decoded.BackupCodes, 5)
	assert.Equal(t, "code1", decoded.BackupCodes[0])
}

func TestTwoFAVerifySetupRequestJSON(t *testing.T) {
	req := TwoFAVerifySetupRequest{
		Code: "123456",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded TwoFAVerifySetupRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "123456", decoded.Code)
}

func TestTwoFAVerifySetupResponseJSON(t *testing.T) {
	resp := TwoFAVerifySetupResponse{
		Message: "Two-factor authentication enabled successfully",
		Enabled: true,
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded TwoFAVerifySetupResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, resp.Message, decoded.Message)
	assert.True(t, decoded.Enabled)
}

func TestTwoFADisableRequestJSON(t *testing.T) {
	req := TwoFADisableRequest{
		Password: "mypassword123",
		Code:     "654321",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded TwoFADisableRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "mypassword123", decoded.Password)
	assert.Equal(t, "654321", decoded.Code)
}

func TestTwoFADisableResponseJSON(t *testing.T) {
	resp := TwoFADisableResponse{
		Message: "Two-factor authentication disabled",
		Enabled: false,
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded TwoFADisableResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, resp.Message, decoded.Message)
	assert.False(t, decoded.Enabled)
}

func TestTwoFAStatusResponseJSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)

	tests := []struct {
		name string
		resp TwoFAStatusResponse
	}{
		{
			name: "enabled with confirmed_at",
			resp: TwoFAStatusResponse{
				Enabled:     true,
				ConfirmedAt: &now,
			},
		},
		{
			name: "disabled without confirmed_at",
			resp: TwoFAStatusResponse{
				Enabled:     false,
				ConfirmedAt: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.resp)
			assert.NoError(t, err)

			var decoded TwoFAStatusResponse
			err = json.Unmarshal(data, &decoded)
			assert.NoError(t, err)

			assert.Equal(t, tt.resp.Enabled, decoded.Enabled)
			if tt.resp.ConfirmedAt != nil {
				assert.NotNil(t, decoded.ConfirmedAt)
			} else {
				assert.Nil(t, decoded.ConfirmedAt)
			}
		})
	}
}

func TestTwoFARegenerateBackupCodesRequestJSON(t *testing.T) {
	req := TwoFARegenerateBackupCodesRequest{
		Code: "789012",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded TwoFARegenerateBackupCodesRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, "789012", decoded.Code)
}

func TestTwoFARegenerateBackupCodesResponseJSON(t *testing.T) {
	resp := TwoFARegenerateBackupCodesResponse{
		BackupCodes: []string{"new-code-1", "new-code-2", "new-code-3"},
	}

	data, err := json.Marshal(resp)
	assert.NoError(t, err)

	var decoded TwoFARegenerateBackupCodesResponse
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Len(t, decoded.BackupCodes, 3)
	assert.Equal(t, "new-code-1", decoded.BackupCodes[0])
}
