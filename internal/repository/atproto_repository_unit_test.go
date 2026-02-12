package repository

import (
	"encoding/base64"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAtprotoRepository_Unit_DecodeTokenKey(t *testing.T) {
	raw := []byte("0123456789abcdef0123456789abcdef")

	b64 := base64.StdEncoding.EncodeToString(raw)
	gotB64, err := DecodeTokenKey(b64)
	require.NoError(t, err)
	assert.Equal(t, raw, gotB64)

	b64URL := base64.URLEncoding.EncodeToString(raw)
	gotURL, err := DecodeTokenKey(b64URL)
	require.NoError(t, err)
	assert.Equal(t, raw, gotURL)

	// Use a 31-byte payload so hex length is 62 (not divisible by 4),
	// which forces DecodeTokenKey to take the hex fallback path.
	hexRaw := raw[:31]
	hexKey := hex.EncodeToString(hexRaw)
	gotHex, err := DecodeTokenKey(hexKey)
	require.NoError(t, err)
	assert.Equal(t, hexRaw, gotHex)
}

func TestAtprotoRepository_Unit_DecodeTokenKey_ErrorCases(t *testing.T) {
	_, err := DecodeTokenKey("")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty key")

	_, err = DecodeTokenKey("not-valid$$$")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid key format")
}
