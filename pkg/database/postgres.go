package database

import (
	"context"
	"database/sql"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"

	"github.com/rs/zerolog"
	"github.com/uptrace/bun/extra/bunzerolog"
)

type PostgresConfig struct {
	DSN             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	Logger          *zerolog.Logger
}

type Postgres struct {
	DB *bun.DB
}

func NewPostgres(ctx context.Context, cfg PostgresConfig) (*Postgres, error) {
	sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(cfg.DSN)))
	sqldb.SetMaxOpenConns(cfg.MaxOpenConns)
	sqldb.SetMaxIdleConns(cfg.MaxIdleConns)
	sqldb.SetConnMaxLifetime(cfg.ConnMaxLifetime)

	db := bun.NewDB(sqldb, pgdialect.New())
	loggerHook := bunzerolog.NewQueryHook(
		bunzerolog.WithLogger(cfg.Logger),
		bunzerolog.WithQueryLogLevel(zerolog.DebugLevel),
		bunzerolog.WithSlowQueryLogLevel(zerolog.WarnLevel),
		bunzerolog.WithErrorQueryLogLevel(zerolog.ErrorLevel),
		bunzerolog.WithSlowQueryThreshold(3 * time.Second),
	)
	db.AddQueryHook(loggerHook)

	if err := db.PingContext(ctx); err != nil {
		if err := db.Close(); err != nil {
			return nil, err
		}

		return nil, err
	}

	return &Postgres{DB: db}, nil
}

func (p *Postgres) Close(_ context.Context) error {
	return p.DB.Close()
}
