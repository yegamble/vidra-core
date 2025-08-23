package crypto

import (
	"bytes"
	"crypto"
	"fmt"
	"io"
	"strings"

	"github.com/ProtonMail/gopenpgp/v2/pgp"
)

// PGPService handles PGP encryption, decryption, and signature operations
type PGPService struct{}

// NewPGPService creates a new PGP service instance
func NewPGPService() *PGPService {
	return &PGPService{}
}

// ValidatePGPPublicKey validates that a PGP public key is properly formatted and usable
func (p *PGPService) ValidatePGPPublicKey(publicKeyArmored string) error {
	// Clean the key string
	publicKeyArmored = strings.TrimSpace(publicKeyArmored)

	// Try to parse the key
	keyRing, err := pgp.NewKeyFromArmored(publicKeyArmored)
	if err != nil {
		return fmt.Errorf("invalid PGP public key format: %w", err)
	}

	// Verify it's a public key (not private)
	if keyRing.IsPrivate() {
		return fmt.Errorf("provided key appears to be a private key, only public keys are allowed")
	}

	// Check if the key is expired
	if keyRing.IsExpired() {
		return fmt.Errorf("PGP public key is expired")
	}

	// Check if the key is revoked
	if keyRing.IsRevoked() {
		return fmt.Errorf("PGP public key is revoked")
	}

	return nil
}

// EncryptMessage encrypts a message using the recipient's PGP public key
func (p *PGPService) EncryptMessage(message string, recipientPublicKey string) (string, error) {
	// Parse recipient's public key
	recipientKey, err := pgp.NewKeyFromArmored(recipientPublicKey)
	if err != nil {
		return "", fmt.Errorf("failed to parse recipient public key: %w", err)
	}

	// Create a key ring with the recipient's key
	recipientKeyRing, err := pgp.NewKeyRing(recipientKey)
	if err != nil {
		return "", fmt.Errorf("failed to create key ring: %w", err)
	}

	// Encrypt the message
	encryptedMessage, err := recipientKeyRing.Encrypt(
		pgp.NewPlainMessageFromString(message),
		nil, // No private key for signing in this basic version
	)
	if err != nil {
		return "", fmt.Errorf("failed to encrypt message: %w", err)
	}

	// Return the armored encrypted message
	armored, err := encryptedMessage.GetArmored()
	if err != nil {
		return "", fmt.Errorf("failed to armor encrypted message: %w", err)
	}

	return armored, nil
}

// DecryptMessage decrypts a message using the recipient's private key
// Note: This would typically be done client-side, but included for completeness
func (p *PGPService) DecryptMessage(encryptedMessage string, privateKeyArmored string, passphrase string) (string, error) {
	// Parse the private key
	privateKey, err := pgp.NewKeyFromArmored(privateKeyArmored)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// Unlock the private key with passphrase
	unlockedKey, err := privateKey.Unlock([]byte(passphrase))
	if err != nil {
		return "", fmt.Errorf("failed to unlock private key: %w", err)
	}

	// Create key ring
	keyRing, err := pgp.NewKeyRing(unlockedKey)
	if err != nil {
		return "", fmt.Errorf("failed to create key ring: %w", err)
	}

	// Parse the encrypted message
	encryptedPGPMessage, err := pgp.NewPGPMessageFromArmored(encryptedMessage)
	if err != nil {
		return "", fmt.Errorf("failed to parse encrypted message: %w", err)
	}

	// Decrypt the message
	decryptedMessage, err := keyRing.Decrypt(encryptedPGPMessage, nil, 0)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt message: %w", err)
	}

	return decryptedMessage.GetString(), nil
}

// SignMessage creates a detached signature for a message using a private key
func (p *PGPService) SignMessage(message string, privateKeyArmored string, passphrase string) (string, error) {
	// Parse the private key
	privateKey, err := pgp.NewKeyFromArmored(privateKeyArmored)
	if err != nil {
		return "", fmt.Errorf("failed to parse private key: %w", err)
	}

	// Unlock the private key with passphrase
	unlockedKey, err := privateKey.Unlock([]byte(passphrase))
	if err != nil {
		return "", fmt.Errorf("failed to unlock private key: %w", err)
	}

	// Create key ring for signing
	signingKeyRing, err := pgp.NewKeyRing(unlockedKey)
	if err != nil {
		return "", fmt.Errorf("failed to create signing key ring: %w", err)
	}

	// Create a plain message
	plainMessage := pgp.NewPlainMessageFromString(message)

	// Create detached signature
	signature, err := signingKeyRing.SignDetached(plainMessage)
	if err != nil {
		return "", fmt.Errorf("failed to create signature: %w", err)
	}

	// Return armored signature
	armoredSignature, err := signature.GetArmored()
	if err != nil {
		return "", fmt.Errorf("failed to armor signature: %w", err)
	}

	return armoredSignature, nil
}

// VerifySignature verifies a detached signature using the sender's public key
func (p *PGPService) VerifySignature(message string, signature string, senderPublicKey string) error {
	// Parse sender's public key
	senderKey, err := pgp.NewKeyFromArmored(senderPublicKey)
	if err != nil {
		return fmt.Errorf("failed to parse sender public key: %w", err)
	}

	// Create key ring for verification
	verifyKeyRing, err := pgp.NewKeyRing(senderKey)
	if err != nil {
		return fmt.Errorf("failed to create verification key ring: %w", err)
	}

	// Parse the signature
	pgpSignature, err := pgp.NewPGPSignatureFromArmored(signature)
	if err != nil {
		return fmt.Errorf("failed to parse signature: %w", err)
	}

	// Create plain message
	plainMessage := pgp.NewPlainMessageFromString(message)

	// Verify the signature
	err = verifyKeyRing.VerifyDetached(plainMessage, pgpSignature, 0)
	if err != nil {
		return fmt.Errorf("signature verification failed: %w", err)
	}

	return nil
}

// EncryptAndSignMessage encrypts a message and creates a signature
func (p *PGPService) EncryptAndSignMessage(message string, recipientPublicKey string, senderPrivateKey string, passphrase string) (encryptedMessage string, signature string, err error) {
	// First encrypt the message
	encryptedMessage, err = p.EncryptMessage(message, recipientPublicKey)
	if err != nil {
		return "", "", fmt.Errorf("encryption failed: %w", err)
	}

	// Then sign the original message (not the encrypted version)
	signature, err = p.SignMessage(message, senderPrivateKey, passphrase)
	if err != nil {
		return "", "", fmt.Errorf("signing failed: %w", err)
	}

	return encryptedMessage, signature, nil
}

// GetKeyFingerprint extracts the fingerprint from a PGP public key
func (p *PGPService) GetKeyFingerprint(publicKeyArmored string) (string, error) {
	key, err := pgp.NewKeyFromArmored(publicKeyArmored)
	if err != nil {
		return "", fmt.Errorf("failed to parse public key: %w", err)
	}

	return key.GetFingerprint(), nil
}

// GetKeyID extracts the key ID from a PGP public key
func (p *PGPService) GetKeyID(publicKeyArmored string) (string, error) {
	key, err := pgp.NewKeyFromArmored(publicKeyArmored)
	if err != nil {
		return "", fmt.Errorf("failed to parse public key: %w", err)
	}

	return key.GetKeyID(), nil
}
