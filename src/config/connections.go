package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var connectionURLSecretPaths = [][]string{{"server", "database", "url"}, {"server", "cache", "url"}}

var envReferencePattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

func seedConnectionEnvironment(configPath string, firstRun bool) error {
	if !firstRun {
		return nil
	}
	root, err := readConfigMap(configPath)
	if err != nil {
		return fmt.Errorf("read initial configuration: %w", err)
	}
	merged := make(map[string]any)
	mergeMaps(merged, root)
	areaDir := filepath.Join(filepath.Dir(configPath), "yaml.d")
	if entries, readErr := os.ReadDir(areaDir); readErr == nil {
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || (filepath.Ext(entry.Name()) != ".yml" && filepath.Ext(entry.Name()) != ".yaml") {
				continue
			}
			values, readErr := readConfigMap(filepath.Join(areaDir, entry.Name()))
			if readErr == nil {
				mergeMaps(merged, values)
			}
		}
	}
	secretPath := filepath.Join(filepath.Dir(configPath), "secrets.yml")
	if values, readErr := readConfigMap(secretPath); readErr == nil {
		mergeMaps(merged, values)
	}
	secrets := make(map[string]any)
	seed := func(envName string, keyPath []string, classify func(string) (string, error)) (string, bool, error) {
		value := strings.TrimSpace(os.Getenv(envName))
		if value == "" {
			return "", false, nil
		}
		if existing, ok := getConfigPath(merged, keyPath); ok && strings.TrimSpace(fmt.Sprint(existing)) != "" {
			return "", false, nil
		}
		if classify != nil {
			classified, err := classify(value)
			if err != nil {
				return "", false, err
			}
			value = classified
		}
		return value, true, nil
	}
	if value, ok, err := seed("DATABASE_URL", []string{"server", "database", "url"}, normalizeDatabaseURL); err != nil {
		return errors.New("DATABASE_URL is invalid; use the URL form")
	} else if ok {
		putConfigPath(merged, []string{"server", "database", "url"}, value)
		if driver, err := databaseDriverForURL(value); err != nil {
			return errors.New("DATABASE_URL is invalid; use the URL form")
		} else {
			putConfigPath(root, []string{"server", "database", "driver"}, driver)
		}
		storeSeededConnection(root, secrets, []string{"server", "database", "url"}, value)
	} else if driver := strings.TrimSpace(os.Getenv("DATABASE_DRIVER")); driver != "" {
		driver = normalizeDatabaseDriver(driver)
		if driver != "sqlite" && driver != "libsql" {
			return errors.New("DATABASE_DRIVER is invalid; use sqlite or libsql")
		}
		putConfigPath(root, []string{"server", "database", "driver"}, driver)
	}
	if value, ok, err := seed("CACHE_URL", []string{"server", "cache", "url"}, normalizeCacheURL); err != nil {
		return errors.New("CACHE_URL is invalid; use a valkey://, valkeys://, redis://, or rediss:// URL")
	} else if ok {
		putConfigPath(merged, []string{"server", "cache", "url"}, value)
		cacheType, _ := cacheTypeForURL(value)
		putConfigPath(root, []string{"server", "cache", "type"}, cacheType)
		storeSeededConnection(root, secrets, []string{"server", "cache", "url"}, value)
	}
	if len(secrets) == 0 && !connectionMapChanged(root, merged) {
		return nil
	}
	configValues := root
	if len(secrets) > 0 {
		secretExisting, readErr := readConfigMap(secretPath)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return fmt.Errorf("read secrets configuration: %w", readErr)
		}
		if secretExisting == nil {
			secretExisting = make(map[string]any)
		}
		mergeMapsPreferExisting(secretExisting, secrets)
		encoded, marshalErr := yaml.Marshal(secretExisting)
		if marshalErr != nil {
			return fmt.Errorf("encode secrets configuration: %w", marshalErr)
		}
		if err := writePrivateAtomic(secretPath, encoded); err != nil {
			return fmt.Errorf("save secrets configuration: %w", err)
		}
	}
	server, _ := configValues["server"].(map[string]any)
	if server == nil {
		server = make(map[string]any)
		configValues["server"] = server
	}
	encoded, err := yaml.Marshal(configValues)
	if err != nil {
		return fmt.Errorf("encode initial configuration: %w", err)
	}
	if err := writePrivateAtomic(configPath, []byte(configHeader+string(encoded))); err != nil {
		return fmt.Errorf("save initial configuration: %w", err)
	}
	return nil
}

func storeSeededConnection(root, secrets map[string]any, path []string, value string) {
	if connectionURLHasCredentials(value) {
		putConfigPath(secrets, path, value)
		deleteConfigPath(root, path)
		return
	}
	putConfigPath(root, path, value)
}

func connectionMapChanged(before, after map[string]any) bool {
	for _, path := range [][]string{{"server", "database"}, {"server", "cache"}} {
		left, leftOK := getConfigPath(before, path)
		right, rightOK := getConfigPath(after, path)
		if leftOK != rightOK || fmt.Sprint(left) != fmt.Sprint(right) {
			return true
		}
	}
	return false
}

func normalizeDatabaseDriver(driver string) string {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "sqlite", "sqlite2", "sqlite3":
		return "sqlite"
	case "libsql", "turso":
		return "libsql"
	default:
		return strings.ToLower(strings.TrimSpace(driver))
	}
}

func normalizeDatabaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "jdbc:") || strings.Contains(value, ";") {
		return "", errors.New("invalid database URL")
	}
	if !strings.Contains(value, "://") {
		if strings.HasPrefix(value, "file:") {
			return value, nil
		}
		return value, nil
	}
	parsed, err := parseConnectionURL(value)
	if err != nil || parsed.Host == "" || parsed.User != nil && parsed.Scheme != "libsql" && parsed.Scheme != "https" {
		return "", errors.New("invalid database URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "libsql", "https", "http":
		if parsed.Scheme == "http" && !databaseHostIsPrivate(parsed.Hostname()) {
			return "", errors.New("insecure database URL host is not private")
		}
		return value, nil
	default:
		return "", errors.New("unsupported database URL scheme")
	}
}

func databaseDriverForURL(value string) (string, error) {
	if !strings.Contains(value, "://") {
		if strings.HasPrefix(value, "file:") {
			return "sqlite", nil
		}
		return "sqlite", nil
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "libsql" || parsed.Scheme == "https" || parsed.Scheme == "http" {
		return "libsql", nil
	}
	return "", errors.New("unsupported database URL scheme")
}

func normalizeCacheURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := parseConnectionURL(value)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "", errors.New("invalid cache URL")
	}
	if _, err := cacheTypeForURL(value); err != nil {
		return "", err
	}
	return value, nil
}

func cacheTypeForURL(value string) (string, error) {
	parsed, err := parseConnectionURL(value)
	if err != nil {
		return "", errors.New("invalid cache URL")
	}
	switch strings.ToLower(parsed.Scheme) {
	case "valkey", "valkeys":
		return "valkey", nil
	case "redis", "rediss":
		return "redis", nil
	default:
		return "", errors.New("unsupported cache URL scheme")
	}
}

func connectionURLHasCredentials(value string) bool {
	if parsed, err := parseConnectionURL(value); err == nil && parsed.User != nil {
		return true
	}
	if query := strings.IndexByte(value, '?'); query >= 0 {
		for _, pair := range strings.Split(value[query+1:], "&") {
			key, val, _ := strings.Cut(pair, "=")
			switch strings.ToLower(key) {
			case "password", "pwd", "token", "authtoken":
				if val != "" {
					return true
				}
			}
		}
	}
	return false
}

func parseConnectionURL(value string) (*url.URL, error) {
	marker := strings.Index(value, "://")
	if marker < 0 {
		return url.Parse(value)
	}
	scheme, rest := value[:marker], value[marker+3:]
	at := strings.LastIndex(rest, "@")
	if at >= 0 {
		userinfo := rest[:at]
		var escaped string
		if username, password, hasPassword := strings.Cut(userinfo, ":"); hasPassword {
			escaped = url.UserPassword(decodeURLUserinfo(username), decodeURLUserinfo(password)).String()
		} else {
			escaped = url.UserPassword("", decodeURLUserinfo(userinfo)).String()
		}
		rest = escaped + "@" + rest[at+1:]
	}
	return url.Parse(scheme + "://" + strings.ReplaceAll(rest, " ", "%20"))
}

// ConnectionURLForDriver returns a driver-safe URL with userinfo escaped.
func ConnectionURLForDriver(value string) (string, error) {
	parsed, err := parseConnectionURL(value)
	if err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func decodeURLUserinfo(value string) string {
	var decoded strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] == '%' && index+2 < len(value) {
			if high, okHigh := fromHex(value[index+1]); okHigh {
				if low, okLow := fromHex(value[index+2]); okLow {
					decoded.WriteByte(high<<4 | low)
					index += 2
					continue
				}
			}
		}
		decoded.WriteByte(value[index])
	}
	return decoded.String()
}

func fromHex(value byte) (byte, bool) {
	switch {
	case value >= '0' && value <= '9':
		return value - '0', true
	case value >= 'a' && value <= 'f':
		return value - 'a' + 10, true
	case value >= 'A' && value <= 'F':
		return value - 'A' + 10, true
	default:
		return 0, false
	}
}

func databaseHostIsPrivate(host string) bool {
	if address, err := netip.ParseAddr(host); err == nil {
		return address.IsLoopback() || address.IsPrivate()
	}
	addresses, err := net.LookupIP(host)
	if err != nil || len(addresses) == 0 {
		return false
	}
	for _, address := range addresses {
		parsed, ok := netip.AddrFromSlice(address)
		if !ok || (!parsed.IsLoopback() && !parsed.IsPrivate()) {
			return false
		}
	}
	return true
}

func expandEnvironmentReferences(root map[string]any) {
	var expand func(any) any
	expand = func(value any) any {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				typed[key] = expand(child)
			}
			return typed
		case []any:
			for i, child := range typed {
				typed[i] = expand(child)
			}
			return typed
		case string:
			return envReferencePattern.ReplaceAllStringFunc(typed, func(reference string) string {
				parts := envReferencePattern.FindStringSubmatch(reference)
				value, exists := os.LookupEnv(parts[1])
				if exists {
					return value
				}
				if len(parts) > 2 {
					return parts[2]
				}
				return ""
			})
		default:
			return value
		}
	}
	for key, value := range root {
		root[key] = expand(value)
	}
}

func warnConnectionEnvironmentMismatch(cfg *Config) {
	if value := strings.TrimSpace(os.Getenv("DATABASE_URL")); value != "" && cfg.Server.Database.URL != value {
		fmt.Fprintln(os.Stderr, "warning: DATABASE_URL differs from the saved connection; environment value ignored")
	}
	if value := strings.TrimSpace(os.Getenv("CACHE_URL")); value != "" && cfg.Server.Cache.URL != value {
		fmt.Fprintln(os.Stderr, "warning: CACHE_URL differs from the saved connection; environment value ignored")
	}
}

func validateConnections(cfg *Config) error {
	defaults := DefaultConfig().Server
	database := &cfg.Server.Database
	database.Driver = normalizeDatabaseDriver(database.Driver)
	if database.Driver == "" {
		database.Driver = defaults.Database.Driver
	}
	if database.URL != "" {
		normalized, err := normalizeDatabaseURL(database.URL)
		if err != nil {
			return errors.New("invalid server.database.url; use the URL form")
		}
		database.URL = normalized
		urlDriver, err := databaseDriverForURL(database.URL)
		if err != nil {
			return errors.New("invalid server.database.url; use the URL form")
		}
		if urlDriver != database.Driver {
			fmt.Fprintln(os.Stderr, "warning: server.database.url selects a different driver; URL scheme takes precedence")
			database.Driver = urlDriver
		}
	}
	if database.Driver != "sqlite" && database.Driver != "libsql" {
		return errors.New("server.database.driver must be sqlite or libsql")
	}
	if database.Driver == "libsql" && database.URL == "" {
		return errors.New("server.database.url is required for the libsql driver")
	}
	if database.Pool.MaxOpen < 1 {
		database.Pool.MaxOpen = defaults.Database.Pool.MaxOpen
		warnInvalid("server.database.pool.max_open")
	}
	if database.Pool.MaxIdle < 0 {
		database.Pool.MaxIdle = defaults.Database.Pool.MaxIdle
		warnInvalid("server.database.pool.max_idle")
	}
	if database.Pool.MaxIdle > database.Pool.MaxOpen {
		database.Pool.MaxIdle = database.Pool.MaxOpen
		warnInvalid("server.database.pool.max_idle")
	}
	if database.Pool.MaxLifetime == "" {
		database.Pool.MaxLifetime = defaults.Database.Pool.MaxLifetime
	}
	if lifetime, err := time.ParseDuration(database.Pool.MaxLifetime); err != nil || lifetime <= 0 {
		database.Pool.MaxLifetime = defaults.Database.Pool.MaxLifetime
		warnInvalid("server.database.pool.max_lifetime")
	}
	if database.Pool.MaxIdleTime == "" {
		database.Pool.MaxIdleTime = defaults.Database.Pool.MaxIdleTime
	}
	if idleTime, err := time.ParseDuration(database.Pool.MaxIdleTime); err != nil || idleTime <= 0 {
		database.Pool.MaxIdleTime = defaults.Database.Pool.MaxIdleTime
		warnInvalid("server.database.pool.max_idle_time")
	}
	if database.Cache.SyncInterval == "" {
		database.Cache.SyncInterval = defaults.Database.Cache.SyncInterval
	}
	if interval, err := time.ParseDuration(database.Cache.SyncInterval); err != nil || interval <= 0 {
		database.Cache.SyncInterval = defaults.Database.Cache.SyncInterval
		warnInvalid("server.database.cache.sync_interval")
	}
	if database.Cache.MaxAge == "" {
		database.Cache.MaxAge = defaults.Database.Cache.MaxAge
	}
	if maxAge, err := time.ParseDuration(database.Cache.MaxAge); err != nil || maxAge <= 0 {
		database.Cache.MaxAge = defaults.Database.Cache.MaxAge
		warnInvalid("server.database.cache.max_age")
	}
	if database.Cache.Enabled && database.Driver != "libsql" {
		return errors.New("server.database.cache.enabled requires the libsql driver")
	}

	cache := &cfg.Server.Cache
	cache.Type = strings.ToLower(strings.TrimSpace(cache.Type))
	if cache.Type == "" {
		cache.Type = defaults.Cache.Type
	}
	if cache.URL != "" {
		normalized, err := normalizeCacheURL(cache.URL)
		if err != nil {
			return errors.New("invalid server.cache.url; use a valkey://, valkeys://, redis://, or rediss:// URL")
		}
		cache.URL = normalized
		urlType, _ := cacheTypeForURL(normalized)
		if cache.Type != urlType {
			fmt.Fprintln(os.Stderr, "warning: server.cache.url selects a different cache type; URL scheme takes precedence")
			cache.Type = urlType
		}
	}
	switch cache.Type {
	case "none", "memory", "valkey", "redis":
	default:
		return errors.New("server.cache.type must be none, memory, valkey, or redis")
	}
	if cache.Port < 1 || cache.Port > 65535 {
		cache.Port = defaults.Cache.Port
		warnInvalid("server.cache.port")
	}
	if cache.DB < 0 || cache.PoolSize < 1 || cache.MinIdle < 0 || cache.MinIdle > cache.PoolSize {
		return errors.New("server.cache db and pool settings are invalid")
	}
	if _, err := time.ParseDuration(cache.Timeout); err != nil {
		cache.Timeout = defaults.Cache.Timeout
		warnInvalid("server.cache.timeout")
	}
	if _, err := time.ParseDuration(cache.TTL); err != nil {
		cache.TTL = defaults.Cache.TTL
		warnInvalid("server.cache.ttl")
	}
	if (cache.TLSCert == "") != (cache.TLSKey == "") {
		return errors.New("server.cache.tls_cert and server.cache.tls_key must be configured together")
	}
	parsedURL, err := parseConnectionURL(cache.URL)
	if cache.URL != "" && err != nil {
		return errors.New("invalid server.cache.url; use a URL form")
	}
	query := parsedURL.Query()
	if value := query.Get("tls"); value != "" {
		parsed, err := ParseBool(value, false)
		if err != nil {
			return errors.New("invalid server.cache.url TLS option")
		}
		cache.TLS = parsed
	} else if parsedURL.Scheme == "rediss" || parsedURL.Scheme == "valkeys" {
		cache.TLS = true
	}
	if value := query.Get("tls_skip_verify"); value != "" {
		parsed, err := ParseBool(value, false)
		if err != nil {
			return errors.New("invalid server.cache.url TLS verification option")
		}
		cache.TLSSkipVerify = parsed
	}
	if cache.TLSCA != "" {
		cache.TLS = true
		cache.TLSSkipVerify = false
	} else if cache.TLS && query.Get("tls_skip_verify") == "" {
		cache.TLSSkipVerify = true
	}
	return nil
}

func RedactConnectionURL(value string) string {
	value = strings.TrimSpace(value)
	if schemeEnd := strings.Index(value, "://"); schemeEnd >= 0 {
		authorityStart := schemeEnd + 3
		authorityEnd := len(value)
		if end := strings.IndexAny(value[authorityStart:], "/?#"); end >= 0 {
			authorityEnd = authorityStart + end
		}
		if at := strings.LastIndex(value[authorityStart:authorityEnd], "@"); at >= 0 {
			value = value[:authorityStart] + "********@" + value[authorityStart+at+1:]
		}
	}
	if query := strings.IndexByte(value, '?'); query >= 0 {
		parts := strings.Split(value[query+1:], "&")
		for index, pair := range parts {
			key, _, hasValue := strings.Cut(pair, "=")
			switch strings.ToLower(key) {
			case "password", "pwd", "token", "authtoken":
				if hasValue {
					parts[index] = key + "=********"
				}
			}
		}
		value = value[:query+1] + strings.Join(parts, "&")
	}
	return value
}

func deleteConfigPath(root map[string]any, path []string) {
	if len(path) == 0 {
		return
	}
	parent := root
	for _, key := range path[:len(path)-1] {
		next, ok := parent[key].(map[string]any)
		if !ok {
			return
		}
		parent = next
	}
	delete(parent, path[len(path)-1])
}
