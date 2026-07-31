package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

const challengeTokenBytes = 32

func NewChallengeToken(random io.Reader) (raw string, hash []byte, err error) {
	if random == nil {
		random = rand.Reader
	}

	token := make([]byte, challengeTokenBytes)
	if _, err := io.ReadFull(random, token); err != nil {
		return "", nil, fmt.Errorf("generate challenge token: %w", err)
	}

	sum := sha256.Sum256(token)
	return base64.RawURLEncoding.EncodeToString(token), sum[:], nil
}

func HashChallengeToken(raw string) ([]byte, error) {
	token, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode challenge token: %w", err)
	}
	if len(token) != challengeTokenBytes {
		return nil, fmt.Errorf("challenge token length is invalid")
	}
	sum := sha256.Sum256(token)
	return sum[:], nil
}
