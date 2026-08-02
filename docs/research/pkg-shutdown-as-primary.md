# Using `pkg/shutdown` as the primary graceful-shutdown orchestrator

**Date:** 2026-08-02  
**Question:** Using `pkg/shutdown` as the *primary* graceful-shutdown source for HTTP, gRPC, databases, Kafka clients, and anything else that must be gracefully stopped — what changes are needed to use it that way?  
**Scope:** whole monorepo surface under `pkg/*` and `services/iam/**` (not only wiring IAM cmds).  
**Related prior notes (verified against sources below; do not treat as authority):**

- [`iam-cmd-vs-pkg-shutdown.md`](./iam-cmd-vs-pkg-shutdown.md) — why IAM dropped `pkg/shutdown`
- [`reusing-pkg-shutdown-in-iam.md`](./reusing-pkg-shutdown-in-iam.md) — reuse via `Shutdown(ctx)` + `Func`, with deps often left on `defer`

**This note goes further:** `Manager` is the **single orchestrator of ordered teardown** (phase 0 listeners/workers, phase 1 dependencies). `defer Close` is not a parallel teardown path for the same resources.

**Primary sources only:** this repository; [os/signal.NotifyContext](https://pkg.go.dev/os/signal#NotifyContext); [net/http.Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown); [gRPC `Server.GracefulStop` / `Stop`](https://pkg.go.dev/google.golang.org/grpc#Server.GracefulStop) and the [official gracefulstop example README](https://raw.githubusercontent.com/grpc/grpc-go/master/examples/features/gracefulstop/README.md); [pgxpool.Pool.Close](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Pool.Close) (module source `pgxpool/pool.go`); [franz-go `kgo.Client.Close`](https://pkg.go.dev/github.com/twmb/franz-go/pkg/kgo#Client.Close) (module source `pkg/kgo/client.go`).

---

## Verdict

Make `pkg/shutdown.Manager` the only place that runs process teardown order. Evolve it from “signal-owned `Wait()` that logs `Close` errors” into a caller-triggered phased closer: `Shutdown(ctx) error`, `Func`, and `errors.Join` of failures. Keep **run-loop** ownership in `cmd/*` (`signal.NotifyContext` + `select` on serve/worker errors). Move HTTP/gRPC/worker-cancel into **phase 0** and DB/Kafka/`app.App` into **phase 1** via `Register` — **not** via `defer Close` for those same objects. Shared `pkg/httpserver` and `pkg/database` already satisfy `GracefulShutdown`; IAM’s service-local types mostly do not and need `Close(context.Context) error` (or `Func` wrappers). Signal handling, serve-error selection, channel draining, and process exit stay outside the Manager.

---

## 1. Current `pkg/shutdown` contract (full read)

**Source:** [`pkg/shutdown/shutdown.go`](../../pkg/shutdown/shutdown.go), [`pkg/shutdown/shutdown_test.go`](../../pkg/shutdown/shutdown_test.go)

| Piece | Behavior | Lines |
| --- | --- | --- |
| `GracefulShutdown` | `Close(context.Context) error` | 16–20 |
| `Manager` | Phased close after signal; lower phases first; within a phase, parallel | 22–24, 65–107 |
| Documented layout | Phase 0 HTTP/RPC, phase 1 databases | 24 |
| `NewManager` | One timeout bounds the **entire** sequence; one context passed to every `Close` | 31–38 |
| `Register` | Append to phase group | 41–45 |
| `Wait` | Owns `signal.Notify(SIGINT/SIGTERM)`, blocks, then `runPhases` | 47–63 |
| Errors | `Close` failures **logged only**; no return value from `Wait` | 83–86, 47–63 |
| Timeout | On `ctx.Done` mid-phase, later phases **do not run** | 100–102; test 60–104 |

Tests cover phase order, timeout abort of later phases, and continuation after a service `Close` error ([`shutdown_test.go:21–132`](../../pkg/shutdown/shutdown_test.go)).

**Gap vs “primary orchestrator”:** there is no exported “run phases now” API, so serve-error paths cannot trigger ordered teardown without faking a signal. `Wait` cannot participate in IAM’s serve/worker `select` ([`cmd/iam/main.go:82–93`](../../services/iam/cmd/iam/main.go), [`cmd/outbox-relay/main.go:125–131`](../../services/iam/cmd/outbox-relay/main.go)). Errors never surface to `main` for non-zero exit.

---

## 2. Inventory: Close / Shutdown / Stop / cancel-run surfaces

### 2.1 `pkg/*`

| Package | Lifecycle API | Satisfies `GracefulShutdown`? | Notes |
| --- | --- | --- | --- |
| `pkg/shutdown` | `Manager` / `GracefulShutdown` | N/A (coordinator) | Zero in-tree callers outside itself (confirmed: only listed in [`CONTEXT-MAP.md:26`](../../CONTEXT-MAP.md), [`go.work:7`](../../go.work), [`Makefile:40–44`](../../Makefile)) |
| `pkg/httpserver` | `Close(ctx) error` → `http.Server.Shutdown` | **Yes** | [`server.go:78–80`](../../pkg/httpserver/server.go). `Start()` fatals on non-`ErrServerClosed` ([71–76](../../pkg/httpserver/server.go)) — fire-and-forget serve model |
| `pkg/database` | `Close(_ context.Context) error` → `bun.DB.Close` | **Yes** | [`postgres.go:55–57`](../../pkg/database/postgres.go) |
| `pkg/logger` | none | N/A | Construct-only ([`logger.go`](../../pkg/logger/logger.go)) |

IAM does **not** import `pkg/httpserver` or `pkg/database` after commit `72ec83f` (see prior note + current `services/iam/go.mod`). They remain relevant as the shared adapters that already match the Manager interface for any future consumer.

### 2.2 `services/iam/**` (process-lifecycle only)

| Component | Current stop API | Context-aware? | Used by |
| --- | --- | --- | --- |
| `httpapi.Server` | `Shutdown(ctx) error` | Yes | both cmds | [`httpapi/server.go:48–53`](../../services/iam/internal/httpapi/server.go) |
| `grpcapi.Server` | `GracefulStop()` only | **No** | `cmd/iam` | [`grpcapi/server.go:44–46`](../../services/iam/internal/grpcapi/server.go) |
| `postgres.Database` | `Close()` | No | `app`, `cmd/outbox-relay` | [`postgres.go:82–84`](../../services/iam/internal/postgres/postgres.go) → `pgxpool.Pool.Close` |
| `kafka.Publisher` | `Close()` | No | `cmd/outbox-relay` | [`publisher.go:73–75`](../../services/iam/internal/kafka/publisher.go) → `kgo.Client.Close` |
| `app.App` | `Close()` via `sync.Once` → DB | No | `cmd/iam` | [`app.go:136–138`](../../services/iam/internal/app/app.go) |
| `relay.Relay` | `Run(ctx, …)` returns on `ctx.Done()` | Cancel, not Close | `cmd/outbox-relay` | [`relay.go:77–88`](../../services/iam/internal/relay/relay.go) |

**Cmd-owned orchestration today (not Manager):**

- `cmd/iam`: `NotifyContext` → `defer application.Close()` → serve HTTP+gRPC → `select` → parallel HTTP `Shutdown` + gRPC `GracefulStop` ([`main.go:20–117`](../../services/iam/cmd/iam/main.go)). On serve-error path, stops gRPC then HTTP with timeout without joining HTTP shutdown error ([83–91](../../services/iam/cmd/iam/main.go)).
- `cmd/outbox-relay`: `NotifyContext` → `defer database.Close()` / `defer publisher.Close()` → serve HTTP + `worker.Run` → `select` → `cancelRun()` then HTTP `Shutdown` ([`main.go:21–152`](../../services/iam/cmd/outbox-relay/main.go)).

**Excluded from Manager inventory (not process teardown):** `rows.Close`, `response.Body.Close`, `r.Body.Close`, `http.Server` in tests, admin `pgx.Conn.Close` in integration tests — request/test scoped, not `GracefulShutdown` candidates.

---

## 3. Official stop semantics (constraints on adapters)

| System | Contract | Implication for `Close(ctx)` |
| --- | --- | --- |
| HTTP | `Shutdown` closes listeners, drains idle conns, waits for active conns or returns `ctx` error; Serve returns `ErrServerClosed` ([pkg.go.dev](https://pkg.go.dev/net/http#Server.Shutdown)) | Map 1:1 to `Close` / keep `Shutdown` as alias |
| gRPC | `GracefulStop` blocks until RPCs finish; `Stop` closes immediately ([grpc-go `server.go` L1910–1924](https://pkg.go.dev/google.golang.org/grpc#Server.GracefulStop)). Official example: arrange a timeout that calls `Stop` so `GracefulStop` cannot block forever ([README](https://raw.githubusercontent.com/grpc/grpc-go/master/examples/features/gracefulstop/README.md)) | IAM today never hard-`Stop`s on timeout ([`cmd/iam/main.go:98–109`](../../services/iam/cmd/iam/main.go)) — fix in `grpcapi.Close` |
| Signals | `NotifyContext` cancels on signal / `stop` / parent done ([pkg.go.dev](https://pkg.go.dev/os/signal#NotifyContext)) | Cmds keep this; Manager must not also `signal.Notify` for the same process path |
| pgx pool | `Pool.Close` blocks until connections returned; **no context**; `sync.Once` ([`pgxpool/pool.go:454–461`](https://github.com/jackc/pgx/blob/v5.10.0/pgxpool/pool.go)) | Adapter may ignore `ctx` or race `Close` in a goroutine and return `ctx.Err()` on deadline (pool may still be closing) |
| franz-go | `Client.Close()` leaves group and closes connections; **no context** on public API ([`kgo/client.go:1268–1288`](https://github.com/twmb/franz-go/blob/v1.21.5/pkg/kgo/client.go)) | Same adapter shape as pgx; publisher is produce-only today so leave-group path is unused |

---

## 4. Required `pkg/shutdown` API changes

Keep `GracefulShutdown` and `Register` unchanged in shape. Add the following.

### 4.1 `Func`

```go
type Func func(context.Context) error

func (f Func) Close(ctx context.Context) error { return f(ctx) }
```

Needed for worker cancel (`cancelRun`) and one-off adapters without renaming every method.

### 4.2 `Shutdown(ctx context.Context) error` (primary entrypoint)

- Apply timeout with `context.WithTimeout(context.Background(), m.timeout)` (same freshness as today’s `Wait` at [`shutdown.go:57–58`](../../pkg/shutdown/shutdown.go)), **ignoring** parent cancellation for the deadline itself so a signal-cancelled `ctx` does not zero the shutdown budget.
- Optionally accept parent only for logging / cause; do not inherit its already-fired cancel as the Close deadline.
- Run current `runPhases` logic; collect every non-nil `Close` error with `errors.Join`.
- On phase timeout: join `context.DeadlineExceeded` (or `ctx.Err()`) with prior Close errors; **skip later phases** (preserve [`shutdown.go:100–102`](../../pkg/shutdown/shutdown.go)).
- Prefer returning errors to the caller; logging in Manager is optional (avoid double-log if cmd logs the join once).

### 4.3 `Wait` / `WaitContext` and signal ownership

| Decision | Rationale |
| --- | --- |
| **Cmds that select on serve/worker errors own signals** via `signal.NotifyContext` | Matches IAM today and [NotifyContext](https://pkg.go.dev/os/signal#NotifyContext); avoids fighting `Wait`’s `signal.Notify` ([`shutdown.go:50–52`](../../pkg/shutdown/shutdown.go)) |
| **`Wait()` remains** for simple “start then block” binaries (historical `pkg/httpserver.Start` style) | Thin wrapper: notify → `return m.Shutdown(context.Background())` |
| **Breaking: `Wait() error`** | Surface Close failures for non-zero exit; update tests |
| **`WaitContext(ctx)` optional** | `select` on `ctx.Done()` **or** signal, then `Shutdown`. Redundant if every real service uses NotifyContext + post-select `Shutdown`; add only if a second consumer wants it |

**Primary-orchestrator rule:** production IAM (and future multi-listener services) call **`Shutdown` after the run `select`**, never block forever in `Wait`. `Wait` is a convenience, not the IAM control plane.

### 4.4 Tests to add

- `Func` registration
- `Shutdown` returns `errors.Join` of Close failures
- Timeout returns deadline error and skips later phases (extend existing abort test to assert returned error)
- `Wait` returns Shutdown’s error (if signature changes)

---

## 5. Shared packages (`pkg/httpserver`, `pkg/database`)

| Package | Change required for Manager-as-primary? |
| --- | --- |
| `pkg/httpserver` | **None for the interface** — already `Close(ctx)`. Optional later: replace `Start`+fatal with error-returning `Serve` so callers can `select` like IAM; that is serve-loop design, not Manager API |
| `pkg/database` | **None** — already `Close(ctx)` |
| `pkg/logger` | **None** |

They are already the reference `GracefulShutdown` implementations. Re-homing IAM onto them is **out of scope** for shutdown orchestration (IAM intentionally uses `net/http` + pgx locally after `72ec83f`).

---

## 6. IAM / component changes

### 6.1 Phase convention (monorepo standard)

| Phase | What | Examples |
| --- | --- | --- |
| **0** | Stop accepting / producing work | HTTP `Shutdown`, gRPC `Close` (graceful+hard), `cancelRun` for `relay.Run` |
| **1** | Release dependencies | `postgres.Database`, `kafka.Publisher`, `app.App` (DB) |
| **2+** | Reserved | e.g. flushers, remote telemetry — none in tree today |

Package comment already states this layout ([`shutdown.go:24`](../../pkg/shutdown/shutdown.go)). **Promote it from comment to the process standard:** every long-running binary registers phase 0 then phase 1; Manager runs both.

Within a phase, closes are **parallel** ([`shutdown.go:76–89`](../../pkg/shutdown/shutdown.go)). That matches `cmd/iam`’s parallel HTTP+gRPC stop on the signal path ([`main.go:98–104`](../../services/iam/cmd/iam/main.go)) and is acceptable for DB∥Kafka after workers stop.

**Timeout budget:** one `ShutdownTimeout` covers **all** phases ([`shutdown.go:31–32`](../../pkg/shutdown/shutdown.go), IAM default `20s` at [`config.go:29`](../../services/iam/internal/config/config.go)). Size it for listeners **plus** deps; phase 1 may see a short remaining deadline if phase 0 is slow.

### 6.2 “Defer vs Register” rule (no double-close)

1. **If Manager owns it, do not `defer Close` it.** Registration transfers teardown ownership to `Shutdown`.
2. **Construction failures before Register:** close on the error path (existing `app.New` pattern at [`app.go:43–112`](../../services/iam/internal/app/app.go)) — Manager never saw the resource.
3. **Listen / wiring failures after Register, before serve loop:** call `manager.Shutdown(...)` (or a `sync.Once` wrapper) before returning so phase 1 still runs; do not also defer the same objects.
4. **Idempotency helps but is not a license to double-register:** `app.Close` and `pgxpool.Close` use `sync.Once` ([`app.go:137`](../../services/iam/internal/app/app.go), pgxpool source above). Prefer single ownership over relying on Once.
5. **`Func(cancelRun)` in phase 0:** `cancelRun` may also appear in `defer cancelRun()` for leak-safety if `Shutdown` never runs — cancel is idempotent; that is OK. Do **not** also manually `cancelRun()` immediately before `Shutdown` if phase 0 already does it (today’s [`outbox-relay/main.go:133`](../../services/iam/cmd/outbox-relay/main.go) becomes the registered Func).

### 6.3 Per-component adaptations

| Component | Change |
| --- | --- |
| `httpapi.Server` | Add `Close(ctx) error` that delegates to `Shutdown` (or register `shutdown.Func(server.Shutdown)`). Keep `Shutdown` name for clarity with net/http. Compile-time: `var _ shutdown.GracefulShutdown = (*httpapi.Server)(nil)` |
| `grpcapi.Server` | Add `Stop()` → `s.server.Stop()`. Add `Close(ctx) error`: start `GracefulStop` in a goroutine; on `ctx.Done()` call `Stop()`; wait for graceful goroutine; return `ctx.Err()` if deadline fired. Aligns with [gRPC gracefulstop guidance](https://raw.githubusercontent.com/grpc/grpc-go/master/examples/features/gracefulstop/README.md) |
| `postgres.Database` | Add `Close(ctx context.Context) error` wrapping `pool.Close()` (ignore ctx or best-effort race). Keep no-arg `Close()` as thin wrapper **or** migrate call sites — pick one exported shape to avoid confusion |
| `kafka.Publisher` | Same: `Close(ctx) error` around `client.Close()` |
| `app.App` | `Close(ctx context.Context) error` via existing `closeOnce` → DB; return nil (or DB error if postgres adapter returns one) |
| `relay.Relay` | **No** `Close` method. Cmd registers `shutdown.Func(func(context.Context) error { cancelRun(); return nil })` in phase 0 |

---

## 7. Cmd control flow when Manager is primary

Both cmds keep the same outer shape; teardown moves into Manager.

```text
main: NotifyContext → run(ctx) → os.Exit on error
run:
  load config / logger
  construct deps
  manager := shutdown.NewManager(ShutdownTimeout, logger)
  Register(0, listeners / cancel Func)
  Register(1, app | db | kafka)          // NOT defer Close for these
  start Serve / worker.Run into channels
  select: serve err | worker err | ctx.Done
  shutdownErr := manager.Shutdown(context.Background())
  drain remaining error channels
  return errors.Join(runErr, shutdownErr, drained…)
```

### 7.1 `cmd/iam`

- Remove `defer application.Close()` ([`main.go:53`](../../services/iam/cmd/iam/main.go)).
- `Register(0, httpServer)`, `Register(0, grpcServer)` (after `Close` exists).
- `Register(1, application)`.
- Replace both branches’ manual stop ([83–109](../../services/iam/cmd/iam/main.go)) with a **single** post-select `manager.Shutdown` (serve-error and signal paths share ordered teardown).
- Drain both `serverErrors` carefully when one value was already received in `select`.
- Re-add module require for `pkg/shutdown` in `services/iam/go.mod`.

### 7.2 `cmd/outbox-relay`

- Remove `defer database.Close()` / `defer publisher.Close()` ([66](../../services/iam/cmd/outbox-relay/main.go), [75](../../services/iam/cmd/outbox-relay/main.go)).
- `Register(0, Func(cancelRun))`, `Register(0, httpServer)`.
- `Register(1, database)`, `Register(1, publisher)` (via `Close(ctx)` adapters).
- Remove the standalone `cancelRun()` + `server.Shutdown` block ([133–140](../../services/iam/cmd/outbox-relay/main.go)); `Shutdown` performs both in phase 0, then deps in phase 1.
- Keep draining `serverErrors` / `relayErrors` in cmd after `Shutdown`.

### 7.3 What stays **outside** Manager

| Concern | Owner | Why |
| --- | --- | --- |
| `signal.NotifyContext` / `stop()` | `cmd` | Need cancel + select with serve errors; [NotifyContext](https://pkg.go.dev/os/signal#NotifyContext) |
| `select` on serve / relay error channels | `cmd` | Manager is teardown-only; does not supervise run loops |
| Draining Serve / `Run` return values | `cmd` | Channels are cmd-local |
| `os.Exit` / printing to stderr | `main` | Process exit policy |
| Listen / `New` / DI wiring | `cmd` / `app` | Startup, not shutdown |
| Construction-time `database.Close` on failed `app.New` | `app` | Resource never registered |
| Request-scoped closes (`Body.Close`, `rows.Close`) | handlers/tests | Not process lifecycle |

---

## 8. Diff from prior research notes

| Topic | [`reusing-pkg-shutdown-in-iam.md`](./reusing-pkg-shutdown-in-iam.md) | This note (primary orchestrator) |
| --- | --- | --- |
| Role of Manager | Phased closer **after** select; helper | **Sole** ordered teardown for listeners **and** deps |
| Phase 1 deps | Prefer `defer Close` | **Register phase 1**; no defer for those objects |
| Serve-error path | Call `Shutdown` | Same, and drop duplicated manual stop branches |
| Shared pkgs | Mentioned as historical adapters | Explicitly: already `GracefulShutdown`; no API change required |
| Signal ownership | Cmd | Unchanged — confirmed |

[`iam-cmd-vs-pkg-shutdown.md`](./iam-cmd-vs-pkg-shutdown.md) remains correct that `Wait()` alone is a poor fit; that is why `Shutdown(ctx) error` is mandatory before Manager can be primary.

---

## 9. Implementation order

1. **`pkg/shutdown`:** `Func`, `Shutdown` + `errors.Join`, refactor `Wait` → `Shutdown`, tests.
2. **IAM adapters:** `httpapi.Close`, `grpcapi.Close` (+ `Stop`), `postgres`/`kafka`/`app` context-aware Close.
3. **`cmd/iam` + `cmd/outbox-relay`:** Register phase 0+1, remove resource `defer Close`, post-select `Shutdown`, depend on `pkg/shutdown`.
4. Leave `pkg/httpserver` / `pkg/database` as-is unless a future service consumes them with the same Manager pattern.

---

## Sources checklist

- [`pkg/shutdown/shutdown.go`](../../pkg/shutdown/shutdown.go), [`shutdown_test.go`](../../pkg/shutdown/shutdown_test.go)
- [`pkg/httpserver/server.go`](../../pkg/httpserver/server.go), [`pkg/database/postgres.go`](../../pkg/database/postgres.go), [`pkg/logger/logger.go`](../../pkg/logger/logger.go)
- [`services/iam/cmd/iam/main.go`](../../services/iam/cmd/iam/main.go), [`cmd/outbox-relay/main.go`](../../services/iam/cmd/outbox-relay/main.go)
- [`httpapi/server.go`](../../services/iam/internal/httpapi/server.go), [`grpcapi/server.go`](../../services/iam/internal/grpcapi/server.go), [`postgres/postgres.go`](../../services/iam/internal/postgres/postgres.go), [`kafka/publisher.go`](../../services/iam/internal/kafka/publisher.go), [`app/app.go`](../../services/iam/internal/app/app.go), [`relay/relay.go`](../../services/iam/internal/relay/relay.go), [`config/config.go`](../../services/iam/internal/config/config.go)
- [`CONTEXT-MAP.md`](../../CONTEXT-MAP.md), [`go.work`](../../go.work), [`Makefile`](../../Makefile)
- Official: [NotifyContext](https://pkg.go.dev/os/signal#NotifyContext), [http.Server.Shutdown](https://pkg.go.dev/net/http#Server.Shutdown), [grpc Server.GracefulStop/Stop](https://pkg.go.dev/google.golang.org/grpc#Server.GracefulStop), [gracefulstop README](https://raw.githubusercontent.com/grpc/grpc-go/master/examples/features/gracefulstop/README.md), [pgxpool.Pool.Close](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool#Pool.Close), [kgo.Client.Close](https://pkg.go.dev/github.com/twmb/franz-go/pkg/kgo#Client.Close)
