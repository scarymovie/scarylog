# scarylog

This project `scarylog` is a Go module designed to provide a convenient wrapper around Go's structured logger, `slog`. The primary goal is to simplify logging operations and enforce consistent logging practices across applications.

## Key Features

*   **Simple Wrapper**: Provides a straightforward and convenient wrapper around Go's standard `slog` logger.
*   **Colored Output**: Supports colored log output to the console for improved readability and easier debugging.
*   **Flexible Configuration**: Offers flexible configuration through functional options like `WithLevel` and others.
*   **Default Attributes**: Allows setting default attributes that will be included in all log entries for consistent context.

## Project Status

This project now includes a basic `slog` wrapper. The initial setup includes:

*   Go module `scarylog`
*   Go version 1.25
*   `slog` wrapper with `Info`, `Warn`, `Error`, and `Debug` methods.
