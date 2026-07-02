package secretbox

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"testing"
)

func newTestKEK(t *testing.T) []byte {
	t.Helper()
	kek := make([]byte, 32)
	if _, err := rand.Read(kek); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return kek
}

func TestSealOpenRoundTrip(t *testing.T) {
	c, err := NewCipher(newTestKEK(t))
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	plain := []byte("-----BEGIN PRIVATE KEY----- ... -----END PRIVATE KEY-----")
	sealed, err := c.Seal(plain)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !IsSealed(sealed) {
		t.Fatalf("sealed value %q not enc-prefixed", sealed)
	}
	got, err := c.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("round trip = %q, want %q", got, plain)
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	c, _ := NewCipher(newTestKEK(t))
	a, _ := c.Seal([]byte("same"))
	b, _ := c.Seal([]byte("same"))
	if a == b {
		t.Error("two seals of the same plaintext are identical — nonce is not random")
	}
}

func TestOpenWrongKEKFails(t *testing.T) {
	c1, _ := NewCipher(newTestKEK(t))
	c2, _ := NewCipher(newTestKEK(t))
	sealed, _ := c1.Seal([]byte("secret"))
	if _, err := c2.Open(sealed); err == nil {
		t.Error("Open with the wrong KEK must fail (GCM auth)")
	}
}

func TestOpenTamperFails(t *testing.T) {
	c, _ := NewCipher(newTestKEK(t))
	sealed, _ := c.Seal([]byte("secret"))
	// Flip a byte in the base64 body.
	body := []byte(sealed[len(EncPrefix):])
	body[len(body)-2] ^= 0x01
	if _, err := c.Open(EncPrefix + string(body)); err == nil {
		t.Error("Open of tampered ciphertext must fail")
	}
}

func TestOpenRejectsNonPrefixed(t *testing.T) {
	c, _ := NewCipher(newTestKEK(t))
	if _, err := c.Open("not-encrypted-raw-pem"); err == nil {
		t.Error("Open of a non-enc value must fail")
	}
}

func TestNewCipherRejectsWrongLength(t *testing.T) {
	if _, err := NewCipher(make([]byte, 16)); err == nil {
		t.Error("NewCipher must reject a non-32-byte KEK")
	}
}

func TestNewCipherFromBase64(t *testing.T) {
	kek := newTestKEK(t)
	c, err := NewCipherFromBase64(base64.StdEncoding.EncodeToString(kek))
	if err != nil {
		t.Fatalf("NewCipherFromBase64: %v", err)
	}
	sealed, _ := c.Seal([]byte("hi"))
	if _, err := c.Open(sealed); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if _, err := NewCipherFromBase64("!!!not-base64!!!"); err == nil {
		t.Error("NewCipherFromBase64 must reject invalid base64")
	}
}

func TestIsSealed(t *testing.T) {
	if IsSealed("raw-pem") {
		t.Error("raw value reported as sealed")
	}
	if !IsSealed(EncPrefix + "abc") {
		t.Error("enc-prefixed value not reported as sealed")
	}
}
