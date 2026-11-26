package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func recordCLIAudit(event string, fields map[string]string) {
	path, err := configPath()
	if err != nil {
		return
	}
	record := map[string]string{"timestamp": time.Now().UTC().Format(time.RFC3339Nano), "event": event}
	for key, value := range fields {
		record[key] = value
	}
	data, err := json.Marshal(record)
	if err != nil {
		return
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return
	}
	logFile, err := os.OpenFile(filepath.Join(directory, "cli.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_ = logFile.Chmod(0o600)
	_, _ = fmt.Fprintln(logFile, string(data))
	_ = logFile.Close()
	_, _ = fmt.Fprintln(os.Stderr, string(data))
}
