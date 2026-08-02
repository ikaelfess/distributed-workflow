package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net/netip"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrSessionNotFound = errors.New("authentication session not found")
)

type AuthenticationSessionView struct {
	ID             string
	CreatedAt      time.Time
	LastUsedAt     time.Time
	ClientMetadata string
	IP             *netip.Addr
	Current        bool
}

type SessionManagementStore interface {
	FindAccessTokenByHash(
		context.Context,
		pgx.Tx,
		[]byte,
		time.Time,
	) (AccessTokenRecord, error)
	FindSessionCSRFHash(context.Context, pgx.Tx, string) ([]byte, error)
	ListSessionsByUser(
		context.Context,
		pgx.Tx,
		string,
	) ([]AuthenticationSessionView, error)
	RevokeSession(context.Context, pgx.Tx, string, string, time.Time) (bool, error)
	RevokeAllSessionsForUser(context.Context, pgx.Tx, string, time.Time) error
}

type SessionService struct {
	sessions SessionManagementStore
	audits   AuditStore
	tx       TransactionManager
	clock    Clock
}

func NewSessionService(
	sessions SessionManagementStore,
	audits AuditStore,
	tx TransactionManager,
	clock Clock,
) (*SessionService, error) {
	if sessions == nil || audits == nil || tx == nil {
		return nil, errors.New("session stores are required")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &SessionService{
		sessions: sessions,
		audits:   audits,
		tx:       tx,
		clock:    clock,
	}, nil
}

func (s *SessionService) List(
	ctx context.Context,
	rawAccessToken string,
) ([]AuthenticationSessionView, error) {
	record, err := s.requireActiveAccess(ctx, rawAccessToken)
	if err != nil {
		return nil, err
	}

	var views []AuthenticationSessionView
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		listed, err := s.sessions.ListSessionsByUser(ctx, tx, record.UserID)
		if err != nil {
			return err
		}
		for i := range listed {
			listed[i].Current = listed[i].ID == record.SessionID
		}
		views = listed
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list authentication sessions: %w", err)
	}
	return views, nil
}

func (s *SessionService) Logout(
	ctx context.Context,
	rawAccessToken string,
	rawCSRFToken string,
) error {
	return s.revokeCurrent(ctx, rawAccessToken, rawCSRFToken, AuditLogoutSucceeded, "logout")
}

func (s *SessionService) RevokeOne(
	ctx context.Context,
	rawAccessToken string,
	rawCSRFToken string,
	sessionID string,
) (revokedCurrent bool, err error) {
	record, err := s.requireActiveAccess(ctx, rawAccessToken)
	if err != nil {
		return false, err
	}
	if err := s.requireCSRF(ctx, record.SessionID, rawCSRFToken); err != nil {
		return false, err
	}

	now := s.clock.Now()
	var found bool
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ok, err := s.sessions.RevokeSession(ctx, tx, record.UserID, sessionID, now)
		if err != nil {
			return err
		}
		found = ok
		if !ok {
			return nil
		}
		return s.audits.Append(
			ctx,
			tx,
			AuditSessionRevoked,
			&record.UserID,
			&record.UserID,
			RetentionRoutine,
			map[string]any{"outcome": "revoked_one"},
		)
	})
	if err != nil {
		return false, fmt.Errorf("revoke authentication session: %w", err)
	}
	if !found {
		return false, ErrSessionNotFound
	}
	return sessionID == record.SessionID, nil
}

func (s *SessionService) RevokeAll(
	ctx context.Context,
	rawAccessToken string,
	rawCSRFToken string,
) error {
	record, err := s.requireActiveAccess(ctx, rawAccessToken)
	if err != nil {
		return err
	}
	if err := s.requireCSRF(ctx, record.SessionID, rawCSRFToken); err != nil {
		return err
	}

	now := s.clock.Now()
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := s.sessions.RevokeAllSessionsForUser(ctx, tx, record.UserID, now); err != nil {
			return err
		}
		return s.audits.Append(
			ctx,
			tx,
			AuditSessionsRevokedAll,
			&record.UserID,
			&record.UserID,
			RetentionSensitive,
			map[string]any{"outcome": "revoked_all"},
		)
	})
	if err != nil {
		return fmt.Errorf("revoke all authentication sessions: %w", err)
	}
	return nil
}

func (s *SessionService) revokeCurrent(
	ctx context.Context,
	rawAccessToken string,
	rawCSRFToken string,
	eventType string,
	outcome string,
) error {
	record, err := s.requireActiveAccess(ctx, rawAccessToken)
	if err != nil {
		return err
	}
	if err := s.requireCSRF(ctx, record.SessionID, rawCSRFToken); err != nil {
		return err
	}

	now := s.clock.Now()
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		ok, err := s.sessions.RevokeSession(ctx, tx, record.UserID, record.SessionID, now)
		if err != nil {
			return err
		}
		if !ok {
			return ErrAuthenticationSession
		}
		return s.audits.Append(
			ctx,
			tx,
			eventType,
			&record.UserID,
			&record.UserID,
			RetentionRoutine,
			map[string]any{"outcome": outcome},
		)
	})
	if err != nil {
		return fmt.Errorf("logout authentication session: %w", err)
	}
	return nil
}

func (s *SessionService) requireActiveAccess(
	ctx context.Context,
	rawAccessToken string,
) (AccessTokenRecord, error) {
	tokenHash, err := HashOpaqueToken(rawAccessToken)
	if err != nil {
		return AccessTokenRecord{}, ErrInvalidAccessToken
	}

	now := s.clock.Now()
	var record AccessTokenRecord
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		found, err := s.sessions.FindAccessTokenByHash(ctx, tx, tokenHash, now)
		if err != nil {
			return err
		}
		record = found
		return nil
	})
	if errors.Is(err, ErrInvalidAccessToken) {
		return AccessTokenRecord{}, ErrInvalidAccessToken
	}
	if err != nil {
		return AccessTokenRecord{}, fmt.Errorf("resolve access token: %w", err)
	}
	if record.RevokedAt != nil || record.SessionRevokedAt != nil {
		return AccessTokenRecord{}, ErrInvalidAccessToken
	}
	if !record.ExpiresAt.After(now) {
		return AccessTokenRecord{}, ErrInvalidAccessToken
	}
	if record.UserStatus != StatusActive {
		return AccessTokenRecord{}, ErrInvalidAccessToken
	}
	return record, nil
}

func (s *SessionService) requireCSRF(
	ctx context.Context,
	sessionID string,
	rawCSRFToken string,
) error {
	csrfHash, err := HashOpaqueToken(rawCSRFToken)
	if err != nil {
		return ErrInvalidCSRFToken
	}

	var stored []byte
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		hash, err := s.sessions.FindSessionCSRFHash(ctx, tx, sessionID)
		if err != nil {
			return err
		}
		stored = hash
		return nil
	})
	if errors.Is(err, ErrAuthenticationSession) {
		return ErrInvalidAccessToken
	}
	if err != nil {
		return fmt.Errorf("resolve csrf secret: %w", err)
	}
	if subtle.ConstantTimeCompare(stored, csrfHash) != 1 {
		return ErrInvalidCSRFToken
	}
	return nil
}
