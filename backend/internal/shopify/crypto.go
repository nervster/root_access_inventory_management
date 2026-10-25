package shopify

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// Sealer encrypts stores' tokens before they're saved (AES-256-GCM), so the database alone can't
// reach anyone's store. Each token is bound to its store: a token copied to another row won't open.
type Sealer struct {
	aead cipher.AEAD
}

// NewSealer takes the key from SHOPIFY_TOKEN_KEY: 32 random bytes, base64-encoded
// (make one with: openssl rand -base64 32). Losing the key means every store must connect again.
func NewSealer(base64Key string) (*Sealer, error) {
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil || len(key) != 32 {
		return nil, errors.New("SHOPIFY_TOKEN_KEY must be 32 bytes, base64-encoded (make one with: openssl rand -base64 32)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead}, nil
}

// Seal encrypts token for shop. The result starts with a random nonce.
func (s *Sealer) Seal(shop, token string) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce) // never fails (crypto/rand panics instead)
	return s.aead.Seal(nonce, nonce, []byte(token), []byte(shop))
}

// Open decrypts what Seal made for shop.
func (s *Sealer) Open(shop string, sealed []byte) (string, error) {
	size := s.aead.NonceSize()
	if len(sealed) < size {
		return "", errors.New("shopify: sealed token too short")
	}
	token, err := s.aead.Open(nil, sealed[:size], sealed[size:], []byte(shop))
	if err != nil {
		return "", fmt.Errorf("shopify: can't decrypt token (was SHOPIFY_TOKEN_KEY changed?): %w", err)
	}
	return string(token), nil
}
