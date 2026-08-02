package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/jackc/pgx/v5"
)

type UserStore struct{}

func NewUserStore() *UserStore {
	return &UserStore{}
}

func (s *UserStore) FindByEmail(
	ctx context.Context,
	tx pgx.Tx,
	email identity.EmailAddress,
) (*identity.User, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, email, password_hash, status, access_level, email_verified_at
		FROM users
		WHERE email = $1
		FOR UPDATE
	`, email.String())

	var user identity.User
	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.Status,
		&user.AccessLevel,
		&user.EmailVerifiedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by email: %w", err)
	}
	return &user, nil
}

func (s *UserStore) FindByID(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
) (*identity.User, error) {
	row := tx.QueryRow(ctx, `
		SELECT id, email, password_hash, status, access_level, email_verified_at
		FROM users
		WHERE id = $1
		FOR UPDATE
	`, userID)

	var user identity.User
	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.Status,
		&user.AccessLevel,
		&user.EmailVerifiedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find user by id: %w", err)
	}
	return &user, nil
}

func (s *UserStore) InsertUnverified(
	ctx context.Context,
	tx pgx.Tx,
	email identity.EmailAddress,
	passwordHash string,
) (*identity.User, error) {
	row := tx.QueryRow(ctx, `
		INSERT INTO users (email, password_hash, status, access_level)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (email) DO NOTHING
		RETURNING id, email, password_hash, status, access_level, email_verified_at
	`,
		email.String(),
		passwordHash,
		identity.StatusUnverified,
		identity.AccessLevelStandard,
	)

	var user identity.User
	err := row.Scan(
		&user.ID,
		&user.Email,
		&user.PasswordHash,
		&user.Status,
		&user.AccessLevel,
		&user.EmailVerifiedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("insert unverified user: %w", err)
	}
	return &user, nil
}

func (s *UserStore) UpdatePasswordHash(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	passwordHash string,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET password_hash = $2,
		    updated_at = NOW()
		WHERE id = $1
	`, userID, passwordHash)
	if err != nil {
		return fmt.Errorf("update password hash: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("update password hash: user not found")
	}
	return nil
}

func (s *UserStore) Activate(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
	verifiedAt time.Time,
) error {
	tag, err := tx.Exec(ctx, `
		UPDATE users
		SET email_verified_at = COALESCE(email_verified_at, $2),
		    status = CASE
		        WHEN status = $3 THEN $3
		        ELSE $4
		    END,
		    updated_at = $2
		WHERE id = $1
		  AND status IN ($3, $5)
	`,
		userID,
		verifiedAt,
		identity.StatusSuspended,
		identity.StatusActive,
		identity.StatusUnverified,
	)
	if err != nil {
		return fmt.Errorf("activate user: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return identity.ErrInvalidVerification
	}
	return nil
}
