package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEmailVerificationErrors(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected string
	}{
		{"ErrEmailAlreadyVerified", ErrEmailAlreadyVerified, "email already verified"},
		{"ErrVerificationTokenExpired", ErrVerificationTokenExpired, "verification token expired"},
		{"ErrInvalidVerificationToken", ErrInvalidVerificationToken, "invalid verification token"},
		{"ErrInvalidVerificationCode", ErrInvalidVerificationCode, "invalid verification code"},
		{"ErrEmailNotVerified", ErrEmailNotVerified, "email not verified"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.NotNil(t, tt.err)
			assert.Equal(t, tt.expected, tt.err.Error())
		})
	}
}

func TestEmailVerificationErrorsAreDistinct(t *testing.T) {
	errors := []error{
		ErrEmailAlreadyVerified,
		ErrVerificationTokenExpired,
		ErrInvalidVerificationToken,
		ErrInvalidVerificationCode,
		ErrEmailNotVerified,
	}

	for i := 0; i < len(errors); i++ {
		for j := i + 1; j < len(errors); j++ {
			assert.NotEqual(t, errors[i].Error(), errors[j].Error(),
				"Errors at index %d and %d should have different messages", i, j)
		}
	}
}

func TestEmailVerificationTokenJSONRoundTrip(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	expires := now.Add(24 * time.Hour)
	token := EmailVerificationToken{
		ID:        "token-id-123",
		UserID:    "user-id-456",
		Token:     "verification-token-abc",
		Code:      "123456",
		ExpiresAt: expires,
		CreatedAt: now,
		UsedAt:    nil,
	}

	data, err := json.Marshal(token)
	assert.NoError(t, err)

	var decoded EmailVerificationToken
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, token.ID, decoded.ID)
	assert.Equal(t, token.UserID, decoded.UserID)
	assert.Equal(t, token.Token, decoded.Token)
	assert.Equal(t, token.Code, decoded.Code)
	assert.Nil(t, decoded.UsedAt)
}

func TestEmailVerificationTokenWithUsedAt(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	usedAt := now.Add(1 * time.Hour)
	token := EmailVerificationToken{
		ID:        "token-id-123",
		UserID:    "user-id-456",
		Token:     "verification-token-abc",
		Code:      "654321",
		ExpiresAt: now.Add(24 * time.Hour),
		CreatedAt: now,
		UsedAt:    &usedAt,
	}

	data, err := json.Marshal(token)
	assert.NoError(t, err)

	var decoded EmailVerificationToken
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.NotNil(t, decoded.UsedAt)
	assert.Equal(t, usedAt.UTC(), decoded.UsedAt.UTC())
}

func TestVerifyEmailRequestJSON(t *testing.T) {
	req := VerifyEmailRequest{
		Token: "some-token",
		Code:  "123456",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded VerifyEmailRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, req.Token, decoded.Token)
	assert.Equal(t, req.Code, decoded.Code)
}

func TestResendVerificationRequestJSON(t *testing.T) {
	req := ResendVerificationRequest{
		Email: "user@example.com",
	}

	data, err := json.Marshal(req)
	assert.NoError(t, err)

	var decoded ResendVerificationRequest
	err = json.Unmarshal(data, &decoded)
	assert.NoError(t, err)

	assert.Equal(t, req.Email, decoded.Email)
}
