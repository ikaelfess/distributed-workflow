package postgres

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type AuditStore struct{}

func NewAuditStore() *AuditStore {
	return &AuditStore{}
}

func (s *AuditStore) Append(
	ctx context.Context,
	tx pgx.Tx,
	eventType string,
	actorUserID *string,
	targetUserID *string,
	retentionClass string,
	metadata map[string]any,
) error {
	if metadata == nil {
		metadata = map[string]any{}
	}
	payload, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events (
			event_type,
			actor_user_id,
			target_user_id,
			metadata,
			retention_class
		)
		VALUES ($1, $2, $3, $4, $5)
	`, eventType, actorUserID, targetUserID, payload, retentionClass)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}
