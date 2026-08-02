package identity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/delivery"
	"github.com/jackc/pgx/v5"
)

var (
	ErrInvalidPasswordReset = errors.New("invalid password reset")
)

type PasswordResetSessionStore interface {
	RevokeAllSessionsForUser(context.Context, pgx.Tx, string, time.Time) error
}

type PasswordResetService struct {
	users      UserStore
	challenges ChallengeStore
	sessions   PasswordResetSessionStore
	audits     AuditStore
	outbox     OutboxWriter
	tx         TransactionManager
	passwords  *PasswordHasher
	encryptor  *delivery.EnvelopeEncryptor
	topic      string
	resetTTL   time.Duration
	verifyTTL  time.Duration
	clock      Clock
	random     io.Reader
}

func NewPasswordResetService(
	users UserStore,
	challenges ChallengeStore,
	sessions PasswordResetSessionStore,
	audits AuditStore,
	outbox OutboxWriter,
	tx TransactionManager,
	passwords *PasswordHasher,
	encryptor *delivery.EnvelopeEncryptor,
	topic string,
	resetTTL time.Duration,
	verifyTTL time.Duration,
	clock Clock,
	random io.Reader,
) (*PasswordResetService, error) {
	if users == nil || challenges == nil || sessions == nil || audits == nil ||
		outbox == nil || tx == nil {
		return nil, errors.New("password reset stores are required")
	}
	if passwords == nil || encryptor == nil {
		return nil, errors.New("password reset crypto dependencies are required")
	}
	if topic == "" {
		return nil, errors.New("email delivery topic is required")
	}
	if resetTTL <= 0 || verifyTTL <= 0 {
		return nil, errors.New("challenge ttl must be greater than zero")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &PasswordResetService{
		users:      users,
		challenges: challenges,
		sessions:   sessions,
		audits:     audits,
		outbox:     outbox,
		tx:         tx,
		passwords:  passwords,
		encryptor:  encryptor,
		topic:      topic,
		resetTTL:   resetTTL,
		verifyTTL:  verifyTTL,
		clock:      clock,
		random:     random,
	}, nil
}

// Request always succeeds externally. Known addresses receive a reset or
// verification challenge depending on User status; unknown addresses are a no-op.
func (s *PasswordResetService) Request(ctx context.Context, rawEmail string) error {
	canonicalEmail, err := CanonicalEmailAddress(rawEmail)
	if err != nil {
		// Enumeration-safe: invalid syntax still looks accepted.
		return nil
	}

	now := s.clock.Now()
	if err := s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		user, err := s.users.FindByEmail(ctx, tx, canonicalEmail)
		if err != nil {
			return err
		}
		if user == nil {
			return s.audits.Append(
				ctx,
				tx,
				AuditPasswordResetRequested,
				nil,
				nil,
				RetentionRoutine,
				map[string]any{"outcome": "unknown_email"},
			)
		}

		targetID := user.ID
		switch user.Status {
		case StatusUnverified:
			if err := s.issueVerificationChallenge(
				ctx,
				tx,
				*user,
				canonicalEmail,
				now,
			); err != nil {
				return err
			}
			return s.audits.Append(
				ctx,
				tx,
				AuditPasswordResetRequested,
				nil,
				&targetID,
				RetentionRoutine,
				map[string]any{"outcome": "verification_challenge_issued"},
			)
		case StatusActive, StatusSuspended:
			if err := s.issuePasswordResetChallenge(
				ctx,
				tx,
				*user,
				canonicalEmail,
				now,
			); err != nil {
				return err
			}
			return s.audits.Append(
				ctx,
				tx,
				AuditPasswordResetRequested,
				nil,
				&targetID,
				RetentionSensitive,
				map[string]any{"outcome": "password_reset_challenge_issued"},
			)
		default:
			return s.audits.Append(
				ctx,
				tx,
				AuditPasswordResetRequested,
				nil,
				&targetID,
				RetentionRoutine,
				map[string]any{"outcome": "ignored_status"},
			)
		}
	}); err != nil {
		return fmt.Errorf("request password reset: %w", err)
	}
	return nil
}

func (s *PasswordResetService) Complete(
	ctx context.Context,
	rawChallenge string,
	newPassword string,
) error {
	if err := ValidatePasswordLength(newPassword); err != nil {
		if auditErr := s.recordFailure(ctx, "invalid_password"); auditErr != nil {
			return fmt.Errorf("record password reset failure: %w", auditErr)
		}
		return ErrInvalidPasswordReset
	}

	tokenHash, err := HashChallengeToken(rawChallenge)
	if err != nil {
		if auditErr := s.recordFailure(ctx, "invalid_challenge"); auditErr != nil {
			return fmt.Errorf("record password reset failure: %w", auditErr)
		}
		return ErrInvalidPasswordReset
	}

	passwordHash, err := s.passwords.Hash(newPassword)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}

	now := s.clock.Now()
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		challenge, err := s.challenges.FindUsableByTokenHash(
			ctx,
			tx,
			tokenHash,
			ChallengeTypePasswordReset,
			now,
		)
		if errors.Is(err, ErrChallengeNotUsable) {
			return ErrInvalidPasswordReset
		}
		if err != nil {
			return err
		}

		user, err := s.users.FindByID(ctx, tx, challenge.UserID)
		if err != nil {
			return err
		}
		if user == nil {
			return ErrInvalidPasswordReset
		}
		// Suspended and active may reset; unverified should not hold reset challenges.
		if user.Status != StatusActive && user.Status != StatusSuspended {
			return ErrInvalidPasswordReset
		}

		if err := s.challenges.Consume(ctx, tx, challenge.ID, now); err != nil {
			if errors.Is(err, ErrChallengeNotUsable) {
				return ErrInvalidPasswordReset
			}
			return err
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
			nil,
			&targetID,
			RetentionSensitive,
			map[string]any{"outcome": "password_reset"},
		); err != nil {
			return err
		}
		return s.audits.Append(
			ctx,
			tx,
			AuditPasswordResetSucceeded,
			nil,
			&targetID,
			RetentionSensitive,
			map[string]any{
				"outcome": "password_reset",
				"status":  user.Status,
			},
		)
	})
	if errors.Is(err, ErrInvalidPasswordReset) {
		if auditErr := s.recordFailure(ctx, "invalid_challenge"); auditErr != nil {
			return fmt.Errorf("record password reset failure: %w", auditErr)
		}
		return ErrInvalidPasswordReset
	}
	if err != nil {
		return fmt.Errorf("complete password reset: %w", err)
	}
	return nil
}

func (s *PasswordResetService) issuePasswordResetChallenge(
	ctx context.Context,
	tx pgx.Tx,
	user User,
	email EmailAddress,
	now time.Time,
) error {
	if err := s.challenges.SupersedeActive(ctx, tx, user.ID, ChallengeTypePasswordReset); err != nil {
		return err
	}
	rawToken, tokenHash, err := NewChallengeToken(s.random)
	if err != nil {
		return err
	}
	expiresAt := now.Add(s.resetTTL)
	if err := s.challenges.Insert(
		ctx,
		tx,
		user.ID,
		ChallengeTypePasswordReset,
		tokenHash,
		expiresAt,
	); err != nil {
		return err
	}

	event, err := delivery.NewEmailDeliveryEvent(now, delivery.EmailPayload{
		Recipient: email.String(),
		Template:  EmailTemplatePasswordReset,
		Variables: map[string]string{
			"challenge":  rawToken,
			"expires_at": expiresAt.Format(time.RFC3339),
		},
	}, s.encryptor)
	if err != nil {
		return fmt.Errorf("create delivery event: %w", err)
	}
	return s.outbox.EnqueueEmailDelivery(ctx, tx, s.topic, event)
}

func (s *PasswordResetService) issueVerificationChallenge(
	ctx context.Context,
	tx pgx.Tx,
	user User,
	email EmailAddress,
	now time.Time,
) error {
	if err := s.challenges.SupersedeActive(ctx, tx, user.ID, ChallengeTypeVerification); err != nil {
		return err
	}
	rawToken, tokenHash, err := NewChallengeToken(s.random)
	if err != nil {
		return err
	}
	expiresAt := now.Add(s.verifyTTL)
	if err := s.challenges.Insert(
		ctx,
		tx,
		user.ID,
		ChallengeTypeVerification,
		tokenHash,
		expiresAt,
	); err != nil {
		return err
	}

	event, err := delivery.NewEmailDeliveryEvent(now, delivery.EmailPayload{
		Recipient: email.String(),
		Template:  EmailTemplateVerify,
		Variables: map[string]string{
			"challenge":  rawToken,
			"expires_at": expiresAt.Format(time.RFC3339),
		},
	}, s.encryptor)
	if err != nil {
		return fmt.Errorf("create delivery event: %w", err)
	}
	return s.outbox.EnqueueEmailDelivery(ctx, tx, s.topic, event)
}

func (s *PasswordResetService) recordFailure(ctx context.Context, outcome string) error {
	if err := s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.audits.Append(
			ctx,
			tx,
			AuditPasswordResetFailed,
			nil,
			nil,
			RetentionRoutine,
			map[string]any{"outcome": outcome},
		)
	}); err != nil {
		return fmt.Errorf("insert password reset failure audit: %w", err)
	}
	return nil
}
