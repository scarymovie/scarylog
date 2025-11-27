package main

import (
	"errors"
	"log/slog"
	"scarySlog"
)

func main() {
	// Default logger (INFO level)
	defaultLogger := scarySlog.NewLogger()
	defaultLogger.Info("Application started (default logger)", "version", "1.0.0")
	defaultLogger.Debug("This debug message will not be shown by default logger")

	// Logger with DEBUG level
	debugLogger := scarySlog.NewLogger(scarySlog.WithLevel(slog.LevelDebug))
	debugLogger.Info("Application started (debug logger)", "version", "1.0.0")
	debugLogger.Debug("This debug message WILL be shown by debug logger")

	// Logger with default attributes
	attributedLogger := scarySlog.NewLogger(
		scarySlog.WithDefaultAttrs("service", "payment-processor", "env", "production"),
		scarySlog.WithLevel(slog.LevelDebug),
	)
	attributedLogger.Info("Processing new transaction", "transaction_id", "abc-123")
	attributedLogger.Error("Failed to process transaction", errors.New("database error"), "transaction_id", "abc-123")

	// Logger with a group for dynamic context
	groupedLogger := scarySlog.NewLogger(
		scarySlog.WithDefaultAttrs("service", "user-service"),
		scarySlog.WithGroup("context"),
	)
	groupedLogger.Info("User logged in", "user_id", "usr-456", "ip_address", "192.168.1.100")
	groupedLogger.Warn("User password nearing expiration", "user_id", "usr-456", "days_left", 3)
}
