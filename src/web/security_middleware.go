package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/securitydata"
)

var submittedURLPattern = regexp.MustCompile(`https?://[^\s<>"']+`)

type securityContextKey struct{}

var securityDataState = struct {
	sync.RWMutex
	manager *securitydata.Manager
	geoIP   *securitydata.GeoIP
}{}

// ConfigureSecurityData installs the downloaded operational datasets used by request middleware.
func ConfigureSecurityData(manager *securitydata.Manager, geoIP *securitydata.GeoIP) {
	securityDataState.Lock()
	securityDataState.manager = manager
	securityDataState.geoIP = geoIP
	securityDataState.Unlock()
}

// ReplaceGeoIP atomically installs refreshed readers after a successful dataset update.
func ReplaceGeoIP(geoIP *securitydata.GeoIP) {
	securityDataState.Lock()
	previous := securityDataState.geoIP
	securityDataState.geoIP = geoIP
	securityDataState.Unlock()
	if previous != nil && previous != geoIP {
		_ = previous.Close()
	}
}

// CloseSecurityData releases the active GeoIP readers after HTTP traffic stops.
func CloseSecurityData() {
	securityDataState.Lock()
	reader := securityDataState.geoIP
	securityDataState.geoIP = nil
	securityDataState.Unlock()
	if reader != nil {
		_ = reader.Close()
	}
}

func allowlistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isAllowlisted(requestClientIP(r), config.Get().Server.Security.Allowlist) {
			r = r.WithContext(context.WithValue(r.Context(), securityContextKey{}, true))
		}
		next.ServeHTTP(w, r)
	})
}

func requestIsAllowlisted(r *http.Request) bool {
	value, _ := r.Context().Value(securityContextKey{}).(bool)
	return value
}

func isAllowlisted(value string, entries []config.AllowlistEntry) bool {
	address, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	address = address.Unmap()
	for _, entry := range entries {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(entry.CIDR))
		if err == nil {
			if prefix.Contains(address) {
				return true
			}
			continue
		}
		candidate, err := netip.ParseAddr(strings.TrimSpace(entry.CIDR))
		if err == nil && address == candidate.Unmap() {
			return true
		}
	}
	return false
}

func blocklistMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestIsAllowlisted(r) {
			next.ServeHTTP(w, r)
			return
		}
		configuration := config.Get().Server
		address, err := netip.ParseAddr(requestClientIP(r))
		if err == nil {
			for _, entry := range configuration.Security.BlockedIPs {
				if blockedPrefixContains(entry.CIDR, address) {
					writeError(w, r, http.StatusForbidden, "FORBIDDEN", tr(r, "errors.forbidden"))
					return
				}
			}
		}
		state := securityDataStateSnapshot()
		if state.manager != nil && configuration.Security.Blocklists.Enabled {
			if err == nil {
				if source, checkErr := state.manager.IsIPBlocked(address, configuredBlocklistSources(configuration.Security.Blocklists.ExtraSources)); checkErr == nil && source != "" {
					writeError(w, r, http.StatusForbidden, "FORBIDDEN", tr(r, "errors.forbidden"))
					return
				}
			}
			origin := r.Header.Get("Origin")
			if origin == "" {
				origin = r.Header.Get("Referer")
			}
			candidates := []string{origin, r.URL.Query().Get("url"), r.URL.Query().Get("email")}
			candidates = append(candidates, submittedDomainTargets(r)...)
			for _, candidate := range candidates {
				if source, checkErr := state.manager.IsDomainBlocked(candidate, configuredBlocklistSources(configuration.Security.Blocklists.ExtraSources)); checkErr == nil && source != "" {
					writeError(w, r, http.StatusForbidden, "FORBIDDEN", tr(r, "errors.forbidden"))
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func submittedDomainTargets(r *http.Request) []string {
	if r.Body == nil || r.ContentLength > 1<<20 {
		return nil
	}
	original := r.Body
	body, err := io.ReadAll(io.LimitReader(original, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		r.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), original))
		return nil
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	values := []string{}
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.Contains(contentType, "application/x-www-form-urlencoded") {
		if parsed, err := url.ParseQuery(string(body)); err == nil {
			for _, entries := range parsed {
				values = append(values, entries...)
			}
		}
	} else if strings.Contains(contentType, "application/json") {
		var input any
		if json.Unmarshal(body, &input) == nil {
			collectStringValues(input, &values)
		}
	}
	targets := make([]string, 0, len(values)*2)
	for _, value := range values {
		if strings.Contains(value, "@") || strings.Contains(value, "://") {
			targets = append(targets, value)
		}
		targets = append(targets, submittedURLPattern.FindAllString(value, -1)...)
	}
	return targets
}

func collectStringValues(value any, values *[]string) {
	switch typed := value.(type) {
	case string:
		*values = append(*values, typed)
	case []any:
		for _, item := range typed {
			collectStringValues(item, values)
		}
	case map[string]any:
		for _, item := range typed {
			collectStringValues(item, values)
		}
	}
}

func blockedPrefixContains(value string, address netip.Addr) bool {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err == nil {
		return prefix.Contains(address)
	}
	blocked, err := netip.ParseAddr(strings.TrimSpace(value))
	return err == nil && blocked.Unmap() == address.Unmap()
}

func geoIPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requestIsAllowlisted(r) {
			next.ServeHTTP(w, r)
			return
		}
		configuration := config.Get().Server.GeoIP
		address, err := netip.ParseAddr(requestClientIP(r))
		if !configuration.Enabled || err != nil || address.IsPrivate() || address.IsLoopback() || address.IsLinkLocalUnicast() || address.IsUnspecified() {
			next.ServeHTTP(w, r)
			return
		}
		record, err := lookupGeoIP(address)
		if err != nil || record.Country.Code == "" {
			next.ServeHTTP(w, r)
			return
		}
		country := strings.ToUpper(record.Country.Code)
		if countryDenied(country, configuration.AllowCountries, configuration.DenyCountries) {
			writeError(w, r, http.StatusForbidden, "FORBIDDEN", tr(r, "errors.forbidden"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func lookupGeoIP(address netip.Addr) (securitydata.GeoIPRecord, error) {
	securityDataState.RLock()
	defer securityDataState.RUnlock()
	if securityDataState.geoIP == nil {
		return securitydata.GeoIPRecord{}, errors.New("GeoIP database unavailable")
	}
	return securityDataState.geoIP.Lookup(address.AsSlice())
}

func countryDenied(country string, allow, deny []string) bool {
	if len(allow) != 0 {
		for _, candidate := range allow {
			if strings.EqualFold(strings.TrimSpace(candidate), country) {
				return false
			}
		}
		return true
	}
	for _, candidate := range deny {
		if strings.EqualFold(strings.TrimSpace(candidate), country) {
			return true
		}
	}
	return false
}

func configuredBlocklistSources(values []config.BlocklistSourceConfig) []securitydata.Source {
	sources := make([]securitydata.Source, 0, len(values))
	for _, source := range values {
		sources = append(sources, securitydata.Source{ID: source.ID, Type: source.Type, URL: source.URL, Enabled: source.Enabled})
	}
	return sources
}

func securityDataStateSnapshot() struct {
	manager *securitydata.Manager
	geoIP   *securitydata.GeoIP
} {
	securityDataState.RLock()
	defer securityDataState.RUnlock()
	return struct {
		manager *securitydata.Manager
		geoIP   *securitydata.GeoIP
	}{manager: securityDataState.manager, geoIP: securityDataState.geoIP}
}
