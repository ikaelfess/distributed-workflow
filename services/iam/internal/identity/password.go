package identity

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

var (
	ErrPasswordTooShort = errors.New("password is too short")
	ErrPasswordTooLong  = errors.New("password is too long")
)

type PasswordPolicy struct {
	Time    uint32
	Memory  uint32
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

type PasswordHasher struct {
	policy PasswordPolicy
	random io.Reader
}

func NewPasswordHasher(policy PasswordPolicy) *PasswordHasher {
	if policy.Time == 0 {
		policy.Time = 1
	}
	if policy.Memory == 0 {
		policy.Memory = 64 * 1024
	}
	if policy.Threads == 0 {
		policy.Threads = 1
	}
	if policy.KeyLen == 0 {
		policy.KeyLen = 32
	}
	if policy.SaltLen == 0 {
		policy.SaltLen = 16
	}

	return &PasswordHasher{
		policy: policy,
		random: rand.Reader,
	}
}

func (h *PasswordHasher) Hash(password string) (string, error) {
	if err := ValidatePasswordLength(password); err != nil {
		return "", err
	}

	salt := make([]byte, h.policy.SaltLen)
	if _, err := io.ReadFull(h.random, salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}

	hash := argon2.IDKey(
		[]byte(password),
		salt,
		h.policy.Time,
		h.policy.Memory,
		h.policy.Threads,
		h.policy.KeyLen,
	)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.policy.Memory,
		h.policy.Time,
		h.policy.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

func (h *PasswordHasher) Verify(encoded, password string) bool {
	memory, timeCost, threads, salt, hash, err := decodeArgon2id(encoded)
	if err != nil {
		return false
	}

	computed := argon2.IDKey([]byte(password), salt, timeCost, memory, threads, uint32(len(hash)))
	return subtle.ConstantTimeCompare(hash, computed) == 1
}

func ValidatePasswordLength(password string) error {
	length := len([]rune(password))
	if length < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if length > MaxPasswordLength {
		return ErrPasswordTooLong
	}
	return nil
}

func decodeArgon2id(encoded string) (uint32, uint32, uint8, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return 0, 0, 0, nil, nil, errors.New("invalid argon2id encoding")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("parse argon2id version: %w", err)
	}
	if version != argon2.Version {
		return 0, 0, 0, nil, nil, errors.New("unsupported argon2id version")
	}

	var memory, timeCost uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &timeCost, &threads); err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("parse argon2id parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("decode argon2id salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return 0, 0, 0, nil, nil, fmt.Errorf("decode argon2id hash: %w", err)
	}
	return memory, timeCost, threads, salt, hash, nil
}
