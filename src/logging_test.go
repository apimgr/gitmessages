package main

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/mode"
)

func TestConfigureStructuredLoggerUsesModeDefault(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })

	mode.InitializeWithConfig("production", "", nil)
	if err := configureStructuredLogger(""); err != nil {
		t.Fatal(err)
	}
	if slog.Default().Enabled(t.Context(), slog.LevelDebug) {
		t.Fatal("production mode unexpectedly enabled debug logging")
	}

	debugOverride := true
	mode.InitializeWithConfig("production", "", &debugOverride)
	if err := configureStructuredLogger(""); err != nil {
		t.Fatal(err)
	}
	if !slog.Default().Enabled(t.Context(), slog.LevelDebug) {
		t.Fatal("explicit debug flag did not enable debug logging in production")
	}

	mode.InitializeWithConfig("development", "", nil)
	if err := configureStructuredLogger(""); err != nil {
		t.Fatal(err)
	}
	if !slog.Default().Enabled(t.Context(), slog.LevelDebug) {
		t.Fatal("development mode did not enable debug logging")
	}
}

func TestConfigureStructuredLoggerHonorsExplicitLevel(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	mode.InitializeWithConfig("development", "", nil)

	if err := configureStructuredLogger("error"); err != nil {
		t.Fatal(err)
	}
	if slog.Default().Enabled(t.Context(), slog.LevelWarn) {
		t.Fatal("explicit error level did not override development debug level")
	}
	if err := configureStructuredLogger("invalid"); err == nil {
		t.Fatal("invalid level was accepted")
	}
}

func TestStructuredLoggerWritesDebugWhenEnabled(t *testing.T) {
	previous := slog.Default()
	t.Cleanup(func() { slog.SetDefault(previous) })
	var output bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug})))
	slog.Debug("diagnostic", "path", "/health")
	if !strings.Contains(output.String(), "diagnostic") {
		t.Fatalf("debug log was not emitted: %q", output.String())
	}
}
