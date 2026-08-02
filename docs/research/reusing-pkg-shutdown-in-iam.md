# Reusing `pkg/shutdown` in IAM

**Date:** 2026-08-02  
**Question:** How can we reuse `pkg/shutdown` in the IAM service? Include package changes if needed.  
**Related:** [`iam-cmd-vs-pkg-shutdown.md`](./iam-cmd-vs-pkg-shutdown.md) (why it was dropped).  
**Primary sources:** repo source, [os/signal.NotifyContext](https://pkg.go.dev/os/signal#NotifyContext), [net/http.Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown), [gRPC GracefulStop example](https://github.com/grpc/grpc-go/blob/master/examples/features/gracefulstop/README.md).

---

## Verdict

Reuse is practical, but **not with today’s `Manager.Wait()` alone**. IAM needs a context-driven “run phases now” entrypoint that returns joined `Close` errors, plus a small `Func` adapter. Keep signal handling and serve/worker select loops in `cmd/*` (`signal.NotifyContext`); let `pkg/shutdown` own only **ordered, timed teardown**. Do **not** put serve loops, gRPC specifics, or Kafka/relay cancellation policy into the shared package.

---

## Gaps that block reuse today

| Gap | Evidence | Impact on IAM |
| --- | --- | --- |
| `runPhases` is unexported | [`pkg/shutdown/shutdown.go`](../../pkg/shutdown/shutdown.go) L65 | Cannot trigger phased close after a serve error without sending a fake signal |
| `Wait()` owns signals via `signal.Notify` | same file L47–54 | Conflicts with IAM’s `signal.NotifyContext` + select on serve errors ([`cmd/iam/main.go`](../../services/iam/cmd/iam/main.go) L20–26, L82–93) |
| `Wait()` returns no error | L47–63; Close failures logged only L83–86 | IAM joins shutdown/serve errors and exits non-zero ([`cmd/iam/main.go`](../../services/iam/cmd/iam/main.go) L111–113) |
| Components lack `Close(ctx) error` | `httpapi.Shutdown`, `grpcapi.GracefulStop()`, `app.Close()`, `postgres.Close()`, `kafka.Publisher.Close()` | Need adapters or method renames |
| Worker stops via cancel, not Close | [`relay.Run`](../../services/iam/internal/relay/relay.go) L77–88 returns on `ctx.Done()` | Phase registration should wrap `cancel`, not invent a Close on Relay |

**Minimal “adapters only, no package change” path:** only viable if IAM regresses to fire-and-forget serve + block in `Wait()` (the prototype shape in `72ec83f^`). That drops serve-error-driven shutdown. **Not recommended.**

---

## Recommended design

### Split responsibilities

```text
cmd (IAM)                         pkg/shutdown
─────────────────────             ────────────────────────────
signal.NotifyContext              GracefulShutdown / Func
start Serve / worker.Run          Register(phase, …)
select: serve err | ctx.Done      Shutdown(ctx) error  ← NEW
drain serve channels              phased Close + timeout + Join
defer or phase-1 resource Close   (optional) Wait() thin wrapper
```

This matches Go’s recommended signal pattern: cancel a context on SIGINT/SIGTERM ([`NotifyContext`](https://pkg.go.dev/os/signal#NotifyContext)), then shut down with a timeout ([`http.Server.Shutdown`](https://pkg.go.dev/net/http#Server.Shutdown)). Idiomatic skill guidance also prefers `NotifyContext` over raw `signal.Notify` channels for process shutdown.

### Proposed `pkg/shutdown` API changes

Keep `GracefulShutdown` and `Register` as-is. Add:

```go
// Func adapts a function to GracefulShutdown.
type Func func(context.Context) error

func (f Func) Close(ctx context.Context) error { return f(ctx) }

// Shutdown runs registered phases under Manager.timeout.
// Parent ctx may already be canceled (signal or serve failure); timeout
// is applied via context.WithTimeout(context.Background(), m.timeout)
// so Close still gets a fresh deadline (same as Wait today).
// Returns errors.Join of Close failures and/or context.DeadlineExceeded
// if a phase hits the timeout before completion.
func (m *Manager) Shutdown(ctx context.Context) error
```

Behavioral upgrades inside phase execution (replace log-only):

1. Collect every non-nil `Close` error with `errors.Join` (and still log, or log only at cmd — prefer return + let cmd log once to avoid double-handling).
2. On phase timeout, return `context.DeadlineExceeded` (joined with prior Close errors) and **stop later phases** (current `runPhases` already skips later phases on timeout — [`shutdown.go`](../../pkg/shutdown/shutdown.go) L100–102).
3. Export the logic used by both `Shutdown` and a thin `Wait`:

```go
// Wait keeps backward compatibility for the prototype style:
// block on SIGINT/SIGTERM, then Shutdown. Prefer NotifyContext +
// Shutdown at new call sites. Return Shutdown's error so simple
// binaries can exit non-zero (breaking change from void Wait).
func (m *Manager) Wait() error {
    // existing signal.Notify loop…
    return m.Shutdown(context.Background())
}
```

Optional later (not required for IAM):

- `WaitContext(ctx)` that selects on `ctx.Done()` **or** signal — convenience only; redundant if cmds already use `NotifyContext` + `Shutdown`.
- Per-phase timeouts — overkill; IAM uses one `ShutdownTimeout` today ([`cmd/iam/main.go`](../../services/iam/cmd/iam/main.go) L95).

### What not to put in `pkg/shutdown`

- Starting HTTP/gRPC listeners or `select` on serve error channels  
- Knowledge of gRPC `GracefulStop` / `Stop`  
- Automatic `signal.NotifyContext` ownership that forbids caller select loops  
- Double-closing resources already `defer`’d in cmd  

---

## IAM adaptation

### Adapters (service-local or thin methods)

| Component | Adapter |
| --- | --- |
| `httpapi.Server` | `shutdown.Func(httpServer.Shutdown)` or add `Close(ctx) error` aliasing `Shutdown` |
| `grpcapi.Server` | Expose `Stop()` (hard stop) alongside existing `GracefulStop()`; implement `Close(ctx)` that runs `GracefulStop` in a goroutine and on `ctx.Done()` calls `Stop()` then waits — aligns with [gRPC gracefulstop guidance](https://github.com/grpc/grpc-go/blob/master/examples/features/gracefulstop/README.md). Today IAM races timeout but never hard-`Stop`s ([`cmd/iam/main.go`](../../services/iam/cmd/iam/main.go) L98–109). |
| Relay cancel | `shutdown.Func(func(context.Context) error { cancelRun(); return nil })` in phase 0 **before or with** HTTP |
| `app.App` / DB / Kafka | Prefer keep `defer Close()` after servers stop (simpler, matches current order). Or phase 1 `Func` wrappers — **pick one** to avoid double Close |

Compile-time check if methods are added:

```go
var _ shutdown.GracefulShutdown = (*httpapi.Server)(nil)
```

### Sketched `cmd/iam` shape

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    if err := run(ctx); err != nil { /* exit 1 */ }
}

func run(ctx context.Context) error {
    // … config, logger, app …
    defer application.Close() // phase-1 resources stay on defer

    // … listeners, httpServer, grpcServer …

    manager := shutdown.NewManager(appConfig.ShutdownTimeout, appLogger)
    manager.Register(0, httpServer)                    // needs Close = Shutdown
    manager.Register(0, shutdown.Func(grpcServer.Close)) // or method on grpcapi.Server

    serverErrors := make(chan error, 2)
    go func() { serverErrors <- httpServer.Serve(httpListener) }()
    go func() { serverErrors <- grpcServer.Serve(grpcListener) }()

    var runErr error
    select {
    case runErr = <-serverErrors:
    case <-ctx.Done():
    }

    shutdownErr := manager.Shutdown(context.Background())
    serveErr := errors.Join(<-serverErrors, <-serverErrors) // drain both; adjust if one already taken
    return errors.Join(runErr, shutdownErr, serveErr)
}
```

Notes:

- On premature HTTP failure, today’s code stops gRPC then HTTP ([`cmd/iam/main.go`](../../services/iam/cmd/iam/main.go) L83–91). With the manager, **always** call `Shutdown` after the select — phase 0 stops both peers regardless of which failed. Drain logic must account for “one error already received.”
- `defer application.Close()` still runs after `run` returns, preserving “servers before DB.”

### Sketched `cmd/outbox-relay` shape

```go
runContext, cancelRun := context.WithCancel(ctx)
defer cancelRun()

manager := shutdown.NewManager(appConfig.ShutdownTimeout, appLogger)
manager.Register(0, shutdown.Func(func(context.Context) error {
    cancelRun()
    return nil
}))
manager.Register(0, server) // HTTP Shutdown via Close

// start server + worker.Run(runContext, …)

select {
case runErr = <-serverErrors:
case runErr = <-relayErrors:
case <-ctx.Done():
}

shutdownErr := manager.Shutdown(context.Background())
// join remaining channels + shutdownErr
// defer database.Close / publisher.Close unchanged
```

Putting `cancelRun` in phase 0 (parallel with HTTP) matches current “cancel then Shutdown” ([`outbox-relay/main.go`](../../services/iam/cmd/outbox-relay/main.go) L133–140). Worker exit is observed by draining `relayErrors`, not by `Manager`.

---

## Migration steps (implementation order)

1. **`pkg/shutdown`:** add `Func`, export `Shutdown(ctx) error` with `errors.Join`; refactor `Wait` to call it; extend tests (error join, timeout error, Func).  
2. **IAM adapters:** `Close(ctx)` on `httpapi.Server` and `grpcapi.Server` (gRPC: GracefulStop + Stop on deadline).  
3. **`cmd/iam` / `cmd/outbox-relay`:** wire `NewManager` + `Register` + `Shutdown` after select; re-add `pkg/shutdown` to `services/iam/go.mod`.  
4. Keep resource `defer Close` unless intentionally moving them to phase 1.

---

## Alternatives considered

| Option | Pros | Cons |
| --- | --- | --- |
| A. Adapters + current `Wait()` only | No package change | Loses serve-error path; fights `NotifyContext` |
| B. **Recommended:** `Shutdown` + `Func` + cmd-owned signals | Small API; fits both binaries; reusable for future services | Requires package change + drain-channel care |
| C. Fat supervisor in `pkg/shutdown` (start servers, select, etc.) | Tiny mains | Overfits IAM; hard to test; wrong layer |
| D. Leave unused `pkg/shutdown`, keep IAM as-is | Zero churn | Shared package stays dead; duplicate teardown logic |

---

## Conclusion

To reuse `pkg/shutdown` in IAM without regressing the foundation rewrite: **evolve the package into a phased closer invoked by the caller** (`Shutdown(ctx) error` + `Func`), and **keep IAM’s `NotifyContext` + serve/worker select in cmd**. Register listeners/cancels in phase 0; leave DB/Kafka/App on `defer` (or phase 1, not both). That restores the shared module’s purpose from [`CONTEXT-MAP.md`](../../CONTEXT-MAP.md) without reintroducing the prototype’s fire-and-forget serve model.
