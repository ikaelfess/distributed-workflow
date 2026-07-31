package identity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

type ValidatedIdentity struct {
	UserID                  string
	SubjectKind             string
	AccessLevel             string
	AuthenticationSessionID string
	ExpiresAt               time.Time
}

const SubjectKindUser = "USER"

type ValidateService struct {
	sessions SessionStore
	audits   AuditStore
	tx       TransactionManager
	clock    Clock
}

func NewValidateService(
	sessions SessionStore,
	audits AuditStore,
	tx TransactionManager,
	clock Clock,
) (*ValidateService, error) {
	if sessions == nil || audits == nil || tx == nil {
		return nil, errors.New("validation stores are required")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &ValidateService{
		sessions: sessions,
		audits:   audits,
		tx:       tx,
		clock:    clock,
	}, nil
}

func (s *ValidateService) ValidateAccessToken(
	ctx context.Context,
	rawToken string,
) (ValidatedIdentity, error) {
	tokenHash, err := HashOpaqueToken(rawToken)
	if err != nil {
		_ = s.recordFailure(ctx, "malformed")
		return ValidatedIdentity{}, ErrMalformedAccessToken
	}

	now := s.clock.Now()
	var (
		identity ValidatedIdentity
		failed   bool
	)
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		record, err := s.sessions.FindAccessTokenByHash(ctx, tx, tokenHash, now)
		if errors.Is(err, ErrInvalidAccessToken) {
			failed = true
			return s.audits.Append(
				ctx,
				tx,
				AuditValidationFailed,
				nil,
				nil,
				RetentionRoutine,
				map[string]any{"outcome": "unknown"},
			)
		}
		if err != nil {
			return err
		}
		if record.RevokedAt != nil || record.SessionRevokedAt != nil {
			failed = true
			return s.audits.Append(
				ctx,
				tx,
				AuditValidationFailed,
				nil,
				&record.UserID,
				RetentionRoutine,
				map[string]any{"outcome": "revoked"},
			)
		}
		if !record.ExpiresAt.After(now) {
			failed = true
			return s.audits.Append(
				ctx,
				tx,
				AuditValidationFailed,
				nil,
				&record.UserID,
				RetentionRoutine,
				map[string]any{"outcome": "expired"},
			)
		}
		if record.UserStatus != StatusActive {
			failed = true
			return s.audits.Append(
				ctx,
				tx,
				AuditValidationFailed,
				nil,
				&record.UserID,
				RetentionRoutine,
				map[string]any{"outcome": "user_" + record.UserStatus},
			)
		}

		if err := s.sessions.TouchSession(ctx, tx, record.SessionID, now); err != nil {
			return err
		}
		if err := s.audits.Append(
			ctx,
			tx,
			AuditValidationSucceeded,
			&record.UserID,
			&record.UserID,
			RetentionRoutine,
			map[string]any{"outcome": "validated"},
		); err != nil {
			return err
		}

		identity = ValidatedIdentity{
			UserID:                  record.UserID,
			SubjectKind:             SubjectKindUser,
			AccessLevel:             record.AccessLevel,
			AuthenticationSessionID: record.SessionID,
			ExpiresAt:               record.ExpiresAt,
		}
		return nil
	})
	if err != nil {
		return ValidatedIdentity{}, fmt.Errorf("validate access token: %w", err)
	}
	if failed {
		return ValidatedIdentity{}, ErrInvalidAccessToken
	}
	return identity, nil
}

func (s *ValidateService) recordFailure(ctx context.Context, outcome string) error {
	return s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.audits.Append(
			ctx,
			tx,
			AuditValidationFailed,
			nil,
			nil,
			RetentionRoutine,
			map[string]any{"outcome": outcome},
		)
	})
}
