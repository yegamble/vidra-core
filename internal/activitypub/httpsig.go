package activitypub

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type HTTPSignatureVerifier struct{}

func NewHTTPSignatureVerifier() *HTTPSignatureVerifier {
	return &HTTPSignatureVerifier{}
}

func (v *HTTPSignatureVerifier) VerifyRequest(r *http.Request, publicKeyPEM string) error {
	sigParams, err := v.extractSignatureParams(r)
	if err != nil {
		return err
	}

	if err := v.validateDateFreshness(r); err != nil {
		return err
	}

	headers := sigParams["headers"]
	if err := v.verifyBodyDigest(r, headers); err != nil {
		return err
	}

	return v.verifySignatureValue(r, sigParams, publicKeyPEM)
}

// extractSignatureParams parses and validates the Signature header parameters.
func (v *HTTPSignatureVerifier) extractSignatureParams(r *http.Request) (map[string]string, error) {
	sigHeader := r.Header.Get("Signature")
	if sigHeader == "" {
		return nil, fmt.Errorf("missing Signature header")
	}

	sigParams, err := parseSignatureHeader(sigHeader)
	if err != nil {
		return nil, fmt.Errorf("failed to parse signature header: %w", err)
	}

	if _, ok := sigParams["keyId"]; !ok {
		return nil, fmt.Errorf("missing keyId in signature")
	}

	if _, ok := sigParams["algorithm"]; !ok {
		sigParams["algorithm"] = "rsa-sha256"
	}

	if _, ok := sigParams["headers"]; !ok {
		sigParams["headers"] = "(request-target)"
	}

	if _, ok := sigParams["signature"]; !ok {
		return nil, fmt.Errorf("missing signature in signature header")
	}

	return sigParams, nil
}

// validateDateFreshness checks that the Date header, if present, is within an acceptable time window.
func (v *HTTPSignatureVerifier) validateDateFreshness(r *http.Request) error {
	dateHeader := r.Header.Get("Date")
	if dateHeader == "" {
		return nil
	}

	requestTime, err := http.ParseTime(dateHeader)
	if err != nil {
		return fmt.Errorf("invalid Date header: %w", err)
	}

	age := time.Since(requestTime)
	if age > 5*time.Minute {
		return fmt.Errorf("signature expired: request is %v old (max 5 minutes)", age)
	}
	if age < -1*time.Minute {
		return fmt.Errorf("signature date is in the future: %v", age)
	}

	return nil
}

// verifyBodyDigest verifies the Digest header for POST/PUT requests and ensures
// the digest header is included in the signed headers list.
func (v *HTTPSignatureVerifier) verifyBodyDigest(r *http.Request, headers string) error {
	if r.Method != "POST" && r.Method != "PUT" {
		return nil
	}

	digestHeader := r.Header.Get("Digest")
	if digestHeader == "" {
		return fmt.Errorf("missing Digest header for %s request", r.Method)
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("failed to read request body: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	if err := verifyDigest(bodyBytes, digestHeader); err != nil {
		return fmt.Errorf("digest verification failed: %w", err)
	}

	headersList := strings.Split(headers, " ")
	for _, h := range headersList {
		if strings.ToLower(strings.TrimSpace(h)) == "digest" {
			return nil
		}
	}
	return fmt.Errorf("digest header must be included in signature for %s requests", r.Method)
}

// verifySignatureValue decodes the signature, builds the signing string, and performs
// cryptographic verification against the public key.
func (v *HTTPSignatureVerifier) verifySignatureValue(r *http.Request, sigParams map[string]string, publicKeyPEM string) error {
	algorithm := sigParams["algorithm"]
	if algorithm != "rsa-sha256" {
		return fmt.Errorf("unsupported algorithm: %s", algorithm)
	}

	sigBytes, err := base64.StdEncoding.DecodeString(sigParams["signature"])
	if err != nil {
		return fmt.Errorf("failed to decode signature: %w", err)
	}

	signingString, err := buildSigningString(r, strings.Split(sigParams["headers"], " "))
	if err != nil {
		return fmt.Errorf("failed to build signing string: %w", err)
	}

	publicKey, err := parsePublicKey(publicKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to parse public key: %w", err)
	}

	hash := sha256.Sum256([]byte(signingString))
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, hash[:], sigBytes); err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

func verifyDigest(body []byte, digestHeader string) error {
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
		hash := sha512.Sum512(body)
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

func SignRequest(r *http.Request, privateKeyPEM string, keyID string) error {
	privateKey, err := parsePrivateKey(privateKeyPEM)
	if err != nil {
		return fmt.Errorf("failed to parse private key: %w", err)
	}

	if r.Header.Get("Date") == "" {
		r.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}

	if r.Method == "POST" || r.Method == "PUT" {
		if r.Body != nil && r.Header.Get("Digest") == "" {
			bodyBytes, err := io.ReadAll(r.Body)
			if err != nil {
				return fmt.Errorf("failed to read request body for digest: %w", err)
			}

			hash := sha256.Sum256(bodyBytes)
			digest := "SHA-256=" + base64.StdEncoding.EncodeToString(hash[:])
			r.Header.Set("Digest", digest)

			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		}
	}

	headers := []string{"(request-target)", "host", "date"}
	if r.Header.Get("Digest") != "" {
		headers = append(headers, "digest")
	}

	signingString, err := buildSigningString(r, headers)
	if err != nil {
		return fmt.Errorf("failed to build signing string: %w", err)
	}

	hash := sha256.Sum256([]byte(signingString))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, hash[:])
	if err != nil {
		return fmt.Errorf("failed to sign request: %w", err)
	}

	sigBase64 := base64.StdEncoding.EncodeToString(signature)

	sigHeader := fmt.Sprintf(`keyId="%s",algorithm="rsa-sha256",headers="%s",signature="%s"`,
		keyID, strings.Join(headers, " "), sigBase64)

	r.Header.Set("Signature", sigHeader)

	return nil
}

func parseSignatureHeader(header string) (map[string]string, error) {
	params := make(map[string]string)

	parts := strings.Split(header, ",")
	for _, part := range parts {
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

func buildSigningString(r *http.Request, headers []string) (string, error) {
	var lines []string

	for _, header := range headers {
		header = strings.ToLower(strings.TrimSpace(header))

		if header == "(request-target)" {
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

func parsePrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	priv, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
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

func GenerateKeyPair() (publicKeyPEM, privateKeyPEM string, err error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 3072)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate key pair: %w", err)
	}

	privateKeyBytes := x509.MarshalPKCS1PrivateKey(privateKey)
	privateKeyBlock := &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: privateKeyBytes,
	}
	privateKeyPEM = string(pem.EncodeToMemory(privateKeyBlock))

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
