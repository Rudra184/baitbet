// Package secrets encrypts small at-rest values (SMTP passwords) with
// AES-256-GCM under a single server-held key.
package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
)

// Box encrypts and decrypts with a fixed 32-byte key.
type Box struct {
	gcm cipher.AEAD
}

// RandomKey generates a fresh base64-encoded 32-byte key for standalone/dev
// runs where SIM_SECRET_KEY isn't set.
func RandomKey() string {
	key := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		panic(err)
	}
	return base64.StdEncoding.EncodeToString(key)
}

// NewBox builds a Box from a base64-encoded 32-byte key (SIM_SECRET_KEY).
func NewBox(base64Key string) (*Box, error) {
	key, err := base64.StdEncoding.DecodeString(base64Key)
	if err != nil {
		return nil, errors.New("secrets: key is not valid base64")
	}
	if len(key) != 32 {
		return nil, errors.New("secrets: key must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{gcm: gcm}, nil
}

// Encrypt returns a base64 string safe to store in a text column. Encrypting
// an empty string returns an empty string (no secret to protect).
func (b *Box) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}
	nonce := make([]byte, b.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := b.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt reverses Encrypt. An empty input returns an empty string.
func (b *Box) Decrypt(stored string) (string, error) {
	if stored == "" {
		return "", nil
	}
	data, err := base64.StdEncoding.DecodeString(stored)
	if err != nil {
		return "", errors.New("secrets: stored value is not valid base64")
	}
	nonceSize := b.gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("secrets: stored value too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plain, err := b.gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", errors.New("secrets: decryption failed")
	}
	return string(plain), nil
}
