package auth

import (
	"encoding/base64"
	"testing"
)

func TestHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := CheckPassword("correct-horse-battery-staple", hash)
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if !ok {
		t.Fatal("expected password to match")
	}
}

func TestHashWrongPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := CheckPassword("wrong-password", hash)
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if ok {
		t.Fatal("expected password not to match")
	}
}

func TestHashUniqueSalts(t *testing.T) {
	h1, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	h2, err := HashPassword("same-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if h1 == h2 {
		t.Fatal("expected different encoded hashes for same password")
	}
}

func TestHashMalformed(t *testing.T) {
	cases := []string{"", "$argon2id$", "notahash"}
	for _, c := range cases {
		_, err := CheckPassword("anything", c)
		if err == nil {
			t.Errorf("expected error for malformed hash %q", c)
		}
	}
}

func TestHashCustomConfig(t *testing.T) {
	cfg := HashConfig{
		Time:        1,
		Memory:      16384,
		Parallelism: 1,
		KeyLength:   32,
		SaltLength:  16,
	}

	hash, err := HashPasswordWithConfig("test-password", cfg)
	if err != nil {
		t.Fatalf("HashPasswordWithConfig: %v", err)
	}

	ok, err := CheckPassword("test-password", hash)
	if err != nil {
		t.Fatalf("CheckPassword: %v", err)
	}
	if !ok {
		t.Fatal("expected password to match with custom config")
	}
}

func TestGenerateTokenLength(t *testing.T) {
	tok, err := GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("decoding token: %v", err)
	}
	if len(b) != 32 {
		t.Fatalf("expected 32 bytes, got %d", len(b))
	}
}

func TestGenerateTokenN(t *testing.T) {
	tok, err := GenerateTokenN(64)
	if err != nil {
		t.Fatalf("GenerateTokenN: %v", err)
	}

	b, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("decoding token: %v", err)
	}
	if len(b) != 64 {
		t.Fatalf("expected 64 bytes, got %d", len(b))
	}
}

func TestGenerateTokenUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for range 100 {
		tok, err := GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		if _, ok := seen[tok]; ok {
			t.Fatal("duplicate token generated")
		}
		seen[tok] = struct{}{}
	}
}

func TestGenerateTokenZeroLength(t *testing.T) {
	_, err := GenerateTokenN(0)
	if err == nil {
		t.Fatal("expected error for zero length")
	}
}
