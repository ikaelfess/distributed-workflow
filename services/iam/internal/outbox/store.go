package outbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/delivery"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Database interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

type Store struct {
	database Database
}

type ClaimedEvent struct {
	ID         string
	Topic      string
	EventKey   string
	Payload    []byte
	ClaimToken string
	Attempts   int
	CreatedAt  time.Time
}

func NewStore(database Database) (*Store, error) {
	if database == nil {
		return nil, errors.New("database is required")
	}
	return &Store{database: database}, nil
}

func (s *Store) EnqueueEmailDelivery(
	ctx context.Context,
	tx pgx.Tx,
	topic string,
	event delivery.EmailDeliveryEvent,
) error {
	if tx == nil {
		return errors.New("transaction is required")
	}
	if topic == "" {
		return errors.New("topic is required")
	}
	if event.ID == "" || event.Type != delivery.EmailDeliveryEventType {
		return errors.New("valid email delivery event is required")
	}

	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("encode outbox event: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO outbox_events (
			id,
			topic,
			event_key,
			event_type,
			schema_version,
			payload,
			occurred_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`,
		event.ID,
		topic,
		event.ID,
		event.Type,
		event.SchemaVersion,
		payload,
		event.OccurredAt,
	)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func (s *Store) Claim(
	ctx context.Context,
	now time.Time,
	lease time.Duration,
	limit int,
) ([]ClaimedEvent, error) {
	if now.IsZero() {
		return nil, errors.New("claim time is required")
	}
	if lease <= 0 {
		return nil, errors.New("lease must be greater than zero")
	}
	if limit < 1 {
		return nil, errors.New("limit must be at least one")
	}

	claimToken, err := uuid.NewV7()
	if err != nil {
		return nil, fmt.Errorf("generate claim token: %w", err)
	}

	rows, err := s.database.Query(ctx, `
		WITH candidates AS (
			SELECT id
			FROM outbox_events
			WHERE available_at <= $1
			  AND (claim_token IS NULL OR locked_until <= $1)
			ORDER BY created_at, id
			FOR UPDATE SKIP LOCKED
			LIMIT $2
		)
		UPDATE outbox_events AS event
		SET claim_token = $3,
		    locked_until = $4,
		    attempts = attempts + 1
		FROM candidates
		WHERE event.id = candidates.id
		RETURNING
			event.id,
			event.topic,
			event.event_key,
			event.payload,
			event.claim_token,
			event.attempts,
			event.created_at
	`, now, limit, claimToken, now.Add(lease))
	if err != nil {
		return nil, fmt.Errorf("claim outbox events: %w", err)
	}
	defer rows.Close()

	events := make([]ClaimedEvent, 0, limit)
	for rows.Next() {
		var event ClaimedEvent
		if err := rows.Scan(
			&event.ID,
			&event.Topic,
			&event.EventKey,
			&event.Payload,
			&event.ClaimToken,
			&event.Attempts,
			&event.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan claimed outbox event: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate claimed outbox events: %w", err)
	}

	return events, nil
}

func (s *Store) Delete(ctx context.Context, eventID, claimToken string) (bool, error) {
	tag, err := s.database.Exec(ctx, `
		DELETE FROM outbox_events
		WHERE id = $1 AND claim_token = $2
	`, eventID, claimToken)
	if err != nil {
		return false, fmt.Errorf("delete delivered outbox event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) Release(
	ctx context.Context,
	eventID string,
	claimToken string,
	availableAt time.Time,
) (bool, error) {
	if availableAt.IsZero() {
		return false, errors.New("available at is required")
	}

	tag, err := s.database.Exec(ctx, `
		UPDATE outbox_events
		SET available_at = $3,
		    locked_until = NULL,
		    claim_token = NULL,
		    last_error = 'publish failed'
		WHERE id = $1 AND claim_token = $2
	`, eventID, claimToken, availableAt)
	if err != nil {
		return false, fmt.Errorf("release outbox event: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}
