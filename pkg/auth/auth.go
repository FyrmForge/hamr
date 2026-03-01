// Package auth provides password hashing with Argon2id and
// cryptographically-secure token generation.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// HashConfig holds the tuneable parameters for Argon2id hashing.
type HashConfig struct {
	Time        uint32
	Memory      uint32
	Parallelism uint8
	KeyLength   uint32
	SaltLength  uint32
}

// DefaultHashConfig is a sensible default for production use.
var DefaultHashConfig = HashConfig{
	Time:        3,
	Memory:      64 * 1024,
	Parallelism: 2,
	KeyLength:   32,
	SaltLength:  16,
}

// HashPassword hashes password using DefaultHashConfig.
func HashPassword(password string) (string, error) {
	return HashPasswordWithConfig(password, DefaultHashConfig)
}

// HashPasswordWithConfig hashes password with the given Argon2id parameters and
// returns a PHC-format encoded string.
func HashPasswordWithConfig(password string, cfg HashConfig) (string, error) {
	salt := make([]byte, cfg.SaltLength)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: generating salt: %w", err)
	}

	hash := argon2.IDKey([]byte(password), salt, cfg.Time, cfg.Memory, cfg.Parallelism, cfg.KeyLength)

	encoded := fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		cfg.Memory, cfg.Time, cfg.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	)
	return encoded, nil
}

// CheckPassword verifies password against an Argon2id PHC-format encoded hash.
// It returns (true, nil) on match, (false, nil) on mismatch, or an error if
// the encoded hash cannot be parsed.
func CheckPassword(password, encodedHash string) (bool, error) {
	cfg, salt, hash, err := decodePHC(encodedHash)
	if err != nil {
		return false, err
	}

	candidate := argon2.IDKey([]byte(password), salt, cfg.Time, cfg.Memory, cfg.Parallelism, cfg.KeyLength)

	if subtle.ConstantTimeCompare(hash, candidate) == 1 {
		return true, nil
	}
	return false, nil
}

// decodePHC parses a PHC-format Argon2id string into its components.
func decodePHC(encoded string) (HashConfig, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 {
		return HashConfig{}, nil, nil, errors.New("auth: invalid hash format")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return HashConfig{}, nil, nil, fmt.Errorf("auth: parsing version: %w", err)
	}

	var cfg HashConfig
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &cfg.Memory, &cfg.Time, &cfg.Parallelism); err != nil {
		return HashConfig{}, nil, nil, fmt.Errorf("auth: parsing params: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return HashConfig{}, nil, nil, fmt.Errorf("auth: decoding salt: %w", err)
	}

	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return HashConfig{}, nil, nil, fmt.Errorf("auth: decoding hash: %w", err)
	}

	cfg.KeyLength = uint32(len(hash))
	cfg.SaltLength = uint32(len(salt))

	return cfg, salt, hash, nil
}

// GenerateToken returns a 32-byte cryptographically-secure random token
// encoded with base64 raw-URL encoding.
func GenerateToken() (string, error) {
	return GenerateTokenN(32)
}

// GenerateTokenN returns an n-byte cryptographically-secure random token
// encoded with base64 raw-URL encoding.
func GenerateTokenN(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("auth: token length must be positive")
	}
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: generating token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
