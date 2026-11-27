package web

import (
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/apimgr/gitmessages/src/common/i18n"
)

const preferenceCookieAge = 31536000

var preferenceControls = map[string]bool{"csrf_token": true, "code": true, "redirect": true, "action": true, "consent_form": true, "consent_preferences": true, "consent_analytics": true, "ccpa_opt_out": true}

func registerPreferenceSyncRoutes(mux routeRegistrar) {
	mux.HandleFunc("/server/preferences/sync", preferenceSyncPage)
	mux.HandleFunc("/server/preferences/sync/import", preferenceSyncImport)
}

func preferenceState(r *http.Request) (string, string, url.Values) {
	theme := selectedTheme(r)
	lang := languageCode(r)
	if lang == "" || !i18n.IsSupported(lang) {
		lang = "en"
	}
	values := url.Values{"theme": {theme}, "lang": {lang}}
	return theme, lang, values
}

func canonicalPreferences(r *http.Request) (string, string, string) {
	_, _, values := preferenceState(r)
	query := values.Encode()
	code := base64.RawURLEncoding.EncodeToString([]byte(query))
	return query, code, BuildURL(r, "/server/preferences/sync/import") + "?" + query
}

func preferenceSyncPage(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !validCSRF(r) {
			writeHTTPError(w, r, "CSRF token validation failed", http.StatusForbidden)
			return
		}
		if err := r.ParseForm(); err != nil {
			w.Header().Set("Cache-Control", "no-store")
			http.Redirect(w, r, "/server/preferences/sync", http.StatusSeeOther)
			return
		}
		_, _, consentRequired := applyPreferenceValues(w, r, mergePreferenceValues(r.Form, r.Form.Get("code")))
		w.Header().Set("Cache-Control", "no-store")
		redirect := preferenceRedirect(r, "/server/preferences/sync")
		if consentRequired {
			redirect = addQueryParam(redirect, "consent_required", "1")
		}
		http.Redirect(w, r, redirect, http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		writeHTTPError(w, r, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, _, code, exportURL := preferenceExport(r)
	if wantsFrontendText(r) {
		writeText(w, http.StatusOK, "URL: "+exportURL+"\nCode: "+code+"\n")
		return
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "application/json") {
		theme, lang, _ := preferenceState(r)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"theme": theme, "lang": lang, "prefs": map[string]string{}, "url": exportURL, "code": code}})
		return
	}
	csrf := ensureCSRFCookie(w, r)
	body := `<section class="section-card" aria-labelledby="sync-export"><h2 id="sync-export">` + tr(r, "preferences.sync") + `</h2><div class="form-group"><label for="sync-url">` + tr(r, "preferences.sync.export_link") + `</label><input id="sync-url" class="user-select-all" value="` + htmlEscape(exportURL) + `" readonly><button class="copy-btn" type="button" data-copy-text="` + htmlEscape(exportURL) + `" data-copy-label="` + htmlEscape(tr(r, "preferences.sync.copy")) + `" data-copied-label="` + htmlEscape(tr(r, "copied")) + `" aria-label="` + htmlEscape(tr(r, "preferences.sync.copy")) + `" aria-live="polite">` + htmlEscape(tr(r, "preferences.sync.copy")) + `</button></div><div class="form-group"><label for="sync-code">` + tr(r, "preferences.sync.export_code") + `</label><input id="sync-code" class="user-select-all" value="` + htmlEscape(code) + `" readonly><button class="copy-btn" type="button" data-copy-text="` + htmlEscape(code) + `" data-copy-label="` + htmlEscape(tr(r, "preferences.sync.copy")) + `" data-copied-label="` + htmlEscape(tr(r, "copied")) + `" aria-label="` + htmlEscape(tr(r, "preferences.sync.copy")) + `" aria-live="polite">` + htmlEscape(tr(r, "preferences.sync.copy")) + `</button></div><form method="post" action="/server/preferences/sync"><input type="hidden" name="csrf_token" value="` + htmlEscape(csrf) + `"><div class="form-group"><label for="sync-import">` + tr(r, "preferences.sync.import") + `</label><input id="sync-import" name="code" maxlength="2048" autocomplete="off"></div><button type="submit">` + tr(r, "preferences.sync.import") + `</button></form></section>`
	page(w, r, tr(r, "preferences.sync"), templateHTML(body))
}

func preferenceExport(r *http.Request) (string, string, string, string) {
	query, code, exportURL := canonicalPreferences(r)
	_, lang, _ := preferenceState(r)
	return query, lang, code, exportURL
}

func preferenceSyncImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		writeHTTPError(w, r, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, _, consentRequired := applyPreferenceValues(w, r, mergePreferenceValues(r.URL.Query(), r.URL.Query().Get("code")))
	w.Header().Set("Cache-Control", "no-store")
	redirect := preferenceRedirect(r, "/")
	if consentRequired {
		redirect = addQueryParam(redirect, "consent_required", "1")
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func preferenceSyncAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != APIBasePath()+"/server/preferences/sync" || !method(w, r, http.MethodGet) {
		return
	}
	_, code, exportURL := canonicalPreferences(r)
	theme, lang, _ := preferenceState(r)
	w.Header().Set("Cache-Control", "no-store")
	if wantsText(r) {
		writeText(w, http.StatusOK, "URL: "+exportURL+"\nCode: "+code+"\n")
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"theme": theme, "lang": lang, "prefs": map[string]string{}, "url": exportURL, "code": code}})
}

func preferenceSyncImportAPI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != APIBasePath()+"/server/preferences/sync/import" || !method(w, r, http.MethodGet) {
		return
	}
	values := mergePreferenceValues(r.URL.Query(), r.URL.Query().Get("code"))
	applyPreferenceValues(w, r, values)
	writePreferenceResult(w, r, values)
}

func apiPreferenceSave(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	values := make(url.Values)
	if strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		var input map[string]json.RawMessage
		decoder := json.NewDecoder(r.Body)
		if decoder.Decode(&input) != nil {
			writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "No valid preferences were supplied")
			return
		}
		for key, raw := range input {
			var value string
			if json.Unmarshal(raw, &value) == nil {
				values.Set(key, value)
			}
		}
	} else if err := r.ParseForm(); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "No valid preferences were supplied")
		return
	} else {
		values = r.Form
	}
	valid, _, _ := validPreferenceInputs(values)
	if len(valid) == 0 {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", "No valid preferences were supplied")
		return
	}
	applyPreferenceValues(w, r, values)
	writePreferenceResult(w, r, values)
}

func mergePreferenceValues(direct url.Values, code string) url.Values {
	merged := make(url.Values)
	if len(code) <= 2048 {
		code = strings.TrimSpace(code)
		if index := strings.Index(code, "?"); index >= 0 {
			code = code[index+1:]
		}
		if strings.Contains(strings.TrimRight(code, "="), "=") {
			if parsed, err := url.ParseQuery(code); err == nil {
				merged = parsed
			}
		} else if code != "" {
			if decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(code, "=")); err == nil {
				if parsed, parseErr := url.ParseQuery(string(decoded)); parseErr == nil {
					merged = parsed
				}
			} else if decoded, err := base64.URLEncoding.DecodeString(code); err == nil {
				if parsed, parseErr := url.ParseQuery(string(decoded)); parseErr == nil {
					merged = parsed
				}
			}
		}
	}
	for key, values := range direct {
		if key != "code" && len(values) > 0 {
			merged[key] = append([]string(nil), values...)
		}
	}
	return merged
}

func validPreferenceInputs(values url.Values) (url.Values, []string, []string) {
	valid := make(url.Values)
	ignored := make([]string, 0)
	inputKeys := make([]string, 0, len(values))
	for key := range values {
		inputKeys = append(inputKeys, key)
	}
	sort.Strings(inputKeys)
	considered := 0
	for _, key := range inputKeys {
		entries := values[key]
		if preferenceControls[key] {
			continue
		}
		considered++
		if considered > 32 || len(entries) == 0 || len(entries[0]) > 64 {
			ignored = append(ignored, key)
			continue
		}
		value := entries[0]
		switch key {
		case "theme":
			if value == "dark" || value == "light" || value == "auto" {
				valid.Set(key, value)
			} else {
				ignored = append(ignored, key)
			}
		case "lang":
			candidate := strings.ToLower(strings.TrimSpace(value))
			if i18n.IsSupported(candidate) {
				valid.Set(key, candidate)
			} else if strings.Contains(candidate, "-") && i18n.IsSupported(strings.SplitN(candidate, "-", 2)[0]) {
				valid.Set(key, strings.SplitN(candidate, "-", 2)[0])
			} else {
				ignored = append(ignored, key)
			}
		default:
			ignored = append(ignored, key)
		}
	}
	sort.Strings(ignored)
	keys := make([]string, 0, len(valid))
	for key := range valid {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return valid, keys, ignored
}

func applyPreferenceValues(w http.ResponseWriter, r *http.Request, values url.Values) ([]string, []string, bool) {
	valid, keys, ignored := validPreferenceInputs(values)
	if privacySignalFromRequest(r) != "" {
		ignored = append(ignored, keys...)
		sort.Strings(ignored)
		return nil, ignored, len(keys) > 0
	}
	var consent cookieConsentState
	if cookie, err := r.Cookie("cookie_consent"); err == nil {
		if raw, decodeErr := url.PathUnescape(cookie.Value); decodeErr == nil {
			_ = json.Unmarshal([]byte(raw), &consent)
		}
	}
	if !consent.Preferences {
		ignored = append(ignored, keys...)
		sort.Strings(ignored)
		return nil, ignored, len(keys) > 0
	}
	scheme, _, _ := GetURLVars(r)
	for key, entries := range valid {
		if len(entries) > 0 {
			http.SetCookie(w, &http.Cookie{Name: key, Value: entries[0], Path: "/", MaxAge: preferenceCookieAge, SameSite: http.SameSiteLaxMode, Secure: scheme == "https"})
		}
	}
	return keys, ignored, false
}

func writePreferenceResult(w http.ResponseWriter, r *http.Request, values url.Values) {
	valid, keys, ignored := validPreferenceInputs(values)
	consentRequired := false
	applied := keys
	if privacySignalFromRequest(r) != "" || !preferenceCategoryAllowed(r) {
		ignored = append(ignored, keys...)
		sort.Strings(ignored)
		applied = make([]string, 0)
		consentRequired = len(keys) > 0
	}
	theme, lang, _ := preferenceState(r)
	if !consentRequired {
		if value := valid.Get("theme"); value != "" {
			theme = value
		}
		if value := valid.Get("lang"); value != "" {
			lang = value
		}
	}
	data := map[string]any{"theme": theme, "lang": lang, "prefs": map[string]string{}, "applied": applied, "ignored": ignored}
	if consentRequired {
		data["consent_required"] = true
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func preferenceRedirect(r *http.Request, fallback string) string {
	for _, candidate := range []string{r.URL.Query().Get("redirect"), r.Form.Get("redirect"), refererPath(r)} {
		if candidate == "" || !strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "//") || strings.HasPrefix(candidate, "/\\") || strings.ContainsAny(candidate, "\r\n") {
			continue
		}
		parsed, err := url.Parse(candidate)
		if err != nil || parsed.IsAbs() || parsed.Host != "" || strings.HasPrefix(parsed.Path, "/server/preferences/sync/import") || parsed.Path == "/server/preferences" || parsed.Path == "/server/preferences/sync" {
			continue
		}
		return candidate
	}
	return fallback
}

func refererPath(r *http.Request) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Host == "" || !strings.EqualFold(ref.Host, r.Host) {
		return ""
	}
	return ref.RequestURI()
}

func htmlEscape(value string) string { return template.HTMLEscapeString(value) }

func addQueryParam(target, key, value string) string {
	parsed, err := url.Parse(target)
	if err != nil {
		return target
	}
	query := parsed.Query()
	query.Set(key, value)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}
