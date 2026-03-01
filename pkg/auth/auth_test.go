package auth

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// HashPassword / CheckPassword
// ---------------------------------------------------------------------------

func TestHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	require.NoError(t, err)

	ok, err := CheckPassword("correct-horse-battery-staple", hash)
	require.NoError(t, err)
	assert.True(t, ok, "password should match")
}

func TestHashWrongPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery-staple")
	require.NoError(t, err)

	ok, err := CheckPassword("wrong-password", hash)
	require.NoError(t, err)
	assert.False(t, ok, "different password should not match")
}

func TestHashUniqueSalts(t *testing.T) {
	h1, err := HashPassword("same-password")
	require.NoError(t, err)
	h2, err := HashPassword("same-password")
	require.NoError(t, err)
	assert.NotEqual(t, h1, h2, "same password should produce different hashes")
}

func TestHashMalformed(t *testing.T) {
	cases := []string{"", "$argon2id$", "notahash"}
	for _, c := range cases {
		_, err := CheckPassword("anything", c)
		assert.Error(t, err, "malformed hash %q should error", c)
	}
}

func TestHashMalformedVariants(t *testing.T) {
	tests := []struct {
		name string
		hash string
	}{
		{"bad version", "$argon2id$v=ZZ$m=65536,t=3,p=2$c2FsdA$aGFzaA"},
		{"bad params", "$argon2id$v=19$garbage$c2FsdA$aGFzaA"},
		{"bad base64 salt", "$argon2id$v=19$m=65536,t=3,p=2$!!!invalid!!!$aGFzaA"},
		{"bad base64 hash", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$!!!invalid!!!"},
		{"too few parts", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA"},
		{"too many parts", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA$extra"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CheckPassword("anything", tt.hash)
			assert.Error(t, err)
		})
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
	require.NoError(t, err)

	ok, err := CheckPassword("test-password", hash)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestHashCustomConfigParamsEncoded(t *testing.T) {
	cfg := HashConfig{
		Time:        1,
		Memory:      16384,
		Parallelism: 1,
		KeyLength:   32,
		SaltLength:  16,
	}

	hash, err := HashPasswordWithConfig("test", cfg)
	require.NoError(t, err)
	assert.Contains(t, hash, "m=16384,t=1,p=1")
}

func TestHashPasswordEmpty(t *testing.T) {
	hash, err := HashPassword("")
	require.NoError(t, err)

	ok, err := CheckPassword("", hash)
	require.NoError(t, err)
	assert.True(t, ok, "empty password should match")

	ok, err = CheckPassword("not-empty", hash)
	require.NoError(t, err)
	assert.False(t, ok, "non-empty should not match empty hash")
}

func TestHashPHCFormat(t *testing.T) {
	hash, err := HashPassword("test")
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(hash, "$argon2id$v=19$"), "should start with PHC prefix")

	parts := strings.Split(hash, "$")
	require.Len(t, parts, 6, "PHC string should have 6 $-separated parts")

	// Salt and hash segments must be valid RawStdEncoding base64.
	_, err = base64.RawStdEncoding.DecodeString(parts[4])
	assert.NoError(t, err, "salt should be valid RawStdEncoding base64")

	_, err = base64.RawStdEncoding.DecodeString(parts[5])
	assert.NoError(t, err, "hash should be valid RawStdEncoding base64")
}

// ---------------------------------------------------------------------------
// GenerateToken / GenerateTokenN
// ---------------------------------------------------------------------------

func TestGenerateTokenLength(t *testing.T) {
	tok, err := GenerateToken()
	require.NoError(t, err)

	b, err := base64.RawURLEncoding.DecodeString(tok)
	require.NoError(t, err)
	assert.Len(t, b, 32)
}

func TestGenerateTokenN(t *testing.T) {
	tok, err := GenerateTokenN(64)
	require.NoError(t, err)

	b, err := base64.RawURLEncoding.DecodeString(tok)
	require.NoError(t, err)
	assert.Len(t, b, 64)
}

func TestGenerateTokenUniqueness(t *testing.T) {
	seen := make(map[string]struct{}, 100)
	for range 100 {
		tok, err := GenerateToken()
		require.NoError(t, err)
		assert.NotContains(t, seen, tok, "duplicate token")
		seen[tok] = struct{}{}
	}
}

func TestGenerateTokenZeroLength(t *testing.T) {
	_, err := GenerateTokenN(0)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token length must be positive")
}

func TestGenerateTokenNegativeLength(t *testing.T) {
	_, err := GenerateTokenN(-1)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "token length must be positive")
}

func TestGenerateTokenURLSafe(t *testing.T) {
	for range 50 {
		tok, err := GenerateToken()
		require.NoError(t, err)
		assert.False(t, strings.ContainsAny(tok, "+/="),
			"token should be URL-safe, got %q", tok)
	}
}
