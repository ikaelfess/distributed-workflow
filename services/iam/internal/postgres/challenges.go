package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/jackc/pgx/v5"
)

type ChallengeStore struct{}

func NewChallengeStore() *ChallengeStore {
	return &ChallengeStore{}
}

func (s *ChallengeStore) SupersedeActive(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	challengeType string,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE challenges
		SET superseded_at = NOW()
		WHERE user_id = $1
		  AND challenge_type = $2
		  AND consumed_at IS NULL
		  AND superseded_at IS NULL
	`, userID, challengeType)
	if err != nil {
		return fmt.Errorf("supersede challenges: %w", err)
	}
	return nil
}

func (s *ChallengeStore) Insert(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	challengeType string,
	tokenHash []byte,
	expiresAt time.Time,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO challenges (user_id, challenge_type, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
	`, userID, challengeType, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("insert challenge: %w", err)
	}
	return nil
}

func (s *ChallengeStore) FindUsableByTokenHash(
	ctx context.Context,
	tx pgx.Tx,
	tokenHash []byte,
	challengeType string,
	now time.Time,
) (identity.Challenge, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, user_id, challenge_type, expires_at
		FROM challenges
		WHERE token_hash = $1
		  AND challenge_type = $2
		  AND consumed_at IS NULL
		  AND superseded_at IS NULL
		  AND expires_at > $3
		FOR UPDATE
	`, tokenHash, challengeType, now)

	var challenge identity.Challenge
	err := row.Scan(
		&challenge.ID,
		&challenge.UserID,
		&challenge.ChallengeType,
		&challenge.ExpiresAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.Challenge{}, identity.ErrChallengeNotUsable
	}
	if err != nil {
		return identity.Challenge{}, fmt.Errorf("find challenge: %w", err)
	}
	return challenge, nil
}

func (s *ChallengeStore) Consume(
	ctx context.Context,
	tx pgx.Tx,
	challengeID string,
	consumedAt time.Time,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE challenges
		SET consumed_at = $2
		WHERE id = $1
		  AND consumed_at IS NULL
		  AND superseded_at IS NULL
	`, challengeID, consumedAt)
	if err != nil {
		return fmt.Errorf("consume challenge: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return identity.ErrChallengeNotUsable
	}
	return nil
}
