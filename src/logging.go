package main

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/apimgr/gitmessages/src/metrics"
	"github.com/apimgr/gitmessages/src/mode"
	serverbanner "github.com/apimgr/gitmessages/src/server/banner"
)

func configureStructuredLogger(configuredLevel string) error {
	level := strings.ToLower(strings.TrimSpace(configuredLevel))
	if level == "" {
		if mode.IsDebugEnabled() {
			level = "debug"
		} else {
			level = mode.GetLogLevel()
		}
	}

	var minimum slog.Level
	switch level {
	case "debug":
		minimum = slog.LevelDebug
	case "info":
		minimum = slog.LevelInfo
	case "warn", "warning":
		minimum = slog.LevelWarn
	case "error":
		minimum = slog.LevelError
	default:
		return fmt.Errorf("unsupported log level %q", configuredLevel)
	}

	output := serverbanner.SynchronizedConsoleWriter(io.MultiWriter(os.Stderr, metrics.LogWriter()))
	slog.SetDefault(slog.New(slog.NewTextHandler(output, &slog.HandlerOptions{Level: minimum})))
	return nil
}
