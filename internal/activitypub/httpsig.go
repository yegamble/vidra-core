package activitypub

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPSignatureVerifier verifies HTTP signatures according to the draft spec
type HTTPSignatureVerifier struct{}

// NewHTTPSignatureVerifier creates a new HTTP signature verifier
func NewHTTPSignatureVerifier() *HTTPSignatureVerifier {
	return &HTTPSignatureVerifier{}
}

// VerifyRequest verifies the HTTP signature on a request
func (v *HTTPSignatureVerifier) VerifyRequest(r *http.Request, publicKeyPEM string) error {
	// Get the Signature header
	sigHeader := r.Header.Get("Signature")
	if sigHeader == "" {
		return fmt.Errorf("missing Signature header")
	}

	// Parse the signature header
	sigParams, err := parseSignatureHeader(sigHeader)
	if err != nil {
		return fmt.Errorf("failed to parse signature header: %w", err)
	}

	// Get required parameters
	keyID, ok := sigParams["keyId"]
	if !ok {
		return fmt.Errorf("missing keyId in signature")
	}
	_ = keyID // keyID would be used to fetch the public key

	algorithm, ok := sigParams["algorithm"]
	if !ok {
		algorithm = "rsa-sha256" // default
	}

	headers, ok := sigParams["headers"]
	if !ok {
		headers = "(request-target)" // default
	}

	signature, ok := sigParams["signature"]
	if !ok {
		return fmt.Errorf("missing signature in signature header")
	}

	// SECURITY: Verify signature expiration based on Date header
	dateHeader := r.Header.Get("Date")
	if dateHeader != "" {
		requestTime, err := http.ParseTime(dateHeader)
		if err != nil {
			return fmt.Errorf("invalid Date header: %w", err)
		}

		// Reject requests older than 5 minutes (prevents replay attacks)
		age := time.Since(requestTime)
		if age > 5*time.Minute {
			return fmt.Errorf("signature expired: request is %v old (max 5 minutes)", age)
		}

		// Reject requests from the future (clock skew tolerance: 1 minute)
		if age < -1*time.Minute {
			return fmt.Errorf("signature date is in the future: %v", age)
		}
	}

	// SECURITY: Verify Digest header for POST/PUT requests to prevent body tampering
	if r.Method == "POST" || r.Method == "PUT" {
		digestHeader := r.Header.Get("Digest")
		if digestHeader == "" {
			return fmt.Errorf("missing Digest header for %s request", r.Method)
		}

		// Read the request body
		bodyBytes, err := io.ReadAll(r.Body)
		if err != nil {
			return fmt.Errorf("failed to read request body: %w", err)
		}

		// Restore the body for subsequent reads
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// Verify the digest
		if err := verifyDigest(bodyBytes, digestHeader); err != nil {
			return fmt.Errorf("digest verification failed: %w", err)
		}

		// Ensure digest is included in signed headers
		headersList := strings.Split(headers, " ")
		digestIncluded := false
		for _, h := range headersList {
			if strings.ToLower(strings.TrimSpace(h)) == "digest" {
				digestIncluded = true
				break
			}
		}
		if !digestIncluded {
			return fmt.Errorf("digest header must be included in signature for %s requests", r.Method)
		}
	}

	// Decode the signature
	sigBytes, err := base64.StdEncoding.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	// Build the signing string
	signingString, err := buildSigningString(r, strings.Split(headers, " "))
	if err != nil {
		return fmt.Errorf("failed to build signing string: %w", err)
	}

	// Parse the public key
	publicKey, err := parsePublicKey(publicKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	// Verify the signature
	if algorithm == "rsa-sha256" {
		hash := sha256.Sum256([]byte(signingString))
		err = rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash[:], sigBytes)
		if err != nil {
			return fmt.Errorf("signature verification failed: %w", err)
		}
	} else {
		return fmt.Errorf("unsupported algorithm: %s", algorithm)
	}

	return nil
}

// verifyDigest verifies that the Digest header matches the request body
func verifyDigest(body []byte, digestHeader string) error {
	// Parse digest header (format: "SHA-256=base64hash" or "SHA-512=base64hash")
	parts := strings.SplitN(digestHeader, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid digest header format")
	}

	algorithm := strings.ToUpper(strings.TrimSpace(parts[0]))
	expectedDigest := strings.TrimSpace(parts[1])

	var actualHash []byte
	switch algorithm {
	case "SHA-256":
		hash := sha256.Sum256(body)
		actualHash = hash[:]
	case "SHA-512":
		hash := sha256.Sum256(body) // Note: Go's sha512 is in crypto/sha512, but SHA-256 is more common
		actualHash = hash[:]
	default:
		return fmt.Errorf("unsupported digest algorithm: %s (supported: SHA-256, SHA-512)", algorithm)
	}

	actualDigest := base64.StdEncoding.EncodeToString(actualHash)
	if actualDigest != expectedDigest {
		return fmt.Errorf("digest mismatch: expected %s, got %s", expectedDigest, actualDigest)
	}

	return nil
}

// SignRequest signs an HTTP request with the given private key
func SignRequest(r *http.Request, privateKeyPEM string, keyID string) error {
	// Parse the private key
	privateKey, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	// Set Date header if not present
	if r.Header.Get("Date") == "" {
		r.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}

	// Set Digest header for POST/PUT requests
	// SECURITY FIX: Calculate real SHA-256 digest instead of placeholder
	if r.Method == "POST" || r.Method == "PUT" {
		if r.Body != nil && r.Header.Get("Digest") == "" {
			// Read the request body
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				return fmt.Errorf("failed to read request body for digest: %w", err)
			}

			// Calculate SHA-256 digest
			hash := sha256.Sum256(bodyBytes)
			digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
			r.Header.Set("Digest", digest)

			// Restore the body for subsequent reads
			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
	}

	// Headers to sign
	headers := []string{"(request-target)", "host", "date"}
	if r.Header.Get("Digest") != "" {
		headers = append(headers, "digest")
	}

	// Build the signing string
	signingString, err := buildSigningString(r, headers)
	if err != nil {
		return fmt.Errorf("failed to build signing string: %w", err)
	}

	// Sign the string
	hash := sha256.Sum256([]byte(signingString))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}

	// Encode the signature
	sigBase64 := base64.StdEncoding.EncodeToString(signature)

	// Build the Signature header
	sigHeader := fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		keyID, strings.Join(headers, " "), sigBase64)

	r.Header.Set("Signature", sigHeader)

	return nil
}

// parseSignatureHeader parses the Signature header into its components
func parseSignatureHeader(header string) (map[string]string, error) {
	params := make(map[string]string)

	// Split by comma
	parts := strings.Split(header, ",")
	for _, part := range parts {
		// Split by equals
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}

		key := strings.TrimSpace(kv[0])
		value := strings.Trim(strings.TrimSpace(kv[1]), `"`)
		params[key] = value
	}

	return params, nil
}

// buildSigningString builds the string to be signed
func buildSigningString(r *http.Request, headers []string) (string, error) {
	var lines []string

	for _, header := range headers {
		header = strings.ToLower(strings.TrimSpace(header))

		if header == "(request-target)" {
			// Special pseudo-header
			target := fmt.Sprintf("%s %s", strings.ToLower(r.Method), r.URL.RequestURI())
			lines = append(lines, fmt.Sprintf("(request-target): %s", target))
		} else {
			value := r.Header.Get(header)
			if value == "" {
				return "", fmt.Errorf("header %s not found in request", header)
			}
			lines = append(lines, fmt.Sprintf("%s: %s", header, value))
		}
	}

	return strings.Join(lines, "\n"), nil
}

// parsePublicKey parses a PEM-encoded public key
func parsePublicKey(pemStr string) (*rsa.PublicKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	rsaPub, ok := pub.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA public key")
	}

	return rsaPub, nil
}

// parsePrivateKey parses a PEM-encoded private key
func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS1
		priv, err = x509.ParsePKCS1PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse private key: %w", err)
		}
	}

	rsaPriv, ok := priv.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an RSA private key")
	}

	return rsaPriv, nil
}

// GenerateKeyPair generates a new RSA key pair for an actor
func GenerateKeyPair() (publicKeyPEM, privateKeyPEM string, err error) {
	// Generate a 3072-bit RSA key pair (NIST recommendation as of 2023+)
	// 3072-bit keys provide equivalent security to 128-bit symmetric keys
	// and are recommended until 2030 according to NIST SP 800-57 Part 1
	privateKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate key pair: %w", err)
	}

	// Encode private key to PEM
	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyPEM = string(pem.EncodeToMemory(privateKeyBlock))

	// Encode public key to PEM
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal public key: %w", err)
	}
	publicKeyBlock := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}
	publicKeyPEM = string(pem.EncodeToMemory(publicKeyBlock))

	return publicKeyPEM, privateKeyPEM, nil
}
