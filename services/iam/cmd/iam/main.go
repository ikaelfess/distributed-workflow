package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ikaelfess/distributed-workflow/pkg/database"
	"github.com/ikaelfess/distributed-workflow/pkg/httpserver"
	"github.com/ikaelfess/distributed-workflow/pkg/logger"
	"github.com/ikaelfess/distributed-workflow/pkg/shutdown"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/bootstrap"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/config"
)

func main() {
	appConfig, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "app config err: %v\n", err)
		os.Exit(1)
	}

	appLogger, err := logger.NewLogger(logger.Config{
		ServiceName: appConfig.ServiceName,
		Level:       appConfig.LogLevel,
		Pretty:      true,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "app logger err: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	db, err := database.NewPostgres(ctx, database.PostgresConfig{
		DSN:             appConfig.DatabaseUrl,
		MaxOpenConns:    appConfig.DbMaxOpenConns,
		MaxIdleConns:    appConfig.DbMaxIdleConns,
		ConnMaxLifetime: appConfig.DbConnMaxLifetime,
		Logger:          &appLogger,
	})
	if err != nil {
		appLogger.Fatal().Err(err).Msg("db failed")
	}

	server := httpserver.New(httpserver.Config{
		Addr:         appConfig.ServerAddress,
		ReadTimeout:  appConfig.ReadTimeout,
		WriteTimeout: appConfig.WriteTimeout,
		IdleTimeout:  appConfig.IdleTimeout,
		Logger:       appLogger,
	})

	app := bootstrap.New(db)
	server.RegisterModules(app.Modules...)

	go server.Start()

	manager := shutdown.New()
	manager.Register(0, server.Close)
	manager.Register(1, db.Close)

	runCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-runCtx.Done()
	stop()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), appConfig.ShutdownTimeout)
	defer shutdownCancel()
	if err := manager.Shutdown(shutdownCtx); err != nil {
		appLogger.Error().Err(err).Msg("shutdown")
	}
}
