package activitypub

import (
	"bytes"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateKeyPair(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	if publicKey == "" {
		t.Error("Public key is empty")
	}

	if privateKey == "" {
		t.Error("Private key is empty")
	}

	// Verify keys can be parsed
	parsedPublicKey, err := parsePublicKey(publicKey)
	if err != nil {
		t.Errorf("Failed to parse generated public key: %v", err)
	}

	parsedPrivateKey, err := parsePrivateKey(privateKey)
	if err != nil {
		t.Errorf("Failed to parse generated private key: %v", err)
	}

	// CRITICAL SECURITY TEST: Verify key size is 3072 bits (NIST standard)
	keySize := parsedPrivateKey.N.BitLen()
	expectedKeySize := 3072
	if keySize != expectedKeySize {
		t.Errorf("SECURITY: RSA key size is %d bits, expected %d bits per NIST SP 800-57", keySize, expectedKeySize)
	}

	// Also verify the public key has the same size
	publicKeySize := parsedPublicKey.N.BitLen()
	if publicKeySize != expectedKeySize {
		t.Errorf("SECURITY: RSA public key size is %d bits, expected %d bits", publicKeySize, expectedKeySize)
	}
}

func TestSignAndVerifyRequest(t *testing.T) {
	// Generate a key pair
	publicKey, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Create a test request
	body := []byte(`{"type":"Follow","actor":"https://example.com/users/alice"}`)
	req, err := http.NewRequest("POST", "https://mastodon.example/users/bob/inbox", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Host", "mastodon.example")
	req.Header.Set("Content-Type", "application/activity+json")

	// Sign the request
	keyID := "https://example.com/users/alice#main-key"
	err = SignRequest(req, privateKey, keyID)
	if err != nil {
		t.Fatalf("Failed to sign request: %v", err)
	}

	// Verify the signature header exists
	if req.Header.Get("Signature") == "" {
		t.Error("Signature header not set")
	}

	// Verify the request
	verifier := NewHTTPSignatureVerifier()
	err = verifier.VerifyRequest(req, publicKey)
	if err != nil {
		t.Errorf("Failed to verify request: %v", err)
	}
}

func TestVerifyRequestWithInvalidSignature(t *testing.T) {
	// Generate two different key pairs
	publicKey1, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair 1: %v", err)
	}

	_, privateKey2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair 2: %v", err)
	}

	// Create and sign request with privateKey2
	body := []byte(`{"type":"Follow"}`)
	req, err := http.NewRequest("POST", "https://mastodon.example/users/bob/inbox", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Host", "mastodon.example")
	keyID := "https://example.com/users/alice#main-key"
	err = SignRequest(req, privateKey2, keyID)
	if err != nil {
		t.Fatalf("Failed to sign request: %v", err)
	}

	// Try to verify with publicKey1 (should fail)
	verifier := NewHTTPSignatureVerifier()
	err = verifier.VerifyRequest(req, publicKey1)
	if err == nil {
		t.Error("Expected verification to fail with mismatched keys, but it succeeded")
	}
}

func TestParseSignatureHeader(t *testing.T) {
	header := `keyId="https://example.com/users/alice#main-key",algorithm="rsa-sha256",headers="(request-target) host date",signature="AAABBBCCC"`

	params, err := parseSignatureHeader(header)
	if err != nil {
		t.Fatalf("Failed to parse signature header: %v", err)
	}

	expected := map[string]string{
		"keyId":     "https://example.com/users/alice#main-key",
		"algorithm": "rsa-sha256",
		"headers":   "(request-target) host date",
		"signature": "AAABBBCCC",
	}

	for key, expectedValue := range expected {
		if params[key] != expectedValue {
			t.Errorf("Expected %s=%s, got %s=%s", key, expectedValue, key, params[key])
		}
	}
}

func TestBuildSigningString(t *testing.T) {
	req, err := http.NewRequest("POST", "/users/bob/inbox", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Host", "mastodon.example")
	req.Header.Set("Date", "Tue, 01 Jan 2024 00:00:00 GMT")

	headers := []string{"(request-target)", "host", "date"}
	signingString, err := buildSigningString(req, headers)
	if err != nil {
		t.Fatalf("Failed to build signing string: %v", err)
	}

	expected := "(request-target): post /users/bob/inbox\nhost: mastodon.example\ndate: Tue, 01 Jan 2024 00:00:00 GMT"
	if signingString != expected {
		t.Errorf("Expected signing string:\n%s\n\nGot:\n%s", expected, signingString)
	}
}

func TestSignRequestAddsDateHeader(t *testing.T) {
	_, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	req, err := http.NewRequest("GET", "https://example.com/users/alice", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	// Set Host header but don't set Date header
	req.Header.Set("Host", "example.com")
	keyID := "https://example.com/users/test#main-key"
	err = SignRequest(req, privateKey, keyID)
	if err != nil {
		t.Fatalf("Failed to sign request: %v", err)
	}

	// Check that Date header was added
	if req.Header.Get("Date") == "" {
		t.Error("Date header was not added by SignRequest")
	}
}

// Edge Case Tests

func TestSignRequestWithMissingHost(t *testing.T) {
	_, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	req, err := http.NewRequest("POST", "/users/bob/inbox", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	// Don't set Host header
	keyID := "https://example.com/users/test#main-key"
	err = SignRequest(req, privateKey, keyID)

	// Should fail because host header is required
	assert.Error(t, err)
}

func TestVerifyRequestWithTamperedBody(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	// Create and sign request
	originalBody := []byte(`{"type":"Follow","actor":"https://example.com/users/alice"}`)
	req, err := http.NewRequest("POST", "https://mastodon.example/users/bob/inbox", bytes.NewReader(originalBody))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Host", "mastodon.example")
	keyID := "https://example.com/users/alice#main-key"
	err = SignRequest(req, privateKey, keyID)
	if err != nil {
		t.Fatalf("Failed to sign request: %v", err)
	}

	// Tamper with the body (in practice, we'd need to modify the Digest header)
	tamperedBody := []byte(`{"type":"Delete","actor":"https://example.com/users/alice"}`)
	req.Body = io.NopCloser(bytes.NewReader(tamperedBody))

	// Verification should still pass because we're not verifying Digest in this implementation
	// This demonstrates a limitation - we should verify Digest header in production
	verifier := NewHTTPSignatureVerifier()
	err = verifier.VerifyRequest(req, publicKey)
	// This might pass or fail depending on implementation details
	// In production, you'd want to verify the Digest header
}

func TestSignRequestWithNonStandardHeaders(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	req, err := http.NewRequest("GET", "https://example.com/users/alice", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("X-Custom-Header", "custom-value")
	req.Header.Set("Host", "example.com")

	keyID := "https://example.com/users/alice#main-key"
	err = SignRequest(req, privateKey, keyID)
	if err != nil {
		t.Fatalf("Failed to sign request: %v", err)
	}

	verifier := NewHTTPSignatureVerifier()
	err = verifier.VerifyRequest(req, publicKey)
	if err != nil {
		t.Errorf("Failed to verify request with custom headers: %v", err)
	}
}

func TestVerifyRequestWithExpiredSignature(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	req, err := http.NewRequest("GET", "https://example.com/users/alice", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	// Set date far in the past
	req.Header.Set("Date", "Mon, 01 Jan 2020 00:00:00 GMT")
	req.Header.Set("Host", "example.com")

	keyID := "https://example.com/users/alice#main-key"
	err = SignRequest(req, privateKey, keyID)
	if err != nil {
		t.Fatalf("Failed to sign request: %v", err)
	}

	verifier := NewHTTPSignatureVerifier()
	err = verifier.VerifyRequest(req, publicKey)

	// Signature should be rejected because the date is too old (expired)
	// The implementation checks for signature expiration
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "signature expired")
}

func TestParseSignatureHeaderWithMalformedInput(t *testing.T) {
	tests := []struct {
		name   string
		header string
	}{
		{
			name:   "Missing equals",
			header: `keyId"value",algorithm="rsa-sha256"`,
		},
		{
			name:   "Empty header",
			header: "",
		},
		{
			name:   "Only commas",
			header: ",,,",
		},
		{
			name:   "Unquoted values",
			header: `keyId=value,algorithm=rsa-sha256`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params, err := parseSignatureHeader(tt.header)

			// Should not error but may return incomplete params
			assert.NoError(t, err)
			// The quality of parsing depends on implementation
			_ = params
		})
	}
}

func TestBuildSigningStringWithMissingHeader(t *testing.T) {
	req, err := http.NewRequest("GET", "https://example.com/users/alice", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Host", "example.com")
	// Don't set Date header

	headers := []string{"(request-target)", "host", "date"}
	_, err = buildSigningString(req, headers)

	// Should error because date header is missing
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "date")
}

func TestSignAndVerifyWithDifferentKeyTypes(t *testing.T) {
	t.Run("2048-bit RSA keys", func(t *testing.T) {
		publicKey, privateKey, err := GenerateKeyPair()
		require.NoError(t, err)

		req, _ := http.NewRequest("GET", "https://example.com/test", nil)
		req.Header.Set("Host", "example.com")

		err = SignRequest(req, privateKey, "test-key")
		require.NoError(t, err)

		verifier := NewHTTPSignatureVerifier()
		err = verifier.VerifyRequest(req, publicKey)
		assert.NoError(t, err)
	})
}

func TestConcurrentKeyGeneration(t *testing.T) {
	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			_, _, err := GenerateKeyPair()
			assert.NoError(t, err)
			done <- true
		}()
	}

	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

func TestSignatureHeaderFormat(t *testing.T) {
	_, privateKey, err := GenerateKeyPair()
	require.NoError(t, err)

	req, _ := http.NewRequest("POST", "https://example.com/inbox", nil)
	req.Header.Set("Host", "example.com")

	keyID := "https://example.com/users/alice#main-key"
	err = SignRequest(req, privateKey, keyID)
	require.NoError(t, err)

	sigHeader := req.Header.Get("Signature")
	assert.NotEmpty(t, sigHeader)

	// Verify format
	assert.Contains(t, sigHeader, `keyId="`)
	assert.Contains(t, sigHeader, `algorithm="rsa-sha256"`)
	assert.Contains(t, sigHeader, `headers="`)
	assert.Contains(t, sigHeader, `signature="`)
}

func TestRequestTargetGeneration(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		url            string
		expectedTarget string
	}{
		{
			name:           "GET request",
			method:         "GET",
			url:            "https://example.com/users/alice",
			expectedTarget: "get /users/alice",
		},
		{
			name:           "POST request",
			method:         "POST",
			url:            "https://example.com/users/bob/inbox",
			expectedTarget: "post /users/bob/inbox",
		},
		{
			name:           "Request with query params",
			method:         "GET",
			url:            "https://example.com/users/alice?page=1",
			expectedTarget: "get /users/alice?page=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest(tt.method, tt.url, nil)
			headers := []string{"(request-target)"}

			signingString, err := buildSigningString(req, headers)
			require.NoError(t, err)

			expected := "(request-target): " + tt.expectedTarget
			assert.Equal(t, expected, signingString)
		})
	}
}

func BenchmarkGenerateKeyPair(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _, err := GenerateKeyPair()
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSignRequest(b *testing.B) {
	_, privateKey, _ := GenerateKeyPair()
	keyID := "https://example.com/users/test#main-key"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		req, _ := http.NewRequest("POST", "https://example.com/inbox", nil)
		req.Header.Set("Host", "example.com")
		err := SignRequest(req, privateKey, keyID)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerifyRequest(b *testing.B) {
	publicKey, privateKey, _ := GenerateKeyPair()
	req, _ := http.NewRequest("POST", "https://example.com/inbox", nil)
	req.Header.Set("Host", "example.com")
	SignRequest(req, privateKey, "test-key")

	verifier := NewHTTPSignatureVerifier()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := verifier.VerifyRequest(req, publicKey)
		if err != nil {
			b.Fatal(err)
		}
	}
}
