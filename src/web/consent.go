package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/config"
)

type privacySignalContextKey struct{}

func privacySignal(r *http.Request) string {
	if r.Header.Get("Sec-GPC") == "1" {
		return "gpc"
	}
	if config.Get().Web.Headers.HonorDNT && r.Header.Get("DNT") == "1" {
		return "dnt"
	}
	return ""
}

func privacySignalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signal := privacySignal(r)
		if signal != "" {
			ctx := context.WithValue(r.Context(), privacySignalContextKey{}, signal)
			r = r.WithContext(ctx)
			recordPrivacySignal(r.Context(), signal, r)
		}
		next.ServeHTTP(w, r)
	})
}

func recordPrivacySignal(ctx context.Context, signal string, r *http.Request) {
	writeComplianceEvent(ctx, "compliance.gpc_honored", map[string]any{"signal": signal}, r)
}

func recordConsentUpdated(ctx context.Context, r *http.Request, consentType string, value bool) {
	writeComplianceEvent(ctx, "compliance.consent_updated", map[string]any{
		"consent_type":   consentType,
		"new_value":      value,
		"policy_version": config.Get().Server.Privacy.Consent.Policy.Version,
		"actor":          consentActor(r),
	}, r)
}

func consentActor(r *http.Request) map[string]string {
	if store := currentAuthStore(); store != nil {
		if cookie := ownerCookieFromRequest(r); cookie != nil {
			if identity, err := store.Authenticate(r.Context(), cookie.Value); err == nil && identity.Kind == "primary" {
				return map[string]string{"type": "owner", "id": identity.OwnerID}
			}
		}
	}
	return map[string]string{"type": "anonymous"}
}

func writeComplianceEvent(ctx context.Context, action string, event map[string]any, r *http.Request) {
	details, err := json.Marshal(event)
	if err != nil {
		slog.Error("failed to encode compliance audit event", "action", action, "error", err)
		return
	}
	store := getContactStore()
	if store == nil {
		slog.Info("compliance event", "action", action, "details", string(details))
		return
	}
	actorIP := ""
	if r != nil {
		actorIP = anonymizedAuditIP(r)
	}
	if err := store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log(level,category,action,actor_ip,details,success) VALUES('info','compliance',?,?,?,1)`, action, nullableAuditIP(actorIP), string(details))
		return err
	}); err != nil {
		slog.Error("failed to record compliance event", "action", action, "error", err)
	}
}

func anonymizedAuditIP(r *http.Request) string {
	_, host, _ := GetURLVars(r)
	if onion := config.Get().Server.Tor.OnionAddress; onion != "" && strings.EqualFold(host, onion) {
		return "tor"
	}
	if i2p := config.Get().Server.I2P.B32Address; i2p != "" && strings.EqualFold(host, i2p) {
		return "i2p"
	}
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		remote = r.RemoteAddr
	}
	ip := net.ParseIP(remote)
	if ip == nil {
		return ""
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return net.IPv4(ipv4[0], ipv4[1], ipv4[2], 0).String()
	}
	masked := ip.To16()
	for index := 6; index < len(masked); index++ {
		masked[index] = 0
	}
	return masked.String()
}

func nullableAuditIP(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func privacySignalFromRequest(r *http.Request) string {
	if value, ok := r.Context().Value(privacySignalContextKey{}).(string); ok {
		return value
	}
	return privacySignal(r)
}

func preferenceCategoryAllowed(r *http.Request) bool {
	cookie, err := r.Cookie("cookie_consent")
	if err != nil {
		return false
	}
	value, err := url.PathUnescape(cookie.Value)
	if err != nil {
		return false
	}
	var state cookieConsentState
	return config.Get().Server.Privacy.Cookies.Preferences.Enabled && json.Unmarshal([]byte(value), &state) == nil && state.Essential && state.Preferences && state.Timestamp > 0
}

func hasConsentCookie(r *http.Request) bool {
	cookie, err := r.Cookie("cookie_consent")
	if err != nil {
		return false
	}
	value, err := url.PathUnescape(cookie.Value)
	if err != nil {
		return false
	}
	var state cookieConsentState
	if json.Unmarshal([]byte(value), &state) != nil || !state.Essential || state.Timestamp <= 0 {
		return false
	}
	return true
}

func consentBannerMarkup(r *http.Request, csrf string) string {
	consent := config.Get().Server.Privacy.Consent
	if !consent.ShowUntilAcknowledged || hasConsentCookie(r) || r.URL.Path == "/server/preferences" {
		return ""
	}
	policyURL := validConsentPolicyURL(consent.Policy.URL)
	redirect := r.URL.RequestURI()
	if !validConsentRedirect(redirect) {
		redirect = "/"
	}
	message := config.Get().Server.Privacy.GetConsentMessage()
	if strings.TrimSpace(message) == "" {
		message = config.DefaultConfig().Server.Privacy.GetConsentMessage()
	}
	aria := template.HTMLEscapeString(tr(r, "privacy.cookies_title"))
	policyText := strings.TrimSpace(consent.Policy.Text)
	if policyText == "" {
		policyText = config.DefaultConfig().Server.Privacy.Consent.Policy.Text
	}
	var body strings.Builder
	fmt.Fprintf(&body, `<div id="cookie-consent" class="cookie-banner cookie-banner-bottom" data-sold="%t" role="region" aria-label="%s"><div class="cookie-banner-content"><span class="cookie-message">%s - <a href="%s" class="policy-link">%s</a></span><div class="cookie-buttons">`, config.Get().Server.Privacy.Data.Sold, aria, template.HTMLEscapeString(message), template.HTMLEscapeString(policyURL), template.HTMLEscapeString(policyText))
	for _, choice := range []struct{ name, class, label string }{{"decline", "btn-decline", consent.Buttons.Decline}, {"accept", "btn-accept", consent.Buttons.Accept}} {
		fmt.Fprintf(&body, `<form method="post" action="/server/consent"><input type="hidden" name="csrf_token" value="%s"><input type="hidden" name="choice" value="%s"><input type="hidden" name="redirect" value="%s"><button type="submit" class="%s">%s</button></form>`, template.HTMLEscapeString(csrf), choice.name, template.HTMLEscapeString(redirect), choice.class, template.HTMLEscapeString(choice.label))
	}
	if consent.ShowPreferences {
		fmt.Fprintf(&body, `<a class="cookie-preferences-link" href="/server/preferences">%s</a>`, template.HTMLEscapeString(consent.PreferencesText))
	}
	body.WriteString(`</div></div></div>`)
	return body.String()
}

func validConsentPolicyURL(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, "\\\r\n") {
		return "/server/privacy"
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "/server/privacy"
	}
	if parsed.IsAbs() && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" && parsed.User == nil {
		return parsed.String()
	}
	if strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(parsed.Path, "//") && parsed.Host == "" {
		return parsed.String()
	}
	return "/server/privacy"
}

func validConsentRedirect(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && !strings.ContainsAny(value, "\r\n") && parsed.Scheme == "" && parsed.Host == "" && parsed.User == nil && strings.HasPrefix(parsed.Path, "/") && !strings.HasPrefix(parsed.Path, "//") && !strings.Contains(parsed.Path, "\\")
}

func consentPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/consent" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodPost) {
		return
	}
	if r.ContentLength > 8192 {
		writeHTTPError(w, r, "Consent request is too large", http.StatusRequestEntityTooLarge)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if !validCSRF(r) {
		writeHTTPError(w, r, "CSRF token validation failed", http.StatusForbidden)
		return
	}
	privacy := config.Get().Server.Privacy
	preferences, analytics := false, false
	switch r.FormValue("choice") {
	case "accept":
		preferences = privacy.Cookies.Preferences.Enabled
		analytics = privacy.Cookies.Analytics.Enabled
	case "decline":
	case "save":
		preferences = r.FormValue("preferences") == "on" && privacy.Cookies.Preferences.Enabled
		analytics = r.FormValue("analytics") == "on" && privacy.Cookies.Analytics.Enabled
	default:
		writeHTTPError(w, r, "Invalid consent choice", http.StatusBadRequest)
		return
	}
	if privacySignalFromRequest(r) != "" {
		preferences, analytics = false, false
	}
	state, _ := json.Marshal(cookieConsentState{Essential: true, Preferences: preferences, Analytics: analytics, Timestamp: time.Now().Unix()})
	if previous := readCookieConsent(r); hasConsentCookie(r) && (previous.Preferences && !preferences || previous.Analytics && !analytics) {
		w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	}
	http.SetCookie(w, &http.Cookie{Name: "cookie_consent", Value: url.PathEscape(string(state)), Path: "/", MaxAge: preferenceCookieAge, SameSite: http.SameSiteLaxMode, Secure: requestIsSecure(r)})
	recordConsentUpdated(r.Context(), r, "preferences", preferences)
	recordConsentUpdated(r.Context(), r, "analytics", analytics)
	w.Header().Set("Cache-Control", "no-store")
	redirect := r.FormValue("redirect")
	if !validConsentRedirect(redirect) {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}
