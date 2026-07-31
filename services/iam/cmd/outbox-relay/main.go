package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/ikaelfess/distributed-workflow/pkg/logger"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/config"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/kafka"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/outbox"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/postgres"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/relay"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "outbox relay: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	appConfig, err := config.LoadRelay()
	if err != nil {
		return err
	}

	appLogger, err := logger.NewLogger(logger.Config{
		ServiceName: "iam-outbox-relay",
		Level:       appConfig.LogLevel,
		Pretty:      appConfig.AppEnv == "local",
	})
	if err != nil {
		return fmt.Errorf("configure logger: %w", err)
	}

	connectContext, cancelConnect := context.WithTimeout(
		ctx,
		appConfig.DatabaseConnectTimeout,
	)
	database, err := postgres.Open(connectContext, postgres.Config{
		URL:               appConfig.DatabaseURL,
		MaxConns:          appConfig.DBMaxConns,
		MinConns:          appConfig.DBMinConns,
		MaxConnLifetime:   appConfig.DBMaxConnLifetime,
		MaxConnIdleTime:   appConfig.DBMaxConnIdleTime,
		HealthCheckPeriod: appConfig.DBHealthCheckPeriod,
	})
	cancelConnect()
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer database.Close()

	publisher, err := kafka.NewPublisher(
		appConfig.KafkaBrokerList(),
		appConfig.KafkaDeliveryTimeout,
	)
	if err != nil {
		return err
	}
	defer publisher.Close()

	store, err := outbox.NewStore(database)
	if err != nil {
		return err
	}
	worker, err := relay.New(relay.Config{
		BatchSize:    appConfig.RelayBatchSize,
		Lease:        appConfig.RelayLease,
		PollInterval: appConfig.RelayPollInterval,
		RetryDelay:   appConfig.RelayRetryDelay,
	}, store, publisher, relay.SystemClock{})
	if err != nil {
		return err
	}
	readiness, err := relay.NewReadiness(database, publisher)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", appConfig.RelayServerAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", appConfig.RelayServerAddress, err)
	}

	server := httpapi.NewServer(httpapi.ServerConfig{
		ReadTimeout:       appConfig.ReadTimeout,
		ReadHeaderTimeout: appConfig.ReadHeaderTimeout,
		WriteTimeout:      appConfig.WriteTimeout,
		IdleTimeout:       appConfig.IdleTimeout,
	}, httpapi.NewHandler(httpapi.Dependencies{Readiness: readiness}), appLogger)

	runContext, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	serverErrors := make(chan error, 1)
	go func() {
		serverErrors <- server.Serve(listener)
	}()

	relayErrors := make(chan error, 1)
	go func() {
		relayErrors <- worker.Run(runContext, func(err error) {
			appLogger.Error().Err(err).Msg("outbox relay iteration failed")
		})
	}()

	var runErr error
	serverStopped := false
	relayStopped := false
	select {
	case runErr = <-serverErrors:
		serverStopped = true
	case runErr = <-relayErrors:
		relayStopped = true
	case <-ctx.Done():
	}

	cancelRun()
	shutdownContext, cancelShutdown := context.WithTimeout(
		context.Background(),
		appConfig.ShutdownTimeout,
	)
	defer cancelShutdown()

	shutdownErr := server.Shutdown(shutdownContext)
	if !serverStopped {
		runErr = errors.Join(runErr, <-serverErrors)
	}
	if !relayStopped {
		runErr = errors.Join(runErr, <-relayErrors)
	}
	if err := errors.Join(runErr, shutdownErr); err != nil {
		return err
	}

	appLogger.Info().Msg("outbox relay stopped")
	return nil
}
