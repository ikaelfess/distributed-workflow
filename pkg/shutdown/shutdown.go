// Package shutdown coordinates ordered resource Close during process shutdown.
//
// The process owns OS signals and the shutdown deadline. Pass a context
// bounded by that deadline to [Manager.Shutdown].
package shutdown

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
)

// Manager runs registered closers in ordered phases. Lower phase numbers
// complete first; within a phase, closers run in parallel.
//
// Register is not safe for concurrent use. Register from one goroutine,
// then call Shutdown.
type Manager struct {
	phases map[int][]func(context.Context) error
}

// New returns a Manager with an initialized phase map.
func New() *Manager {
	return &Manager{phases: make(map[int][]func(context.Context) error)}
}

// Register adds a closer to a phase. Phases run in ascending order (0, 1, 2…).
// Multiple Register calls with the same phase append to that phase's group.
func (m *Manager) Register(phase int, closer func(context.Context) error) {
	if m.phases == nil {
		m.phases = make(map[int][]func(context.Context) error)
	}
	m.phases[phase] = append(m.phases[phase], closer)
}

// Shutdown runs registered closers in phase order until every closer returns
// or ctx is done. Close errors and panics do not skip remaining work; a done
// context skips later phases. The result is errors.Join of closer failures,
// recovered panics, and ctx.Err() if the deadline fired.
func (m *Manager) Shutdown(ctx context.Context) error {
	var (
		mu   sync.Mutex
		errs []error
	)
	add := func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}
	joined := func(extra ...error) error {
		mu.Lock()
		defer mu.Unlock()
		all := make([]error, 0, len(errs)+len(extra))
		all = append(all, errs...)
		all = append(all, extra...)
		return errors.Join(all...)
	}

	for _, phase := range slices.Sorted(maps.Keys(m.phases)) {
		var wg sync.WaitGroup
		for _, closer := range m.phases[phase] {
			wg.Go(func() {
				defer func() {
					if r := recover(); r != nil {
						add(fmt.Errorf("phase %d: panic: %v", phase, r))
					}
				}()
				if err := closer(ctx); err != nil {
					add(fmt.Errorf("phase %d: %w", phase, err))
				}
			})
		}

		phaseDone := make(chan struct{})
		go func() {
			defer close(phaseDone)
			wg.Wait()
		}()

		select {
		case <-phaseDone:
			if err := ctx.Err(); err != nil {
				return joined(err)
			}
		case <-ctx.Done():
			// ponytail: in-flight closers are not waited; they must respect ctx
			// or they outlive Shutdown until process exit. Upgrade: wait on wg
			// after Done if a stuck closer must be observed in the Join.
			return joined(ctx.Err())
		}
	}

	return joined()
}
