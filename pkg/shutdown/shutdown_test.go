package shutdown

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestManagerShutdownRunsInPhaseOrder(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		steps []int
	)

	record := func(phase int) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			steps = append(steps, phase)
			return nil
		}
	}

	manager := New()
	manager.Register(1, record(1))
	manager.Register(0, record(0))
	manager.Register(2, record(2))

	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v, want nil", err)
	}

	want := []int{0, 1, 2}
	if len(steps) != len(want) {
		t.Fatalf("len(steps) = %d, want %d", len(steps), len(want))
	}
	for i := range want {
		if steps[i] != want[i] {
			t.Fatalf("steps[%d] = %d, want %d", i, steps[i], want[i])
		}
	}
}

func TestManagerShutdownStopsLaterPhasesOnContextDone(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	release := make(chan struct{})

	manager := New()
	manager.Register(0, func(ctx context.Context) error {
		close(started)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})
	ranSecondPhase := false
	manager.Register(1, func(context.Context) error {
		ranSecondPhase = true
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- manager.Shutdown(ctx)
	}()

	<-started
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Shutdown() = %v, want context.Canceled", err)
		}
	case <-t.Context().Done():
		t.Fatal("Shutdown() did not return after context cancellation")
	}

	close(release)
	if ranSecondPhase {
		t.Fatal("phase after timeout should not run")
	}
}

func TestManagerShutdownSkipsLaterPhasesWhenContextAlreadyDone(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ranSecondPhase := false
	manager := New()
	manager.Register(0, func(ctx context.Context) error {
		return ctx.Err()
	})
	manager.Register(1, func(context.Context) error {
		ranSecondPhase = true
		return nil
	})

	err := manager.Shutdown(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown() = %v, want context.Canceled", err)
	}
	if ranSecondPhase {
		t.Fatal("later phase ran after context was already done")
	}
}

func TestManagerShutdownContinuesAfterCloseError(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	ranSecondPhase := false

	manager := New()
	manager.Register(0, func(context.Context) error {
		return boom
	})
	manager.Register(1, func(context.Context) error {
		ranSecondPhase = true
		return nil
	})

	err := manager.Shutdown(context.Background())
	if !errors.Is(err, boom) {
		t.Fatalf("Shutdown() = %v, want boom", err)
	}

	if !ranSecondPhase {
		t.Fatal("second phase did not run after close error")
	}
}

func TestManagerShutdownJoinsCloseErrors(t *testing.T) {
	t.Parallel()

	first := errors.New("first")
	second := errors.New("second")

	manager := New()
	manager.Register(0, func(context.Context) error { return first })
	manager.Register(1, func(context.Context) error { return second })

	err := manager.Shutdown(context.Background())
	if !errors.Is(err, first) {
		t.Fatalf("Shutdown() missing first: %v", err)
	}
	if !errors.Is(err, second) {
		t.Fatalf("Shutdown() missing second: %v", err)
	}
}

func TestManagerShutdownRecoversPanic(t *testing.T) {
	t.Parallel()

	ranSecondPhase := false

	manager := New()
	manager.Register(0, func(context.Context) error {
		panic("boom")
	})
	manager.Register(1, func(context.Context) error {
		ranSecondPhase = true
		return nil
	})

	err := manager.Shutdown(context.Background())
	if err == nil {
		t.Fatal("Shutdown() = nil, want panic error")
	}
	if !ranSecondPhase {
		t.Fatal("second phase did not run after panic")
	}
}

func TestManagerShutdownZeroValueRegister(t *testing.T) {
	t.Parallel()

	ran := false
	var manager Manager
	manager.Register(0, func(context.Context) error {
		ran = true
		return nil
	})

	if err := manager.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v, want nil", err)
	}
	if !ran {
		t.Fatal("zero-value Register did not run closer")
	}
}

func TestNewShutdownEmpty(t *testing.T) {
	t.Parallel()

	if err := New().Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() = %v, want nil", err)
	}
}
