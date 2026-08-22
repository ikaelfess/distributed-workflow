# shutdown

Ordered `Close` during process shutdown. The process owns OS signals and the deadline; this package only runs registered closers.

```go
manager := shutdown.New()
manager.Register(0, server.Close) // listeners first
manager.Register(1, db.Close)     // then dependencies
```

`Register` is not safe for concurrent use: register from one goroutine, then call `Shutdown`.

## Example: process main

```go
package main

import (
    "context"
    "log"
    "os/signal"
    "syscall"
    "time"

    "github.com/ikaelfess/distributed-workflow/pkg/shutdown"
)

func main() {
    server := newHTTPServer()
    db := newDB()

    go server.ListenAndServe()

    manager := shutdown.New()
    manager.Register(0, server.Shutdown)
    manager.Register(1, db.Close)

    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    <-ctx.Done()
    stop() // restore default: a second SIGINT kills a hung drain

    shutdownCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
    defer cancel()
    if err := manager.Shutdown(shutdownCtx); err != nil {
      log.Fatal(err)
    }
}
```

`http.Server.Shutdown` and a `Close(context.Context) error` method both match `Register`.

## Example: phases

Lower phase numbers finish first. Closers in the same phase run in parallel.

```go
manager := shutdown.New()
manager.Register(0, httpServer.Shutdown)
manager.Register(0, rpcServer.Shutdown) // same phase as HTTP
manager.Register(1, db.Close)
```

Zero value works (`var m shutdown.Manager`); `New()` just allocates the map up front.

## Behavior

- Close errors and panics do not skip remaining work. A done context skips later phases.
- `Shutdown` returns `errors.Join` of closer failures, recovered panics (`phase N: panic: …`), and `ctx.Err()` if the deadline fired.
- Closers must respect `ctx`, or they can outlive `Shutdown` until the process exits.
- The caller handles the error (log, `os.Exit`, ignore).
