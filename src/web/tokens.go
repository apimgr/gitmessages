package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html"
	"html/template"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/auth"
	"github.com/apimgr/gitmessages/src/common/i18n"
)

const ownerCookieName = "gitmessages_owner_token_7gT2qA"
const csrfCookieName = "csrf_token"
const tokenFlashCookieName = "token_flash"

var authStoreMu sync.RWMutex
var authStore *auth.Store
var tokenRateMu sync.Mutex
var tokenRate = map[string]tokenRateWindow{}
var tokenFailureRate = map[string]tokenRateWindow{}
var tokenStrictCount, tokenFailureCount = 10, 5
var tokenStrictWindow, tokenFailureWindow = 15 * time.Minute, 15 * time.Minute
var tokenFlashMu sync.Mutex
var tokenFlashes = map[string]tokenFlash{}

type tokenFlash struct {
	value, toast string
	expires      time.Time
}

func tokenText(r *http.Request, key string) string {
	lang := "en"
	if r != nil {
		lang = r.Header.Get("X-GitMessages-Language")
	}
	return i18n.T(lang, "tokens."+key)
}

func tokenPageTitle(r *http.Request) string {
	lang := "en"
	if r != nil {
		lang = r.Header.Get("X-GitMessages-Language")
	}
	return i18n.T(lang, "preferences.tokens.title")
}

func tokenStatusText(r *http.Request, status string) string {
	switch strings.ToLower(status) {
	case "active":
		return tokenText(r, "active")
	case "expired":
		return tokenText(r, "expired")
	case "revoked":
		return tokenText(r, "revoked")
	default:
		return status
	}
}

type tokenRateWindow struct {
	started time.Time
	count   int
}

// ConfigureAuth installs the shared token store used by token pages and MCP.
func ConfigureAuth(store *auth.Store) {
	authStoreMu.Lock()
	authStore = store
	authStoreMu.Unlock()
}

// ConfigureTokenRateLimits applies the server.rate_limit strict and auth_failures settings.
func ConfigureTokenRateLimits(strictCount int, strictWindow time.Duration, failureCount int, failureWindow time.Duration) {
	tokenRateMu.Lock()
	defer tokenRateMu.Unlock()
	if strictCount > 0 {
		tokenStrictCount = strictCount
	}
	if strictWindow > 0 {
		tokenStrictWindow = strictWindow
	}
	if failureCount > 0 {
		tokenFailureCount = failureCount
	}
	if failureWindow > 0 {
		tokenFailureWindow = failureWindow
	}
}

func currentAuthStore() *auth.Store {
	authStoreMu.RLock()
	defer authStoreMu.RUnlock()
	return authStore
}

func registerTokenRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/server/preferences/tokens", tokenPageOrAction)
	mux.HandleFunc("/server/preferences/token/create", tokenWebAction("create"))
	mux.HandleFunc("/server/preferences/token/restore", tokenWebAction("restore"))
	mux.HandleFunc("/server/preferences/token/forget", tokenWebAction("forget"))
	mux.HandleFunc("/server/preferences/token/reveal", tokenWebAction("primary-reveal"))
	mux.HandleFunc("/server/preferences/token/rotate", tokenWebAction("primary-rotate"))
	mux.HandleFunc("/server/preferences/tokens/", tokenLinkedWebAction)
	mux.HandleFunc("/tokens.js", tokenCopyScript)
	mux.HandleFunc(apiBase+"/server/preferences/tokens", tokenAPI)
	mux.HandleFunc(apiBase+"/server/preferences/tokens/", tokenAPI)
}

func tokenPageOrAction(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		if !strictTokenAllowed(w, r) {
			return
		}
		if !validCSRF(r) {
			http.Error(w, "CSRF token validation failed", http.StatusForbidden)
			return
		}
		store := currentAuthStore()
		identity, ok := ownerCookieIdentity(w, r, store)
		if !ok || identity.Kind != "primary" {
			http.Error(w, "Primary token required", http.StatusForbidden)
			return
		}
		expires, err := parseFormTokenExpiry(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		raw, _, err := store.CreateLinked(r.Context(), identity.OwnerID, r.FormValue("name"), expires)
		if err != nil {
			http.Error(w, "Unable to create linked token", http.StatusBadRequest)
			return
		}
		setTokenFlash(w, raw, "tokens.toast_created")
		http.Redirect(w, r, "/server/preferences/tokens#primary", http.StatusSeeOther)
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store := currentAuthStore()
	if store == nil {
		http.Error(w, "Token management is unavailable", http.StatusServiceUnavailable)
		return
	}
	identity, ok := ownerCookieIdentity(w, r, store)
	flash, foundFlash := takeTokenFlash(w, r)
	if foundFlash && flash.value != "" {
		w.Header().Set("Cache-Control", "no-store")
		tokenResultPage(w, r, flash.value, strings.TrimPrefix(flash.toast, "tokens."))
		return
	}
	csrf := ensureCSRFCookie(w, r)
	var tokens []auth.Token
	if ok {
		var err error
		tokens, err = store.List(r.Context(), identity.OwnerID)
		if err != nil {
			http.Error(w, "Unable to load tokens", http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	var body strings.Builder
	body.WriteString(`<p><a href="/server/preferences">` + html.EscapeString(tokenText(r, "back")) + `</a></p><h1>` + html.EscapeString(tokenText(r, "title")) + `</h1>`)
	if foundFlash && flash.toast != "" {
		body.WriteString(`<p role="status" class="toast">` + html.EscapeString(tokenText(r, strings.TrimPrefix(flash.toast, "tokens."))) + `</p>`)
	}
	if !ok {
		body.WriteString(`<section class="section-card"><h2>` + html.EscapeString(tokenText(r, "new")) + `</h2><form method="post" action="/server/preferences/token/create"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><button type="submit">` + html.EscapeString(tokenText(r, "create")) + `</button></form><h2>` + html.EscapeString(tokenText(r, "existing")) + `</h2><form method="post" action="/server/preferences/token/restore"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><label>` + html.EscapeString(tokenText(r, "primary_label")) + `<input name="token" autocomplete="off" placeholder="` + html.EscapeString(tokenText(r, "paste_hint")) + `" required></label><button type="submit">` + html.EscapeString(tokenText(r, "restore")) + `</button></form></section>`)
		page(w, r, tokenPageTitle(r), templateHTML(body.String()))
		return
	}
	primary := primaryToken(tokens)
	primaryMasked, _ := store.MaskedValue(r.Context(), identity.OwnerID, primary.ID)
	body.WriteString(`<section class="section-card" id="primary"><h2>` + html.EscapeString(tokenText(r, "primary")) + `</h2><p><span class="status-badge">` + html.EscapeString(tokenText(r, "active")) + `</span></p><p class="copy-value"><code>` + html.EscapeString(primaryMasked) + `</code></p><p>` + html.EscapeString(i18n.Format(r.Header.Get("X-GitMessages-Language"), "tokens.access", map[string]string{"resources": "items"})) + `</p><form method="post" action="/server/preferences/token/reveal"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><button type="submit">` + html.EscapeString(tokenText(r, "reveal")) + `</button></form><form method="post" action="/server/preferences/token/rotate"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><button type="button" popovertarget="rotate-primary">` + html.EscapeString(tokenText(r, "rotate")) + `</button><div id="rotate-primary" popover><p>` + html.EscapeString(tokenText(r, "ask_rotate")) + `</p><button type="submit" popovertarget="rotate-primary" popovertargetaction="hide">` + html.EscapeString(tokenText(r, "confirm_rotate")) + `</button></div></form></section>`)
	linkedCount := 0
	for _, token := range tokens {
		if token.Kind == "linked" {
			linkedCount++
		}
	}
	body.WriteString(`<section class="section-card"><h2>` + html.EscapeString(i18n.Format(r.Header.Get("X-GitMessages-Language"), "tokens.linked_count", map[string]string{"count": strconv.Itoa(linkedCount)})) + `</h2>`)
	for _, token := range tokens {
		if token.Kind != "linked" {
			continue
		}
		masked, _ := store.MaskedValue(r.Context(), identity.OwnerID, token.ID)
		expiryDate := ""
		if token.ExpiresAt != nil {
			expiryDate = token.ExpiresAt.Format("2006-01-02")
		}
		body.WriteString(`<article id="token-` + strconv.FormatInt(token.ID, 10) + `"><h3>` + html.EscapeString(token.Name) + `</h3><p><span class="status-badge">` + html.EscapeString(tokenStatusText(r, token.Status)) + `</span> · <code>` + html.EscapeString(masked) + `</code></p><dl><dt>` + html.EscapeString(tokenText(r, "created")) + `</dt><dd>` + token.CreatedAt.Format(time.RFC3339) + `</dd><dt>` + html.EscapeString(tokenText(r, "last_used")) + `</dt><dd>` + tokenDate(r, token.LastUsedAt) + `</dd><dt>` + html.EscapeString(tokenText(r, "expires")) + `</dt><dd>` + tokenDate(r, token.ExpiresAt) + `</dd></dl><form method="post" action="/server/preferences/tokens/` + strconv.FormatInt(token.ID, 10) + `/reveal"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><button>` + html.EscapeString(tokenText(r, "reveal")) + `</button></form><form method="post" id="rotate-form-` + strconv.FormatInt(token.ID, 10) + `" action="/server/preferences/tokens/` + strconv.FormatInt(token.ID, 10) + `/rotate"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><button type="button" popovertarget="rotate-linked-` + strconv.FormatInt(token.ID, 10) + `">` + html.EscapeString(tokenText(r, "rotate")) + `</button><div id="rotate-linked-` + strconv.FormatInt(token.ID, 10) + `" popover><p>` + html.EscapeString(tokenText(r, "ask_rotate")) + `</p><button type="submit" form="rotate-form-` + strconv.FormatInt(token.ID, 10) + `">` + html.EscapeString(tokenText(r, "confirm_rotate")) + `</button></div></form><details><summary>` + html.EscapeString(tokenText(r, "edit")) + `</summary><form method="post" action="/server/preferences/tokens/` + strconv.FormatInt(token.ID, 10) + `/update"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><label>` + html.EscapeString(tokenText(r, "name")) + `<input name="name" maxlength="64" value="` + html.EscapeString(token.Name) + `" required></label><label>` + html.EscapeString(tokenText(r, "expires_on")) + `<input type="date" name="expiry" value="` + expiryDate + `"></label><button>` + html.EscapeString(tokenText(r, "save")) + `</button></form></details><form method="post" action="/server/preferences/tokens/` + strconv.FormatInt(token.ID, 10) + `/revoke"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><button type="button" popovertarget="revoke-linked-` + strconv.FormatInt(token.ID, 10) + `">` + html.EscapeString(tokenText(r, "revoke")) + `</button><div id="revoke-linked-` + strconv.FormatInt(token.ID, 10) + `" popover><p>` + html.EscapeString(tokenText(r, "ask_revoke")) + `</p><button type="submit">` + html.EscapeString(tokenText(r, "confirm_revoke")) + `</button></div></form></article>`)
	}
	if linkedCount == 0 {
		body.WriteString(`<p>` + html.EscapeString(tokenText(r, "empty")) + `</p>`)
	}
	body.WriteString(`</section><section class="section-card"><h2>` + html.EscapeString(tokenText(r, "create_linked")) + `</h2><form method="post" action="/server/preferences/tokens"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><label>` + html.EscapeString(tokenText(r, "name")) + `<input name="name" maxlength="64" required><small>` + html.EscapeString(tokenText(r, "example")) + `</small></label><label>` + html.EscapeString(tokenText(r, "expiry")) + `<select name="expiry"><option value="never">` + html.EscapeString(tokenText(r, "never")) + `</option><option value="7d">` + html.EscapeString(tokenText(r, "7d")) + `</option><option value="30d">` + html.EscapeString(tokenText(r, "30d")) + `</option><option value="90d">` + html.EscapeString(tokenText(r, "90d")) + `</option><option value="1y">` + html.EscapeString(tokenText(r, "1y")) + `</option><option value="custom">` + html.EscapeString(tokenText(r, "custom")) + `</option></select></label><details><summary>` + html.EscapeString(tokenText(r, "custom_expiry")) + `</summary><label>` + html.EscapeString(tokenText(r, "expires_on")) + `<input type="date" name="expiry_date"></label></details><button type="submit">` + html.EscapeString(tokenText(r, "create")) + `</button></form></section><section class="section-card"><h2>` + html.EscapeString(tokenText(r, "browser")) + `</h2><form method="post" action="/server/preferences/token/forget"><input type="hidden" name="csrf_token" value="` + html.EscapeString(csrf) + `"><button type="button" popovertarget="forget-token">` + html.EscapeString(tokenText(r, "forget")) + `</button><div id="forget-token" popover><p>` + html.EscapeString(tokenText(r, "ask_forget")) + `</p><button type="submit">` + html.EscapeString(tokenText(r, "confirm_forget")) + `</button></div></form><p>` + html.EscapeString(i18n.Format(r.Header.Get("X-GitMessages-Language"), "tokens.preserved", map[string]string{"resources": "items"})) + `</p></section>`)
	page(w, r, tokenPageTitle(r), templateHTML(body.String()))
}

func templateHTML(value string) template.HTML { return template.HTML(value) }

func tokenWebAction(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if action == "create" || action == "restore" || action == "primary-reveal" || action == "primary-rotate" {
			if !strictTokenAllowed(w, r) {
				return
			}
		}
		if !validCSRF(r) {
			http.Error(w, "CSRF token validation failed", http.StatusForbidden)
			return
		}
		store := currentAuthStore()
		if store == nil {
			http.Error(w, "Token management is unavailable", http.StatusServiceUnavailable)
			return
		}
		switch action {
		case "create":
			if hasValidPresentedToken(r.Context(), r, store) {
				http.Error(w, "A valid token is already present", http.StatusConflict)
				return
			}
			raw, _, err := store.IssueOwner(r.Context())
			if err != nil {
				http.Error(w, "Unable to create token", 500)
				return
			}
			setOwnerCookie(w, raw, store.CookieMaxAge())
			setTokenFlash(w, raw, "tokens.toast_created")
			http.Redirect(w, r, "/server/preferences/tokens#primary", http.StatusSeeOther)
		case "restore":
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Invalid form", 400)
				return
			}
			raw := strings.TrimSpace(r.FormValue("token"))
			_, err := store.Restore(r.Context(), raw)
			if err != nil {
				if !tokenAuthFailureAllowed(w, r) {
					return
				}
				http.Error(w, "Invalid primary token", http.StatusUnauthorized)
				return
			}
			setOwnerCookie(w, raw, store.CookieMaxAge())
			http.Redirect(w, r, "/server/preferences/tokens", http.StatusSeeOther)
		case "forget":
			clearOwnerCookie(w)
			w.Header().Set("Clear-Site-Data", `"storage"`)
			http.Redirect(w, r, "/server/preferences/tokens", http.StatusSeeOther)
		case "primary-reveal", "primary-rotate":
			identity, ok := ownerCookieIdentity(w, r, store)
			if !ok || identity.Kind != "primary" {
				http.Error(w, "Primary token required", http.StatusForbidden)
				return
			}
			var raw string
			var err error
			if action == "primary-rotate" {
				tokens, listErr := store.List(r.Context(), identity.OwnerID)
				if listErr != nil {
					http.Error(w, "Unable to load tokens", 500)
					return
				}
				primary := primaryToken(tokens)
				raw, _, err = store.Rotate(r.Context(), identity.OwnerID, primary.ID)
				if err == nil {
					setOwnerCookie(w, raw, store.CookieMaxAge())
				}
			} else {
				token, revealErr := store.Reveal(r.Context(), identity.OwnerID, identity.TokenID)
				raw, err = token.Value, revealErr
			}
			if err != nil {
				http.Error(w, "Unable to access token", 500)
				return
			}
			if action == "primary-rotate" {
				setTokenFlash(w, raw, "tokens.toast_rotated")
				http.Redirect(w, r, "/server/preferences/tokens#primary", http.StatusSeeOther)
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			tokenResultPage(w, r, raw)
		}
	}
}

func tokenLinkedWebAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !strictTokenAllowed(w, r) {
		return
	}
	if !validCSRF(r) {
		http.Error(w, "CSRF token validation failed", http.StatusForbidden)
		return
	}
	store := currentAuthStore()
	identity, ok := ownerCookieIdentity(w, r, store)
	if !ok || identity.Kind != "primary" {
		http.Error(w, "Primary token required", http.StatusForbidden)
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/server/preferences/tokens/"), "/"), "/")
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if !isLinkedToken(r.Context(), store, identity.OwnerID, id) {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "reveal":
		token, err := store.Reveal(r.Context(), identity.OwnerID, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		tokenResultPage(w, r, token.Value)
	case "rotate":
		raw, _, err := store.Rotate(r.Context(), identity.OwnerID, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		setTokenFlash(w, raw, "tokens.toast_rotated")
		http.Redirect(w, r, "/server/preferences/tokens#token-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	case "revoke":
		if err := store.RevokeLinked(r.Context(), identity.OwnerID, id); err != nil {
			http.NotFound(w, r)
			return
		}
		setTokenToast(w, "tokens.toast_revoked")
		http.Redirect(w, r, "/server/preferences/tokens#token-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	case "update":
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form", 400)
			return
		}
		expires, err := parseFormTokenExpiry(r)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if err := store.UpdateLinked(r.Context(), identity.OwnerID, id, r.FormValue("name"), expires); err != nil {
			http.NotFound(w, r)
			return
		}
		setTokenToast(w, "tokens.toast_renamed")
		http.Redirect(w, r, "/server/preferences/tokens#token-"+strconv.FormatInt(id, 10), http.StatusSeeOther)
	default:
		http.NotFound(w, r)
	}
}

func tokenAPI(w http.ResponseWriter, r *http.Request) {
	store := currentAuthStore()
	if store == nil {
		tokenJSONError(w, 503, "UNAVAILABLE", "Token management is unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, apiBase+"/server/preferences/tokens")
	if (r.Method == http.MethodPost || r.Method == http.MethodPatch || r.Method == http.MethodDelete) && !strictTokenAllowed(w, r) {
		return
	}
	if path == "" || path == "/" {
		if r.Method == http.MethodGet {
			identity, err := bearerIdentity(r.Context(), r, store, true)
			if err != nil {
				tokenAuthError(w, r, err)
				return
			}
			tokens, err := store.List(r.Context(), identity.OwnerID)
			if err != nil {
				tokenJSONError(w, 500, "INTERNAL_ERROR", "Unable to load tokens")
				return
			}
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": tokens})
			return
		}
		if r.Method == http.MethodPost {
			identity, err := bearerIdentity(r.Context(), r, store, true)
			if err != nil {
				tokenAuthError(w, r, err)
				return
			}
			var input struct {
				Name   string `json:"name"`
				Expiry string `json:"expiry"`
			}
			if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&input) != nil {
				tokenJSONError(w, 400, "VALIDATION_FAILED", "Invalid request body")
				return
			}
			expires, err := parseTokenExpiry(input.Expiry)
			if err != nil {
				tokenJSONError(w, 400, "VALIDATION_FAILED", err.Error())
				return
			}
			raw, token, err := store.CreateLinked(r.Context(), identity.OwnerID, input.Name, expires)
			if err != nil {
				tokenJSONError(w, 400, "VALIDATION_FAILED", err.Error())
				return
			}
			token.Value = ""
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, r, http.StatusCreated, map[string]any{"ok": true, "data": map[string]any{"token": token, "value": raw}})
			return
		}
		w.Header().Set("Allow", "GET, POST")
		tokenJSONError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed")
		return
	}
	if path == "/primary" && r.Method == http.MethodPost {
		if hasValidBearer(r.Context(), r, store) {
			tokenJSONError(w, 409, "CONFLICT", "A valid token is already present")
			return
		}
		raw, _, err := store.IssueOwner(r.Context())
		if err != nil {
			tokenJSONError(w, 500, "INTERNAL_ERROR", "Unable to create token")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, r, http.StatusCreated, map[string]any{"ok": true, "data": map[string]string{"owner_token": raw}})
		return
	}
	if path == "/primary/reveal" || path == "/primary/rotate" {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			tokenJSONError(w, 405, "METHOD_NOT_ALLOWED", "Method not allowed")
			return
		}
		identity, err := bearerIdentity(r.Context(), r, store, true)
		if err != nil {
			tokenAuthError(w, r, err)
			return
		}
		tokens, err := store.List(r.Context(), identity.OwnerID)
		if err != nil {
			tokenJSONError(w, 500, "INTERNAL_ERROR", "Unable to load token")
			return
		}
		primary := primaryToken(tokens)
		var raw string
		if path == "/primary/reveal" {
			token, revealErr := store.Reveal(r.Context(), identity.OwnerID, primary.ID)
			raw, err = token.Value, revealErr
		} else {
			raw, _, err = store.Rotate(r.Context(), identity.OwnerID, primary.ID)
		}
		if err != nil {
			tokenJSONError(w, 500, "INTERNAL_ERROR", "Unable to access token")
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"token": raw}})
		return
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		http.NotFound(w, r)
		return
	}
	identity, err := bearerIdentity(r.Context(), r, store, true)
	if err != nil {
		tokenAuthError(w, r, err)
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id < 1 {
		http.NotFound(w, r)
		return
	}
	if !isLinkedToken(r.Context(), store, identity.OwnerID, id) {
		http.NotFound(w, r)
		return
	}
	if len(parts) == 2 && r.Method == http.MethodPatch {
		var input struct {
			Name   string `json:"name"`
			Expiry string `json:"expiry"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&input) != nil {
			tokenJSONError(w, 400, "VALIDATION_FAILED", "Invalid request body")
			return
		}
		expires, err := parseTokenExpiry(input.Expiry)
		if err != nil {
			tokenJSONError(w, 400, "VALIDATION_FAILED", err.Error())
			return
		}
		if err := store.UpdateLinked(r.Context(), identity.OwnerID, id, input.Name, expires); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]bool{"updated": true}})
		return
	}
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "reveal":
		if r.Method != http.MethodPost {
			break
		}
		token, err := store.Reveal(r.Context(), identity.OwnerID, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"token": token.Value}})
	case "rotate":
		if r.Method != http.MethodPost {
			break
		}
		raw, token, err := store.Rotate(r.Context(), identity.OwnerID, id)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		token.Value = ""
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"token": token, "value": raw}})
	case "revoke":
		if r.Method != http.MethodDelete {
			break
		}
		if err := store.RevokeLinked(r.Context(), identity.OwnerID, id); err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]bool{"revoked": true}})
	default:
		http.NotFound(w, r)
	}
}

func bearerIdentity(ctx context.Context, r *http.Request, store *auth.Store, primary bool) (auth.Identity, error) {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 7 || !strings.EqualFold(value[:7], "Bearer ") {
		return auth.Identity{}, auth.ErrInvalidToken
	}
	identity, err := store.AuthenticateBearer(ctx, strings.TrimSpace(value[7:]))
	if err != nil {
		return identity, err
	}
	if identity.Admin || (primary && identity.Kind != "primary") {
		return auth.Identity{}, auth.ErrPrimaryOnly
	}
	return identity, nil
}

func ownerCookieIdentity(w http.ResponseWriter, r *http.Request, store *auth.Store) (auth.Identity, bool) {
	cookie, err := r.Cookie(ownerCookieName)
	if err != nil || store == nil {
		return auth.Identity{}, false
	}
	identity, err := store.Authenticate(r.Context(), cookie.Value)
	if err != nil || identity.Kind != "primary" {
		clearOwnerCookie(w)
		return auth.Identity{}, false
	}
	setOwnerCookie(w, cookie.Value, store.CookieMaxAge())
	return identity, true
}

// TokenCookieMiddleware refreshes a valid owner cookie on frontend requests and
// clears invalid cookies. API and MCP requests intentionally ignore cookies.
func TokenCookieMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, apiBase) && r.URL.Path != "/mcp" && r.URL.Path != "/api/mcp" {
			if cookie, err := r.Cookie(ownerCookieName); err == nil {
				store := currentAuthStore()
				identity, authErr := auth.Identity{}, errors.New("unavailable")
				if store != nil {
					identity, authErr = store.Authenticate(r.Context(), cookie.Value)
				}
				if authErr != nil || identity.Kind != "primary" {
					clearOwnerCookie(w)
				} else {
					setOwnerCookie(w, cookie.Value, store.CookieMaxAge())
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func setTokenFlash(w http.ResponseWriter, value, toast string) {
	var id [24]byte
	if _, err := rand.Read(id[:]); err != nil {
		return
	}
	key := base64.RawURLEncoding.EncodeToString(id[:])
	tokenFlashMu.Lock()
	tokenFlashes[key] = tokenFlash{value: value, toast: toast, expires: time.Now().Add(2 * time.Minute)}
	tokenFlashMu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: tokenFlashCookieName, Value: key, Path: "/server/preferences/tokens", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: 120})
}

func setTokenToast(w http.ResponseWriter, toast string) { setTokenFlash(w, "", toast) }

func takeTokenFlash(w http.ResponseWriter, r *http.Request) (tokenFlash, bool) {
	cookie, err := r.Cookie(tokenFlashCookieName)
	if err != nil {
		return tokenFlash{}, false
	}
	http.SetCookie(w, &http.Cookie{Name: tokenFlashCookieName, Value: "", Path: "/server/preferences/tokens", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	tokenFlashMu.Lock()
	flash, ok := tokenFlashes[cookie.Value]
	delete(tokenFlashes, cookie.Value)
	for key, entry := range tokenFlashes {
		if time.Now().After(entry.expires) {
			delete(tokenFlashes, key)
		}
	}
	tokenFlashMu.Unlock()
	return flash, ok && time.Now().Before(flash.expires)
}

func hasValidPresentedToken(ctx context.Context, r *http.Request, store *auth.Store) bool {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) >= 7 && strings.EqualFold(value[:7], "Bearer ") {
		_, err := store.AuthenticateBearer(ctx, strings.TrimSpace(value[7:]))
		return err == nil
	}
	if cookie, err := r.Cookie(ownerCookieName); err == nil {
		_, err = store.Authenticate(ctx, cookie.Value)
		return err == nil
	}
	return false
}

func hasValidBearer(ctx context.Context, r *http.Request, store *auth.Store) bool {
	if store == nil || r == nil {
		return false
	}
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 7 || !strings.EqualFold(value[:7], "Bearer ") {
		return false
	}
	_, err := store.AuthenticateBearer(ctx, strings.TrimSpace(value[7:]))
	return err == nil
}

func ensureCSRFCookie(w http.ResponseWriter, r *http.Request) string {
	if cookie, err := r.Cookie(csrfCookieName); err == nil && cookie.Value != "" {
		return cookie.Value
	}
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	value := base64.RawURLEncoding.EncodeToString(raw[:])
	http.SetCookie(w, &http.Cookie{Name: csrfCookieName, Value: value, Path: "/", SameSite: http.SameSiteStrictMode, Secure: requestUsesHTTPS(r), HttpOnly: false, MaxAge: 86400})
	return value
}

func requestUsesHTTPS(r *http.Request) bool {
	return requestIsSecure(r)
}

func validCSRF(r *http.Request) bool {
	if value := strings.TrimSpace(r.Header.Get("Authorization")); len(value) > 7 && strings.EqualFold(value[:7], "Bearer ") {
		if store := currentAuthStore(); store != nil {
			if identity, err := store.AuthenticateBearer(r.Context(), strings.TrimSpace(value[7:])); err == nil && identity.Kind == "primary" {
				return true
			}
		}
	}
	if err := r.ParseForm(); err != nil {
		return false
	}
	cookie, err := r.Cookie(csrfCookieName)
	if err != nil {
		return false
	}
	got := r.FormValue("csrf_token")
	if got == "" {
		got = r.Header.Get("X-CSRF-Token")
	}
	return got != "" && len(got) == len(cookie.Value) && subtle.ConstantTimeCompare([]byte(got), []byte(cookie.Value)) == 1
}

func setOwnerCookie(w http.ResponseWriter, raw string, age time.Duration) {
	if age <= 0 {
		age = 365 * 24 * time.Hour
	}
	http.SetCookie(w, &http.Cookie{Name: ownerCookieName, Value: raw, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(age.Seconds())})
}

func clearOwnerCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: ownerCookieName, Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func primaryToken(tokens []auth.Token) auth.Token {
	for _, token := range tokens {
		if token.Kind == "primary" {
			return token
		}
	}
	return auth.Token{}
}

func isLinkedToken(ctx context.Context, store *auth.Store, ownerID string, id int64) bool {
	tokens, err := store.List(ctx, ownerID)
	if err != nil {
		return false
	}
	for _, token := range tokens {
		if token.ID == id {
			return token.Kind == "linked"
		}
	}
	return false
}

func parseFormTokenExpiry(r *http.Request) (*time.Time, error) {
	value := r.FormValue("expiry")
	if value == "custom" {
		value = r.FormValue("expiry_date")
	}
	return parseTokenExpiry(value)
}

func tokenDate(r *http.Request, value *time.Time) string {
	if value == nil {
		return tokenText(r, "never")
	}
	return value.Format(time.RFC3339)
}

func tokenResultPage(w http.ResponseWriter, r *http.Request, value string, toast ...string) {
	message := ""
	if len(toast) > 0 && toast[0] != "" {
		message = `<p role="status" class="toast">` + html.EscapeString(tokenText(r, toast[0])) + `</p>`
	}
	page(w, r, tokenPageTitle(r), templateHTML(message+`<p>`+html.EscapeString(tokenText(r, "your_token"))+`</p><p class="copy-value"><code id="revealed-token">`+html.EscapeString(value)+`</code></p><button type="button" data-copy-token="revealed-token">`+html.EscapeString(tokenText(r, "copy"))+`</button><p>`+html.EscapeString(tokenText(r, "shown_once"))+`</p><p><a href="/server/preferences/tokens">`+html.EscapeString(tokenText(r, "return"))+`</a></p><script src="/tokens.js" defer></script>`))
}

func tokenCopyScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = io.WriteString(w, `document.addEventListener("click",async(e)=>{const b=e.target.closest("[data-copy-token]");if(!b)return;const n=document.getElementById(b.dataset.copyToken);if(n)await navigator.clipboard.writeText(n.textContent.trim())});`)
}

func parseTokenExpiry(value string) (*time.Time, error) {
	switch value {
	case "", "never":
		return nil, nil
	case "7d":
		v := time.Now().UTC().Add(7 * 24 * time.Hour)
		return &v, nil
	case "30d":
		v := time.Now().UTC().Add(30 * 24 * time.Hour)
		return &v, nil
	case "90d":
		v := time.Now().UTC().Add(90 * 24 * time.Hour)
		return &v, nil
	case "1y":
		v := time.Now().UTC().AddDate(1, 0, 0)
		return &v, nil
	default:
		v, err := time.Parse("2006-01-02", value)
		if err != nil {
			return nil, errors.New("expiry must be never, 7d, 30d, 90d, 1y, or an ISO date")
		}
		v = v.UTC().Add(23*time.Hour + 59*time.Minute + 59*time.Second)
		if !v.After(time.Now()) {
			return nil, errors.New("expiry must be in the future")
		}
		return &v, nil
	}
}

func tokenJSONError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": false, "error": code, "message": message})
}

func strictTokenAllowed(w http.ResponseWriter, r *http.Request) bool {
	ip := requestClientIP(r)
	now := time.Now()
	tokenRateMu.Lock()
	key := rateIdentity(r, ip)
	for key, window := range tokenRate {
		if now.Sub(window.started) >= tokenStrictWindow {
			delete(tokenRate, key)
		}
	}
	window := tokenRate[key]
	if window.started.IsZero() || now.Sub(window.started) >= tokenStrictWindow {
		window = tokenRateWindow{started: now}
	}
	window.count++
	tokenRate[key] = window
	allowed := window.count <= tokenStrictCount
	tokenRateMu.Unlock()
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(tokenStrictWindow.Seconds())))
		tokenJSONError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Token action rate limit exceeded")
		return false
	}
	return true
}

func tokenAuthError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, auth.ErrPrimaryOnly) {
		w.Header().Set("WWW-Authenticate", `Bearer error="insufficient_scope"`)
		tokenJSONError(w, http.StatusForbidden, "FORBIDDEN", "A primary token is required")
		return
	}
	if !tokenAuthFailureAllowed(w, r) {
		return
	}
	w.Header().Set("WWW-Authenticate", `Bearer realm="gitmessages", error="invalid_token"`)
	tokenJSONError(w, http.StatusUnauthorized, "UNAUTHORIZED", "A valid primary token is required")
}

// TokenAuthFailure records invalid or expired bearer credentials for the shared auth-failure limit.
func TokenAuthFailure(w http.ResponseWriter, r *http.Request) bool {
	return tokenAuthFailureAllowed(w, r)
}

func tokenAuthFailureAllowed(w http.ResponseWriter, r *http.Request) bool {
	ip := "unknown"
	if r != nil {
		ip = requestClientIP(r)
	}
	now := time.Now()
	tokenRateMu.Lock()
	for key, window := range tokenFailureRate {
		if now.Sub(window.started) >= tokenFailureWindow {
			delete(tokenFailureRate, key)
		}
	}
	window := tokenFailureRate[ip]
	if window.started.IsZero() || now.Sub(window.started) >= tokenFailureWindow {
		window = tokenRateWindow{started: now}
	}
	window.count++
	tokenFailureRate[ip] = window
	allowed := window.count <= tokenFailureCount
	tokenRateMu.Unlock()
	if !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(tokenFailureWindow.Seconds())))
		tokenJSONError(w, http.StatusTooManyRequests, "RATE_LIMITED", "Authentication failure limit exceeded")
		return false
	}
	return true
}

func rateIdentity(r *http.Request, ip string) string {
	if r == nil {
		return "ip:" + ip
	}
	if store := currentAuthStore(); store != nil {
		if value := strings.TrimSpace(r.Header.Get("Authorization")); len(value) > 7 && strings.EqualFold(value[:7], "Bearer ") {
			if identity, err := store.AuthenticateBearer(r.Context(), strings.TrimSpace(value[7:])); err == nil {
				return "owner:" + identity.OwnerID
			}
		}
		if cookie, err := r.Cookie(ownerCookieName); err == nil {
			if identity, authErr := store.Authenticate(r.Context(), cookie.Value); authErr == nil {
				return "owner:" + identity.OwnerID
			}
		}
	}
	return "ip:" + ip
}
