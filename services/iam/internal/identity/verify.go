package identity

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidRegistration = errors.New("invalid registration")
	ErrInvalidVerification = errors.New("invalid verification")
)

type VerifyService struct {
	users      UserStore
	challenges ChallengeStore
	audits     AuditStore
	tx         TransactionManager
	clock      Clock
}

func NewVerifyService(
	users UserStore,
	challenges ChallengeStore,
	audits AuditStore,
	tx TransactionManager,
	clock Clock,
) (*VerifyService, error) {
	if users == nil || challenges == nil || audits == nil || tx == nil {
		return nil, errors.New("verification stores are required")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &VerifyService{
		users:      users,
		challenges: challenges,
		audits:     audits,
		tx:         tx,
		clock:      clock,
	}, nil
}

func (s *VerifyService) VerifyEmail(ctx context.Context, rawToken string) error {
	tokenHash, err := HashChallengeToken(rawToken)
	if err != nil {
		if auditErr := s.recordFailure(ctx); auditErr != nil {
			return fmt.Errorf("record verification failure: %w", auditErr)
		}
		return ErrInvalidVerification
	}

	now := s.clock.Now()
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		challenge, err := s.challenges.FindUsableByTokenHash(ctx, tx, tokenHash, now)
		if err != nil {
			return err
		}

		if err := s.challenges.Consume(ctx, tx, challenge.ID, now); err != nil {
			return err
		}
		if err := s.users.Activate(ctx, tx, challenge.UserID, now); err != nil {
			return err
		}

		targetID := challenge.UserID
		return s.audits.Append(
			ctx,
			tx,
			AuditVerificationSucceeded,
			nil,
			&targetID,
			RetentionRoutine,
			map[string]any{"outcome": "activated"},
		)
	})
	if errors.Is(err, ErrInvalidVerification) {
		if auditErr := s.recordFailure(ctx); auditErr != nil {
			return fmt.Errorf("record verification failure: %w", auditErr)
		}
		return ErrInvalidVerification
	}
	if err != nil {
		return fmt.Errorf("verify email: %w", err)
	}
	return nil
}

func (s *VerifyService) recordFailure(ctx context.Context) error {
	if err := s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.audits.Append(
			ctx,
			tx,
			AuditVerificationFailed,
			nil,
			nil,
			RetentionRoutine,
			map[string]any{"outcome": "invalid_challenge"},
		)
	}); err != nil {
		return fmt.Errorf("insert verification failure audit: %w", err)
	}
	return nil
}
