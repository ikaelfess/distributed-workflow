package delivery_test

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"testing"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/delivery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewEmailDeliveryEvent(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	encryptor, err := delivery.NewEnvelopeEncryptor("notifications-2026-01", &privateKey.PublicKey)
	require.NoError(t, err)

	occurredAt := time.Date(2026, time.July, 31, 2, 30, 0, 0, time.UTC)
	payload := delivery.EmailPayload{
		Recipient: "user@example.com",
		Template:  "verify-email",
		Variables: map[string]string{"challenge": "raw-secret"},
	}

	event, err := delivery.NewEmailDeliveryEvent(occurredAt, payload, encryptor)
	require.NoError(t, err)

	assert.Equal(t, delivery.EmailDeliveryEventType, event.Type)
	assert.Equal(t, 1, event.SchemaVersion)
	assert.Equal(t, occurredAt, event.OccurredAt)
	assert.NotEmpty(t, event.ID)

	encoded, err := json.Marshal(event)
	require.NoError(t, err)
	assert.False(t, bytes.Contains(encoded, []byte("user@example.com")))
	assert.False(t, bytes.Contains(encoded, []byte("raw-secret")))

	decrypted, err := delivery.DecryptEnvelope(
		privateKey,
		event.AssociatedData(),
		event.Envelope,
	)
	require.NoError(t, err)

	var delivered delivery.EmailPayload
	require.NoError(t, json.Unmarshal(decrypted, &delivered))
	assert.Equal(t, payload, delivered)
}

func TestEmailDeliveryEvent_AuthenticatesOccurredAt(t *testing.T) {
	t.Parallel()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	encryptor, err := delivery.NewEnvelopeEncryptor("notifications-2026-01", &privateKey.PublicKey)
	require.NoError(t, err)

	event, err := delivery.NewEmailDeliveryEvent(
		time.Date(2026, time.July, 31, 2, 30, 0, 0, time.UTC),
		delivery.EmailPayload{
			Recipient: "user@example.com",
			Template:  "verify-email",
			Variables: map[string]string{"challenge": "raw-secret"},
		},
		encryptor,
	)
	require.NoError(t, err)
	event.OccurredAt = event.OccurredAt.Add(time.Hour)

	_, err = delivery.DecryptEnvelope(privateKey, event.AssociatedData(), event.Envelope)
	require.Error(t, err)
}
