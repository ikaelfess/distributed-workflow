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

	KafkaBrokers         string        `env:"KAFKA_BROKERS" env-default:"localhost:9092"`
	KafkaDeliveryTimeout time.Duration `env:"KAFKA_DELIVERY_TIMEOUT" env-default:"5s"`
	EmailDeliveryTopic   string        `env:"EMAIL_DELIVERY_TOPIC" env-default:"iam.email-delivery-request.v1"`

	NotificationsDeliveryPublicKeyFile string `env:"NOTIFICATIONS_DELIVERY_PUBLIC_KEY_FILE"`
	NotificationsDeliveryKeyID         string `env:"NOTIFICATIONS_DELIVERY_KEY_ID" env-default:"notifications-delivery-v1"`

	RelayServerAddress string        `env:"RELAY_SERVER_ADDRESS" env-default:"localhost:3002"`
	RelayBatchSize     int           `env:"RELAY_BATCH_SIZE" env-default:"50"`
	RelayLease         time.Duration `env:"RELAY_LEASE" env-default:"30s"`
	RelayPollInterval  time.Duration `env:"RELAY_POLL_INTERVAL" env-default:"1s"`
	RelayRetryDelay    time.Duration `env:"RELAY_RETRY_DELAY" env-default:"5s"`

	VerificationChallengeTTL time.Duration `env:"VERIFICATION_CHALLENGE_TTL" env-default:"24h"`
}

func Load() (Config, error) {
	var cfg Config
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return Config{}, fmt.Errorf("read environment: %w", err)
	}
	return cfg, nil
}

func LoadAPI() (Config, error) {
	cfg, err := Load()
	if err != nil {
		return Config{}, err
	}
	if err := cfg.ValidateAPI(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadRelay() (Config, error) {
	cfg, err := Load()
	if err != nil {
		return Config{}, err
	}
	if err := cfg.ValidateRelay(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) ValidateAPI() error {
	var validationErrors []error
	c.appendSharedValidation(&validationErrors)

	if strings.TrimSpace(c.ServerAddress) == "" {
		validationErrors = append(validationErrors, errors.New("server address is required"))
	} else if _, _, err := net.SplitHostPort(c.ServerAddress); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("server address: %w", err))
	}
	if strings.TrimSpace(c.EmailDeliveryTopic) == "" {
		validationErrors = append(validationErrors, errors.New("email delivery topic is required"))
	}
	if strings.TrimSpace(c.NotificationsDeliveryPublicKeyFile) == "" {
		validationErrors = append(
			validationErrors,
			errors.New("notifications delivery public key file is required"),
		)
	}
	if strings.TrimSpace(c.NotificationsDeliveryKeyID) == "" {
		validationErrors = append(
			validationErrors,
			errors.New("notifications delivery key id is required"),
		)
	}
	if c.VerificationChallengeTTL <= 0 {
		validationErrors = append(
			validationErrors,
			errors.New("verification challenge ttl must be greater than zero"),
		)
	}

	if err := errors.Join(validationErrors...); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	return nil
}

func (c Config) ValidateRelay() error {
	var validationErrors []error
	c.appendSharedValidation(&validationErrors)
	c.appendPositiveDuration(&validationErrors, "kafka delivery timeout", c.KafkaDeliveryTimeout)
	c.appendPositiveDuration(&validationErrors, "relay lease", c.RelayLease)
	c.appendPositiveDuration(&validationErrors, "relay poll interval", c.RelayPollInterval)
	c.appendPositiveDuration(&validationErrors, "relay retry delay", c.RelayRetryDelay)

	if len(c.KafkaBrokerList()) == 0 {
		validationErrors = append(validationErrors, errors.New("at least one kafka broker is required"))
	}
	if strings.TrimSpace(c.EmailDeliveryTopic) == "" {
		validationErrors = append(validationErrors, errors.New("email delivery topic is required"))
	}
	if _, _, err := net.SplitHostPort(c.RelayServerAddress); err != nil {
		validationErrors = append(validationErrors, fmt.Errorf("relay server address: %w", err))
	}
	if c.RelayBatchSize < 1 {
		validationErrors = append(validationErrors, errors.New("relay batch size must be at least one"))
	}
	if c.RelayLease <= c.KafkaDeliveryTimeout {
		validationErrors = append(
			validationErrors,
			errors.New("relay lease must exceed kafka delivery timeout"),
		)
	}

	if err := errors.Join(validationErrors...); err != nil {
		return fmt.Errorf("validate config: %w", err)
	}
	return nil
}

func (c Config) appendSharedValidation(validationErrors *[]error) {
	databaseURL, err := url.Parse(c.DatabaseURL)
	if err != nil {
		*validationErrors = append(*validationErrors, fmt.Errorf("database url: %w", err))
	} else if databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql" {
		*validationErrors = append(*validationErrors, errors.New("database url must use postgres scheme"))
	} else {
		if databaseURL.Hostname() == "" {
			*validationErrors = append(*validationErrors, errors.New("database url host is required"))
		}
		if strings.Trim(databaseURL.Path, "/") == "" {
			*validationErrors = append(*validationErrors, errors.New("database url name is required"))
		}
	}

	if c.DBMaxConns < 1 {
		*validationErrors = append(*validationErrors, errors.New("db max conns must be at least 1"))
	}
	if c.DBMinConns < 0 {
		*validationErrors = append(*validationErrors, errors.New("db min conns must not be negative"))
	}
	if c.DBMinConns > c.DBMaxConns {
		*validationErrors = append(
			*validationErrors,
			errors.New("db min conns must not exceed db max conns"),
		)
	}

	c.appendPositiveDuration(validationErrors, "db max conn lifetime", c.DBMaxConnLifetime)
	c.appendPositiveDuration(validationErrors, "db max conn idle time", c.DBMaxConnIdleTime)
	c.appendPositiveDuration(validationErrors, "db health check period", c.DBHealthCheckPeriod)
	c.appendPositiveDuration(validationErrors, "database connect timeout", c.DatabaseConnectTimeout)
	c.appendPositiveDuration(validationErrors, "shutdown timeout", c.ShutdownTimeout)
	c.appendPositiveDuration(validationErrors, "read timeout", c.ReadTimeout)
	c.appendPositiveDuration(validationErrors, "read header timeout", c.ReadHeaderTimeout)
	c.appendPositiveDuration(validationErrors, "write timeout", c.WriteTimeout)
	c.appendPositiveDuration(validationErrors, "idle timeout", c.IdleTimeout)

	if strings.TrimSpace(c.ServiceName) == "" {
		*validationErrors = append(*validationErrors, errors.New("service name is required"))
	}
	if strings.TrimSpace(c.LogLevel) == "" {
		*validationErrors = append(*validationErrors, errors.New("log level is required"))
	}
}

func (c Config) appendPositiveDuration(
	validationErrors *[]error,
	name string,
	value time.Duration,
) {
	if value <= 0 {
		*validationErrors = append(
			*validationErrors,
			fmt.Errorf("%s must be greater than zero", name),
		)
	}
}

func (c Config) KafkaBrokerList() []string {
	var brokers []string
	for broker := range strings.SplitSeq(c.KafkaBrokers, ",") {
		broker = strings.TrimSpace(broker)
		if broker != "" {
			brokers = append(brokers, broker)
		}
	}
	return brokers
}
