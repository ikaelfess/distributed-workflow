# Why IAM `cmd/*` entrypoints do not use `pkg/shutdown`

**Date:** 2026-08-02  
**Question:** Why do IAM service `cmd/*` entrypoints not use `pkg/shutdown`?  
**Primary sources only:** repository source, git history, official Go package docs. GitHub Issues/PRs were not readable (`gh` auth tokens invalid at research time); issue `#14` is cited only via the closing commit message.

## Placement note

This repo has `docs/adr/`, `docs/agents/`, and service-local ADRs under `services/*/docs/adr/`, but no existing research-note convention or prior files under `research/` / `.agents/`. An empty `docs/research/` directory exists; this note is written there as the sensible research landing zone.

---

## Verdict

IAM previously used `pkg/shutdown` in its prototype binary. Commit [`72ec83f`](https://github.com/ikaelfess/distributed-workflow/commit/72ec83f3c481f5b1c2631ad96eb9182c9bf10c5d) (“Replace IAM prototype with runtime foundation”, `Closes #14`) **intentionally removed** that dependency (along with `pkg/httpserver` and `pkg/database`) and replaced it with a local `signal.NotifyContext` + explicit server-stop / `defer Close` lifecycle. `outbox-relay` was introduced later with the same local pattern and never imported `pkg/shutdown`. No other service `cmd` in this repo currently consumes `pkg/shutdown`; the package is shared infrastructure with zero in-tree callers.

This is **not** an accidental omission or an unfinished migration *into* `pkg/shutdown`. It is a **deliberate move away** from the shared shutdown manager during the IAM runtime rewrite. The replacement pattern already covers signal-driven graceful stop, and the current IAM runtimes need control that `pkg/shutdown.Manager.Wait()` does not expose (serve-error vs signal select, error return from shutdown, context-cancelled workers, dual HTTP+gRPC stop APIs).

---

## 1. `pkg/shutdown` API and lifecycle contract

**Source:** [`pkg/shutdown/shutdown.go`](../../pkg/shutdown/shutdown.go)

| Concern | Contract | Lines |
| --- | --- | --- |
| Component interface | `GracefulShutdown` requires `Close(context.Context) error` | 16–20 |
| Coordinator | `Manager` runs registered services in ordered phases after SIGINT/SIGTERM; lower phase numbers first; within a phase, closes run in parallel | 22–24 |
| Construction | `NewManager(timeout, logger)` — timeout bounds the **entire** shutdown sequence; one context is passed to every `Close` | 31–38 |
| Registration | `Register(phase int, gs GracefulShutdown)` appends to a phase group | 41–45 |
| Wait / signals | `Wait()` owns signal notification (`SIGINT`/`SIGTERM`), blocks until a signal, then runs phases | 47–63 |
| Phase execution | `runPhases` sorts phase keys ascending; on timeout during a phase, later phases do not run | 65–107 |
| Errors | `Close` failures are **logged only**; they do not abort later phases or return to the caller of `Wait` | 83–86, 47–63 |

Documented intended layout (package comment): phase 0 for HTTP/RPC, phase 1 for databases ([`pkg/shutdown/shutdown.go:24`](../../pkg/shutdown/shutdown.go)).

Tests confirm phase ordering, timeout abort of later phases, and continuation after a service `Close` error ([`pkg/shutdown/shutdown_test.go:21–132`](../../pkg/shutdown/shutdown_test.go)).

**Lifecycle shape:** start components elsewhere → `Register` them → block in `Wait()` until signal → phased `Close` with a shared timeout context → process ends. `Wait()` returns `void` and does not participate in a “run until serve error **or** signal” select loop.

---

## 2. IAM binaries and how they shut down today

The only service binaries under `services/*/cmd/` in this repo are IAM’s two entrypoints:

- [`services/iam/cmd/iam/main.go`](../../services/iam/cmd/iam/main.go)
- [`services/iam/cmd/outbox-relay/main.go`](../../services/iam/cmd/outbox-relay/main.go)

Neither imports `pkg/shutdown` (confirmed by repo-wide search; IAM `go.mod` has no `pkg/shutdown` require).

### 2.1 `cmd/iam`

**Signal / context** ([`main.go:20–31`](../../services/iam/cmd/iam/main.go)):

```go
ctx, stop := signal.NotifyContext(
    context.Background(),
    syscall.SIGINT,
    syscall.SIGTERM,
)
defer stop()
```

Official Go contract: `signal.NotifyContext` returns a child context cancelled when one of the listed signals arrives, when `stop` is called, or when the parent is done ([pkg.go.dev/os/signal#NotifyContext](https://pkg.go.dev/os/signal#NotifyContext)).

**Run loop** ([`main.go:49–118`](../../services/iam/cmd/iam/main.go)):

1. Build `app.App`; `defer application.Close()` (DB close via [`app.go:136–138`](../../services/iam/internal/app/app.go)).
2. Listen HTTP + gRPC; start both `Serve` loops into a buffered `serverErrors` channel.
3. `select` on first serve error **or** `ctx.Done()`.
4. On signal path: timeout context from `appConfig.ShutdownTimeout`; run `grpcServer.GracefulStop()` in a goroutine and `httpServer.Shutdown(shutdownContext)` in parallel; join serve/shutdown errors and return them.

HTTP stop API is `Shutdown(ctx)` ([`httpapi/server.go:48–53`](../../services/iam/internal/httpapi/server.go)), not `Close(ctx)`. gRPC stop API is `GracefulStop()` with **no** context ([`grpcapi/server.go:44–46`](../../services/iam/internal/grpcapi/server.go)).

### 2.2 `cmd/outbox-relay`

Same `signal.NotifyContext` entry ([`main.go:21–32`](../../services/iam/cmd/outbox-relay/main.go)).

**Run loop** ([`main.go:107–152`](../../services/iam/cmd/outbox-relay/main.go)):

1. `defer database.Close()` / `defer publisher.Close()` (no-context closes).
2. Start HTTP serve + `worker.Run(runContext, …)` where `runContext` is a cancellable child of the signal context.
3. `select` on server error, relay error, or `ctx.Done()`.
4. `cancelRun()` so the relay loop exits on `ctx.Done()` ([`relay.go:77–88`](../../services/iam/internal/relay/relay.go)), then `server.Shutdown(shutdownContext)`, then join errors.

Introduced in [`bb4fec6`](https://github.com/ikaelfess/distributed-workflow/commit/bb4fec651c6b0b3cda15bc1aa94117650638a92f) already using this pattern — never used `pkg/shutdown`.

---

## 3. Other services / cmds that use `pkg/shutdown`

**Current tree:** no Go file outside `pkg/shutdown` itself imports or calls `shutdown.NewManager` / `GracefulShutdown`. Repo-wide matches for the module path are only:

- [`CONTEXT-MAP.md:26`](../../CONTEXT-MAP.md) — lists `pkg/shutdown/` under shared technical modules
- [`go.work:7`](../../go.work) — workspace membership
- [`Makefile:40–44`](../../Makefile) — `update-shared-packages` still `go get`s `pkg/shutdown@latest` for every service

There are **no** other `services/*/cmd/**/main.go` files. Therefore there is no “healthy peer pattern still using `pkg/shutdown`” to compare against in the live tree.

### Historical consumer (IAM prototype)

Before the foundation rewrite, IAM **did** use the shared stack:

```go
go server.Start()

manager := shutdown.NewManager(appConfig.ShutdownTimeout, appLogger)
manager.Register(0, server)
manager.Register(1, db)
manager.Wait()
```

That exact content is in the parent of `72ec83f` (`git show 72ec83f^:services/iam/cmd/iam/main.go`). It matched the package’s documented phase layout: HTTP at phase 0, DB at phase 1. Compatibility came from:

- [`pkg/httpserver/server.go:78–80`](../../pkg/httpserver/server.go) — `Close(ctx)` → `http.Server.Shutdown`
- [`pkg/database/postgres.go:55–57`](../../pkg/database/postgres.go) — `Close(_ context.Context) error`

Those adapters satisfy `GracefulShutdown`. Current IAM types largely do **not** (`Shutdown` / `GracefulStop` / no-context `Close`).

---

## 4. History, docs, ADRs, TODOs, issues

| Evidence | Finding |
| --- | --- |
| `bf9ca13` (2026-03-07) | Introduces `pkg/shutdown` with flat (non-phased) manager |
| `1ce4a6d` (2026-03-29) | Adds phased `Manager`, documents HTTP-then-DB layout |
| `dc40d68` (2026-04-04) | Extracts `runPhases`, adds tests |
| `3fe94a1` (2026-07-25) | IAM draft `cmd/iam` uses `pkg/shutdown` |
| **`72ec83f` (2026-07-31)** | **Removes** `pkg/shutdown`, `pkg/httpserver`, `pkg/database` from IAM `main.go` **and** `go.mod`; switches to `NotifyContext` + local HTTP shutdown. Message: “Replace IAM prototype with runtime foundation… Closes #14” |
| `bb4fec6` (2026-07-31) | Adds `outbox-relay` with local pattern only |
| `6bab3ce` (2026-07-31) | Extends `cmd/iam` for dual HTTP+gRPC graceful stop — still no `pkg/shutdown` |
| ADRs / docs | No ADR or agent doc discusses omitting `pkg/shutdown`. `CONTEXT-MAP.md` still lists the package as shared infrastructure |
| TODOs / FIXMEs | No TODO/FIXME in IAM cmds or `pkg/shutdown` about re-adopting or migrating the package |
| GitHub Issues | Could not fetch `#14` body (`gh` keyring tokens invalid). Closing reference exists only in commit `72ec83f` |

There is **no** written ADR saying “do not use `pkg/shutdown` in IAM.” The strongest primary evidence of intent is the rewrite commit that deleted the import and module dependency while replacing the lifecycle.

---

## 5. Why the omission fits the current design

### 5.1 Explicit removal during foundation rewrite (primary answer)

`72ec83f` is a wholesale replacement of the Gin/`pkg/httpserver`/`pkg/database`/`pkg/shutdown` prototype with contract-first `net/http`, service-local postgres, and an error-returning `run(ctx)` main. Dropping `pkg/shutdown` is part of that same change set, not a later accidental drift.

### 5.2 Alternative pattern already covers signal graceful stop

IAM cmds already:

- Cancel on SIGINT/SIGTERM via stdlib `NotifyContext` ([pkg.go.dev](https://pkg.go.dev/os/signal#NotifyContext))
- Bound shutdown work with `context.WithTimeout(..., ShutdownTimeout)`
- Stop accept loops (`http.Server.Shutdown`, `grpc.Server.GracefulStop`)
- Release infra with `defer …Close()`

So the **core need** that `Manager.Wait()` addresses (wait for signal, then close with timeout) is already implemented locally.

### 5.3 `pkg/shutdown` is a poor fit for current IAM runtime shapes

Concrete mismatches (all from sources above):

1. **`Wait()` owns signals and blocks only on signals** — IAM needs `select` among serve failures, worker failures, and `ctx.Done()` ([`iam/main.go:82–93`](../../services/iam/cmd/iam/main.go), [`outbox-relay/main.go:125–131`](../../services/iam/cmd/outbox-relay/main.go)).
2. **`Wait()` swallows `Close` errors** (log only) — IAM returns joined shutdown/serve errors to `main` for non-zero exit.
3. **Interface mismatch** — manager requires `Close(ctx) error`; IAM HTTP uses `Shutdown`, gRPC uses `GracefulStop()`, DB/Kafka/App use no-context `Close`.
4. **Dual-protocol / worker coordination** — IAM runs HTTP+gRPC stop in parallel with timeout racing ([`iam/main.go:95–109`](../../services/iam/cmd/iam/main.go)); relay cancels a worker context then shuts HTTP ([`outbox-relay/main.go:133–146`](../../services/iam/cmd/outbox-relay/main.go)). `Manager` only runs registered `Close` methods in phases; it does not cancel arbitrary run contexts.
5. **Shared package is unused** — after the rewrite, nothing in-tree re-adopted it; Makefile still offers `go get` plumbing, but IAM no longer depends on the module.

### 5.4 What this is *not*

| Hypothesis | Assessment |
| --- | --- |
| Incomplete migration *to* `pkg/shutdown` | Rejected — history shows migration *off* it |
| Accidental inconsistency with other services | Weak — no other cmds exist; inconsistency is with a leftover shared package, not peer binaries |
| Package unsuitable forever | Overstated — it still matches simple “Start then Wait” apps that implement `Close(ctx)`; IAM’s current entrypoints are not that shape |

---

## Sources checklist

- [`pkg/shutdown/shutdown.go`](../../pkg/shutdown/shutdown.go), [`shutdown_test.go`](../../pkg/shutdown/shutdown_test.go)
- [`services/iam/cmd/iam/main.go`](../../services/iam/cmd/iam/main.go), [`services/iam/cmd/outbox-relay/main.go`](../../services/iam/cmd/outbox-relay/main.go)
- [`services/iam/internal/httpapi/server.go`](../../services/iam/internal/httpapi/server.go), [`grpcapi/server.go`](../../services/iam/internal/grpcapi/server.go), [`app/app.go`](../../services/iam/internal/app/app.go), [`relay/relay.go`](../../services/iam/internal/relay/relay.go)
- [`pkg/httpserver/server.go`](../../pkg/httpserver/server.go), [`pkg/database/postgres.go`](../../pkg/database/postgres.go)
- [`CONTEXT-MAP.md`](../../CONTEXT-MAP.md), [`Makefile`](../../Makefile), [`go.work`](../../go.work)
- Commits: `bf9ca13`, `1ce4a6d`, `dc40d68`, `3fe94a1`, `72ec83f`, `bb4fec6`, `6bab3ce`
- Official: [os/signal.NotifyContext](https://pkg.go.dev/os/signal#NotifyContext)
