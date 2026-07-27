# scarylog

Этот проект `scarylog` - это Go-модуль, предназначенный для предоставления удобной обертки вокруг структурированного логгера Go, `slog`. Основная цель - упростить операции логгирования и обеспечить единообразную практику логгирования в приложениях.

Доступные языки: [English](README.en.md)

## Установка

Для установки пакета выполните:

```bash
  go get github.com/scarymovie/scarylog/v2
```

## Возможности

- Уровневые методы `Info`/`Warn`/`Debug`/`Error` и их контекстные варианты
  `InfoContext`/`WarnContext`/`DebugContext`/`ErrorContext`, прокидывающие `ctx`
  в обработчик (для context-aware хендлеров, например trace-корреляции).
- Автоматический захват caller и стек-трейса на ошибках. `ErrorMsg` — когда нужно
  стабильное низкокардинальное сообщение, а текст ошибки в атрибуте `error`.
- Группировка, перезапись и чтение атрибутов, проброс логгера через `context.Context`.
- `SetDefault` — логгер приложения как фолбэк для `FromContext`, чтобы записи
  вне request-scope не теряли `service`/`version`/`environment`.
- HTTP middleware в подпакете [`scaryhttp`](scaryhttp/) — авто-`X-Request-ID`
  (и любое число дополнительных correlation id), request-scoped логгер в контексте
  и лог жизненного цикла запроса, в том числе при панике. Только stdlib.
  WebSocket-апгрейды и стриминг не ломаются: writer, который получает хендлер,
  реализует ровно те же интерфейсы, что и оригинальный.

Логгер приложения ставится один раз при старте:

```go
base := scarylog.NewLogger(
    scarylog.WithDefaultAttrs("service", "my-service", "version", version),
)
scarylog.SetDefault(base)
```

Подробное руководство по использованию — в [`SKILL.md`](SKILL.md).

## Лицензия

[MIT](LICENSE)
