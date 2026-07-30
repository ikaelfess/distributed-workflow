package logger

import (
	"fmt"
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type Config struct {
	ServiceName string
	Level       string
	Pretty      bool
}

func NewLogger(cfg Config) (zerolog.Logger, error) {
	level, err := zerolog.ParseLevel(cfg.Level)
	if err != nil {
		return zerolog.Logger{}, fmt.Errorf("parse log level %q: %w", cfg.Level, err)
	}

	zerolog.SetGlobalLevel(level)
	zerolog.TimeFieldFormat = time.RFC3339

	if cfg.Pretty {
		return log.Output(zerolog.ConsoleWriter{Out: os.Stdout}).
			With().
			Timestamp().
			Str("service", cfg.ServiceName).
			Logger(), nil
	}

	return zerolog.New(os.Stdout).
		With().
		Timestamp().
		Str("service", cfg.ServiceName).
		Logger(), nil
}
