package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidPasswordChange = errors.New("invalid password change")
)

type PasswordChangeService struct {
	users     UserStore
	sessions  SessionManagementStore
	audits    AuditStore
	tx        TransactionManager
	passwords *PasswordHasher
	clock     Clock
}

func NewPasswordChangeService(
	users UserStore,
	sessions SessionManagementStore,
	audits AuditStore,
	tx TransactionManager,
	passwords *PasswordHasher,
	clock Clock,
) (*PasswordChangeService, error) {
	if users == nil || sessions == nil || audits == nil || tx == nil {
		return nil, errors.New("password change stores are required")
	}
	if passwords == nil {
		return nil, errors.New("password hasher is required")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &PasswordChangeService{
		users:     users,
		sessions:  sessions,
		audits:    audits,
		tx:        tx,
		passwords: passwords,
		clock:     clock,
	}, nil
}

func (s *PasswordChangeService) Change(
	ctx context.Context,
	rawAccessToken string,
	csrfToken string,
	currentPassword string,
	newPassword string,
) error {
	if err := ValidatePasswordLength(newPassword); err != nil {
		if auditErr := s.recordFailure(ctx, nil, "invalid_password"); auditErr != nil {
			return fmt.Errorf("record password change failure: %w", auditErr)
		}
		return ErrInvalidPasswordChange
	}

	accessHash, err := HashOpaqueToken(rawAccessToken)
	if err != nil {
		return ErrInvalidAccessToken
	}
	csrfHash, err := HashOpaqueToken(csrfToken)
	if err != nil {
		return ErrInvalidCSRFToken
	}

	now := s.clock.Now()
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		record, err := s.sessions.FindAccessTokenByHash(ctx, tx, accessHash, now)
		if err != nil {
			return err
		}
		if record.RevokedAt != nil || record.SessionRevokedAt != nil {
			return ErrInvalidAccessToken
		}
		if !record.ExpiresAt.After(now) {
			return ErrInvalidAccessToken
		}
		if record.UserStatus != StatusActive {
			return ErrInvalidAccessToken
		}

		storedCSRF, err := s.sessions.FindSessionCSRFHash(ctx, tx, record.SessionID)
		if err != nil {
			if errors.Is(err, ErrAuthenticationSession) {
				return ErrInvalidAccessToken
			}
			return err
		}
		if subtle.ConstantTimeCompare(storedCSRF, csrfHash) != 1 {
			return ErrInvalidCSRFToken
		}

		user, err := s.users.FindByID(ctx, tx, record.UserID)
		if err != nil {
			return err
		}
		if user == nil || user.Status != StatusActive {
			return ErrInvalidAccessToken
		}

		if !s.passwords.Verify(user.PasswordHash, currentPassword) {
			targetID := user.ID
			if err := s.audits.Append(
				ctx,
				tx,
				AuditPasswordChangeFailed,
				&targetID,
				&targetID,
				RetentionRoutine,
				map[string]any{"outcome": "current_password_mismatch"},
			); err != nil {
				return err
			}
			return ErrInvalidPasswordChange
		}

		passwordHash, err := s.passwords.Hash(newPassword)
		if err != nil {
			return fmt.Errorf("hash password: %w", err)
		}
		if err := s.users.UpdatePasswordHash(ctx, tx, user.ID, passwordHash); err != nil {
			return err
		}
		if err := s.sessions.RevokeAllSessionsForUser(ctx, tx, user.ID, now); err != nil {
			return err
		}

		targetID := user.ID
		if err := s.audits.Append(
			ctx,
			tx,
			AuditSessionsRevokedAll,
			&targetID,
			&targetID,
			RetentionSensitive,
			map[string]any{"outcome": "password_change"},
		); err != nil {
			return err
		}
		return s.audits.Append(
			ctx,
			tx,
			AuditPasswordChangeSucceeded,
			&targetID,
			&targetID,
			RetentionSensitive,
			map[string]any{"outcome": "password_changed"},
		)
	})
	if errors.Is(err, ErrInvalidAccessToken) ||
		errors.Is(err, ErrAuthenticationSession) ||
		errors.Is(err, ErrInvalidCSRFToken) ||
		errors.Is(err, ErrInvalidPasswordChange) {
		return err
	}
	if err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	return nil
}

func (s *PasswordChangeService) recordFailure(
	ctx context.Context,
	actorID *string,
	outcome string,
) error {
	if err := s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.audits.Append(
			ctx,
			tx,
			AuditPasswordChangeFailed,
			actorID,
			actorID,
			RetentionRoutine,
			map[string]any{"outcome": outcome},
		)
	}); err != nil {
		return fmt.Errorf("insert password change failure audit: %w", err)
	}
	return nil
}
