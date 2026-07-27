# CLAUDE.md

Guidance for working in this repository.

## What this is

`scarylog` is a thin, opinionated wrapper around the standard library `log/slog`.
It standardizes structured logging across services: leveled methods, automatic
caller/stack capture on errors, attribute grouping, attribute overwrite, and
`context.Context` propagation. There are no third-party dependencies — stdlib only.

## Layout

- `logger.go` — `Logger`, functional `Option`s (`WithLevel`, `WithHandler`,
  `WithWriter`, `WithDefaultAttrs`, `WithGroup`, `WithAttrRemapping`, `WithTimeFormat`,
  `WithSource`),
  the leveled methods (`Info`/`Warn`/`Debug`/`Error`/`ErrorMsg`) and their context-aware
  variants (`InfoContext`/`WarnContext`/`DebugContext`/`ErrorContext`/`ErrorMsgContext`,
  which forward `ctx` to the handler), the level-generic `Log`/`Enabled`, plus `With`,
  `WithOverwrite`, `Group`.
- `handler.go` — `optionsHandler`, which applies the options that can still be
  honored on top of a handler supplied via `WithHandler` (`WithLevel` always;
  attribute remapping and time format for user attributes only — the record's own
  `time`/`level`/`msg` keys belong to that handler's `ReplaceAttr`).
- `doc.go` — package-level godoc overview.
- `context.go` — `ToContext` / `FromContext` / `FromContextOK`, and
  `SetDefault` / `Default` for the application-wide fallback. `FromContext` never
  returns nil and never panics, including for a nil ctx or a nil logger in ctx.
- `scaryhttp/` — stdlib-only `net/http` middleware (`Middleware`) that attaches
  per-request correlation ids + a request-scoped logger and logs the request
  lifecycle (including on panic). Same module, no third-party deps.
- `scaryhttp/responsewriter.go` — the response-writer wrapper. One concrete type
  per combination of `http.Flusher`/`http.Hijacker`/`io.ReaderFrom`/`http.Pusher`,
  chosen by bitmask, so the writer handed to the handler implements **exactly**
  the interfaces the original does. This is what keeps WebSocket upgrades and
  streaming alive behind the middleware; do not collapse it into unconditional
  methods, which would make every writer claim capabilities it may not have.
- `logger_test.go` — table/behavioral tests; output is asserted by parsing the JSON
  records the handler emits.
- `SKILL.md` — AI-assistant usage guide for this library (shipped with the repo).
- `workerpool/` — a **separate, unrelated** skill about implementing worker pools.
  Do not modify or commit it as part of scarylog changes.

## Conventions

- **No third-party deps.** Keep `go.mod`'s require block empty (stdlib only). Stack
  traces are detected via `fmt.Formatter` + `%+v`, not via any error library, and
  `stackOf` walks the whole `Unwrap` chain (single and `[]error`) — `fmt.Errorf`
  with `%w` returns a `*fmt.wrapError` that is not a `fmt.Formatter`, so checking
  only the top error would disable stacks for the wrapping idiom this repo
  documents.
- **`Error(err error, args ...any)`** — the error is the first arg and becomes the
  log message. Add context by wrapping (`fmt.Errorf("...: %w", err)`), not a separate
  message string. Passing `nil` is safe.
- **Never call `slog.Logger.Log` to emit.** It records the PC of *its* caller —
  this package — so `AddSource` would label every record with a frame inside
  scarylog. All logging funnels through `Logger.emit`, which captures the PC
  itself using `callerSkip`. That constant assumes every public method reaches
  `emit` through exactly one helper (`log`, `errorLog` or `errorMsgLog`); adding
  or removing a frame on any of those paths silently moves the reported source.
  `TestSourcePointsAtCallSite` pins the depth for every public method.
- Tests assert on parsed JSON output via a custom handler writing to a buffer; for
  options that only affect the built-in stdout handler (`WithLevel`,
  `WithAttrRemapping`, `WithTimeFormat`), capture `os.Stdout` (see `captureStdout`).
- Every public feature documented in `SKILL.md` must have a corresponding test.

## Commands

```bash
go build ./...
go vet ./...
go test ./... -race -cover     # concurrent paths must pass under -race
go mod tidy                     # require block must stay empty
```

## Versioning

This module is on **v2** (module path `github.com/scarymovie/scarylog/v2`, first
released as v2.0.1). v2 intentionally broke the `Error` signature (see above)
versus v1.0.x. Per Go semantic-import-versioning, the `/v2` path suffix is required
so v1 consumers keep working on the old path. Any further breaking change requires a new major suffix
(`/v3`) and module-path bump.

**v2.1.0** answers an integration report from the `websocket-server` team (echo +
gorilla/websocket): `scaryhttp` could not be enabled at all, because wrapping the
response writer killed the WebSocket upgrade. Everything in the release is
additive — new options and methods, plus bug fixes — so no `/v3` is needed.
Two behaviour changes worth knowing when reading old code: `scaryhttp` now derives
its per-request logger with `WithOverwrite` rather than `With` (a base logger
already carrying `request_id` used to emit the key twice), and `WithOverwrite`
preserves attribute order instead of ranging over a map.
