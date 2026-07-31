package identity

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

const opaqueTokenBytes = 32

func NewOpaqueToken(random io.Reader) (raw string, hash []byte, err error) {
	if random == nil {
		random = rand.Reader
	}

	token := make([]byte, opaqueTokenBytes)
	if _, err := io.ReadFull(random, token); err != nil {
		return "", nil, fmt.Errorf("generate opaque token: %w", err)
	}

	sum := sha256.Sum256(token)
	return base64.RawURLEncoding.EncodeToString(token), sum[:], nil
}

func HashOpaqueToken(raw string) ([]byte, error) {
	token, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode opaque token: %w", err)
	}
	if len(token) != opaqueTokenBytes {
		return nil, fmt.Errorf("opaque token length is invalid")
	}
	sum := sha256.Sum256(token)
	return sum[:], nil
}
