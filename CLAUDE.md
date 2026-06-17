# CLAUDE.md

Guidance for working in this repository.

## What this is

`scarylog` is a thin, opinionated wrapper around the standard library `log/slog`.
It standardizes structured logging across services: leveled methods, automatic
caller/stack capture on errors, attribute grouping, attribute overwrite, and
`context.Context` propagation. There are no third-party dependencies — stdlib only.

## Layout

- `logger.go` — `Logger`, functional `Option`s (`WithLevel`, `WithHandler`,
  `WithDefaultAttrs`, `WithGroup`, `WithAttrRemapping`, `WithTimeFormat`), the leveled
  methods (`Info`/`Warn`/`Debug`/`Error`) and their context-aware variants
  (`InfoContext`/`WarnContext`/`DebugContext`/`ErrorContext`, which forward `ctx` to the
  handler), plus `With`, `WithOverwrite`, `Group`.
- `doc.go` — package-level godoc overview.
- `context.go` — `ToContext` / `FromContext` (returns a shared lazy default logger
  when none is in the context, never nil, never panics).
- `scaryhttp/` — stdlib-only `net/http` middleware (`Middleware`) that attaches a
  per-request id + request-scoped logger and logs the request lifecycle. Same module,
  no third-party deps.
- `logger_test.go` — table/behavioral tests; output is asserted by parsing the JSON
  records the handler emits.
- `SKILL.md` — AI-assistant usage guide for this library (shipped with the repo).
- `workerpool/` — a **separate, unrelated** skill about implementing worker pools.
  Do not modify or commit it as part of scarylog changes.

## Conventions

- **No third-party deps.** Keep `go.mod`'s require block empty (stdlib only). Stack
  traces are detected via `fmt.Formatter` + `%+v`, not via any error library.
- **`Error(err error, args ...any)`** — the error is the first arg and becomes the
  log message. Add context by wrapping (`fmt.Errorf("...: %w", err)`), not a separate
  message string. Passing `nil` is safe.
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
