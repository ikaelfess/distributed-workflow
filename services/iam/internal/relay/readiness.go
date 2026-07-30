package relay

import (
	"context"
	"errors"
	"fmt"
)

type Probe interface {
	Ping(context.Context) error
}

type Readiness struct {
	postgres Probe
	kafka    Probe
}

func NewReadiness(postgres Probe, kafka Probe) (*Readiness, error) {
	if postgres == nil {
		return nil, errors.New("postgres probe is required")
	}
	if kafka == nil {
		return nil, errors.New("kafka probe is required")
	}
	return &Readiness{postgres: postgres, kafka: kafka}, nil
}

func (r *Readiness) Ping(ctx context.Context) error {
	if err := r.postgres.Ping(ctx); err != nil {
		return fmt.Errorf("postgres readiness: %w", err)
	}
	if err := r.kafka.Ping(ctx); err != nil {
		return fmt.Errorf("kafka readiness: %w", err)
	}
	return nil
}
