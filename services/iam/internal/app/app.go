package app

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/config"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/postgres"
)

type App struct {
	closeOnce sync.Once
	database  *postgres.Database
	handler   http.Handler
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

	return &App{
		database: database,
		handler:  httpapi.NewHandler(database),
	}, nil
}

func (a *App) Handler() http.Handler {
	return a.handler
}

func (a *App) Close() {
	a.closeOnce.Do(a.database.Close)
}
