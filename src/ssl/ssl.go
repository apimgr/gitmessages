package ssl

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	legacme "github.com/go-acme/lego/v5/acme"
	"github.com/go-acme/lego/v5/certcrypto"
	"github.com/go-acme/lego/v5/certificate"
	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/lego"
	"github.com/go-acme/lego/v5/providers/dns"
	"github.com/go-acme/lego/v5/registration"
	xcryptoacme "golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// Config holds SSL/TLS configuration
type Config struct {
	Enabled     bool
	CertPath    string
	Cert        string
	Key         string
	MinVersion  string
	LetsEncrypt LetsEncryptConfig
}

// LetsEncryptConfig holds Let's Encrypt settings
type LetsEncryptConfig struct {
	Enabled        bool
	Email          string
	Challenge      string // http-01, tls-alpn-01, dns-01
	Staging        bool
	DNSProvider    string
	DNSCredentials map[string]string
}

// Manager handles SSL/TLS certificates
type Manager struct {
	config      Config
	certManager *autocert.Manager
	certDomains []string
	mu          sync.RWMutex
}

var dnsEnvironmentMu sync.Mutex

type legoAccount struct {
	Email        string                   `json:"email"`
	Registration *legacme.ExtendedAccount `json:"registration,omitempty"`
	key          crypto.Signer
}

func (account *legoAccount) GetEmail() string { return account.Email }

func (account *legoAccount) GetRegistration() *legacme.ExtendedAccount {
	return account.Registration
}

func (account *legoAccount) GetPrivateKey() crypto.Signer { return account.key }

// NewManager creates a new SSL manager
func NewManager(cfg Config) *Manager {
	return &Manager{
		config: cfg,
	}
}

// ReloadLetsEncrypt updates ACME settings without changing TLS listener settings.
func (m *Manager) ReloadLetsEncrypt(configuration LetsEncryptConfig, domains ...[]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	configuration.DNSCredentials = cloneCredentials(configuration.DNSCredentials)
	m.config.LetsEncrypt = configuration
	m.certManager = nil
	m.certDomains = nil
	if !configuration.Enabled || !m.config.Enabled || ParseChallenge(configuration.Challenge) != "http-01" || m.config.Cert != "" || m.config.Key != "" || len(domains) == 0 || len(domains[0]) == 0 {
		return nil
	}
	_, err := m.getLetsEncryptTLSConfig(domains[0])
	return err
}

func cloneCredentials(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for name, value := range source {
		cloned[name] = value
	}
	return cloned
}

// GetTLSConfig returns TLS configuration for the server
func (m *Manager) GetTLSConfig(domains []string) (*tls.Config, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.config.Enabled {
		return nil, nil
	}
	if m.config.LetsEncrypt.Enabled && ParseChallenge(m.config.LetsEncrypt.Challenge) == "dns-01" {
		if len(domains) == 0 {
			return nil, fmt.Errorf("DNS-01 certificate requires at least one configured host")
		}
		if err := m.ensureDNSCertificate(domains); err != nil {
			return nil, err
		}
		cert, key := m.appManagedCertPaths(domains[0])
		return m.loadTLSCertificate(cert, key, domains)
	}
	if m.config.Cert != "" || m.config.Key != "" {
		if m.config.Cert == "" || m.config.Key == "" {
			return nil, fmt.Errorf("both certificate and key paths must be configured")
		}
		return m.loadTLSCertificate(m.config.Cert, m.config.Key, domains)
	}

	// Check system-managed and application-managed certificates in spec order.
	if cert, key := m.findExistingCerts(domains); cert != "" && key != "" {
		return m.loadTLSCertificate(cert, key, domains)
	}

	// Use Let's Encrypt if enabled
	if m.config.LetsEncrypt.Enabled {
		return m.getLetsEncryptTLSConfig(domains)
	}

	// Check for manual certificates
	if cert, key := m.findManualCerts(domains); cert != "" && key != "" {
		return m.loadTLSCertificate(cert, key, domains)
	}

	return nil, fmt.Errorf("no certificates available and Let's Encrypt not enabled")
}

func (m *Manager) loadTLSCertificate(certPath, keyPath string, domains []string) (*tls.Config, error) {
	certificate, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load certificate or key: %w", err)
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}
	if now := time.Now(); now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, fmt.Errorf("certificate is not currently valid")
	}
	for _, domain := range domains {
		if strings.TrimSpace(domain) == "" {
			continue
		}
		if err := leaf.VerifyHostname(domain); err != nil {
			return nil, fmt.Errorf("certificate does not cover configured host %q", domain)
		}
	}
	minimumVersion := uint16(tls.VersionTLS12)
	if strings.EqualFold(strings.TrimSpace(m.config.MinVersion), "TLS1.3") {
		minimumVersion = tls.VersionTLS13
	} else if value := strings.TrimSpace(m.config.MinVersion); value != "" && !strings.EqualFold(value, "TLS1.2") {
		return nil, fmt.Errorf("unsupported minimum TLS version %q", value)
	}
	log.Printf("Using TLS certificate: %s", certPath)
	return &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: minimumVersion}, nil
}

// getLetsEncryptTLSConfig configures autocert for Let's Encrypt
func (m *Manager) getLetsEncryptTLSConfig(domains []string) (*tls.Config, error) {
	if m.certManager == nil || !sameDomains(m.certDomains, domains) {
		cacheDir := filepath.Join(m.config.CertPath, "autocert")
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return nil, fmt.Errorf("failed to create cert cache dir: %w", err)
		}
		m.certManager = &autocert.Manager{
			Prompt:      autocert.AcceptTOS,
			HostPolicy:  autocert.HostWhitelist(domains...),
			Cache:       autocert.DirCache(cacheDir),
			Email:       m.config.LetsEncrypt.Email,
			RenewBefore: 7 * 24 * time.Hour,
		}
		if m.config.LetsEncrypt.Staging {
			m.certManager.Client = &xcryptoacme.Client{DirectoryURL: "https://acme-staging-v02.api.letsencrypt.org/directory"}
		}
		m.certDomains = append(m.certDomains[:0], domains...)
	}

	tlsConfig := m.certManager.TLSConfig().Clone()
	if ParseChallenge(m.config.LetsEncrypt.Challenge) == "http-01" {
		filtered := tlsConfig.NextProtos[:0]
		for _, protocol := range tlsConfig.NextProtos {
			if protocol != xcryptoacme.ALPNProto {
				filtered = append(filtered, protocol)
			}
		}
		tlsConfig.NextProtos = filtered
	}
	tlsConfig.MinVersion = tls.VersionTLS12
	if strings.EqualFold(strings.TrimSpace(m.config.MinVersion), "TLS1.3") {
		tlsConfig.MinVersion = tls.VersionTLS13
	}
	return tlsConfig, nil
}

func sameDomains(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// GetHTTPHandler returns HTTP handler for ACME challenges
func (m *Manager) GetHTTPHandler(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		manager, challengeType := m.certManager, m.config.LetsEncrypt.Challenge
		m.mu.RUnlock()
		if manager != nil && ParseChallenge(challengeType) == "http-01" {
			manager.HTTPHandler(fallback).ServeHTTP(w, r)
			return
		}
		fallback.ServeHTTP(w, r)
	})
}

// ValidateDNSProvider constructs the configured provider without creating DNS records.
func (m *Manager) ValidateDNSProvider() error {
	_, err := m.newDNSProvider()
	return err
}

// RenewDNSCertificate performs the daily certificate renewal check for DNS-01 certificates.
func (m *Manager) RenewDNSCertificate(domains []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureDNSCertificate(domains)
}

// RenewCertificate checks the configured certificate source and asks autocert
// to refresh HTTP-01 or TLS-ALPN-01 certificates when they enter its renewal window.
func (m *Manager) RenewCertificate(domains []string) error {
	if len(domains) == 0 {
		return fmt.Errorf("certificate renewal requires at least one configured host")
	}
	m.mu.RLock()
	letsencryptEnabled := m.config.LetsEncrypt.Enabled
	challengeType := m.config.LetsEncrypt.Challenge
	m.mu.RUnlock()
	if !letsencryptEnabled {
		return nil
	}
	if ParseChallenge(challengeType) == "dns-01" {
		return m.RenewDNSCertificate(domains)
	}
	if _, err := m.GetTLSConfig(domains); err != nil {
		return err
	}
	m.mu.RLock()
	manager := m.certManager
	m.mu.RUnlock()
	if manager == nil {
		return nil
	}
	for _, domain := range domains {
		if _, err := manager.GetCertificate(&tls.ClientHelloInfo{ServerName: domain}); err != nil {
			return fmt.Errorf("renew certificate for %s: %w", domain, err)
		}
	}
	return nil
}

func (m *Manager) ensureDNSCertificate(domains []string) error {
	if len(domains) == 0 {
		return fmt.Errorf("DNS-01 certificate requires at least one configured host")
	}
	certPath, keyPath := m.appManagedCertPaths(domains[0])
	if certificateNeedsRenewal(certPath, 7*24*time.Hour) {
		if err := m.issueDNSCertificate(domains, certPath, keyPath); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) appManagedCertPaths(domain string) (string, string) {
	directory := filepath.Join(m.config.CertPath, "letsencrypt", domain)
	return filepath.Join(directory, "fullchain.pem"), filepath.Join(directory, "privkey.pem")
}

func certificateNeedsRenewal(path string, renewBefore time.Duration) bool {
	contents, err := os.ReadFile(path)
	if err != nil {
		return true
	}
	block, _ := pem.Decode(contents)
	if block == nil {
		return true
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	return err != nil || !time.Now().Add(renewBefore).Before(certificate.NotAfter)
}

func (m *Manager) newDNSProvider() (challenge.Provider, error) {
	providerName := strings.ToLower(strings.TrimSpace(m.config.LetsEncrypt.DNSProvider))
	if providerName == "" {
		return nil, fmt.Errorf("DNS-01 requires server.ssl.letsencrypt.dns_provider")
	}
	if providerName == "dnsupdate" {
		providerName = "rfc2136"
	}
	credentials := make(map[string]string, len(m.config.LetsEncrypt.DNSCredentials)+1)
	for name, value := range m.config.LetsEncrypt.DNSCredentials {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "=\x00") {
			return nil, fmt.Errorf("DNS-01 credential has an invalid environment variable name")
		}
		credentials[name] = value
	}
	if providerName == "rfc2136" || providerName == "dnsupdate" {
		for name, value := range credentials {
			if strings.HasPrefix(name, "RFC2136_") {
				credentials["DNSUPDATE_"+strings.TrimPrefix(name, "RFC2136_")] = value
			}
		}
	}
	dnsEnvironmentMu.Lock()
	defer dnsEnvironmentMu.Unlock()
	type priorValue struct {
		value string
		set   bool
	}
	prior := make(map[string]priorValue, len(credentials))
	names := make([]string, 0, len(credentials))
	for name := range credentials {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		value, exists := os.LookupEnv(name)
		prior[name] = priorValue{value: value, set: exists}
		if err := os.Setenv(name, credentials[name]); err != nil {
			for restoreName, old := range prior {
				restoreEnvironment(restoreName, old.value, old.set)
			}
			return nil, fmt.Errorf("could not prepare DNS provider credential %s", name)
		}
	}
	defer func() {
		for _, name := range names {
			restoreEnvironment(name, prior[name].value, prior[name].set)
		}
	}()
	provider, err := dns.NewDNSChallengeProviderByName(providerName)
	if err != nil {
		return nil, fmt.Errorf("initialize DNS provider %q: %s", providerName, redactCredentialValues(err.Error(), credentials))
	}
	return provider, nil
}

func restoreEnvironment(name, value string, wasSet bool) {
	if wasSet {
		_ = os.Setenv(name, value)
		return
	}
	_ = os.Unsetenv(name)
}

func redactCredentialValues(message string, credentials map[string]string) string {
	for _, value := range credentials {
		if value != "" {
			message = strings.ReplaceAll(message, value, "[redacted]")
		}
	}
	return message
}

func (m *Manager) issueDNSCertificate(domains []string, certPath, keyPath string) error {
	provider, err := m.newDNSProvider()
	if err != nil {
		return err
	}
	account, err := m.loadLegoAccount()
	if err != nil {
		return err
	}
	legoConfig := lego.NewConfig(account)
	if m.config.LetsEncrypt.Staging {
		legoConfig.CADirURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
	}
	client, err := lego.NewClient(legoConfig)
	if err != nil {
		return fmt.Errorf("create ACME client: %w", err)
	}
	if err := client.Challenge.SetDNS01Provider(provider); err != nil {
		return fmt.Errorf("configure DNS-01 challenge provider: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if account.Registration == nil {
		registrationResource, resolveErr := client.Registration.ResolveAccountByKey(ctx)
		if resolveErr != nil {
			registrationResource, err = client.Registration.Register(ctx, registration.RegisterOptions{TermsOfServiceAgreed: true})
			if err != nil {
				return fmt.Errorf("register ACME account: %w", err)
			}
		}
		account.Registration = registrationResource
		if err := m.saveLegoAccount(account); err != nil {
			return err
		}
	}
	resource, err := client.Certificate.Obtain(ctx, certificate.ObtainRequest{Domains: domains, KeyType: certcrypto.EC256, Bundle: true})
	if err != nil {
		return fmt.Errorf("obtain DNS-01 certificate: %s", redactCredentialValues(err.Error(), m.config.LetsEncrypt.DNSCredentials))
	}
	if err := writePrivateAtomic(certPath, resource.Certificate); err != nil {
		return fmt.Errorf("store app-managed certificate: %w", err)
	}
	if err := writePrivateAtomic(keyPath, resource.PrivateKey); err != nil {
		return fmt.Errorf("store app-managed private key: %w", err)
	}
	return nil
}

func (m *Manager) loadLegoAccount() (*legoAccount, error) {
	accountPath := m.legoAccountPath()
	if data, err := os.ReadFile(accountPath); err == nil {
		var stored struct {
			Registration *legacme.ExtendedAccount `json:"registration"`
		}
		if err := json.Unmarshal(data, &stored); err != nil {
			return nil, fmt.Errorf("read ACME account state: %w", err)
		}
		keyPEM, err := os.ReadFile(accountPath + ".key")
		if err != nil {
			return nil, fmt.Errorf("read ACME account key: %w", err)
		}
		block, _ := pem.Decode(keyPEM)
		if block == nil {
			return nil, fmt.Errorf("ACME account key is invalid")
		}
		key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("parse ACME account key: %w", err)
		}
		signer, ok := key.(crypto.Signer)
		if !ok {
			return nil, fmt.Errorf("ACME account key has an unsupported type")
		}
		return &legoAccount{Email: m.config.LetsEncrypt.Email, Registration: stored.Registration, key: signer}, nil
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read ACME account state: %w", err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ACME account key: %w", err)
	}
	account := &legoAccount{Email: m.config.LetsEncrypt.Email, key: key}
	if err := m.saveLegoAccount(account); err != nil {
		return nil, err
	}
	return account, nil
}

func (m *Manager) saveLegoAccount(account *legoAccount) error {
	accountPath := m.legoAccountPath()
	if err := os.MkdirAll(filepath.Dir(accountPath), 0o700); err != nil {
		return fmt.Errorf("create ACME account directory: %w", err)
	}
	keyPEM, err := x509.MarshalPKCS8PrivateKey(account.key)
	if err != nil {
		return fmt.Errorf("encode ACME account key: %w", err)
	}
	if err := writePrivateAtomic(accountPath+".key", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyPEM})); err != nil {
		return fmt.Errorf("store ACME account key: %w", err)
	}
	state, err := json.Marshal(struct {
		Registration *legacme.ExtendedAccount `json:"registration"`
	}{Registration: account.Registration})
	if err != nil {
		return fmt.Errorf("encode ACME account state: %w", err)
	}
	if err := writePrivateAtomic(accountPath, state); err != nil {
		return fmt.Errorf("store ACME account state: %w", err)
	}
	return nil
}

func (m *Manager) legoAccountPath() string {
	endpoint := "production"
	if m.config.LetsEncrypt.Staging {
		endpoint = "staging"
	}
	return filepath.Join(m.config.CertPath, "letsencrypt", "accounts", endpoint, "account.json")
}

func writePrivateAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".ssl-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer func() {
		if err := os.Remove(temporaryPath); err != nil && !os.IsNotExist(err) {
			log.Printf("remove temporary SSL file: %v", err)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

// findExistingCerts looks for existing certificates (e.g., from certbot)
func (m *Manager) findExistingCerts(domains []string) (certPath, keyPath string) {
	// The shared certbot directory named "domain" is checked before host-specific paths.
	for _, domain := range []string{"domain"} {
		cert := filepath.Join("/etc/letsencrypt/live", domain, "fullchain.pem")
		key := filepath.Join("/etc/letsencrypt/live", domain, "privkey.pem")
		if fileExists(cert) && fileExists(key) {
			return cert, key
		}
	}
	for _, domain := range domains {
		cert := filepath.Join("/etc/letsencrypt/live", domain, "fullchain.pem")
		key := filepath.Join("/etc/letsencrypt/live", domain, "privkey.pem")
		if fileExists(cert) && fileExists(key) {
			return cert, key
		}
	}
	if m.config.CertPath != "" {
		for _, directory := range []string{"letsencrypt", "local"} {
			for _, domain := range domains {
				cert := filepath.Join(m.config.CertPath, directory, domain, "fullchain.pem")
				key := filepath.Join(m.config.CertPath, directory, domain, "privkey.pem")
				if directory == "local" {
					cert = filepath.Join(m.config.CertPath, directory, domain, "cert.pem")
					key = filepath.Join(m.config.CertPath, directory, domain, "key.pem")
				}
				if fileExists(cert) && fileExists(key) {
					return cert, key
				}
			}
		}
	}
	return "", ""
}

// findManualCerts looks for manually placed certificates
func (m *Manager) findManualCerts(domains []string) (certPath, keyPath string) {
	// Check legacy cert names under the SSL directory after the documented directories.
	if m.config.CertPath != "" {
		for _, domain := range domains {
			cert := filepath.Join(m.config.CertPath, domain+".crt")
			key := filepath.Join(m.config.CertPath, domain+".key")
			if fileExists(cert) && fileExists(key) {
				return cert, key
			}

			// Also try fullchain format
			cert = filepath.Join(m.config.CertPath, domain, "fullchain.pem")
			key = filepath.Join(m.config.CertPath, domain, "privkey.pem")
			if fileExists(cert) && fileExists(key) {
				return cert, key
			}
		}
	}
	return "", ""
}

// ChallengeServer handles ACME HTTP-01 challenges
type ChallengeServer struct {
	tokens map[string]string
	mu     sync.RWMutex
}

// NewChallengeServer creates a challenge server
func NewChallengeServer() *ChallengeServer {
	return &ChallengeServer{
		tokens: make(map[string]string),
	}
}

// SetToken sets a challenge token
func (cs *ChallengeServer) SetToken(token, auth string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	cs.tokens[token] = auth
}

// ClearToken removes a challenge token
func (cs *ChallengeServer) ClearToken(token string) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	delete(cs.tokens, token)
}

// ServeHTTP handles ACME challenge requests
func (cs *ChallengeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) bool {
	if !strings.HasPrefix(r.URL.Path, "/.well-known/acme-challenge/") {
		return false
	}

	token := strings.TrimPrefix(r.URL.Path, "/.well-known/acme-challenge/")
	cs.mu.RLock()
	auth, ok := cs.tokens[token]
	cs.mu.RUnlock()

	if !ok {
		http.NotFound(w, r)
		return true
	}

	w.Header().Set("Content-Type", "text/plain")
	_, _ = w.Write([]byte(auth))
	return true
}

// ParseChallenge parses challenge type from string
func ParseChallenge(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "http-01", "http01", "http":
		return "http-01"
	case "tls-alpn-01", "tlsalpn01", "tls-alpn", "tls":
		return "tls-alpn-01"
	case "dns-01", "dns01", "dns":
		return "dns-01"
	default:
		return "http-01"
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
