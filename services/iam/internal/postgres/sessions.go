package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/jackc/pgx/v5"
)

type SessionStore struct{}

func NewSessionStore() *SessionStore {
	return &SessionStore{}
}

func (s *SessionStore) InsertSession(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	csrfSecretHash []byte,
	now time.Time,
	clientMetadata string,
	ip *netip.Addr,
) (string, error) {
	var sessionID string
	err := tx.QueryRow(ctx, `
		INSERT INTO authentication_sessions (
			user_id,
			csrf_secret_hash,
			created_at,
			last_used_at,
			client_metadata,
			ip
		)
		VALUES ($1, $2, $3, $3, $4, $5)
		RETURNING id
	`, userID, csrfSecretHash, now, clientMetadata, ip).Scan(&sessionID)
	if err != nil {
		return "", fmt.Errorf("insert authentication session: %w", err)
	}
	return sessionID, nil
}

func (s *SessionStore) InsertAccessToken(
	ctx context.Context,
	tx pgx.Tx,
	sessionID string,
	tokenHash []byte,
	expiresAt time.Time,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO access_tokens (session_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
	`, sessionID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("insert access token: %w", err)
	}
	return nil
}

func (s *SessionStore) InsertRefreshToken(
	ctx context.Context,
	tx pgx.Tx,
	sessionID string,
	familyID uuid.UUID,
	tokenHash []byte,
	expiresAt time.Time,
) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO refresh_tokens (
			session_id,
			family_id,
			token_hash,
			expires_at
		)
		VALUES ($1, $2, $3, $4)
	`, sessionID, familyID, tokenHash, expiresAt)
	if err != nil {
		return fmt.Errorf("insert refresh token: %w", err)
	}
	return nil
}

func (s *SessionStore) FindAccessTokenByHash(
	ctx context.Context,
	tx pgx.Tx,
	tokenHash []byte,
	_ time.Time,
) (identity.AccessTokenRecord, error) {
	row := tx.QueryRow(ctx, `
		SELECT
			at.session_id,
			s.user_id,
			u.access_level,
			u.status,
			at.expires_at,
			at.revoked_at,
			s.revoked_at
		FROM access_tokens at
		INNER JOIN authentication_sessions s ON s.id = at.session_id
		INNER JOIN users u ON u.id = s.user_id
		WHERE at.token_hash = $1
	`, tokenHash)

	var record identity.AccessTokenRecord
	err := row.Scan(
		&record.SessionID,
		&record.UserID,
		&record.AccessLevel,
		&record.UserStatus,
		&record.ExpiresAt,
		&record.RevokedAt,
		&record.SessionRevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.AccessTokenRecord{}, identity.ErrInvalidAccessToken
	}
	if err != nil {
		return identity.AccessTokenRecord{}, fmt.Errorf("find access token: %w", err)
	}
	return record, nil
}

func (s *SessionStore) TouchSession(
	ctx context.Context,
	tx pgx.Tx,
	sessionID string,
	now time.Time,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE authentication_sessions
		SET last_used_at = $2
		WHERE id = $1
		  AND revoked_at IS NULL
	`, sessionID, now)
	if err != nil {
		return fmt.Errorf("touch authentication session: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return identity.ErrInvalidAccessToken
	}
	return nil
}
