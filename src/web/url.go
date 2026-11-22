package web

import (
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/config"
	"golang.org/x/net/publicsuffix"
)

var validURLHostLabel = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
var validURLPrefixCharacters = regexp.MustCompile(`^[A-Za-z0-9._~!$&'()*+,;=:@%/-]+$`)

var learnedURLDomains = struct {
	sync.RWMutex
	observations map[string][]time.Time
}{observations: make(map[string][]time.Time)}

// GetURLVars resolves the public origin for a request, accepting proxy headers only from trusted peers.
func GetURLVars(r *http.Request) (proto, fqdn, port string) {
	if r == nil {
		return "http", "localhost", ""
	}
	cfg := config.Get().Server
	peerTrusted := trustedProxyPeer(r, cfg)
	requestHost := strings.TrimSpace(r.Host)

	requestName, _, requestValid := splitHostPort(requestHost)
	if cfg.Tor.OnionAddress != "" && requestValid && strings.EqualFold(requestName, cfg.Tor.OnionAddress) {
		fqdn = cfg.Tor.OnionAddress
		return "http", fqdn, ""
	}
	if cfg.I2P.Enabled && cfg.I2P.B32Address != "" && requestValid && strings.EqualFold(requestName, cfg.I2P.B32Address) {
		fqdn = cfg.I2P.B32Address
		return "http", fqdn, ""
	}

	proto = "http"
	proxySchemeFound := false
	if peerTrusted {
		for _, value := range []string{r.Header.Get("X-Forwarded-Proto"), forwardedScheme(r.Header.Get("X-Forwarded-Ssl")), r.Header.Get("X-Url-Scheme"), forwardedScheme(r.Header.Get("Front-End-Https"))} {
			value = strings.ToLower(strings.TrimSpace(strings.Split(value, ",")[0]))
			if value == "http" || value == "https" {
				proto = value
				proxySchemeFound = true
				break
			}
		}
	}
	if !proxySchemeFound && r.TLS != nil {
		proto = "https"
	}

	configuredDomains := domainList(osDomainList(), cfg.FQDN)
	hostCandidate := ""
	if peerTrusted {
		for _, candidate := range []string{firstHeader(r, "X-Forwarded-Host"), firstHeader(r, "X-Real-Host"), firstHeader(r, "X-Original-Host")} {
			candidateHost, _, ok := splitHostPort(candidate)
			if ok && validHostname(candidateHost) {
				hostCandidate = candidateHost
				recordURLDomain(candidateHost)
				break
			}
		}
	}
	if hostCandidate == "" {
		candidateHost, _, ok := splitHostPort(requestHost)
		if ok && validHostname(candidateHost) && domainAllowed(candidateHost, configuredDomains) {
			hostCandidate = candidateHost
		}
	}
	if hostCandidate == "" && len(configuredDomains) > 0 {
		hostCandidate = configuredDomains[0]
	}
	if hostCandidate == "" {
		hostCandidate = fallbackHostname()
	}
	fqdn = hostCandidate

	if peerTrusted {
		candidate := firstNonEmptyHeader(r, "X-Forwarded-Port", "X-Real-Port")
		if validPortNumber(candidate) {
			port = candidate
		}
	}
	if port == "" {
		_, hostPort, ok := splitHostPort(requestHost)
		if ok {
			port = hostPort
		}
	}
	if port == "" && validPortNumber(cfg.Port) {
		port = cfg.Port
	}
	if port == "80" || port == "443" {
		port = ""
	}
	return proto, fqdn, port
}

// BuildURL joins a request-specific public origin and validated proxy/config path prefix.
func BuildURL(r *http.Request, path string) string {
	proto, host, port := GetURLVars(r)
	if port != "" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	prefix := requestBasePath(r)
	if path != "" && !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return proto + "://" + host + prefix + path
}

func requestBasePath(r *http.Request) string {
	cfg := config.Get().Server
	if r != nil && trustedProxyPeer(r, cfg) {
		for _, name := range []string{"X-Forwarded-Prefix", "X-Forwarded-Path", "X-Script-Name"} {
			if value := validURLPrefix(r.Header.Get(name)); value != "" {
				return value
			}
		}
	}
	return validURLPrefix(cfg.BaseURL)
}

func validURLPrefix(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "/" {
		return ""
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.ContainsAny(value, "?#\\") {
		return ""
	}
	if !validURLPrefixCharacters.MatchString(value) {
		return ""
	}
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.IsAbs() || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	for _, part := range strings.Split(value, "/") {
		if part == "." || part == ".." {
			return ""
		}
	}
	return strings.TrimRight(value, "/")
}

func trustedProxyPeer(r *http.Request, cfg config.ServerConfig) bool {
	if r == nil {
		return false
	}
	address := r.RemoteAddr
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	ip := net.ParseIP(strings.Trim(address, "[]"))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
		return true
	}
	listen := net.ParseIP(strings.Trim(cfg.Address, "[]"))
	if v4 := ip.To4(); v4 != nil && listen != nil && listen.To4() != nil && v4[0] == listen.To4()[0] && v4[1] == listen.To4()[1] && v4[2] == listen.To4()[2] {
		return true
	}
	for _, trusted := range cfg.TrustedProxies.Additional {
		if _, network, err := net.ParseCIDR(trusted); err == nil && network.Contains(ip) {
			return true
		}
		if net.ParseIP(trusted) != nil && net.ParseIP(trusted).Equal(ip) {
			return true
		}
	}
	return false
}

func requestClientIP(r *http.Request) string {
	if r == nil {
		return "unknown"
	}
	peer, ok := parseClientIP(r.RemoteAddr)
	if !ok {
		return "unknown"
	}
	cfg := config.Get().Server
	if !trustedProxyPeer(r, cfg) {
		return peer.String()
	}
	header := strings.TrimSpace(cfg.TrustedProxies.ClientIPHeader)
	if header == "" || strings.EqualFold(header, "auto") {
		header = "auto"
	}
	var hops []string
	if header == "auto" {
		for _, candidate := range []string{"X-Forwarded-For", "Forwarded", "CF-Connecting-IP", "True-Client-IP", "X-Real-IP", "X-Client-IP"} {
			if len(r.Header.Values(candidate)) > 0 {
				header = candidate
				break
			}
		}
	}
	values := r.Header.Values(header)
	if len(values) == 0 {
		return peer.String()
	}
	switch strings.ToLower(header) {
	case "x-forwarded-for":
		for _, value := range values {
			hops = append(hops, strings.Split(value, ",")...)
		}
	case "forwarded":
		for _, value := range values {
			for _, element := range strings.Split(value, ",") {
				for _, parameter := range strings.Split(element, ";") {
					name, forwardedValue, found := strings.Cut(strings.TrimSpace(parameter), "=")
					if found && strings.EqualFold(name, "for") {
						hops = append(hops, strings.Trim(forwardedValue, `"`))
						break
					}
				}
			}
		}
	default:
		hops = values
	}
	if len(hops) > 32 {
		hops = hops[len(hops)-32:]
	}
	current := peer
	for index := len(hops) - 1; index >= 0; index-- {
		hop, valid := parseClientIP(hops[index])
		if !valid {
			return current.String()
		}
		current = hop
		if !trustedClientIP(hop, cfg) {
			return hop.String()
		}
	}
	return current.String()
}

func parseClientIP(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	} else {
		value = strings.Trim(value, `"[]`)
	}
	if zone := strings.LastIndexByte(value, '%'); zone >= 0 {
		value = value[:zone]
	}
	address, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Addr{}, false
	}
	return address.Unmap(), true
}

func trustedClientIP(address netip.Addr, cfg config.ServerConfig) bool {
	if address.IsLoopback() || address.IsPrivate() || address.IsLinkLocalUnicast() {
		return true
	}
	listen, err := netip.ParseAddr(strings.Trim(cfg.Address, "[]"))
	if err == nil && address.Is4() && listen.Unmap().Is4() {
		prefix, _ := netip.ParsePrefix(listen.Unmap().String() + "/24")
		if prefix.Contains(address) {
			return true
		}
	}
	for _, value := range cfg.TrustedProxies.Additional {
		if prefix, err := netip.ParsePrefix(value); err == nil && prefix.Contains(address) {
			return true
		}
		if allowed, err := netip.ParseAddr(value); err == nil && allowed.Unmap() == address {
			return true
		}
	}
	return false
}

func splitHostPort(value string) (host, port string, ok bool) {
	value = strings.TrimSpace(strings.Split(value, ",")[0])
	if value == "" || strings.ContainsAny(value, " /\\\t\r\n") {
		return "", "", false
	}
	if strings.HasPrefix(value, "[") {
		host, port, err := net.SplitHostPort(value)
		return host, port, err == nil
	}
	if strings.Count(value, ":") == 1 {
		host, port, err := net.SplitHostPort(value)
		return host, port, err == nil && validPortNumber(port)
	}
	return strings.Trim(value, "[]"), "", validHostname(strings.Trim(value, "[]"))
}

func validHostname(host string) bool {
	if net.ParseIP(host) != nil {
		return true
	}
	if len(host) == 0 || len(host) > 253 || strings.HasSuffix(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if !validURLHostLabel.MatchString(label) {
			return false
		}
	}
	return true
}

func validPortNumber(value string) bool {
	n, err := strconv.Atoi(value)
	return err == nil && n > 0 && n <= 65535
}

func domainList(configured, fromEnv string) []string {
	values := strings.Split(configured+","+fromEnv, ",")
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(strings.ToLower(value))
		if validHostname(value) {
			out = append(out, value)
		}
	}
	return out
}

func osDomainList() string { return strings.TrimSpace(os.Getenv("DOMAIN")) }

func domainAllowed(host string, domains []string) bool {
	if len(domains) == 0 {
		return true
	}
	for _, domain := range domains {
		if strings.EqualFold(host, domain) {
			return true
		}
	}
	wildcard := wildcardFromDomains(domains)
	return wildcard != "" && strings.HasSuffix(strings.ToLower(host), strings.TrimPrefix(strings.ToLower(wildcard), "*"))
}

func firstHeader(r *http.Request, name string) string {
	return strings.TrimSpace(strings.Split(r.Header.Get(name), ",")[0])
}

func firstNonEmptyHeader(r *http.Request, names ...string) string {
	for _, name := range names {
		if value := firstHeader(r, name); value != "" {
			return value
		}
	}
	return ""
}

func forwardedScheme(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), "on") {
		return "https"
	}
	return ""
}

func requestIsSecure(r *http.Request) bool {
	scheme, _, _ := GetURLVars(r)
	return scheme == "https"
}

// GetBaseDomain returns the configured primary domain or the learned registrable domain.
func GetBaseDomain() string {
	if domains := domainList(osDomainList(), config.Get().Server.FQDN); len(domains) > 0 {
		return baseDomain(domains[0])
	}
	learnedURLDomains.RLock()
	defer learnedURLDomains.RUnlock()
	var selected string
	var samples int
	for host, observations := range learnedURLDomains.observations {
		base := baseDomain(host)
		if len(observations) > samples {
			selected, samples = base, len(observations)
		}
	}
	return selected
}

// GetWildcardDomain returns a wildcard after the configured or observed hosts share a base domain.
func GetWildcardDomain() string {
	if domains := domainList(osDomainList(), config.Get().Server.FQDN); len(domains) > 0 {
		return wildcardFromDomains(domains)
	}
	cfg := config.Get().Server.URLDetection
	minimum := cfg.MinSamples
	if minimum < 1 {
		minimum = 3
	}
	cutoff := time.Now().Add(-urlLearningWindow(cfg.SampleWindow))
	learnedURLDomains.RLock()
	defer learnedURLDomains.RUnlock()
	base := ""
	samples := 0
	hosts := make(map[string]bool)
	for host, observations := range learnedURLDomains.observations {
		for _, observedAt := range observations {
			if observedAt.Before(cutoff) {
				continue
			}
			candidateBase := baseDomain(host)
			if base == "" {
				base = candidateBase
			}
			if candidateBase == base {
				samples++
				hosts[host] = true
			}
		}
	}
	if samples >= minimum && len(hosts) > 1 && base != "" {
		return "*." + base
	}
	return ""
}

func learnedURLHosts() []string {
	learnedURLDomains.RLock()
	defer learnedURLDomains.RUnlock()
	hosts := make([]string, 0, len(learnedURLDomains.observations))
	for host := range learnedURLDomains.observations {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

func recordURLDomain(host string) {
	server := config.Get().Server
	cfg := server.URLDetection
	if !cfg.Learning || osDomainList() != "" || server.FQDN != "" || !validHostname(host) {
		return
	}
	window := urlLearningWindow(cfg.SampleWindow)
	cutoff := time.Now().Add(-window)
	learnedURLDomains.Lock()
	defer learnedURLDomains.Unlock()
	observations := learnedURLDomains.observations[host]
	kept := observations[:0]
	for _, observedAt := range observations {
		if !observedAt.Before(cutoff) {
			kept = append(kept, observedAt)
		}
	}
	learnedURLDomains.observations[host] = append(kept, time.Now())
}

func urlLearningWindow(value string) time.Duration {
	window, err := time.ParseDuration(value)
	if err != nil || window <= 0 {
		return 5 * time.Minute
	}
	return window
}

func baseDomain(host string) string {
	if net.ParseIP(host) != nil {
		return host
	}
	base, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return host
	}
	return base
}

func wildcardFromDomains(domains []string) string {
	if len(domains) < 2 {
		return ""
	}
	base := baseDomain(domains[0])
	for _, domain := range domains[1:] {
		if baseDomain(domain) != base {
			return ""
		}
	}
	return "*." + base
}

func fallbackHostname() string {
	if hostname, err := os.Hostname(); err == nil && validHostname(hostname) {
		return hostname
	}
	if hostname := strings.TrimSpace(os.Getenv("HOSTNAME")); validHostname(hostname) {
		return hostname
	}
	for _, address := range publicInterfaceAddresses() {
		return address
	}
	return "localhost"
}

func publicInterfaceAddresses() []string {
	interfaces, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	for _, address := range interfaces {
		var ip net.IP
		switch value := address.(type) {
		case *net.IPNet:
			ip = value.IP
		case *net.IPAddr:
			ip = value.IP
		}
		if ip != nil && ip.IsGlobalUnicast() && !ip.IsPrivate() && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return []string{ip.String()}
		}
	}
	return nil
}
