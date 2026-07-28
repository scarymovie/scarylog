# Contributing to scarylog

Languages: [Русский](CONTRIBUTING.md)

`scarylog` is a thin, deliberately narrow wrapper around the standard library's
`log/slog`. It standardizes structured logging across services: leveled methods,
automatic caller/stack capture on errors, attribute grouping and overwrite, and
`context.Context` propagation.

Before opening a pull request, read [`SKILL.md`](../SKILL.md) — the API guide —
and [`CLAUDE.md`](../CLAUDE.md) — how the internals are put together. The
invariants below are reasons to reject a PR even when the tests are green.

## Invariants

### 1. No third-party dependencies

`go.mod`'s require block must stay **empty**. Standard library only, across the
whole module, `scaryhttp` included. Stack traces are detected through
`fmt.Formatter` and `%+v`, not through any error library. CI enforces this:
`go mod tidy` must be a no-op, and `go.mod` must not contain a single `require`
line.

### 2. `Error(err error, args ...any)` — the error comes first

The error becomes the record's message. Context is added by **wrapping**, not by
a separate message string:

```go
if err := do(); err != nil {
    log.Error(fmt.Errorf("charge customer %s: %w", id, err))
}
```

Passing `nil` is safe. When you need a stable, low-cardinality message with the
error text in an `error` attribute, use `ErrorMsg`. This signature is not up for
change: v2 already broke it relative to v1, and the next breaking change would
require a `/v3` suffix and a module-path bump.

### 3. Never call `slog.Logger.Log` to emit

`slog.Logger.Log` records the PC of *its* caller — a frame inside `scarylog` — so
with `AddSource` every record would point at this package instead of the call
site. All logging funnels through `Logger.emit`, which captures the PC itself
using the `callerSkip` constant.

`callerSkip` assumes every public method reaches `emit` through exactly one
helper — `log`, `errorLog` or `errorMsgLog`. Adding or removing a frame on any of
those paths silently moves the reported source.
`TestSourcePointsAtCallSite` pins the depth for every public method; if you touch
the call chain, that test must stay green without eyeballing a new `callerSkip`.

### 4. `stackOf`'s walk must stay iterative

`stackOf` walks the whole `Unwrap` chain, including the `Unwrap() []error`
branching. Branches are carried on an explicit `pending` slice under a **single**
`maxUnwrapNodes` budget for the entire tree. Recursing per branch restarts the
budget at every level: a cyclic multi-error then walks forever and the process
dies with `fatal error: stack overflow` — unrecoverable, and raised from inside
the error-logging path. `TestErrorCyclicMultiError` guards this.

### 5. `scaryhttp`'s `http.ResponseWriter` wrapper: one type per combination

`scaryhttp/responsewriter.go` holds one concrete type per combination of
`http.Flusher` / `http.Hijacker` / `io.ReaderFrom` / `http.Pusher`, chosen by
bitmask. The writer handed to the handler implements **exactly** the interfaces
the original does. That is what keeps WebSocket upgrades and streaming alive
behind the middleware. Do not collapse it into unconditional methods — every
writer would then claim capabilities it may not have.

### 6. Every feature documented in `SKILL.md` needs a test

If you add a public feature, document it in `SKILL.md` and add a test. The
converse holds too: do not document anything in `SKILL.md` that no test covers.

## Test style

- Tests parse the JSON the handler emits and assert on the parsed records, not on
  substrings.
- For options that only affect the built-in stdout handler (`WithLevel`,
  `WithAttrRemapping`, `WithTimeFormat`), capture `os.Stdout` — see the
  `captureStdout` helper.
- Concurrent paths must pass under `-race`.
- Test order must not be load-bearing: CI runs `-shuffle=on`. Do not rely on a
  previous test having warmed anything up; wait for the state you need
  explicitly, against a deadline.

## Commands

Run all of these before sending a PR:

```bash
go build ./...
go vet ./...
go test ./... -race -cover     # concurrent paths must pass under -race
go mod tidy                     # require block must stay empty
```

On top of that, CI runs `golangci-lint` (v2, config `.golangci.yml` at the repo
root), `govulncheck`, a benchmark smoke run, and the tests with
`-shuffle=on -covermode=atomic`. Locally that is:

```bash
go test ./... -race -coverprofile=coverage.out -covermode=atomic -count=1 -shuffle=on -timeout=5m
go test ./... -run='^$' -bench=. -benchtime=1x
golangci-lint run
```

## Versioning

The module is on **v2**: path `github.com/scarymovie/scarylog/v2`. Any breaking
API change requires a new major suffix (`/v3`) and a module-path bump, per Go's
semantic import versioning. Additive changes (new options, new methods) and fixes
go into the v2 line.

## Process

1. Fork and branch from `main`.
2. Keep the PR focused: one change, one PR.
3. Run the commands above; paste the output when it is not obvious.
4. Fill in the checklist in the pull request template.
5. `@scarymovie` owns every path (see `CODEOWNERS`); review is required.

Vulnerabilities do not go through issues or PRs: see [`SECURITY.en.md`](SECURITY.en.md).
