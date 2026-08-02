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

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/app"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/config"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/grpcapi"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/httpapi"
)

func main() {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "iam: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx context.Context) error {
	appConfig, err := config.LoadAPI()
	if err != nil {
		return err
	}

	appLogger, err := logger.NewLogger(logger.Config{
		ServiceName: appConfig.ServiceName,
		Level:       appConfig.LogLevel,
		Pretty:      appConfig.AppEnv == "local",
	})
	if err != nil {
		return fmt.Errorf("configure logger: %w", err)
	}

	application, err := app.New(ctx, appConfig)
	if err != nil {
		return err
	}
	defer application.Close()

	httpListener, err := net.Listen("tcp", appConfig.ServerAddress)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", appConfig.ServerAddress, err)
	}

	grpcListener, err := net.Listen("tcp", appConfig.GRPCAddress)
	if err != nil {
		return fmt.Errorf("listen on grpc %s: %w", appConfig.GRPCAddress, err)
	}

	httpServer := httpapi.NewServer(httpapi.ServerConfig{
		ReadTimeout:       appConfig.ReadTimeout,
		ReadHeaderTimeout: appConfig.ReadHeaderTimeout,
		WriteTimeout:      appConfig.WriteTimeout,
		IdleTimeout:       appConfig.IdleTimeout,
	}, application.Handler(), appLogger)

	grpcServer := grpcapi.NewServer(application.ValidateService(), appLogger)

	serverErrors := make(chan error, 2)
	go func() {
		serverErrors <- httpServer.Serve(httpListener)
	}()
	go func() {
		serverErrors <- grpcServer.Serve(grpcListener)
	}()

	select {
	case err := <-serverErrors:
		grpcServer.GracefulStop()
		shutdownContext, cancel := context.WithTimeout(
			context.Background(),
			appConfig.ShutdownTimeout,
		)
		defer cancel()
		_ = httpServer.Shutdown(shutdownContext)
		return errors.Join(err, <-serverErrors)
	case <-ctx.Done():
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), appConfig.ShutdownTimeout)
	defer cancel()

	grpcDone := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(grpcDone)
	}()

	shutdownErr := httpServer.Shutdown(shutdownContext)
	select {
	case <-grpcDone:
	case <-shutdownContext.Done():
		shutdownErr = errors.Join(shutdownErr, shutdownContext.Err())
	}

	serveErr := errors.Join(<-serverErrors, <-serverErrors)
	if err := errors.Join(shutdownErr, serveErr); err != nil {
		return err
	}

	appLogger.Info().Msg("iam stopped")
	return nil
}
