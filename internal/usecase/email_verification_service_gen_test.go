package usecase

import (
	"encoding/hex"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGenerateToken_IsHex64(t *testing.T) {
	tok := generateToken()
	assert.Len(t, tok, 64, "token should be 64 hex chars")
	_, err := hex.DecodeString(tok)
	assert.NoError(t, err, "token should be valid hex")
}

func TestGenerateCode_IsSixDigits(t *testing.T) {
	code := generateCode()
	re := regexp.MustCompile(`^\d{6}$`)
	assert.True(t, re.MatchString(code), "code should be six digits")
}
