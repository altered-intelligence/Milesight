package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
)

const cipherVersion byte = 1

// Cipher seals secrets (Tesla access/refresh tokens) at rest with AES-256-GCM.
// Format: version(1) || nonce(12) || ciphertext+tag. The associated data binds a
// ciphertext to its owner so rows cannot be swapped between users.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a cipher from a 32-byte key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("auth: encryption key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// Seal encrypts plaintext bound to associatedData (e.g. the user id).
func (c *Cipher) Seal(plaintext, associatedData []byte) ([]byte, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{cipherVersion}, nonce...)
	return c.aead.Seal(out, nonce, plaintext, associatedData), nil
}

// Open decrypts a value produced by Seal.
func (c *Cipher) Open(sealed, associatedData []byte) ([]byte, error) {
	ns := c.aead.NonceSize()
	if len(sealed) < 1+ns+c.aead.Overhead() || sealed[0] != cipherVersion {
		return nil, errors.New("auth: malformed ciphertext")
	}
	return c.aead.Open(nil, sealed[1:1+ns], sealed[1+ns:], associatedData)
}

// HashToken returns the SHA-256 of an opaque token (refresh tokens are stored hashed).
func HashToken(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// PKCE returns a code verifier and its S256 challenge (RFC 7636).
func PKCE() (verifier, challenge string) {
	b := make([]byte, 48)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}
