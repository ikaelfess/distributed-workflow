package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	DatabaseURL            string        `env:"DATABASE_URL" env-required:"true"`
	DBMaxConns             int32         `env:"DB_MAX_CONNS" env-default:"25"`
	DBMinConns             int32         `env:"DB_MIN_CONNS" env-default:"2"`
	DBMaxConnLifetime      time.Duration `env:"DB_MAX_CONN_LIFETIME" env-default:"30m"`
	DBMaxConnIdleTime      time.Duration `env:"DB_MAX_CONN_IDLE_TIME" env-default:"5m"`
	DBHealthCheckPeriod    time.Duration `env:"DB_HEALTH_CHECK_PERIOD" env-default:"30s"`
	DatabaseConnectTimeout time.Duration `env:"DATABASE_CONNECT_TIMEOUT" env-default:"5s"`

	LogLevel    string `env:"LOG_LEVEL" env-default:"debug"`
	ServiceName string `env:"SERVICE_NAME" env-default:"iam-service"`
	AppEnv      string `env:"APP_ENV" env-default:"local"`

	ServerAddress     string        `env:"SERVER_ADDRESS" env-default:"localhost:3000"`
	ShutdownTimeout   time.Duration `env:"SHUTDOWN_TIMEOUT" env-default:"20s"`
	ReadTimeout       time.Duration `env:"READ_TIMEOUT" env-default:"5s"`
	ReadHeaderTimeout time.Duration `env:"READ_HEADER_TIMEOUT" env-default:"2s"`
	WriteTimeout      time.Duration `env:"WRITE_TIMEOUT" env-default:"10s"`
	IdleTimeout       time.Duration `env:"IDLE_TIMEOUT" env-default:"60s"`
}

func Load() (Config, error) {
	var cfg Config
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return Config{}, fmt.Errorf("read environment: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func (c Config) Validate() error {
	var validationErrors []error

	databaseURL, err := url.Parse(c.DatabaseURL)
	if err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("database url: %w", err))
	} else if databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql" {
		validationErrors = append(validationErrors, errors.New("database url must use postgres scheme"))
	} else {
		if databaseURL.Hostname() == "" {
			validationErrors = append(validationErrors, errors.New("database url host is required"))
		}
		if strings.Trim(databaseURL.Path, "/") == "" {
			validationErrors = append(validationErrors, errors.New("database url name is required"))
		}
	}

	if c.DBMaxConns < 1 {
		validationErrors = append(validationErrors, errors.New("db max conns must be at least 1"))
	}
	if c.DBMinConns < 0 {
		validationErrors = append(validationErrors, errors.New("db min conns must not be negative"))
	}
	if c.DBMinConns > c.DBMaxConns {
		validationErrors = append(validationErrors, errors.New("db min conns must not exceed db max conns"))
	}

	validatePositiveDuration := func(name string, value time.Duration) {
		if value <= 0 {
			validationErrors = append(
				validationErrors,
				fmt.Errorf("%s must be greater than zero", name),
			)
		}
	}

	validatePositiveDuration("db max conn lifetime", c.DBMaxConnLifetime)
	validatePositiveDuration("db max conn idle time", c.DBMaxConnIdleTime)
	validatePositiveDuration("db health check period", c.DBHealthCheckPeriod)
	validatePositiveDuration("database connect timeout", c.DatabaseConnectTimeout)
	validatePositiveDuration("shutdown timeout", c.ShutdownTimeout)
	validatePositiveDuration("read timeout", c.ReadTimeout)
	validatePositiveDuration("read header timeout", c.ReadHeaderTimeout)
	validatePositiveDuration("write timeout", c.WriteTimeout)
	validatePositiveDuration("idle timeout", c.IdleTimeout)

	if strings.TrimSpace(c.ServerAddress) == "" {
		validationErrors = append(validationErrors, errors.New("server address is required"))
	} else if _, _, err := net.SplitHostPort(c.ServerAddress); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("server address: %w", err))
	}
	if strings.TrimSpace(c.ServiceName) == "" {
		validationErrors = append(validationErrors, errors.New("service name is required"))
	}
	if strings.TrimSpace(c.LogLevel) == "" {
		validationErrors = append(validationErrors, errors.New("log level is required"))
	}

	if err := errors.Join(validationErrors...); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}

	return nil
}
