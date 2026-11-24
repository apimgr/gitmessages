package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// EarlyServerSettings reads only the server settings needed before privilege drop.
func EarlyServerSettings(path string) (port, address string, daemonize bool) {
	return earlyValues(readEarlySettings(path, []string{"port", "address", "daemonize"}))
}

// EarlyBaseURL reads the merged base URL without creating or fully loading configuration.
func EarlyBaseURL(path string) string {
	settings := readEarlySettings(path, []string{"baseurl"})
	server, ok := settings["server"].(map[string]any)
	if !ok || server["baseurl"] == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(server["baseurl"]))
}

func readEarlySettings(path string, keys []string) map[string]any {
	merged := make(map[string]any)
	mergeEarlyFile(merged, path, keys)
	directory := filepath.Join(filepath.Dir(path), "yaml.d")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return merged
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	root, _ := filepath.Abs(directory)
	for _, entry := range entries {
		extension := filepath.Ext(entry.Name())
		if extension != ".yml" && extension != ".yaml" || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		file := filepath.Join(directory, entry.Name())
		resolved, err := filepath.EvalSymlinks(file)
		if err != nil {
			continue
		}
		resolved, _ = filepath.Abs(resolved)
		relative, err := filepath.Rel(root, resolved)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		info, err := os.Stat(resolved)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		mergeEarlyFile(merged, resolved, keys)
	}
	return merged
}

func mergeEarlyFile(destination map[string]any, path string, keys []string) {
	values, err := readConfigMap(path)
	if err != nil {
		return
	}
	server, ok := values["server"].(map[string]any)
	if !ok {
		return
	}
	filtered := make(map[string]any)
	for _, key := range keys {
		if value, found := server[key]; found {
			filtered[key] = value
		}
	}
	if len(filtered) == 0 {
		return
	}
	mergeMaps(destination, map[string]any{"server": filtered})
}

func earlyValues(values map[string]any) (port, address string, daemonize bool) {
	server, ok := values["server"].(map[string]any)
	if !ok {
		return "", "", false
	}
	if value, ok := server["port"]; ok && value != nil {
		port = strings.TrimSpace(fmt.Sprint(value))
	}
	if value, ok := server["address"]; ok && value != nil {
		address = strings.TrimSpace(fmt.Sprint(value))
	}
	if value, ok := server["daemonize"].(bool); ok {
		daemonize = value
	}
	return port, address, daemonize
}
