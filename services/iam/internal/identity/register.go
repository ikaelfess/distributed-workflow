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

type Clock interface {
	Now() time.Time
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now().UTC()
}

type TransactionManager interface {
	WithinTransaction(context.Context, func(context.Context, pgx.Tx) error) error
}

type UserStore interface {
	FindByEmail(context.Context, pgx.Tx, EmailAddress) (*User, error)
	FindByID(context.Context, pgx.Tx, string) (*User, error)
	// InsertUnverified inserts a new unverified user. When the email already
	// exists, it returns (nil, nil) without aborting the transaction.
	InsertUnverified(context.Context, pgx.Tx, EmailAddress, string) (*User, error)
	UpdatePasswordHash(context.Context, pgx.Tx, string, string) error
	Activate(context.Context, pgx.Tx, string, time.Time) error
}

type ChallengeStore interface {
	SupersedeActive(context.Context, pgx.Tx, string, string) error
	Insert(
		context.Context,
		pgx.Tx,
		string,
		string,
		[]byte,
		time.Time,
	) error
	FindUsableByTokenHash(
		context.Context,
		pgx.Tx,
		[]byte,
		string,
		time.Time,
	) (Challenge, error)
	Consume(context.Context, pgx.Tx, string, time.Time) error
}

type Challenge struct {
	ID            string
	UserID        string
	ChallengeType string
	ExpiresAt     time.Time
}

type AuditStore interface {
	Append(
		context.Context,
		pgx.Tx,
		string,
		*string,
		*string,
		string,
		map[string]any,
	) error
}

type OutboxWriter interface {
	EnqueueEmailDelivery(
		context.Context,
		pgx.Tx,
		string,
		delivery.EmailDeliveryEvent,
	) error
}

type RegisterService struct {
	users        UserStore
	challenges   ChallengeStore
	audits       AuditStore
	outbox       OutboxWriter
	tx           TransactionManager
	passwords    *PasswordHasher
	encryptor    *delivery.EnvelopeEncryptor
	topic        string
	challengeTTL time.Duration
	clock        Clock
	random       io.Reader
}

func NewRegisterService(
	users UserStore,
	challenges ChallengeStore,
	audits AuditStore,
	outbox OutboxWriter,
	tx TransactionManager,
	passwords *PasswordHasher,
	encryptor *delivery.EnvelopeEncryptor,
	topic string,
	challengeTTL time.Duration,
	clock Clock,
	random io.Reader,
) (*RegisterService, error) {
	if users == nil || challenges == nil || audits == nil || outbox == nil || tx == nil {
		return nil, errors.New("registration stores are required")
	}
	if passwords == nil || encryptor == nil {
		return nil, errors.New("registration crypto dependencies are required")
	}
	if topic == "" {
		return nil, errors.New("email delivery topic is required")
	}
	if challengeTTL <= 0 {
		return nil, errors.New("challenge ttl must be greater than zero")
	}
	if clock == nil {
		clock = SystemClock{}
	}

	return &RegisterService{
		users:        users,
		challenges:   challenges,
		audits:       audits,
		outbox:       outbox,
		tx:           tx,
		passwords:    passwords,
		encryptor:    encryptor,
		topic:        topic,
		challengeTTL: challengeTTL,
		clock:        clock,
		random:       random,
	}, nil
}

func (s *RegisterService) Register(ctx context.Context, email string, password string) error {
	canonicalEmail, err := CanonicalEmailAddress(email)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRegistration, err)
	}
	if err := ValidatePasswordLength(password); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRegistration, err)
	}

	passwordHash, err := s.passwords.Hash(password)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidRegistration, err)
	}

	now := s.clock.Now()
	if err := s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		existing, err := s.users.FindByEmail(ctx, tx, canonicalEmail)
		if err != nil {
			return err
		}

		switch {
		case existing == nil:
			user, err := s.users.InsertUnverified(ctx, tx, canonicalEmail, passwordHash)
			if err != nil {
				return err
			}
			if user == nil {
				existing, findErr := s.users.FindByEmail(ctx, tx, canonicalEmail)
				if findErr != nil {
					return findErr
				}
				if existing == nil {
					return errors.New("user disappeared after email conflict")
				}
				return s.handleExistingUser(
					ctx,
					tx,
					*existing,
					canonicalEmail,
					passwordHash,
					now,
				)
			}
			return s.issueVerification(ctx, tx, *user, canonicalEmail, now)
		default:
			return s.handleExistingUser(
				ctx,
				tx,
				*existing,
				canonicalEmail,
				passwordHash,
				now,
			)
		}
	}); err != nil {
		return fmt.Errorf("register user: %w", err)
	}
	return nil
}

func (s *RegisterService) handleExistingUser(
	ctx context.Context,
	tx pgx.Tx,
	user User,
	email EmailAddress,
	passwordHash string,
	now time.Time,
) error {
	if user.Status == StatusUnverified {
		if err := s.users.UpdatePasswordHash(ctx, tx, user.ID, passwordHash); err != nil {
			return err
		}
		return s.issueVerification(ctx, tx, user, email, now)
	}

	targetID := user.ID
	return s.audits.Append(
		ctx,
		tx,
		AuditRegistrationAccepted,
		nil,
		&targetID,
		RetentionRoutine,
		map[string]any{"outcome": "existing_user"},
	)
}

func (s *RegisterService) issueVerification(
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
	expiresAt := now.Add(s.challengeTTL)
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
	if err := s.outbox.EnqueueEmailDelivery(ctx, tx, s.topic, event); err != nil {
		return err
	}

	targetID := user.ID
	return s.audits.Append(
		ctx,
		tx,
		AuditRegistrationAccepted,
		nil,
		&targetID,
		RetentionRoutine,
		map[string]any{"outcome": "verification_challenge_issued"},
	)
}
