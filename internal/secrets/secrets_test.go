package secrets

import (
	"encoding/base64"
	"testing"
)

func testBox(t *testing.T) *Box {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	b, err := NewBox(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return b
}

func TestRoundTrip(t *testing.T) {
	b := testBox(t)
	enc, err := b.Encrypt("hunter2")
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if enc == "hunter2" {
		t.Fatal("ciphertext must not equal plaintext")
	}
	dec, err := b.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec != "hunter2" {
		t.Fatalf("got %q, want %q", dec, "hunter2")
	}
}

func TestEmptyRoundTrip(t *testing.T) {
	b := testBox(t)
	enc, err := b.Encrypt("")
	if err != nil || enc != "" {
		t.Fatalf("Encrypt(\"\") = %q, %v", enc, err)
	}
	dec, err := b.Decrypt("")
	if err != nil || dec != "" {
		t.Fatalf("Decrypt(\"\") = %q, %v", dec, err)
	}
}

func TestBadKeyRejected(t *testing.T) {
	if _, err := NewBox("not-base64!!!"); err == nil {
		t.Fatal("expected error for invalid base64 key")
	}
	if _, err := NewBox(base64.StdEncoding.EncodeToString([]byte("short"))); err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestTamperedCiphertextRejected(t *testing.T) {
	b := testBox(t)
	enc, _ := b.Encrypt("hunter2")
	raw, _ := base64.StdEncoding.DecodeString(enc)
	raw[len(raw)-1] ^= 0xFF
	tampered := base64.StdEncoding.EncodeToString(raw)
	if _, err := b.Decrypt(tampered); err == nil {
		t.Fatal("expected decryption failure for tampered ciphertext")
	}
}
