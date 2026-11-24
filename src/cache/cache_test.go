package cache

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

func TestMemoryCounterExpiresAndIncrements(t *testing.T) {
	counter, err := New(config.CacheConfig{Type: "memory"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := counter.Close(); err != nil {
			t.Errorf("close cache counter: %v", err)
		}
	})
	ctx := context.Background()
	first, err := counter.Increment(ctx, "client", time.Minute)
	if err != nil || first != 1 {
		t.Fatalf("first increment = %d, %v", first, err)
	}
	second, err := counter.Increment(ctx, "client", time.Minute)
	if err != nil || second != 2 {
		t.Fatalf("second increment = %d, %v", second, err)
	}
	stats := counter.Stats()
	if stats.Items != 1 || stats.Bytes != len("client")+8 || stats.Evictions != 0 {
		t.Fatalf("cache stats after increments = %#v", stats)
	}
	counter.(*memoryCounter).values["client"] = memoryValue{count: second, expires: time.Now().Add(-time.Second)}
	stats = counter.Stats()
	if stats.Items != 0 || stats.Evictions != 1 {
		t.Fatalf("cache stats after expiration = %#v", stats)
	}
	third, err := counter.Increment(ctx, "client", time.Minute)
	if err != nil || third != 1 {
		t.Fatalf("increment after expiry = %d, %v", third, err)
	}
}

func TestMemoryCounterCapacityAndExpiredEntryCleanup(t *testing.T) {
	counter := &memoryCounter{values: make(map[string]memoryValue), defaultTTL: time.Minute}
	now := time.Now()
	for index := 0; index < maxMemoryCounters; index++ {
		counter.values[fmt.Sprintf("key-%d", index)] = memoryValue{count: 1, expires: now.Add(time.Hour)}
	}
	if _, err := counter.Increment(context.Background(), "overflow", time.Minute); err == nil {
		t.Fatal("counter accepted an entry beyond its capacity")
	}
	for key := range counter.values {
		counter.values[key] = memoryValue{count: 1, expires: now.Add(-time.Second)}
	}
	if count, err := counter.Increment(context.Background(), "after-cleanup", time.Minute); err != nil || count != 1 {
		t.Fatalf("increment after expired-entry cleanup = %d, %v", count, err)
	}
	if stats := counter.Stats(); stats.Evictions != maxMemoryCounters {
		t.Fatalf("eviction count after capacity cleanup = %#v", stats)
	}
}

func TestCounterDefaultTTLAndExplicitTTL(t *testing.T) {
	counter, err := New(config.CacheConfig{Type: "memory", TTL: "1h"}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	memory := counter.(*memoryCounter)
	started := time.Now()
	if _, err := counter.Increment(context.Background(), "default", 0); err != nil {
		t.Fatal(err)
	}
	defaultLifetime := memory.values["default"].expires.Sub(started)
	if defaultLifetime < 59*time.Minute || defaultLifetime > time.Hour+time.Second {
		t.Fatalf("default TTL lifetime = %s, want about 1h", defaultLifetime)
	}
	started = time.Now()
	if _, err := counter.Increment(context.Background(), "explicit", time.Minute); err != nil {
		t.Fatal(err)
	}
	explicitLifetime := memory.values["explicit"].expires.Sub(started)
	if explicitLifetime < time.Minute-time.Second || explicitLifetime > time.Minute+time.Second {
		t.Fatalf("explicit TTL lifetime = %s, want about 1m", explicitLifetime)
	}
}

func TestCacheTLSPathsStayInsideConfigSSLDirectory(t *testing.T) {
	configDir := t.TempDir()
	sslDirectory := filepath.Join(configDir, "ssl")
	if err := os.MkdirAll(sslDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sslDirectory, "ca.pem"), []byte("certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := configTLSFile(configDir, "ca.pem"); err != nil {
		t.Fatalf("relative TLS path rejected: %v", err)
	}
	if _, err := configTLSFile(configDir, "../secrets.pem"); err == nil {
		t.Fatal("TLS path traversal was accepted")
	}
}

func TestCacheTLSDefaultsToRequiredUnverifiedMode(t *testing.T) {
	configuration, err := cacheTLSConfig(config.CacheConfig{TLS: true, TLSSkipVerify: true}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if configuration == nil || !configuration.InsecureSkipVerify {
		t.Fatal("TLS require mode should skip certificate verification")
	}
}

func TestRedisCounterParsesURLAndBuildsTLSOptions(t *testing.T) {
	settings := config.CacheConfig{
		URL:           "rediss://url-user:url-pass@127.0.0.1:6380/3?username=query-user&password=query-pass&db=4",
		PoolSize:      4,
		MinIdle:       2,
		Timeout:       "3s",
		Prefix:        "test:",
		TLSSkipVerify: true,
	}
	counter, err := newRedisCounter(settings, t.TempDir(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	redisCache := counter.(*redisCounter)
	t.Cleanup(func() { _ = redisCache.Close() })
	options := redisCache.client.Options()
	if options.Addr != "127.0.0.1:6380" || options.Username != "query-user" || options.Password != "query-pass" || options.DB != 4 {
		t.Fatalf("parsed Redis URL options = %+v", options)
	}
	if options.PoolSize != 4 || options.MinIdleConns != 2 || options.DialTimeout != 3*time.Second || options.TLSConfig == nil || !options.TLSConfig.InsecureSkipVerify {
		t.Fatalf("configured Redis options = %+v", options)
	}
	if redisCache.prefix != "test:" || redisCache.defaultTTL != time.Hour {
		t.Fatalf("Redis counter prefix/TTL = %q/%s", redisCache.prefix, redisCache.defaultTTL)
	}
}

func TestRedisCounterUsesFieldsAndPrivateTLSFiles(t *testing.T) {
	configDir := t.TempDir()
	sslDirectory := filepath.Join(configDir, "ssl")
	if err := os.MkdirAll(sslDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	certificate, key := makeTestCertificate(t)
	if err := os.WriteFile(filepath.Join(sslDirectory, "ca.pem"), certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sslDirectory, "client.pem"), certificate, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sslDirectory, "client-key.pem"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	settings := config.CacheConfig{
		Host: "cache.example.test", Port: 6381, Username: "field-user", Password: "field-pass", DB: 2,
		TLS: true, TLSSkipVerify: false, TLSCA: "ca.pem", TLSCert: "client.pem", TLSKey: "client-key.pem",
	}
	counter, err := newRedisCounter(settings, configDir, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	redisCache := counter.(*redisCounter)
	t.Cleanup(func() { _ = redisCache.Close() })
	options := redisCache.client.Options()
	if options.Addr != "cache.example.test:6381" || options.Username != "field-user" || options.Password != "field-pass" || options.DB != 2 {
		t.Fatalf("field-based Redis options = %+v", options)
	}
	if options.PoolSize != 10 || options.MinIdleConns != 0 || options.DialTimeout != 5*time.Second || options.TLSConfig == nil || options.TLSConfig.InsecureSkipVerify || len(options.TLSConfig.Certificates) != 1 || options.TLSConfig.RootCAs == nil {
		t.Fatalf("defaulted Redis/TLS options = %+v", options)
	}
}

func TestRedisCounterRejectsInvalidConfiguration(t *testing.T) {
	if _, err := newRedisCounter(config.CacheConfig{URL: "redis://[::1"}, t.TempDir(), time.Minute); err == nil {
		t.Fatal("invalid Redis URL was accepted")
	}
	if _, err := newRedisCounter(config.CacheConfig{PoolSize: 2, MinIdle: 3}, t.TempDir(), time.Minute); err == nil {
		t.Fatal("min_idle above pool_size was accepted")
	}
	if _, err := newRedisCounter(config.CacheConfig{TLS: true, TLSCA: "missing.pem"}, t.TempDir(), time.Minute); err == nil {
		t.Fatal("missing TLS CA file was accepted")
	}
	if _, err := cacheTLSConfig(config.CacheConfig{TLSCA: "bad.pem"}, t.TempDir()); err == nil {
		t.Fatal("invalid CA certificate was accepted")
	}
}

func makeTestCertificate(t *testing.T) ([]byte, []byte) {
	t.Helper()
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "cache-test"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment | x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	keyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes})
}

func TestNoneCache(t *testing.T) {
	counter, err := New(config.CacheConfig{Type: "none"}, t.TempDir())
	if err != nil || counter != nil {
		t.Fatalf("New(none) = %v, %v", counter, err)
	}
}
