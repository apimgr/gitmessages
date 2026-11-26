package web

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/apimgr/gitmessages/src/config"
)

type analyticsTracker struct {
	tags string
	body string
}

func trackingTypeName(tracking config.TrackingConfig) string {
	switch tracking.Type {
	case "google":
		return "Google Analytics"
	case "matomo":
		return "Matomo"
	case "piwik":
		return "Piwik"
	case "owa":
		return "Open Web Analytics"
	case "fathom":
		return "Fathom Analytics"
	case "plausible":
		return "Plausible Analytics"
	case "umami":
		return "Umami"
	case "simple":
		return "Simple Analytics"
	case "cloudflare":
		return "Cloudflare Web Analytics"
	default:
		return ""
	}
}

func privacyThirdPartyServices(privacy config.PrivacyConfig, tracking config.TrackingConfig) []config.PrivacyThirdPartyService {
	services := append([]config.PrivacyThirdPartyService(nil), privacy.ThirdParty.Services...)
	name := trackingTypeName(tracking)
	if name == "" {
		return services
	}
	for _, service := range services {
		if strings.EqualFold(strings.TrimSpace(service.Name), name) {
			return services
		}
	}
	return append(services, config.PrivacyThirdPartyService{Name: name, Purpose: "Website analytics after visitor consent", DataSent: "Page views and browser analytics metadata"})
}

func privacySharing(privacy config.PrivacyConfig, tracking config.TrackingConfig) []config.PrivacySharingConfig {
	sharing := append([]config.PrivacySharingConfig(nil), privacy.Data.Sharing...)
	if tracking.Type == "" || tracking.Type == "none" {
		return sharing
	}
	for _, item := range sharing {
		if strings.EqualFold(item.Condition, "analytics") {
			return sharing
		}
	}
	return append(sharing, config.PrivacySharingConfig{Condition: "analytics", When: "Tracking is configured and the visitor has consented", Data: "Page views and browser analytics metadata"})
}

func jsString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func trackerFor(cfg config.TrackingConfig) (analyticsTracker, bool) {
	id := html.EscapeString(cfg.ID)
	base := strings.TrimSuffix(cfg.URL, "/")
	switch cfg.Type {
	case "google":
		if strings.HasPrefix(cfg.ID, "G-") {
			return analyticsTracker{
				tags: `<script async src="https://www.googletagmanager.com/gtag/js?id=` + url.QueryEscape(cfg.ID) + `"></script>`,
				body: "window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments)};gtag('js',new Date());gtag('config'," + jsString(cfg.ID) + ");",
			}, true
		}
		return analyticsTracker{
			tags: `<script async src="https://www.google-analytics.com/analytics.js"></script>`,
			body: "window.ga=window.ga||function(){(ga.q=ga.q||[]).push(arguments)};ga.l=+new Date;ga('create'," + jsString(cfg.ID) + ",'auto');ga('send','pageview');",
		}, true
	case "matomo", "piwik":
		return analyticsTracker{body: "var _paq=window._paq=window._paq||[];_paq.push(['trackPageView']);_paq.push(['enableLinkTracking']);_paq.push(['setTrackerUrl'," + jsString(base+"/matomo.php") + "]);_paq.push(['setSiteId'," + jsString(cfg.ID) + "]);var g=document.createElement('script');g.async=true;g.src=" + jsString(base+"/matomo.js") + ";document.head.appendChild(g);"}, true
	case "owa":
		return analyticsTracker{body: "var owa_baseUrl=" + jsString(base+"/") + ";var owa_cmds=window.owa_cmds=window.owa_cmds||[];owa_cmds.push(['setSiteId'," + jsString(cfg.ID) + "]);owa_cmds.push(['trackPageView']);owa_cmds.push(['trackClicks']);var o=document.createElement('script');o.async=true;o.src=owa_baseUrl+'modules/base/js/owa.tracker-combined-min.js';document.head.appendChild(o);"}, true
	case "fathom":
		src := "https://cdn.usefathom.com/script.js"
		if cfg.URL != "" {
			src = base + "/tracker.js"
		}
		return analyticsTracker{tags: `<script src="` + html.EscapeString(src) + `" data-site="` + id + `" defer></script>`}, true
	case "plausible":
		src := "https://plausible.io/js/script.js"
		if cfg.URL != "" {
			src = base + "/js/script.js"
		}
		return analyticsTracker{tags: `<script defer data-domain="` + id + `" src="` + html.EscapeString(src) + `"></script>`}, true
	case "umami":
		return analyticsTracker{tags: `<script defer src="` + html.EscapeString(base) + `/script.js" data-website-id="` + id + `"></script>`}, true
	case "simple":
		return analyticsTracker{tags: `<script async defer src="https://scripts.simpleanalyticscdn.com/latest.js"></script>`}, true
	case "cloudflare":
		beacon, _ := json.Marshal(map[string]string{"token": cfg.ID})
		return analyticsTracker{tags: `<script defer src="https://static.cloudflareinsights.com/beacon.min.js" data-cf-beacon='` + html.EscapeString(string(beacon)) + `'></script>`}, true
	default:
		return analyticsTracker{}, false
	}
}

func trackingConfigured() bool {
	_, ok := trackerFor(config.Get().Server.Tracking)
	return ok
}

func trackingAllowed(r *http.Request) bool {
	if !trackingConfigured() || !config.Get().Server.Privacy.Cookies.Analytics.Enabled || !hasConsentCookie(r) || !readCookieConsent(r).Analytics {
		return false
	}
	if privacySignalFromRequest(r) != "" {
		return false
	}
	if config.Get().Server.Privacy.Data.Sold {
		if cookie, err := r.Cookie("ccpa_opt_out"); err == nil && cookie.Value == "true" {
			return false
		}
	}
	_, host, _ := GetURLVars(r)
	server := config.Get().Server
	return (server.Tor.OnionAddress == "" || !strings.EqualFold(host, server.Tor.OnionAddress)) && (server.I2P.B32Address == "" || !strings.EqualFold(host, server.I2P.B32Address))
}

func trackingMarkup(r *http.Request) (string, string) {
	if !trackingAllowed(r) {
		return "", ""
	}
	tracker, ok := trackerFor(config.Get().Server.Tracking)
	if !ok {
		return "", ""
	}
	tags := tracker.tags
	if tracker.body != "" {
		tags += `<script async src="/server/tracking.js"></script>`
	}
	noscript := ""
	if config.Get().Server.Tracking.Type == "simple" {
		noscript = `<noscript><img src="https://queue.simpleanalyticscdn.com/noscript.gif" alt="" referrerpolicy="no-referrer-when-downgrade"></noscript>`
	}
	return tags, noscript
}

func trackingScriptPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/tracking.js" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	if !trackingAllowed(r) {
		http.NotFound(w, r)
		return
	}
	tracker, ok := trackerFor(config.Get().Server.Tracking)
	if !ok || tracker.body == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	_, _ = w.Write([]byte(tracker.body))
}

func trackingCSPHosts(r *http.Request) (script, connect, image string) {
	if !trackingAllowed(r) {
		return "", "", ""
	}
	addURL := func(value string) string {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" {
			return ""
		}
		return parsed.Scheme + "://" + parsed.Host
	}
	cfg := config.Get().Server.Tracking
	base := addURL(cfg.URL)
	switch cfg.Type {
	case "google":
		return "https://www.googletagmanager.com https://www.google-analytics.com", "https://www.google-analytics.com", ""
	case "matomo", "piwik", "owa", "umami":
		return base, base, ""
	case "fathom":
		if base == "" {
			base = "https://cdn.usefathom.com"
		}
		return base, base, ""
	case "plausible":
		if base == "" {
			base = "https://plausible.io"
		}
		return base, base, ""
	case "simple":
		return "https://scripts.simpleanalyticscdn.com", "", "https://queue.simpleanalyticscdn.com"
	case "cloudflare":
		return "https://static.cloudflareinsights.com", "", ""
	default:
		return "", "", ""
	}
}
