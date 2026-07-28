## Что меняется / What changes

<!--
Коротко: что и зачем. Ссылка на issue, если есть.
Briefly: what and why. Link the issue if there is one.
-->

## Тип / Type

- [ ] Исправление / Bug fix
- [ ] Аддитивно: новая опция или метод / Additive: new option or method
- [ ] Документация / Documentation
- [ ] CI, сборка, инфраструктура / CI, build, infrastructure
- [ ] Ломающее изменение API — требует `/v3` / Breaking API change — requires `/v3`

## Чек-лист / Checklist

- [ ] `go build ./...`, `go vet ./...`, `go test ./... -race -cover`, `go mod tidy` — зелёные / all green
- [ ] Блок `require` в `go.mod` остался пустым; сторонних зависимостей нет /
      `go.mod`'s require block is still empty; no third-party dependencies
- [ ] Ошибки логируются как `Error(err, ...)` с обёрткой `fmt.Errorf("...: %w", err)`, а не отдельным сообщением /
      Errors are logged as `Error(err, ...)` with `fmt.Errorf("...: %w", err)` wrapping, not a separate message string
- [ ] Ни один путь эмита не вызывает `slog.Logger.Log`; кадры до `emit` не добавлялись и не убирались, `TestSourcePointsAtCallSite` зелёный /
      No emit path calls `slog.Logger.Log`; no frame was added or removed before `emit`, `TestSourcePointsAtCallSite` passes
- [ ] Обход в `stackOf` остался итеративным, с единым бюджетом на всё дерево; `TestErrorCyclicMultiError` зелёный /
      `stackOf`'s walk is still iterative under one budget for the whole tree; `TestErrorCyclicMultiError` passes
- [ ] Если тронут `scaryhttp/responsewriter.go`: writer по-прежнему реализует ровно те же интерфейсы, что и оригинал /
      If `scaryhttp/responsewriter.go` was touched: the writer still implements exactly the same interfaces as the original
- [ ] Каждая новая возможность описана в `SKILL.md` и покрыта тестом /
      Every new feature is documented in `SKILL.md` and covered by a test
- [ ] Тесты не зависят от порядка выполнения (`go test ./... -race -shuffle=on -count=1`) /
      Tests do not depend on execution order (`go test ./... -race -shuffle=on -count=1`)
