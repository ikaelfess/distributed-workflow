package delivery_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/delivery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnvelopeEncryptor_Encrypt(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	encryptor, err := delivery.NewEnvelopeEncryptor("notifications-2026-01", &privateKey.PublicKey)
	require.NoError(t, err)

	associatedData := []byte("email-delivery-request.v1:0198f3c8-72bb-7a11-a439-c49e49ad14e8")
	plaintext := []byte(`{"recipient":"user@example.com","challenge":"raw-secret"}`)

	envelope, err := encryptor.Encrypt(associatedData, plaintext)
	require.NoError(t, err)

	assert.Equal(t, "RSA-OAEP-256+A256GCM", envelope.Algorithm)
	assert.Equal(t, "notifications-2026-01", envelope.KeyID)
	assert.False(t, bytes.Contains(envelope.Ciphertext, []byte("raw-secret")))

	decrypted, err := delivery.DecryptEnvelope(privateKey, associatedData, envelope)
	require.NoError(t, err)
	assert.Equal(t, plaintext, decrypted)
}

func TestDecryptEnvelope_RejectsTampering(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	encryptor, err := delivery.NewEnvelopeEncryptor("notifications-2026-01", &privateKey.PublicKey)
	require.NoError(t, err)

	envelope, err := encryptor.Encrypt([]byte("event-1"), []byte("secret"))
	require.NoError(t, err)
	envelope.Ciphertext[len(envelope.Ciphertext)-1] ^= 0xff

	_, err = delivery.DecryptEnvelope(privateKey, []byte("event-1"), envelope)
	require.Error(t, err)
}

func TestDecryptEnvelope_AuthenticatesKeyID(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	encryptor, err := delivery.NewEnvelopeEncryptor("notifications-2026-01", &privateKey.PublicKey)
	require.NoError(t, err)
	envelope, err := encryptor.Encrypt([]byte("event-1"), []byte("secret"))
	require.NoError(t, err)
	envelope.KeyID = "attacker-selected-key"

	_, err = delivery.DecryptEnvelope(privateKey, []byte("event-1"), envelope)
	require.Error(t, err)
}

func TestParseEnvelopePublicKey(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	encodedKey, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	require.NoError(t, err)

	publicKey, err := delivery.ParseEnvelopePublicKey(pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: encodedKey,
	}))
	require.NoError(t, err)
	assert.Equal(t, privateKey.PublicKey.N, publicKey.N)
	assert.Equal(t, privateKey.PublicKey.E, publicKey.E)
}
