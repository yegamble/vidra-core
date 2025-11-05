package security

import (
	"testing"
	"time"
)

func TestGenerateHLSToken(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	exp := time.Now().Add(1 * time.Hour).Unix()

	token := GenerateHLSToken(secret, path, exp)

	if token == "" {
		t.Error("Expected non-empty token")
	}

	// Token should be a hex string
	if len(token)%2 != 0 {
		t.Error("Token should be a valid hex string (even length)")
	}

	// Same inputs should produce same token
	token2 := GenerateHLSToken(secret, path, exp)
	if token != token2 {
		t.Error("Same inputs should produce same token")
	}
}

func TestGenerateHLSToken_DifferentInputs(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	exp := time.Now().Add(1 * time.Hour).Unix()

	token1 := GenerateHLSToken(secret, path, exp)

	// Different path should produce different token
	token2 := GenerateHLSToken(secret, "video456/master.m3u8", exp)
	if token1 == token2 {
		t.Error("Different paths should produce different tokens")
	}

	// Different expiry should produce different token
	token3 := GenerateHLSToken(secret, path, exp+3600)
	if token1 == token3 {
		t.Error("Different expiries should produce different tokens")
	}

	// Different secret should produce different token
	token4 := GenerateHLSToken("different-secret", path, exp)
	if token1 == token4 {
		t.Error("Different secrets should produce different tokens")
	}
}

func TestVerifyHLSToken_ValidToken(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	now := time.Now()
	exp := now.Add(1 * time.Hour).Unix()

	token := GenerateHLSToken(secret, path, exp)

	// Should verify successfully
	if !VerifyHLSToken(secret, path, exp, token, now) {
		t.Error("Valid token should verify successfully")
	}
}

func TestVerifyHLSToken_ExpiredToken(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	pastTime := time.Now().Add(-2 * time.Hour)
	exp := pastTime.Unix()

	token := GenerateHLSToken(secret, path, exp)

	// Should fail due to expiration
	if VerifyHLSToken(secret, path, exp, token, time.Now()) {
		t.Error("Expired token should not verify")
	}
}

func TestVerifyHLSToken_WrongSecret(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	now := time.Now()
	exp := now.Add(1 * time.Hour).Unix()

	token := GenerateHLSToken(secret, path, exp)

	// Should fail with wrong secret
	if VerifyHLSToken("wrong-secret", path, exp, token, now) {
		t.Error("Token should not verify with wrong secret")
	}
}

func TestVerifyHLSToken_WrongPath(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	now := time.Now()
	exp := now.Add(1 * time.Hour).Unix()

	token := GenerateHLSToken(secret, path, exp)

	// Should fail with wrong path
	if VerifyHLSToken(secret, "video456/master.m3u8", exp, token, now) {
		t.Error("Token should not verify with wrong path")
	}
}

func TestVerifyHLSToken_WrongExpiry(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	now := time.Now()
	exp := now.Add(1 * time.Hour).Unix()

	token := GenerateHLSToken(secret, path, exp)

	// Should fail with wrong expiry
	wrongExp := exp + 3600
	if VerifyHLSToken(secret, path, wrongExp, token, now) {
		t.Error("Token should not verify with wrong expiry")
	}
}

func TestVerifyHLSToken_InvalidTokenFormat(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	now := time.Now()
	exp := now.Add(1 * time.Hour).Unix()

	// Invalid hex string
	if VerifyHLSToken(secret, path, exp, "not-a-hex-string", now) {
		t.Error("Invalid token format should not verify")
	}

	// Partial hex string
	if VerifyHLSToken(secret, path, exp, "abcd", now) {
		t.Error("Partial token should not verify")
	}

	// Empty token
	if VerifyHLSToken(secret, path, exp, "", now) {
		t.Error("Empty token should not verify")
	}
}

func TestVerifyHLSToken_TamperedToken(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	now := time.Now()
	exp := now.Add(1 * time.Hour).Unix()

	token := GenerateHLSToken(secret, path, exp)

	// Tamper with the token (flip last character to ensure it's different)
	if len(token) > 0 {
		lastChar := token[len(token)-1]
		var newChar byte
		if lastChar == 'f' {
			newChar = '0' // If it's 'f', change to '0'
		} else {
			newChar = 'f' // Otherwise, change to 'f'
		}
		tamperedToken := token[:len(token)-1] + string(newChar)
		if VerifyHLSToken(secret, path, exp, tamperedToken, now) {
			t.Error("Tampered token should not verify")
		}
	}
}

func TestVerifyHLSToken_EdgeCaseExpiry(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	now := time.Now()

	// Expiry exactly at current time (should fail)
	exp := now.Unix()
	token := GenerateHLSToken(secret, path, exp)
	if VerifyHLSToken(secret, path, exp, token, now) {
		t.Error("Token expiring at current time should not verify")
	}

	// Expiry 1 second in the future (should pass)
	exp = now.Add(1 * time.Second).Unix()
	token = GenerateHLSToken(secret, path, exp)
	if !VerifyHLSToken(secret, path, exp, token, now) {
		t.Error("Token expiring 1 second in future should verify")
	}
}

func TestHLSToken_TimingConsistency(t *testing.T) {
	secret := "test-secret-key"
	path := "video123/master.m3u8"
	now := time.Now()
	exp := now.Add(5 * time.Minute).Unix()

	token := GenerateHLSToken(secret, path, exp)

	// Verify at different times within valid window
	for i := 0; i < 5; i++ {
		checkTime := now.Add(time.Duration(i) * time.Minute)
		if !VerifyHLSToken(secret, path, exp, token, checkTime) {
			t.Errorf("Token should verify at time offset %d minutes", i)
		}
	}

	// Should fail after expiry
	afterExpiry := now.Add(6 * time.Minute)
	if VerifyHLSToken(secret, path, exp, token, afterExpiry) {
		t.Error("Token should not verify after expiry")
	}
}

func TestHLSToken_SpecialCharactersInPath(t *testing.T) {
	secret := "test-secret-key"
	now := time.Now()
	exp := now.Add(1 * time.Hour).Unix()

	paths := []string{
		"video_123/master.m3u8",
		"video-456/playlist.m3u8",
		"a/b/c/d/segment.ts",
		"中文/test.m3u8",
		"video with spaces/master.m3u8",
		"special!@#$%/file.m3u8",
	}

	for _, path := range paths {
		token := GenerateHLSToken(secret, path, exp)
		if !VerifyHLSToken(secret, path, exp, token, now) {
			t.Errorf("Token should verify for path: %s", path)
		}
	}
}

func TestHLSToken_EmptyValues(t *testing.T) {
	now := time.Now()
	exp := now.Add(1 * time.Hour).Unix()

	// Empty secret
	token := GenerateHLSToken("", "video123/master.m3u8", exp)
	if token == "" {
		t.Error("Should generate token even with empty secret")
	}

	// Empty path
	token = GenerateHLSToken("secret", "", exp)
	if token == "" {
		t.Error("Should generate token even with empty path")
	}
}
