//go:build !pgp
// +build !pgp

package crypto

import (
	"errors"
	"strings"
)

// PGPService is a stub when built without the `pgp` build tag.
// It performs minimal validation and returns placeholders for crypto operations.
type PGPService struct{}

func NewPGPService() *PGPService { return &PGPService{} }

func (p *PGPService) ValidatePGPPublicKey(publicKeyArmored string) error {
	s := strings.TrimSpace(publicKeyArmored)
	if s == "" {
		return errors.New("empty key")
	}
	if !strings.Contains(s, "BEGIN PGP PUBLIC KEY BLOCK") {
		return errors.New("not a PGP public key block")
	}
	return nil
}

func (p *PGPService) EncryptMessage(message string, recipientPublicKey string) (string, error) {
	return "[encrypted]" + message, nil
}

func (p *PGPService) DecryptMessage(encryptedMessage string, privateKeyArmored string, passphrase string) (string, error) {
	return strings.TrimPrefix(encryptedMessage, "[encrypted]"), nil
}

func (p *PGPService) SignMessage(message string, privateKeyArmored string, passphrase string) (string, error) {
	return "[signature]", nil
}

func (p *PGPService) VerifySignature(message string, signature string, senderPublicKey string) error {
	if signature == "" {
		return errors.New("missing signature")
	}
	return nil
}

func (p *PGPService) EncryptAndSignMessage(message string, recipientPublicKey string, senderPrivateKey string, passphrase string) (string, string, error) {
	enc, _ := p.EncryptMessage(message, recipientPublicKey)
	sig, _ := p.SignMessage(message, senderPrivateKey, passphrase)
	return enc, sig, nil
}

func (p *PGPService) GetKeyFingerprint(publicKeyArmored string) (string, error) {
	return "FAKEFINGERPRINT1234", nil
}
func (p *PGPService) GetKeyID(publicKeyArmored string) (string, error) { return "FAKEKEYID5678", nil }

func (p *PGPService) GenerateKeyPair(name, email string) (string, string, string, error) {
	pub := "-----BEGIN PGP PUBLIC KEY BLOCK-----\n...\n-----END PGP PUBLIC KEY BLOCK-----"
	priv := "-----BEGIN PGP PRIVATE KEY BLOCK-----\n...\n-----END PGP PRIVATE KEY BLOCK-----"
	fp := "FAKEFINGERPRINT1234"
	return pub, priv, fp, nil
}
