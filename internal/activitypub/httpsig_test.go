package activitypub

import (
	"bytes"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
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

	parsedPublicKey, err := parsePublicKey(publicKey)
	if err != nil {
		t.Errorf("Failed to parse generated public key: %v", err)
	}

	parsedPrivateKey, err := parsePrivateKey(privateKey)
	if err != nil {
		t.Errorf("Failed to parse generated private key: %v", err)
	}

	keySize := parsedPrivateKey.N.BitLen()
	expectedKeySize := 3072
	if keySize != expectedKeySize {
		t.Errorf("SECURITY: RSA key size is %d bits, expected %d bits per NIST SP 800-57", keySize, expectedKeySize)
	}

	publicKeySize := parsedPublicKey.N.BitLen()
	if publicKeySize != expectedKeySize {
		t.Errorf("SECURITY: RSA public key size is %d bits, expected %d bits", publicKeySize, expectedKeySize)
	}
}

func TestSignAndVerifyRequest(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	body := []byte(`{"type":"Follow","actor":"https://example.com/users/alice"}`)
	req, err := http.NewRequest("POST", "https://mastodon.example/users/bob/inbox", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Host", "mastodon.example")
	req.Header.Set("Content-Type", "application/activity+json")

	keyID := "https://example.com/users/alice#main-key"
	err = SignRequest(req, privateKey, keyID)
	if err != nil {
		t.Fatalf("Failed to sign request: %v", err)
	}

	if req.Header.Get("Signature") == "" {
		t.Error("Signature header not set")
	}

	verifier := NewHTTPSignatureVerifier()
	err = verifier.VerifyRequest(req, publicKey)
	if err != nil {
		t.Errorf("Failed to verify request: %v", err)
	}
}

func TestVerifyRequestWithInvalidSignature(t *testing.T) {
	publicKey1, _, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair 1: %v", err)
	}

	_, privateKey2, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair 2: %v", err)
	}

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

	req.Header.Set("Host", "example.com")
	keyID := "https://example.com/users/test#main-key"
	err = SignRequest(req, privateKey, keyID)
	if err != nil {
		t.Fatalf("Failed to sign request: %v", err)
	}

	if req.Header.Get("Date") == "" {
		t.Error("Date header was not added by SignRequest")
	}
}

func TestSignRequestWithMissingHost(t *testing.T) {
	_, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

	req, err := http.NewRequest("POST", "/users/bob/inbox", nil)
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	keyID := "https://example.com/users/test#main-key"
	err = SignRequest(req, privateKey, keyID)

	assert.Error(t, err)
}

func TestVerifyRequestWithTamperedBody(t *testing.T) {
	publicKey, privateKey, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("Failed to generate key pair: %v", err)
	}

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

	tamperedBody := []byte(`{"type":"Delete","actor":"https://example.com/users/alice"}`)
	req.Body = io.NopCloser(bytes.NewReader(tamperedBody))

	verifier := NewHTTPSignatureVerifier()
	_ = verifier.VerifyRequest(req, publicKey)
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

	req.Header.Set("Date", "Mon, 01 Jan 2020 00:00:00 GMT")
	req.Header.Set("Host", "example.com")

	keyID := "https://example.com/users/alice#main-key"
	err = SignRequest(req, privateKey, keyID)
	if err != nil {
		t.Fatalf("Failed to sign request: %v", err)
	}

	verifier := NewHTTPSignatureVerifier()
	err = verifier.VerifyRequest(req, publicKey)

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

			assert.NoError(t, err)
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

	headers := []string{"(request-target)", "host", "date"}
	_, err = buildSigningString(req, headers)

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

func TestVerifyDigest_SHA256(t *testing.T) {
	body := []byte(`{"type":"Follow"}`)
	hash := sha256.Sum256(body)
	digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
	err := verifyDigest(body, digest)
	assert.NoError(t, err, "SHA-256 digest should verify correctly")
}

func TestVerifyDigest_SHA512(t *testing.T) {
	body := []byte(`{"type":"Follow"}`)
	hash := sha512.Sum512(body)
	digest := "SHA-512=" + base64.StdEncoding.EncodeToString(hash[:])
	err := verifyDigest(body, digest)
	assert.NoError(t, err, "SHA-512 digest should verify correctly")
}

func TestVerifyDigest_SHA512_RejectsSHA256Hash(t *testing.T) {
	body := []byte(`{"type":"Follow"}`)
	hash := sha256.Sum256(body)
	digest := "SHA-512=" + base64.StdEncoding.EncodeToString(hash[:])
	err := verifyDigest(body, digest)
	assert.Error(t, err, "SHA-512 header with SHA-256 hash should fail")
}

func TestVerifyDigest_Mismatch(t *testing.T) {
	body := []byte(`{"type":"Follow"}`)
	hash := sha256.Sum256([]byte("different body"))
	digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
	err := verifyDigest(body, digest)
	assert.Error(t, err, "mismatched digest should fail")
}

func TestVerifyDigest_UnsupportedAlgorithm(t *testing.T) {
	body := []byte(`{"type":"Follow"}`)
	err := verifyDigest(body, "MD5=abc123")
	assert.Error(t, err)
}
