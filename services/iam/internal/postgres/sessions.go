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
	rotatedFromID *string,
) (string, error) {
	var tokenID string
	err := tx.QueryRow(ctx, `
		INSERT INTO refresh_tokens (
			session_id,
			family_id,
			token_hash,
			expires_at,
			rotated_from_id
		)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id
	`, sessionID, familyID, tokenHash, expiresAt, rotatedFromID).Scan(&tokenID)
	if err != nil {
		return "", fmt.Errorf("insert refresh token: %w", err)
	}
	return tokenID, nil
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

func (s *SessionStore) FindRefreshTokenForUpdate(
	ctx context.Context,
	tx pgx.Tx,
	tokenHash []byte,
) (identity.RefreshTokenRecord, error) {
	row := tx.QueryRow(ctx, `
		SELECT
			rt.id,
			rt.session_id,
			rt.family_id,
			s.user_id,
			u.status,
			u.access_level,
			s.csrf_secret_hash,
			rt.expires_at,
			rt.revoked_at,
			rt.reused_at,
			s.revoked_at,
			successor.id,
			successor.created_at
		FROM refresh_tokens rt
		INNER JOIN authentication_sessions s ON s.id = rt.session_id
		INNER JOIN users u ON u.id = s.user_id
		LEFT JOIN refresh_tokens successor ON successor.rotated_from_id = rt.id
		WHERE rt.token_hash = $1
		FOR UPDATE OF rt
	`, tokenHash)

	var record identity.RefreshTokenRecord
	err := row.Scan(
		&record.ID,
		&record.SessionID,
		&record.FamilyID,
		&record.UserID,
		&record.UserStatus,
		&record.AccessLevel,
		&record.CSRFSecretHash,
		&record.ExpiresAt,
		&record.RevokedAt,
		&record.ReusedAt,
		&record.SessionRevokedAt,
		&record.SuccessorID,
		&record.SuccessorCreatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return identity.RefreshTokenRecord{}, identity.ErrRefreshFailed
	}
	if err != nil {
		return identity.RefreshTokenRecord{}, fmt.Errorf("find refresh token: %w", err)
	}
	return record, nil
}

func (s *SessionStore) MarkRefreshReused(
	ctx context.Context,
	tx pgx.Tx,
	tokenID string,
	now time.Time,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE refresh_tokens
		SET reused_at = $2
		WHERE id = $1
	`, tokenID, now)
	if err != nil {
		return fmt.Errorf("mark refresh token reused: %w", err)
	}
	return nil
}

func (s *SessionStore) RevokeTokenFamily(
	ctx context.Context,
	tx pgx.Tx,
	familyID uuid.UUID,
	now time.Time,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE family_id = $1
	`, familyID, now)
	if err != nil {
		return fmt.Errorf("revoke refresh token family: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE access_tokens
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE session_id IN (
			SELECT DISTINCT session_id
			FROM refresh_tokens
			WHERE family_id = $1
		)
	`, familyID, now)
	if err != nil {
		return fmt.Errorf("revoke access tokens for refresh family: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE authentication_sessions
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE id IN (
			SELECT DISTINCT session_id
			FROM refresh_tokens
			WHERE family_id = $1
		)
	`, familyID, now)
	if err != nil {
		return fmt.Errorf("revoke authentication sessions for refresh family: %w", err)
	}
	return nil
}

func (s *SessionStore) RevokeSessionAccessTokens(
	ctx context.Context,
	tx pgx.Tx,
	sessionID string,
	now time.Time,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE access_tokens
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE session_id = $1
		  AND revoked_at IS NULL
	`, sessionID, now)
	if err != nil {
		return fmt.Errorf("revoke session access tokens: %w", err)
	}
	return nil
}

func (s *SessionStore) RevokeRefreshToken(
	ctx context.Context,
	tx pgx.Tx,
	tokenID string,
	now time.Time,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = $2
		WHERE id = $1
		  AND revoked_at IS NULL
	`, tokenID, now)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return identity.ErrRefreshFailed
	}
	return nil
}

func (s *SessionStore) FindSessionCSRFHash(
	ctx context.Context,
	tx pgx.Tx,
	sessionID string,
) ([]byte, error) {
	var hash []byte
	err := tx.QueryRow(ctx, `
		SELECT csrf_secret_hash
		FROM authentication_sessions
		WHERE id = $1
		  AND revoked_at IS NULL
	`, sessionID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, identity.ErrAuthenticationSession
	}
	if err != nil {
		return nil, fmt.Errorf("find session csrf hash: %w", err)
	}
	return hash, nil
}

func (s *SessionStore) ListSessionsByUser(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
) ([]identity.AuthenticationSessionView, error) {
	rows, err := tx.Query(ctx, `
		SELECT
			id,
			created_at,
			last_used_at,
			client_metadata,
			ip
		FROM authentication_sessions
		WHERE user_id = $1
		  AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list authentication sessions: %w", err)
	}
	defer rows.Close()

	var sessions []identity.AuthenticationSessionView
	for rows.Next() {
		var view identity.AuthenticationSessionView
		if err := rows.Scan(
			&view.ID,
			&view.CreatedAt,
			&view.LastUsedAt,
			&view.ClientMetadata,
			&view.IP,
		); err != nil {
			return nil, fmt.Errorf("scan authentication session: %w", err)
		}
		sessions = append(sessions, view)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate authentication sessions: %w", err)
	}
	return sessions, nil
}

func (s *SessionStore) RevokeSession(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	sessionID string,
	now time.Time,
) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE authentication_sessions
		SET revoked_at = $3
		WHERE id = $1
		  AND user_id = $2
		  AND revoked_at IS NULL
	`, sessionID, userID, now)
	if err != nil {
		return false, fmt.Errorf("revoke authentication session: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return false, nil
	}

	if _, err := tx.Exec(ctx, `
		UPDATE access_tokens
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE session_id = $1
	`, sessionID, now); err != nil {
		return false, fmt.Errorf("revoke access tokens for session: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE session_id = $1
	`, sessionID, now); err != nil {
		return false, fmt.Errorf("revoke refresh tokens for session: %w", err)
	}
	return true, nil
}

func (s *SessionStore) RevokeAllSessionsForUser(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	now time.Time,
) error {
	_, err := tx.Exec(ctx, `
		UPDATE authentication_sessions
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE user_id = $1
		  AND revoked_at IS NULL
	`, userID, now)
	if err != nil {
		return fmt.Errorf("revoke all authentication sessions: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE access_tokens
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE session_id IN (
			SELECT id FROM authentication_sessions WHERE user_id = $1
		)
		  AND revoked_at IS NULL
	`, userID, now)
	if err != nil {
		return fmt.Errorf("revoke all access tokens for user: %w", err)
	}
	_, err = tx.Exec(ctx, `
		UPDATE refresh_tokens
		SET revoked_at = COALESCE(revoked_at, $2)
		WHERE session_id IN (
			SELECT id FROM authentication_sessions WHERE user_id = $1
		)
		  AND revoked_at IS NULL
	`, userID, now)
	if err != nil {
		return fmt.Errorf("revoke all refresh tokens for user: %w", err)
	}
	return nil
}
