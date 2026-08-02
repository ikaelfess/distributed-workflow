package identity

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrRefreshFailed         = errors.New("refresh failed")
	ErrRefreshReuseDetected  = errors.New("refresh token reuse detected")
	ErrInvalidCSRFToken      = errors.New("invalid csrf token")
	ErrAuthenticationSession = errors.New("authentication session unavailable")
)

type RefreshTokenRecord struct {
	ID                 string
	SessionID          string
	FamilyID           uuid.UUID
	UserID             string
	UserStatus         string
	AccessLevel        string
	CSRFSecretHash     []byte
	ExpiresAt          time.Time
	RevokedAt          *time.Time
	ReusedAt           *time.Time
	SessionRevokedAt   *time.Time
	SuccessorID        *string
	SuccessorCreatedAt *time.Time
}

type RefreshStore interface {
	FindRefreshTokenForUpdate(
		context.Context,
		pgx.Tx,
		[]byte,
	) (RefreshTokenRecord, error)
	MarkRefreshReused(context.Context, pgx.Tx, string, time.Time) error
	RevokeTokenFamily(context.Context, pgx.Tx, uuid.UUID, time.Time) error
	RevokeSessionAccessTokens(context.Context, pgx.Tx, string, time.Time) error
	RevokeRefreshToken(context.Context, pgx.Tx, string, time.Time) error
	InsertAccessToken(context.Context, pgx.Tx, string, []byte, time.Time) error
	InsertRefreshToken(
		context.Context,
		pgx.Tx,
		string,
		uuid.UUID,
		[]byte,
		time.Time,
		*string,
	) (string, error)
	TouchSession(context.Context, pgx.Tx, string, time.Time) error
}

type RefreshService struct {
	tokens          RefreshStore
	audits          AuditStore
	tx              TransactionManager
	clock           Clock
	random          io.Reader
	accessTokenTTL  time.Duration
	refreshTokenTTL time.Duration
	reuseGrace      time.Duration
}

func NewRefreshService(
	tokens RefreshStore,
	audits AuditStore,
	tx TransactionManager,
	accessTokenTTL time.Duration,
	refreshTokenTTL time.Duration,
	reuseGrace time.Duration,
	clock Clock,
	random io.Reader,
) (*RefreshService, error) {
	if tokens == nil || audits == nil || tx == nil {
		return nil, errors.New("refresh stores are required")
	}
	if accessTokenTTL <= 0 {
		return nil, errors.New("access token ttl must be greater than zero")
	}
	if refreshTokenTTL <= 0 {
		return nil, errors.New("refresh token ttl must be greater than zero")
	}
	if reuseGrace < 0 {
		return nil, errors.New("refresh reuse grace must not be negative")
	}
	if clock == nil {
		clock = SystemClock{}
	}
	return &RefreshService{
		tokens:          tokens,
		audits:          audits,
		tx:              tx,
		clock:           clock,
		random:          random,
		accessTokenTTL:  accessTokenTTL,
		refreshTokenTTL: refreshTokenTTL,
		reuseGrace:      reuseGrace,
	}, nil
}

func (s *RefreshService) Refresh(
	ctx context.Context,
	rawRefreshToken string,
	rawCSRFToken string,
) (IssuedCredentials, error) {
	tokenHash, err := HashOpaqueToken(rawRefreshToken)
	if err != nil {
		return IssuedCredentials{}, ErrRefreshFailed
	}
	csrfHash, err := HashOpaqueToken(rawCSRFToken)
	if err != nil {
		return IssuedCredentials{}, ErrInvalidCSRFToken
	}

	var (
		credentials IssuedCredentials
		failed      error
	)
	err = s.tx.WithinTransaction(ctx, func(ctx context.Context, tx pgx.Tx) error {
		record, err := s.tokens.FindRefreshTokenForUpdate(ctx, tx, tokenHash)
		if errors.Is(err, ErrRefreshFailed) {
			failed = ErrRefreshFailed
			return s.audits.Append(
				ctx,
				tx,
				AuditRefreshRejected,
				nil,
				nil,
				RetentionRoutine,
				map[string]any{"outcome": "unknown"},
			)
		}
		if err != nil {
			return err
		}
		if subtle.ConstantTimeCompare(record.CSRFSecretHash, csrfHash) != 1 {
			failed = ErrInvalidCSRFToken
			return s.audits.Append(
				ctx,
				tx,
				AuditRefreshRejected,
				nil,
				&record.UserID,
				RetentionRoutine,
				map[string]any{"outcome": "invalid_csrf"},
			)
		}
		if record.SessionRevokedAt != nil {
			failed = ErrRefreshFailed
			return s.audits.Append(
				ctx,
				tx,
				AuditRefreshRejected,
				nil,
				&record.UserID,
				RetentionRoutine,
				map[string]any{"outcome": "session_revoked"},
			)
		}
		if record.UserStatus != StatusActive {
			failed = ErrRefreshFailed
			return s.audits.Append(
				ctx,
				tx,
				AuditRefreshRejected,
				nil,
				&record.UserID,
				RetentionRoutine,
				map[string]any{"outcome": "user_" + record.UserStatus},
			)
		}

		now := s.clock.Now()
		if record.ReusedAt != nil {
			failed = ErrRefreshFailed
			return s.audits.Append(
				ctx,
				tx,
				AuditRefreshRejected,
				nil,
				&record.UserID,
				RetentionRoutine,
				map[string]any{"outcome": "already_reused"},
			)
		}
		if record.RevokedAt != nil {
			return s.handleRotatedRefresh(ctx, tx, record, now, &failed)
		}
		if !record.ExpiresAt.After(now) {
			failed = ErrRefreshFailed
			return s.audits.Append(
				ctx,
				tx,
				AuditRefreshRejected,
				nil,
				&record.UserID,
				RetentionRoutine,
				map[string]any{"outcome": "expired"},
			)
		}

		if err := s.tokens.RevokeRefreshToken(ctx, tx, record.ID, now); err != nil {
			return err
		}
		if err := s.tokens.RevokeSessionAccessTokens(ctx, tx, record.SessionID, now); err != nil {
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
		if err := s.tokens.InsertAccessToken(
			ctx,
			tx,
			record.SessionID,
			accessHash,
			accessExpires,
		); err != nil {
			return err
		}
		rotatedFrom := record.ID
		if _, err := s.tokens.InsertRefreshToken(
			ctx,
			tx,
			record.SessionID,
			record.FamilyID,
			refreshHash,
			refreshExpires,
			&rotatedFrom,
		); err != nil {
			return err
		}
		if err := s.tokens.TouchSession(ctx, tx, record.SessionID, now); err != nil {
			return err
		}
		if err := s.audits.Append(
			ctx,
			tx,
			AuditRefreshSucceeded,
			&record.UserID,
			&record.UserID,
			RetentionRoutine,
			map[string]any{"outcome": "rotated"},
		); err != nil {
			return err
		}

		credentials = IssuedCredentials{
			AccessToken:             accessRaw,
			RefreshToken:            refreshRaw,
			CSRFToken:               rawCSRFToken,
			AccessTokenExpiresAt:    accessExpires,
			RefreshTokenExpiresAt:   refreshExpires,
			AuthenticationSessionID: record.SessionID,
			UserID:                  record.UserID,
			AccessLevel:             record.AccessLevel,
		}
		return nil
	})
	if err != nil {
		return IssuedCredentials{}, fmt.Errorf("refresh authentication session: %w", err)
	}
	if failed != nil {
		return IssuedCredentials{}, failed
	}
	return credentials, nil
}

func (s *RefreshService) handleRotatedRefresh(
	ctx context.Context,
	tx pgx.Tx,
	record RefreshTokenRecord,
	now time.Time,
	failed *error,
) error {
	if record.SuccessorID == nil || record.SuccessorCreatedAt == nil {
		*failed = ErrRefreshFailed
		return s.audits.Append(
			ctx,
			tx,
			AuditRefreshRejected,
			nil,
			&record.UserID,
			RetentionRoutine,
			map[string]any{"outcome": "revoked"},
		)
	}

	age := now.Sub(record.SuccessorCreatedAt.UTC())
	if age <= s.reuseGrace {
		*failed = ErrRefreshFailed
		return s.audits.Append(
			ctx,
			tx,
			AuditRefreshRejected,
			nil,
			&record.UserID,
			RetentionRoutine,
			map[string]any{"outcome": "concurrent_reuse"},
		)
	}

	if err := s.tokens.MarkRefreshReused(ctx, tx, record.ID, now); err != nil {
		return err
	}
	if err := s.tokens.RevokeTokenFamily(ctx, tx, record.FamilyID, now); err != nil {
		return err
	}
	*failed = ErrRefreshReuseDetected
	return s.audits.Append(
		ctx,
		tx,
		AuditRefreshReuseDetected,
		nil,
		&record.UserID,
		RetentionSensitive,
		map[string]any{"outcome": "family_revoked"},
	)
}
