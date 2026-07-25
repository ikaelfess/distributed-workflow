package config

import (
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	_ "github.com/joho/godotenv/autoload"
)

type config struct {
	DatabaseUrl       string        `env:"DATABASE_URL" env-required:"true"`
	DbMaxOpenConns    int           `env:"DB_MAX_OPEN_CONNS" env-default:"25"`
	DbMaxIdleConns    int           `env:"DB_MAX_IDLE_CONNS" env-default:"25"`
	DbConnMaxLifetime time.Duration `env:"DB_CONN_MAX_LIFETIME" env-default:"5m"`

	LogLevel          string         `env:"LOG_LEVEL" env-default:"debug"`
	ServiceName       string         `env:"SERVICE_NAME" env-default:"iam-service"`

	AppEnv            string        `env:"APP_ENV" env-default:"local"`
	ServerAddress     string        `env:"SERVER_ADDRESS" env-default:"localhost:3000"`
	ShutdownTimeout   time.Duration `env:"SHUTDOWN_TIMEOUT" env-default:"20s"`
	ReadTimeout       time.Duration `env:"READ_TIMEOUT" env-default:"5s"`
	WriteTimeout      time.Duration `env:"WRITE_TIMEOUT" env-default:"10s"`
	IdleTimeout       time.Duration `env:"IDLE_TIMEOUT" env-default:"60s"`

	JWTSecret         string        `env:"JWT_SECRET" env-required:"true"`
	JWTIssuer         string        `env:"JWT_ISSUER" env-default:"iam-service"`
	JWTAudience       string        `env:"JWT_AUDIENCE" env-default:"distributed-workflow"`
	JWTAccessTTL      time.Duration `env:"JWT_ACCESS_TTL" env-default:"15m"`
}

func Load() (config, error) {
	var c config
	err := cleanenv.ReadEnv(&c)
	return c, err
}
