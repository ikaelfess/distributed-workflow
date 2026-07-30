package relay

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/outbox"
)

type Store interface {
	Claim(context.Context, time.Time, time.Duration, int) ([]outbox.ClaimedEvent, error)
	Delete(context.Context, string, string) (bool, error)
	Release(context.Context, string, string, time.Time) (bool, error)
}

type Publisher interface {
	Publish(context.Context, string, string, []byte) error
}

type Clock interface {
	Now() time.Time
}

type Config struct {
	BatchSize    int
	Lease        time.Duration
	PollInterval time.Duration
	RetryDelay   time.Duration
}

type Relay struct {
	config    Config
	store     Store
	publisher Publisher
	clock     Clock
}

type SystemClock struct{}

func (SystemClock) Now() time.Time {
	return time.Now()
}

func New(config Config, store Store, publisher Publisher, clock Clock) (*Relay, error) {
	if config.BatchSize < 1 {
		return nil, errors.New("batch size must be at least one")
	}
	if config.Lease <= 0 {
		return nil, errors.New("lease must be greater than zero")
	}
	if config.PollInterval <= 0 {
		return nil, errors.New("poll interval must be greater than zero")
	}
	if config.RetryDelay <= 0 {
		return nil, errors.New("retry delay must be greater than zero")
	}
	if store == nil {
		return nil, errors.New("store is required")
	}
	if publisher == nil {
		return nil, errors.New("publisher is required")
	}
	if clock == nil {
		return nil, errors.New("clock is required")
	}

	return &Relay{
		config:    config,
		store:     store,
		publisher: publisher,
		clock:     clock,
	}, nil
}

func (r *Relay) Run(ctx context.Context, report func(error)) error {
	if report == nil {
		return errors.New("error reporter is required")
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			if _, err := r.RunOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				report(err)
			}
			timer.Reset(r.config.PollInterval)
		}
	}
}

func (r *Relay) RunOnce(ctx context.Context) (int, error) {
	now := r.clock.Now()
	events, err := r.store.Claim(
		ctx,
		now,
		r.config.Lease,
		r.config.BatchSize,
	)
	if err != nil {
		return 0, fmt.Errorf("claim events: %w", err)
	}

	var relayErrors []error
	for _, event := range events {
		if err := r.publisher.Publish(ctx, event.Topic, event.EventKey, event.Payload); err != nil {
			if ctx.Err() != nil {
				return 0, ctx.Err()
			}

			released, releaseErr := r.store.Release(
				ctx,
				event.ID,
				event.ClaimToken,
				r.clock.Now().Add(r.config.RetryDelay),
			)
			if releaseErr != nil {
				relayErrors = append(
					relayErrors,
					fmt.Errorf("release event after publish failure: %w", releaseErr),
				)
			} else if !released {
				relayErrors = append(relayErrors, errors.New("release event claim was lost"))
			}
			relayErrors = append(relayErrors, err)
			continue
		}

		deleted, err := r.store.Delete(ctx, event.ID, event.ClaimToken)
		if err != nil {
			relayErrors = append(relayErrors, err)
			continue
		}
		if !deleted {
			relayErrors = append(relayErrors, errors.New("delete event claim was lost"))
		}
	}

	if err := errors.Join(relayErrors...); err != nil {
		return len(events), fmt.Errorf("relay events: %w", err)
	}
	return len(events), nil
}
