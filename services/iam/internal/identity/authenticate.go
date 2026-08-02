package identity

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrAuthenticationFailed = errors.New("authentication failed")
	ErrInvalidAccessToken   = errors.New("invalid access token")
	ErrMalformedAccessToken = errors.New("malformed access token")
)

const dummyPasswordMaterial = "timing-safe-dummy-password-material"

type SessionClientInfo struct {
	UserAgent string
	IP        netip.Addr
}

type IssuedCredentials struct {
	AccessToken             string
	RefreshToken            string
	CSRFToken               string
	AccessTokenExpiresAt    time.Time
	RefreshTokenExpiresAt   time.Time
	AuthenticationSessionID string
	UserID                  string
	AccessLevel             string
}

type SessionStore interface {
	InsertSession(
		context.Context,
		pgx.Tx,
		string,
		[]byte,
		time.Time,
		string,
		*netip.Addr,
	) (string, error)
	InsertAccessToken(
		context.Context,
		pgx.Tx,
		string,
		[]byte,
		time.Time,
	) error
	InsertRefreshToken(
		context.Context,
		pgx.Tx,
		string,
		uuid.UUID,
		[]byte,
		time.Time,
		*string,
	) (string, error)
	FindAccessTokenByHash(
		context.Context,
		pgx.Tx,
		[]byte,
		time.Time,
	) (AccessTokenRecord, error)
	TouchSession(context.Context, pgx.Tx, string, time.Time) error
}

type AccessTokenRecord struct {
	SessionID        string
	UserID           string
	AccessLevel      string
	UserStatus       string
	ExpiresAt        time.Time
	RevokedAt        *time.Time
	SessionRevokedAt *time.Time
}

type AuthenticateUserStore interface {
	FindByEmail(context.Context, pgx.Tx, EmailAddress) (*User, error)
}

type AuthenticateService struct {
	users           AuthenticateUserStore
	sessions        SessionStore
	audits          AuditStore
	tx              TransactionManager
	passwords       *PasswordHasher
	clock           Clock
	random          io.Reader
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	dummyHash       string
}

func NewAuthenticateService(
	users AuthenticateUserStore,
	sessions SessionStore,
	audits AuditStore,
	tx TransactionManager,
	passwords *PasswordHasher,
	accessTokenTTL time.Duration,
	refreshTokenTTL time.Duration,
	clock Clock,
	random io.Reader,
) (*AuthenticateService, error) {
	if users == nil || sessions == nil || audits == nil || tx == nil {
		return nil, errors.New("authentication stores are required")
	}
	if passwords == nil {
		return nil, errors.New("password hasher is required")
	}
	if accessTokenTTL <= 0 {
		return nil, errors.New("access token ttl must be greater than zero")
	}
	if refreshTokenTTL <= 0 {
		return nil, errors.New("refresh token ttl must be greater than zero")
	}
	if clock == nil {
		clock = SystemClock{}
	}

	dummyHash, err := passwords.Hash(dummyPasswordMaterial)
	if err != nil {
		return nil, fmt.Errorf("prepare dummy password hash: %w", err)
	}

	return &AuthenticateService{
		users:           users,
		sessions:        sessions,
		audits:          audits,
		tx:              tx,
		passwords:       passwords,
		clock:           clock,
		random:          random,
		accessTokenTTL:  accessTokenTTL,
		refreshTokenTTL: refreshTokenTTL,
		dummyHash:       dummyHash,
	}, nil
}

func (s *AuthenticateService) Authenticate(
	ctx context.Context,
	email string,
	password string,
	client SessionClientInfo,
) (IssuedCredentials, error) {
	canonicalEmail, err := CanonicalEmailAddress(email)
	if err != nil {
		s.runDummyPasswordCheck(password)
		_ = s.recordFailure(ctx, nil, "invalid_email")
		return IssuedCredentials{}, ErrAuthenticationFailed
	}

	var (
		credentials IssuedCredentials
		failed      bool
	)
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		user, err := s.users.FindByEmail(ctx, tx, canonicalEmail)
		if err != nil {
			return err
		}

		if user == nil {
			s.passwords.Verify(s.dummyHash, password)
			failed = true
			return s.audits.Append(
				ctx,
				tx,
				AuditAuthenticationFailed,
				nil,
				nil,
				RetentionRoutine,
				map[string]any{"outcome": "unknown_email"},
			)
		}
		if !s.passwords.Verify(user.PasswordHash, password) {
			failed = true
			return s.audits.Append(
				ctx,
				tx,
				AuditAuthenticationFailed,
				nil,
				&user.ID,
				RetentionRoutine,
				map[string]any{"outcome": "invalid_password"},
			)
		}
		if user.Status != StatusActive {
			failed = true
			return s.audits.Append(
				ctx,
				tx,
				AuditAuthenticationFailed,
				nil,
				&user.ID,
				RetentionRoutine,
				map[string]any{"outcome": "user_" + user.Status},
			)
		}

		now := s.clock.Now()
		csrfRaw, csrfHash, err := NewOpaqueToken(s.random)
		if err != nil {
			return err
		}

		var ip *netip.Addr
		if client.IP.IsValid() {
			addr := client.IP
			ip = &addr
		}

		sessionID, err := s.sessions.InsertSession(
			ctx,
			tx,
			user.ID,
			csrfHash,
			now,
			client.UserAgent,
			ip,
		)
		if err != nil {
			return err
		}

		accessRaw, accessHash, err := NewOpaqueToken(s.random)
		if err != nil {
			return err
		}
		refreshRaw, refreshHash, err := NewOpaqueToken(s.random)
		if err != nil {
			return err
		}

		accessExpires := now.Add(s.accessTokenTTL)
		refreshExpires := now.Add(s.refreshTokenTTL)
		if err := s.sessions.InsertAccessToken(ctx, tx, sessionID, accessHash, accessExpires); err != nil {
			return err
		}
		familyID, err := uuid.NewV7()
		if err != nil {
			return fmt.Errorf("generate refresh token family id: %w", err)
		}
		if _, err := s.sessions.InsertRefreshToken(
			ctx,
			tx,
			sessionID,
			familyID,
			refreshHash,
			refreshExpires,
			nil,
		); err != nil {
			return err
		}

		if err := s.audits.Append(
			ctx,
			tx,
			AuditAuthenticationSucceeded,
			&user.ID,
			&user.ID,
			RetentionRoutine,
			map[string]any{"outcome": "authenticated"},
		); err != nil {
			return err
		}

		credentials = IssuedCredentials{
			AccessToken:             accessRaw,
			RefreshToken:            refreshRaw,
			CSRFToken:               csrfRaw,
			AccessTokenExpiresAt:    accessExpires,
			RefreshTokenExpiresAt:   refreshExpires,
			AuthenticationSessionID: sessionID,
			UserID:                  user.ID,
			AccessLevel:             user.AccessLevel,
		}
		return nil
	})
	if err != nil {
		return IssuedCredentials{}, fmt.Errorf("authenticate user: %w", err)
	}
	if failed {
		return IssuedCredentials{}, ErrAuthenticationFailed
	}
	return credentials, nil
}

func (s *AuthenticateService) recordFailure(
	ctx context.Context,
	targetUserID *string,
	outcome string,
) error {
	return s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		return s.audits.Append(
			ctx,
			tx,
			AuditAuthenticationFailed,
			nil,
			targetUserID,
			RetentionRoutine,
			map[string]any{"outcome": outcome},
		)
	})
}

func (s *AuthenticateService) runDummyPasswordCheck(password string) {
	s.passwords.Verify(s.dummyHash, password)
}
