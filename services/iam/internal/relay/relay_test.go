package relay_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ikaelfess/distributed-workflow/services/iam/internal/outbox"
	"github.com/ikaelfess/distributed-workflow/services/iam/internal/relay"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time {
	return c.now
}

type fakeStore struct {
	events   []outbox.ClaimedEvent
	deleted  []string
	released []string
}

func (s *fakeStore) Claim(
	context.Context,
	time.Time,
	time.Duration,
	int,
) ([]outbox.ClaimedEvent, error) {
	return s.events, nil
}

func (s *fakeStore) Delete(_ context.Context, eventID, _ string) (bool, error) {
	s.deleted = append(s.deleted, eventID)
	return true, nil
}

func (s *fakeStore) Release(_ context.Context, eventID, _ string, _ time.Time) (bool, error) {
	s.released = append(s.released, eventID)
	return true, nil
}

type fakePublisher struct {
	err error
}

func (p fakePublisher) Publish(context.Context, string, string, []byte) error {
	return p.err
}

func TestRelay_RunOnce(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 31, 2, 50, 0, 0, time.UTC)
	event := outbox.ClaimedEvent{
		ID:         "event-1",
		Topic:      "topic",
		EventKey:   "event-1",
		Payload:    []byte(`{"event_id":"event-1"}`),
		ClaimToken: "claim-1",
		Attempts:   1,
		CreatedAt:  now,
	}

	t.Run("deletes after successful publish", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{events: []outbox.ClaimedEvent{event}}
		worker, err := relay.New(relay.Config{
			BatchSize:    10,
			Lease:        30 * time.Second,
			PollInterval: time.Second,
			RetryDelay:   5 * time.Second,
		}, store, fakePublisher{}, fixedClock{now: now})
		require.NoError(t, err)

		count, err := worker.RunOnce(t.Context())
		require.NoError(t, err)
		assert.Equal(t, 1, count)
		assert.Equal(t, []string{"event-1"}, store.deleted)
		assert.Empty(t, store.released)
	})

	t.Run("releases after publish failure", func(t *testing.T) {
		t.Parallel()

		store := &fakeStore{events: []outbox.ClaimedEvent{event}}
		worker, err := relay.New(relay.Config{
			BatchSize:    10,
			Lease:        30 * time.Second,
			PollInterval: time.Second,
			RetryDelay:   5 * time.Second,
		}, store, fakePublisher{err: errors.New("broker unavailable")}, fixedClock{now: now})
		require.NoError(t, err)

		count, err := worker.RunOnce(t.Context())
		require.Error(t, err)
		assert.Equal(t, 1, count)
		assert.Empty(t, store.deleted)
		assert.Equal(t, []string{"event-1"}, store.released)
	})
}
