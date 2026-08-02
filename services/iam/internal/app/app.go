package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/config"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/delivery"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/identity"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/outbox"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/postgres"
)

type App struct {
	closeOnce sync.Once
	database  *postgres.Database
	handler   http.Handler
	validate  *identity.ValidateService
}

func New(ctx context.Context, cfg config.Config) (*App, error) {
	connectContext, cancel := context.WithTimeout(ctx, cfg.DatabaseConnectTimeout)
	defer cancel()

	database, err := postgres.Open(connectContext, postgres.Config{
		URL:               cfg.DatabaseURL,
		MaxConns:          cfg.DBMaxConns,
		MinConns:          cfg.DBMinConns,
		MaxConnLifetime:   cfg.DBMaxConnLifetime,
		MaxConnIdleTime:   cfg.DBMaxConnIdleTime,
		HealthCheckPeriod: cfg.DBHealthCheckPeriod,
	})
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	encryptor, err := loadEncryptor(cfg)
	if err != nil {
		database.Close()
		return nil, err
	}

	outboxStore, err := outbox.NewStore(database)
	if err != nil {
		database.Close()
		return nil, err
	}

	passwords := identity.NewPasswordHasher(identity.PasswordPolicy{})
	users := postgres.NewUserStore()
	challenges := postgres.NewChallengeStore()
	audits := postgres.NewAuditStore()
	sessions := postgres.NewSessionStore()

	register, err := identity.NewRegisterService(
		users,
		challenges,
		audits,
		outboxStore,
		database,
		passwords,
		encryptor,
		cfg.EmailDeliveryTopic,
		cfg.VerificationChallengeTTL,
		identity.SystemClock{},
		nil,
	)
	if err != nil {
		database.Close()
		return nil, err
	}

	verify, err := identity.NewVerifyService(
		users,
		challenges,
		audits,
		database,
		identity.SystemClock{},
	)
	if err != nil {
		database.Close()
		return nil, err
	}

	authenticate, err := identity.NewAuthenticateService(
		users,
		sessions,
		audits,
		database,
		passwords,
		cfg.AccessTokenTTL,
		cfg.RefreshTokenTTL,
		identity.SystemClock{},
		nil,
	)
	if err != nil {
		database.Close()
		return nil, err
	}

	refresh, err := identity.NewRefreshService(
		sessions,
		audits,
		database,
		cfg.AccessTokenTTL,
		cfg.RefreshTokenTTL,
		cfg.RefreshReuseGracePeriod,
		identity.SystemClock{},
		nil,
	)
	if err != nil {
		database.Close()
		return nil, err
	}

	sessionService, err := identity.NewSessionService(
		sessions,
		audits,
		database,
		identity.SystemClock{},
	)
	if err != nil {
		database.Close()
		return nil, err
	}

	passwordReset, err := identity.NewPasswordResetService(
		users,
		challenges,
		sessions,
		audits,
		outboxStore,
		database,
		passwords,
		encryptor,
		cfg.EmailDeliveryTopic,
		cfg.PasswordResetChallengeTTL,
		cfg.VerificationChallengeTTL,
		identity.SystemClock{},
		nil,
	)
	if err != nil {
		database.Close()
		return nil, err
	}

	passwordChange, err := identity.NewPasswordChangeService(
		users,
		sessions,
		audits,
		database,
		passwords,
		identity.SystemClock{},
	)
	if err != nil {
		database.Close()
		return nil, err
	}

	validate, err := identity.NewValidateService(
		sessions,
		audits,
		database,
		identity.SystemClock{},
	)
	if err != nil {
		database.Close()
		return nil, err
	}

	return &App{
		database: database,
		handler: httpapi.NewHandler(httpapi.Dependencies{
			Readiness:      database,
			Register:       register,
			Verify:         verify,
			Authenticate:   authenticate,
			Refresh:        refresh,
			Sessions:       sessionService,
			PasswordReset:  passwordReset,
			PasswordChange: passwordChange,
			Origins:        httpapi.NewOriginPolicy(cfg.AllowedOriginList()),
		}),
		validate: validate,
	}, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) ValidateService() *identity.ValidateService {
	return a.validate
}

func (a *App) Close() {
	a.closeOnce.Do(a.database.Close)
}

func loadEncryptor(cfg config.Config) (*delivery.EnvelopeEncryptor, error) {
	if cfg.NotificationsDeliveryPublicKeyFile == "" {
		return nil, fmt.Errorf("notifications delivery public key file is required")
	}
	if cfg.NotificationsDeliveryKeyID == "" {
		return nil, fmt.Errorf("notifications delivery key id is required")
	}

	pemBytes, err := os.ReadFile(cfg.NotificationsDeliveryPublicKeyFile)
	if err != nil {
		return nil, fmt.Errorf("read notifications public key: %w", err)
	}
	publicKey, err := delivery.ParseEnvelopePublicKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("parse notifications public key: %w", err)
	}
	return delivery.NewEnvelopeEncryptor(cfg.NotificationsDeliveryKeyID, publicKey)
}
