package delivery

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
)

const (
	EnvelopeAlgorithm = "RSA-OAEP-256+A256GCM"
	contentKeySize    = 32
	minimumRSAKeyBits = 2048
)

type Envelope struct {
	Algorithm  string `json:"algorithm"`
	KeyID      string `json:"key_id"`
	WrappedKey []byte `json:"wrapped_key"`
	Nonce      []byte `json:"nonce"`
	Ciphertext []byte `json:"ciphertext"`
}

type EnvelopeEncryptor struct {
	keyID     string
	publicKey *rsa.PublicKey
	random    io.Reader
}

func NewEnvelopeEncryptor(keyID string, publicKey *rsa.PublicKey) (*EnvelopeEncryptor, error) {
	if keyID == "" {
		return nil, errors.New("key id is required")
	}
	if publicKey == nil {
		return nil, errors.New("public key is required")
	}
	if publicKey.N.BitLen() < minimumRSAKeyBits {
		return nil, fmt.Errorf("public key must be at least %d bits", minimumRSAKeyBits)
	}

	return &EnvelopeEncryptor{
		keyID:     keyID,
		publicKey: publicKey,
		random:    rand.Reader,
	}, nil
}

func ParseEnvelopePublicKey(value []byte) (*rsa.PublicKey, error) {
	block, remaining := pem.Decode(value)
	if block == nil {
		return nil, errors.New("decode public key pem")
	}
	if len(bytes.TrimSpace(remaining)) != 0 {
		return nil, errors.New("public key pem contains trailing data")
	}
	if block.Type != "PUBLIC KEY" {
		return nil, errors.New("public key pem must contain a public key")
	}

	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse public key: %w", err)
	}
	publicKey, ok := parsed.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("public key must be rsa")
	}
	if publicKey.N.BitLen() < minimumRSAKeyBits {
		return nil, fmt.Errorf("public key must be at least %d bits", minimumRSAKeyBits)
	}
	return publicKey, nil
}

func (e *EnvelopeEncryptor) Encrypt(associatedData, plaintext []byte) (Envelope, error) {
	if len(associatedData) == 0 {
		return Envelope{}, errors.New("associated data is required")
	}
	if len(plaintext) == 0 {
		return Envelope{}, errors.New("plaintext is required")
	}
	authenticatedData := envelopeAssociatedData(
		associatedData,
		EnvelopeAlgorithm,
		e.keyID,
	)

	contentKey := make([]byte, contentKeySize)
	defer clear(contentKey)
	if _, err := io.ReadFull(e.random, contentKey); err != nil {
		return Envelope{}, fmt.Errorf("generate content key: %w", err)
	}

	block, err := aes.NewCipher(contentKey)
	if err != nil {
		return Envelope{}, fmt.Errorf("create content cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Envelope{}, fmt.Errorf("create content aead: %w", err)
	}

	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(e.random, nonce); err != nil {
		return Envelope{}, fmt.Errorf("generate content nonce: %w", err)
	}

	wrappedKey, err := rsa.EncryptOAEP(
		sha256.New(),
		e.random,
		e.publicKey,
		contentKey,
		authenticatedData,
	)
	if err != nil {
		return Envelope{}, fmt.Errorf("wrap content key: %w", err)
	}

	return Envelope{
		Algorithm:  EnvelopeAlgorithm,
		KeyID:      e.keyID,
		WrappedKey: wrappedKey,
		Nonce:      nonce,
		Ciphertext: aead.Seal(nil, nonce, plaintext, authenticatedData),
	}, nil
}

func DecryptEnvelope(
	privateKey *rsa.PrivateKey,
	associatedData []byte,
	envelope Envelope,
) ([]byte, error) {
	if privateKey == nil {
		return nil, errors.New("private key is required")
	}
	if envelope.Algorithm != EnvelopeAlgorithm {
		return nil, errors.New("unsupported envelope algorithm")
	}
	if envelope.KeyID == "" {
		return nil, errors.New("key id is required")
	}
	if len(associatedData) == 0 {
		return nil, errors.New("associated data is required")
	}
	authenticatedData := envelopeAssociatedData(
		associatedData,
		envelope.Algorithm,
		envelope.KeyID,
	)

	contentKey, err := rsa.DecryptOAEP(
		sha256.New(),
		rand.Reader,
		privateKey,
		envelope.WrappedKey,
		authenticatedData,
	)
	if err != nil {
		return nil, fmt.Errorf("unwrap content key: %w", err)
	}
	defer clear(contentKey)
	if len(contentKey) != contentKeySize {
		return nil, errors.New("invalid content key length")
	}

	block, err := aes.NewCipher(contentKey)
	if err != nil {
		return nil, fmt.Errorf("create content cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create content aead: %w", err)
	}
	if len(envelope.Nonce) != aead.NonceSize() {
		return nil, errors.New("invalid content nonce length")
	}

	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, authenticatedData)
	if err != nil {
		return nil, fmt.Errorf("decrypt content: %w", err)
	}
	return plaintext, nil
}

func envelopeAssociatedData(base []byte, algorithm, keyID string) []byte {
	value := make([]byte, 0, len(base)+len(algorithm)+len(keyID)+2)
	value = append(value, base...)
	value = append(value, ':')
	value = append(value, algorithm...)
	value = append(value, ':')
	value = append(value, keyID...)
	return value
}
