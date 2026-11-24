package cache

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/redis/go-redis/v9"
)

// Counter provides an atomic, expiring counter for request limits.
type Counter interface {
	Increment(context.Context, string, time.Duration) (int64, error)
	Stats() Stats
	Close() error
}

type Stats struct {
	Items     int
	Bytes     int
	Evictions uint64
}

type memoryCounter struct {
	mu         sync.Mutex
	values     map[string]memoryValue
	defaultTTL time.Duration
	evictions  uint64
}

const maxMemoryCounters = 4096

type memoryValue struct {
	count   int64
	expires time.Time
}

// New creates the configured cache counter. Memory is the default backend.
func New(settings config.CacheConfig, configDir string) (Counter, error) {
	defaultTTL, err := time.ParseDuration(settings.TTL)
	if err != nil || defaultTTL <= 0 {
		defaultTTL = time.Hour
	}
	switch strings.ToLower(strings.TrimSpace(settings.Type)) {
	case "", "memory":
		return &memoryCounter{values: make(map[string]memoryValue), defaultTTL: defaultTTL}, nil
	case "none":
		return nil, nil
	case "redis", "valkey":
		return newRedisCounter(settings, configDir, defaultTTL)
	default:
		return nil, errors.New("unsupported cache type")
	}
}

func (counter *memoryCounter) Increment(_ context.Context, key string, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		ttl = counter.defaultTTL
	}
	counter.mu.Lock()
	defer counter.mu.Unlock()
	now := time.Now()
	value, exists := counter.values[key]
	if !exists && len(counter.values) >= maxMemoryCounters {
		for currentKey, current := range counter.values {
			if !current.expires.After(now) {
				delete(counter.values, currentKey)
				counter.evictions++
			}
		}
		if len(counter.values) >= maxMemoryCounters {
			return 0, errors.New("in-memory cache counter capacity reached")
		}
	}
	if !value.expires.After(now) {
		value = memoryValue{expires: now.Add(ttl)}
	}
	value.count++
	counter.values[key] = value
	return value.count, nil
}

func (counter *memoryCounter) Close() error { return nil }

func (counter *memoryCounter) Stats() Stats {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	now := time.Now()
	stats := Stats{Evictions: counter.evictions}
	for key, value := range counter.values {
		if !value.expires.After(now) {
			delete(counter.values, key)
			counter.evictions++
			continue
		}
		stats.Items++
		stats.Bytes += len(key) + 8
	}
	stats.Evictions = counter.evictions
	return stats
}

type redisCounter struct {
	client     *redis.Client
	prefix     string
	defaultTTL time.Duration
	mu         sync.Mutex
	tracked    map[string]time.Time
	evictions  uint64
}

func newRedisCounter(settings config.CacheConfig, configDir string, defaultTTL time.Duration) (Counter, error) {
	parsed := &url.URL{}
	if settings.URL != "" {
		value, err := config.ConnectionURLForDriver(settings.URL)
		if err != nil {
			return nil, errors.New("invalid cache URL")
		}
		parsed, err = url.Parse(value)
		if err != nil {
			return nil, errors.New("invalid cache URL")
		}
	}
	options := &redis.Options{
		Addr:         net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port)),
		Username:     settings.Username,
		Password:     settings.Password,
		DB:           settings.DB,
		PoolSize:     settings.PoolSize,
		MinIdleConns: settings.MinIdle,
	}
	if settings.URL != "" {
		options.Addr = parsed.Host
		if parsed.User != nil {
			options.Username = parsed.User.Username()
			options.Password, _ = parsed.User.Password()
		}
		if name := strings.TrimPrefix(parsed.Path, "/"); name != "" {
			if database, err := strconv.Atoi(name); err == nil {
				options.DB = database
			}
		}
		query := parsed.Query()
		if query.Has("password") {
			options.Password = query.Get("password")
		}
		if query.Has("username") {
			options.Username = query.Get("username")
		}
		if query.Has("db") {
			if database, err := strconv.Atoi(query.Get("db")); err == nil {
				options.DB = database
			}
		}
	}
	if options.PoolSize < 1 {
		options.PoolSize = 10
	}
	if options.MinIdleConns > options.PoolSize {
		return nil, errors.New("cache min_idle exceeds pool_size")
	}
	timeout, err := time.ParseDuration(settings.Timeout)
	if err != nil || timeout <= 0 {
		timeout = 5 * time.Second
	}
	options.DialTimeout = timeout
	options.ReadTimeout = timeout
	options.WriteTimeout = timeout
	tlsEnabled := settings.TLS
	switch strings.ToLower(parsed.Scheme) {
	case "rediss", "valkeys":
		tlsEnabled = true
	}
	if value := parsed.Query().Get("tls"); value != "" {
		tlsEnabled = strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "yes")
	}
	if tlsEnabled {
		options.TLSConfig, err = cacheTLSConfig(settings, configDir)
		if err != nil {
			return nil, err
		}
	}
	client := redis.NewClient(options)
	return &redisCounter{client: client, prefix: settings.Prefix, defaultTTL: defaultTTL, tracked: make(map[string]time.Time)}, nil
}

func cacheTLSConfig(settings config.CacheConfig, configDir string) (*tls.Config, error) {
	configuration := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: settings.TLSSkipVerify}
	if settings.TLSCA != "" {
		path, err := configTLSFile(configDir, settings.TLSCA)
		if err != nil {
			return nil, err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read cache TLS CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(contents) {
			return nil, errors.New("cache TLS CA file contains no certificates")
		}
		configuration.RootCAs = pool
	}
	if settings.TLSCert != "" && settings.TLSKey != "" {
		certificatePath, err := configTLSFile(configDir, settings.TLSCert)
		if err != nil {
			return nil, err
		}
		keyPath, err := configTLSFile(configDir, settings.TLSKey)
		if err != nil {
			return nil, err
		}
		certificate, err := tls.LoadX509KeyPair(certificatePath, keyPath)
		if err != nil {
			return nil, errors.New("cache TLS client certificate or key is invalid")
		}
		configuration.Certificates = []tls.Certificate{certificate}
	}
	return configuration, nil
}

func configTLSFile(configDir, name string) (string, error) {
	root, err := filepath.Abs(filepath.Join(configDir, "ssl"))
	if err != nil {
		return "", errors.New("invalid config directory for cache TLS")
	}
	path := name
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", errors.New("invalid cache TLS file path")
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("cache TLS files must be inside config_dir/ssl")
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", errors.New("cache TLS file is unavailable")
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return "", errors.New("invalid cache TLS file path")
	}
	resolvedRelative, err := filepath.Rel(root, resolved)
	if err != nil || resolvedRelative == ".." || strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
		return "", errors.New("cache TLS files must resolve inside config_dir/ssl")
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("cache TLS path must name a regular file")
	}
	return resolved, nil
}

func (counter *redisCounter) Increment(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		ttl = counter.defaultTTL
	}
	prefixed := counter.prefix + key
	count, err := counter.client.Incr(ctx, prefixed).Result()
	if err != nil {
		return 0, err
	}
	if count == 1 {
		if err := counter.client.Expire(ctx, prefixed, ttl).Err(); err != nil {
			return 0, err
		}
	}
	counter.mu.Lock()
	if _, exists := counter.tracked[prefixed]; !exists && len(counter.tracked) >= maxMemoryCounters {
		var oldest string
		var oldestExpiry time.Time
		for trackedKey, expiry := range counter.tracked {
			if oldest == "" || expiry.Before(oldestExpiry) {
				oldest, oldestExpiry = trackedKey, expiry
			}
		}
		if oldest != "" {
			delete(counter.tracked, oldest)
		}
	}
	if count == 1 || counter.tracked[prefixed].IsZero() {
		counter.tracked[prefixed] = time.Now().Add(ttl)
	}
	counter.mu.Unlock()
	return count, nil
}

func (counter *redisCounter) Close() error { return counter.client.Close() }

func (counter *redisCounter) Stats() Stats {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	now := time.Now()
	stats := Stats{Evictions: counter.evictions}
	for key, expiry := range counter.tracked {
		if !expiry.After(now) {
			delete(counter.tracked, key)
			counter.evictions++
			continue
		}
		stats.Items++
		stats.Bytes += len(key) + 8
	}
	stats.Evictions = counter.evictions
	return stats
}
