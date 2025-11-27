# scarySlog

This project `scarySlog` is a Go module designed to provide a convenient wrapper around Go's structured logger, `slog`. The primary goal is to simplify logging operations and enforce consistent logging practices across applications.

## Project Status

This project now includes a basic `slog` wrapper. The initial setup includes:

*   Go module `scarySlog`
*   Go version 1.25
*   `slog` wrapper with `Info`, `Warn`, `Error`, and `Debug` methods.

## Getting Started

To use this module, you can clone the repository:

```bash
git clone https://github.com/your-username/scarySlog.git
cd scarySlog
```

## Usage

The `scarySlog` package provides a flexible wrapper around `log/slog`.

### Initialization and Configuration

To create a new logger instance, you can use `scarySlog.NewLogger()`. It accepts optional functional options to configure its behavior.

*   **`scarySlog.WithLevel(level slog.Leveler)`**: Sets the minimum logging level (e.g., `slog.LevelDebug`, `slog.LevelInfo`, `slog.LevelWarn`, `slog.LevelError`).
*   **`scarySlog.WithDefaultAttrs(args ...any)`**: Adds default attributes that will be included with every log entry made by this logger instance. These should be key-value pairs.
*   **`scarySlog.WithGroup(name string)`**: Specifies a name for a `slog.Group` that will wrap all dynamic attributes passed to the logging methods (Info, Warn, Error, Debug). This helps to structure dynamic data within your log entries.

Example:

```go
import (
	"log/slog"
	"scarySlog"
)

func main() {
	// Logger with default INFO level
	defaultLogger := scarySlog.NewLogger()
	
	// Logger with DEBUG level
	debugLogger := scarySlog.NewLogger(scarySlog.WithLevel(slog.LevelDebug))

	// Logger with default attributes and DEBUG level
	attributedLogger := scarySlog.NewLogger(
		scarySlog.WithLevel(slog.LevelDebug),
		scarySlog.WithDefaultAttrs("service", "payment-processor", "env", "production"),
	)

	// Logger with a group for dynamic context
	groupedLogger := scarySlog.NewLogger(
		scarySlog.WithDefaultAttrs("service", "user-service"),
		scarySlog.WithGroup("context"),
	)
}
```

### Available Logging Methods

*   `Info(msg string, args ...any)`: Logs an informational message.
*   `Warn(msg string, args ...any)`: Logs a warning message.
*   `Error(msg string, err error, args ...any)`: Logs an error message with an associated error object. The error is logged as an attribute named "error".
*   `Debug(msg string, args ...any)`: Logs a debug message.

### Running the Example

An example demonstrating the logger's usage and configuration can be found in `examples/main.go`. To run it, navigate to the `examples` directory and execute:

```bash
cd examples
go run main.go
```

## Contributing

(Contribution guidelines will be added here.)

## License

(License information will be added here.)
