package ssl

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// generateSelfSignedPEM builds a throwaway self-signed certificate/key pair
// in PEM form for exercising the manual-cert loading paths without touching
// any real filesystem locations.
func generateSelfSignedPEM(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate test key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("failed to create test certificate: %v", err)
	}

	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

// TestParseChallenge is table-driven over every recognized alias plus the
// default fallback, with whitespace/case normalization.
func TestParseChallenge(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"http-01 exact", "http-01", "http-01"},
		{"http01", "http01", "http-01"},
		{"http", "http", "http-01"},
		{"HTTP uppercase", "HTTP", "http-01"},
		{"padded whitespace", "  http  ", "http-01"},
		{"tls-alpn-01 exact", "tls-alpn-01", "tls-alpn-01"},
		{"tlsalpn01", "tlsalpn01", "tls-alpn-01"},
		{"tls-alpn", "tls-alpn", "tls-alpn-01"},
		{"tls", "tls", "tls-alpn-01"},
		{"dns-01 exact", "dns-01", "dns-01"},
		{"dns01", "dns01", "dns-01"},
		{"dns", "dns", "dns-01"},
		{"unknown defaults to http-01", "bogus", "http-01"},
		{"empty defaults to http-01", "", "http-01"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ParseChallenge(tt.in); got != tt.want {
				t.Errorf("ParseChallenge(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestChallengeServer covers SetToken/ClearToken/ServeHTTP: a non-matching
// path, a known token, and an unknown token.
func TestChallengeServer(t *testing.T) {
	cs := NewChallengeServer()

	t.Run("non-matching path returns false untouched", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/some/other/path", nil)
		rec := httptest.NewRecorder()
		handled := cs.ServeHTTP(rec, req)
		if handled {
			t.Error("ServeHTTP() = true for a non-challenge path, want false")
		}
		if rec.Body.Len() != 0 {
			t.Error("no response body should be written for a non-matching path")
		}
	})

	t.Run("known token returns auth text", func(t *testing.T) {
		cs.SetToken("tok1", "auth-value-1")
		req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/tok1", nil)
		rec := httptest.NewRecorder()
		handled := cs.ServeHTTP(rec, req)
		if !handled {
			t.Fatal("ServeHTTP() = false for a matching path, want true")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", rec.Code)
		}
		if rec.Body.String() != "auth-value-1" {
			t.Errorf("body = %q, want auth-value-1", rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
			t.Errorf("Content-Type = %q, want text/plain", ct)
		}
	})

	t.Run("unknown token returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/unknown", nil)
		rec := httptest.NewRecorder()
		handled := cs.ServeHTTP(rec, req)
		if !handled {
			t.Fatal("ServeHTTP() = false for a matching path, want true")
		}
		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d, want 404", rec.Code)
		}
	})

	t.Run("cleared token returns 404", func(t *testing.T) {
		cs.SetToken("tok2", "auth-value-2")
		cs.ClearToken("tok2")
		req := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/tok2", nil)
		rec := httptest.NewRecorder()
		cs.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("status after ClearToken = %d, want 404", rec.Code)
		}
	})
}

// TestGetTLSConfig_Disabled verifies a disabled SSL config returns
// (nil, nil) without touching the filesystem.
func TestGetTLSConfig_Disabled(t *testing.T) {
	m := NewManager(Config{Enabled: false})
	tlsCfg, err := m.GetTLSConfig([]string{"example.com"})
	if err != nil {
		t.Fatalf("GetTLSConfig() error: %v", err)
	}
	if tlsCfg != nil {
		t.Error("GetTLSConfig() with Enabled=false should return nil config")
	}
}

// TestGetTLSConfig_NoCertsNoLetsEncrypt verifies the explicit error path
// when SSL is enabled but no certificates and no Let's Encrypt are
// configured.
func TestGetTLSConfig_NoCertsNoLetsEncrypt(t *testing.T) {
	tmp := t.TempDir()
	m := NewManager(Config{Enabled: true, CertPath: tmp})
	_, err := m.GetTLSConfig([]string{"example.com"})
	if err == nil {
		t.Fatal("expected error when no certificates are available, got nil")
	}
}

// TestGetTLSConfig_ManualCertsDotCrtDotKey verifies the <domain>.crt /
// <domain>.key naming convention is picked up by findManualCerts via the
// real GetTLSConfig code path.
func TestGetTLSConfig_ManualCertsDotCrtDotKey(t *testing.T) {
	tmp := t.TempDir()
	domain := "example.com"
	certPEM, keyPEM := generateSelfSignedPEM(t)

	if err := os.WriteFile(filepath.Join(tmp, domain+".crt"), certPEM, 0644); err != nil {
		t.Fatalf("failed to write cert: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tmp, domain+".key"), keyPEM, 0644); err != nil {
		t.Fatalf("failed to write key: %v", err)
	}

	m := NewManager(Config{Enabled: true, CertPath: tmp})
	tlsCfg, err := m.GetTLSConfig([]string{domain})
	if err != nil {
		t.Fatalf("GetTLSConfig() error: %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("GetTLSConfig() returned nil config for a valid manual cert")
	}
	if len(tlsCfg.Certificates) != 1 {
		t.Errorf("expected 1 loaded certificate, got %d", len(tlsCfg.Certificates))
	}
}

// TestGetTLSConfig_ManualCertsFullchain verifies the <domain>/fullchain.pem
// and <domain>/privkey.pem naming convention.
func TestGetTLSConfig_ManualCertsFullchain(t *testing.T) {
	tmp := t.TempDir()
	domain := "example.org"
	domainDir := filepath.Join(tmp, domain)
	if err := os.MkdirAll(domainDir, 0755); err != nil {
		t.Fatalf("failed to create domain dir: %v", err)
	}
	certPEM, keyPEM := generateSelfSignedPEM(t)

	if err := os.WriteFile(filepath.Join(domainDir, "fullchain.pem"), certPEM, 0644); err != nil {
		t.Fatalf("failed to write fullchain: %v", err)
	}
	if err := os.WriteFile(filepath.Join(domainDir, "privkey.pem"), keyPEM, 0644); err != nil {
		t.Fatalf("failed to write privkey: %v", err)
	}

	m := NewManager(Config{Enabled: true, CertPath: tmp})
	tlsCfg, err := m.GetTLSConfig([]string{domain})
	if err != nil {
		t.Fatalf("GetTLSConfig() error: %v", err)
	}
	if tlsCfg == nil {
		t.Fatal("GetTLSConfig() returned nil config for a valid fullchain cert")
	}
}

// TestFindExistingCerts_NoneFound verifies the hardcoded /etc/letsencrypt
// lookup returns empty paths when no certs exist there (the only branch
// safely testable without writing to real system paths).
func TestFindExistingCerts_NoneFound(t *testing.T) {
	m := NewManager(Config{Enabled: true})
	cert, key := m.findExistingCerts([]string{"definitely-not-a-real-domain.invalid"})
	if cert != "" || key != "" {
		t.Errorf("findExistingCerts() = (%q, %q), want empty for a domain with no certs", cert, key)
	}
}

// TestGetHTTPHandler_NoCertManager verifies the fallback handler is
// returned when no autocert manager has been configured.
func TestGetHTTPHandler_NoCertManager(t *testing.T) {
	m := NewManager(Config{Enabled: false})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})

	handler := m.GetHTTPHandler(fallback)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusTeapot {
		t.Errorf("expected fallback handler to run, status = %d, want 418", rec.Code)
	}
}
