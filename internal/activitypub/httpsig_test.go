package activitypub

import (
	"bytes"
	"net/http"
	"testing"
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
	_, err = parsePublicKey(publicKey)
	if err != nil {
		t.Errorf("Failed to parse generated public key: %v", err)
	}

	_, err = parsePrivateKey(privateKey)
	if err != nil {
		t.Errorf("Failed to parse generated private key: %v", err)
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

	// Don't set Date header
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
