package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCipherRoundTrip(t *testing.T) {
	key := make([]byte, KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	c, err := NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	plain := "P@ssw0rd-中文-!@#$%^&*()"
	enc, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if enc == plain || enc == "" {
		t.Fatal("ciphertext should differ from plaintext")
	}
	dec, err := c.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if dec != plain {
		t.Fatalf("decrypted = %q, want %q", dec, plain)
	}

	// 相同明文两次加密应产生不同密文（随机 nonce）
	enc2, _ := c.Encrypt(plain)
	if enc == enc2 {
		t.Fatal("expected different ciphertexts due to random nonce")
	}
}

func TestCipherWrongKey(t *testing.T) {
	key := make([]byte, KeySize)
	c1, _ := NewCipher(key)
	enc, _ := c1.Encrypt("secret")

	other := make([]byte, KeySize)
	other[0] = 1
	c2, _ := NewCipher(other)
	if _, err := c2.Decrypt(enc); err == nil {
		t.Fatal("expected decrypt failure with wrong key")
	}
}

func TestCipherEmpty(t *testing.T) {
	c, _ := NewCipher(make([]byte, KeySize))
	if enc, err := c.Encrypt(""); err != nil || enc != "" {
		t.Fatalf("Encrypt(\"\") = %q, %v", enc, err)
	}
	if dec, err := c.Decrypt(""); err != nil || dec != "" {
		t.Fatalf("Decrypt(\"\") = %q, %v", dec, err)
	}
}

func TestLoadOrCreateKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "master.key")
	k1, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	if len(k1) != KeySize {
		t.Fatalf("key size = %d", len(k1))
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != KeySize {
		t.Fatalf("file size = %d", info.Size())
	}
	k2, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey again: %v", err)
	}
	if string(k1) != string(k2) {
		t.Fatal("key should be stable across reads")
	}
}

func TestPasswordHashAndToken(t *testing.T) {
	hash, err := HashPassword("hello-world")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !VerifyPassword(hash, "hello-world") {
		t.Fatal("expected password to verify")
	}
	if VerifyPassword(hash, "wrong") {
		t.Fatal("wrong password should not verify")
	}

	p1, err := RandomPassword(24)
	if err != nil || len(p1) != 24 {
		t.Fatalf("RandomPassword: %q %v", p1, err)
	}
	p2, _ := RandomPassword(24)
	if p1 == p2 {
		t.Fatal("passwords should be random")
	}

	tok, err := RandomToken(32)
	if err != nil || tok == "" {
		t.Fatalf("RandomToken: %q %v", tok, err)
	}
}
