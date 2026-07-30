package delivery

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"
)

const (
	EmailDeliveryEventType     = "iam.email-delivery-request"
	EmailDeliverySchemaVersion = 1
)

type EmailPayload struct {
	Recipient string            `json:"recipient"`
	Template  string            `json:"template"`
	Variables map[string]string `json:"variables"`
}

type EmailDeliveryEvent struct {
	ID            string    `json:"event_id"`
	Type          string    `json:"event_type"`
	SchemaVersion int       `json:"schema_version"`
	OccurredAt    time.Time `json:"occurred_at"`
	Envelope      Envelope  `json:"envelope"`
}

func NewEmailDeliveryEvent(
	occurredAt time.Time,
	payload EmailPayload,
	encryptor *EnvelopeEncryptor,
) (EmailDeliveryEvent, error) {
	if occurredAt.IsZero() {
		return EmailDeliveryEvent{}, errors.New("occurred at is required")
	}
	if payload.Recipient == "" {
		return EmailDeliveryEvent{}, errors.New("recipient is required")
	}
	if payload.Template == "" {
		return EmailDeliveryEvent{}, errors.New("template is required")
	}
	if encryptor == nil {
		return EmailDeliveryEvent{}, errors.New("encryptor is required")
	}

	eventID, err := uuid.NewV7()
	if err != nil {
		return EmailDeliveryEvent{}, fmt.Errorf("generate event id: %w", err)
	}

	event := EmailDeliveryEvent{
		ID:            eventID.String(),
		Type:          EmailDeliveryEventType,
		SchemaVersion: EmailDeliverySchemaVersion,
		OccurredAt:    occurredAt.UTC(),
	}

	plaintext, err := json.Marshal(payload)
	if err != nil {
		return EmailDeliveryEvent{}, fmt.Errorf("encode email payload: %w", err)
	}
	event.Envelope, err = encryptor.Encrypt(event.AssociatedData(), plaintext)
	if err != nil {
		return EmailDeliveryEvent{}, fmt.Errorf("encrypt email payload: %w", err)
	}

	return event, nil
}

func (e EmailDeliveryEvent) AssociatedData() []byte {
	return []byte(
		e.Type +
			":v" + strconv.Itoa(e.SchemaVersion) +
			":" + e.ID +
			":" + e.OccurredAt.UTC().Format(time.RFC3339Nano),
	)
}
