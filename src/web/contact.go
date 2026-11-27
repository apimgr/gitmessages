package web

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/contact"
	"github.com/apimgr/gitmessages/src/database"
	"github.com/apimgr/gitmessages/src/email"
	"github.com/apimgr/gitmessages/src/notify"
	"github.com/apimgr/gitmessages/src/paths"
)

var (
	contactStoreMu sync.RWMutex
	contactStore   *database.Store
)

// ConfigureContact binds contact processing to the shared application database.
func ConfigureContact(store *database.Store) {
	contactStoreMu.Lock()
	contactStore = store
	contactStoreMu.Unlock()
}

func registerContactRoutes(mux routeRegistrar) {
	mux.HandleFunc("/server/contact", frontendPage(contactPage, apiContact, "/server/contact"))
	mux.HandleFunc("/server/security", frontendPage(contactSecurityPage, apiContactSecurity, "/server/security"))
	mux.HandleFunc("/server/security/policy", frontendPage(securityPolicyPage, apiSecurityPolicy, "/server/security/policy"))
	mux.HandleFunc("/server/security/thanks", frontendPage(securityThanksPage, apiSecurityThanks, "/server/security/thanks"))
	mux.HandleFunc("/server/security/report/", securityReportPage)
	mux.HandleFunc("/server/dpo", frontendPage(dpoPage, apiDPO, "/server/dpo"))
	mux.HandleFunc("/.well-known/security.txt", contactSecurityTxt)
	mux.HandleFunc("/.well-known/llms.txt", llmsTxt)
	mux.HandleFunc("/llms.txt", llmsTxt)
	mux.HandleFunc("/.well-known/pgp-key.asc", pgpKeyPage)
}

func contactInputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"type": map[string]any{"type": "string", "enum": []string{"abuse", "dmca", "privacy", "security"}}, "name": map[string]any{"type": "string", "maxLength": 100}, "email": map[string]any{"type": "string", "format": "email", "maxLength": 254}, "form_token": map[string]any{"type": "string"}, "captcha_id": map[string]any{"type": "string"}, "captcha_answer": map[string]any{"type": "string"}, "step": map[string]any{"type": "string", "enum": []string{"submit", "confirm", "edit"}}, "review_token": map[string]any{"type": "string"}, "confirmed": map[string]any{"type": "boolean"}}, "required": []string{"type", "name", "email", "form_token"}, "additionalProperties": true}
}

func currentContactService() (*contact.Service, error) {
	cfg := config.Get()
	contactStoreMu.RLock()
	store := contactStore
	contactStoreMu.RUnlock()
	if store == nil {
		return nil, errors.New("contact database is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	secret, previousSecrets, err := contact.InstallationSecretValues(ctx, store, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	keyText := strings.TrimSpace(cfg.Server.Security.EncryptionKey)
	key, err := hex.DecodeString(keyText)
	if err != nil || len(key) != 32 {
		key, err = base64.RawURLEncoding.DecodeString(keyText)
	}
	if err != nil || len(key) != 32 {
		key, err = base64.StdEncoding.DecodeString(keyText)
	}
	if err != nil || len(key) != 32 {
		return nil, errors.New("security encryption key is unavailable")
	}
	antispam := cfg.Server.Pages.Contact.Antispam
	window, err := time.ParseDuration(antispam.Window)
	if err != nil || window <= 0 {
		window = time.Hour
	}
	settings := contact.Settings{MinFill: antispam.MinFillSeconds, MaxAge: antispam.MaxAgeSeconds, ReviewMin: antispam.ReviewMinSeconds, ReviewMax: antispam.ReviewMaxSeconds, PerIP: antispam.PerIP, PerEmail: antispam.PerEmail, Global: antispam.Global, Window: window, MaxLinks: antispam.MaxLinks, Strikes: antispam.Strikes, BanHours: antispam.BanHours, DuplicateHours: antispam.DuplicateWindowHours, RejectMarkup: antispam.RejectMarkup, Honeypot: antispam.Honeypot, BlockedWords: antispam.BlockedWords, BlockedEmailDomains: antispam.BlockedEmailDomains, CaptchaProvider: cfg.Server.Pages.Contact.Captcha, CaptchaSecretKey: cfg.Server.Pages.Contact.CaptchaSecretKey, PreviousSecrets: previousSecrets}
	if configDir := config.ConfigDirectory(); configDir != "" {
		if key, readErr := os.ReadFile(filepath.Join(configDir, "security", "pgp.pub.asc")); readErr == nil {
			settings.ProjectPGPKey = key
		}
	}
	return contact.New(store, secret, key, settings, contactEmailSender{})
}

type contactEmailSender struct{}

func (contactEmailSender) SendContact(role, from, name, subject, body string, tunneled bool) error {
	cfg := config.Get().Server
	mailcfg := cfg.Notifications.Email
	to := contactRecipient(cfg.Contact, role)
	if role != "security" {
		subject = "[" + seoBrandingTitle() + " contact] " + subject
	}
	var emailErr error
	if mailcfg.Enabled && mailcfg.SMTP.Host != "" && to != "" {
		emailErr = contactMailer(cfg).Send(context.Background(), email.Message{To: to, ReplyTo: from, Subject: subject, Text: body})
	} else if mailcfg.Enabled && mailcfg.SMTP.Host != "" {
		emailErr = errors.New("contact recipient is not configured")
	} else {
		emailErr = errors.New("email features require SMTP configuration")
	}
	if hasContactWebhook(cfg.Contact, role) {
		if tunneled {
			log.Printf("WARN: skipped contact webhook for tunneled %s submission", role)
		} else {
			dispatchContactWebhook(cfg.Contact, role, "", "", "")
		}
	}
	return emailErr
}

func dispatchContactWebhook(cfg config.ContactConfig, role, trackingID, component, severity string) bool {
	webhookRole := role
	if webhookRole == "privacy" || webhookRole == "dmca" {
		webhookRole = map[string]string{"privacy": "general", "dmca": "abuse"}[role]
	}
	deliveries := contactWebhookRecipients(cfg, webhookRole)
	if len(deliveries) == 0 {
		return false
	}
	if severity == "" {
		severity = "info"
	}
	body := "A " + role + " submission was received. Review it through the configured email recipient."
	if role == "security" {
		component = safeWebhookComponent(component)
		body = fmt.Sprintf("Tracking ID: %s\nComponent: %s\nSeverity: %s\nRead the encrypted report with: %s --maintenance security export %s --output FILE", trackingID, component, severity, paths.ProjectName, trackingID)
	}
	event := notify.Event{
		Name: paths.ProjectName, Version: serverVersion, AppURL: "https://" + config.Get().Server.FQDN,
		Event: "contact." + role + ".received", Subject: "Contact " + role + " submission received",
		Body: body, Severity: severity,
	}
	queued := false
	for _, delivery := range deliveries {
		queued = notify.DispatchAsync(delivery.role, delivery.hooks, event) || queued
	}
	return queued
}

func hasContactWebhook(cfg config.ContactConfig, role string) bool {
	switch role {
	case "privacy":
		role = "general"
	case "dmca":
		role = "abuse"
	}
	return len(contactWebhookRecipients(cfg, role)) > 0
}

func safeWebhookComponent(component string) string {
	var builder strings.Builder
	for _, r := range component {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune(" ._/-", r) {
			builder.WriteRune(r)
		}
		if builder.Len() >= 255 {
			break
		}
	}
	return strings.TrimSpace(builder.String())
}

type contactWebhookDelivery struct {
	role  string
	hooks notify.WebhookConfig
}

func contactWebhookRecipients(cfg config.ContactConfig, role string) []contactWebhookDelivery {
	chain := []string{role}
	switch role {
	case "security":
		chain = append(chain, "admin")
	case "abuse":
		chain = append(chain, "general", "admin")
	case "general":
		chain = append(chain, "admin")
	default:
		return nil
	}
	var transports []string
	seen := make(map[string]struct{})
	for _, candidate := range chain {
		for transport := range webhookRecipientConfig(cfg, candidate).Webhooks {
			if strings.HasSuffix(transport, "_secret") {
				continue
			}
			if _, exists := seen[transport]; !exists {
				seen[transport] = struct{}{}
				transports = append(transports, transport)
			}
		}
	}
	sort.Strings(transports)
	deliveries := make([]contactWebhookDelivery, 0, len(transports))
	for _, transport := range transports {
		for _, candidate := range chain {
			recipient := webhookRecipientConfig(cfg, candidate)
			endpoint := strings.TrimSpace(recipient.Webhooks[transport])
			if endpoint == "" {
				continue
			}
			hooks := notify.WebhookConfig{transport: endpoint}
			if secret := recipient.Webhooks[transport+"_secret"]; secret != "" {
				hooks[transport+"_secret"] = secret
			}
			deliveries = append(deliveries, contactWebhookDelivery{role: candidate, hooks: hooks})
			break
		}
	}
	return deliveries
}

func webhookRecipientConfig(cfg config.ContactConfig, role string) config.ContactRecipientConfig {
	switch role {
	case "admin":
		return cfg.Admin
	case "security":
		return cfg.Security
	case "abuse":
		return cfg.Abuse
	case "general":
		return cfg.General
	default:
		return config.ContactRecipientConfig{}
	}
}

func contactMailer(cfg config.ServerConfig) email.Mailer {
	mailcfg := cfg.Notifications.Email
	fromAddress := mailcfg.From.Email
	if fromAddress == "" {
		host := fallbackHostname()
		if host != "" {
			fromAddress = "no-reply@" + host
		}
	}
	fromName := mailcfg.From.Name
	if fromName == "" {
		fromName = seoBrandingTitle()
	}
	return email.Mailer{SMTP: email.SMTPConfig{Host: mailcfg.SMTP.Host, Port: mailcfg.SMTP.Port, Username: mailcfg.SMTP.Username, Password: mailcfg.SMTP.Password, TLS: mailcfg.SMTP.TLS}, Sender: email.Sender{Name: fromName, Address: fromAddress, FQDN: fallbackHostname()}}
}

func (contactEmailSender) SendSecurityNotification(_ string, subject, body, filename string, encrypted []byte, tunneled bool, trackingID, component, severity string) error {
	cfg := config.Get().Server
	webhookQueued := false
	if hasContactWebhook(cfg.Contact, "security") {
		if tunneled {
			log.Printf("WARN: skipped security webhook for tunneled report")
		} else {
			webhookQueued = dispatchSecurityWebhook(cfg.Contact, trackingID, component, severity)
		}
	}
	if !cfg.Notifications.Email.Enabled || cfg.Notifications.Email.SMTP.Host == "" {
		if webhookQueued {
			return nil
		}
		return errors.New("email features require SMTP configuration")
	}
	if strings.TrimSpace(cfg.Contact.Security.Email) == "" {
		recipient := strings.TrimSpace(cfg.Contact.Admin.Email)
		if recipient == "" {
			return errors.New("security and admin recipients are not configured")
		}
		return contactMailer(cfg).Send(context.Background(), email.Message{To: recipient, Subject: "Security report received", Text: securitySummary(trackingID, component, severity)})
	}
	return contactMailer(cfg).Send(context.Background(), email.Message{To: cfg.Contact.Security.Email, Subject: subject, Text: body, Attachments: []email.Attachment{{Name: filename, ContentType: "application/octet-stream", Data: encrypted}}})
}

func securitySummary(trackingID, component, severity string) string {
	component = safeWebhookComponent(component)
	return fmt.Sprintf("Tracking ID: %s\nComponent: %s\nSeverity: %s\nRead the encrypted report with: %s --maintenance security export %s --output FILE", trackingID, component, severity, paths.ProjectName, trackingID)
}

func dispatchSecurityWebhook(cfg config.ContactConfig, trackingID, component, severity string) bool {
	deliveries := contactWebhookRecipients(cfg, "security")
	if len(deliveries) == 0 {
		return false
	}
	event := notify.Event{
		Name: paths.ProjectName, Version: serverVersion, AppURL: "https://" + config.Get().Server.FQDN,
		Event: "security.report.received", Subject: "Security report received", Body: securitySummary(trackingID, component, severity), Severity: severity,
		TrackingID: trackingID,
	}
	queued := false
	for _, delivery := range deliveries {
		queued = notify.DispatchAsync(delivery.role, delivery.hooks, event) || queued
	}
	return queued
}

func (contactEmailSender) SendResearcher(to, subject, body string) error {
	cfg := config.Get().Server
	if !cfg.Notifications.Email.Enabled || cfg.Notifications.Email.SMTP.Host == "" {
		return errors.New("email features require SMTP configuration")
	}
	return contactMailer(cfg).Send(context.Background(), email.Message{To: to, Subject: subject, Text: body})
}

func (contactEmailSender) SendResearcherEncrypted(to, subject string, encrypted []byte) error {
	cfg := config.Get().Server
	if !cfg.Notifications.Email.Enabled || cfg.Notifications.Email.SMTP.Host == "" {
		return errors.New("email features require SMTP configuration")
	}
	return contactMailer(cfg).Send(context.Background(), email.Message{To: to, Subject: subject, Text: string(encrypted)})
}

func contactRecipient(cfg config.ContactConfig, role string) string {
	switch role {
	case "security":
		if cfg.Security.Email != "" {
			return cfg.Security.Email
		}
		return cfg.Admin.Email
	case "abuse", "dmca":
		if cfg.Abuse.Email != "" {
			return cfg.Abuse.Email
		}
		if cfg.General.Email != "" {
			return cfg.General.Email
		}
		return cfg.Admin.Email
	case "privacy":
		if cfg.DPO.Email != "" {
			return cfg.DPO.Email
		}
		if cfg.General.Email != "" {
			return cfg.General.Email
		}
		return cfg.Admin.Email
	default:
		if cfg.General.Email != "" {
			return cfg.General.Email
		}
		return cfg.Admin.Email
	}
}

func contactPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if !config.Get().Server.Pages.Contact.Enabled {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		renderContactForm(w, r, nil, "")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", tr(r, "api.method_not_allowed"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	if !validContactOrigin(r) {
		contactBlocked(r, "origin")
		writeHTTPError(w, r, tr(r, "contact.error.origin"), http.StatusBadRequest)
		return
	}
	if !validCSRF(r) {
		contactBlocked(r, "csrf")
		writeHTTPError(w, r, tr(r, "contact.error.refresh"), http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeHTTPError(w, r, tr(r, "contact.error.invalid"), http.StatusBadRequest)
		return
	}
	input := messageFromValues(r.Form)
	input.RequestHost = r.Host
	input.BaseURL = resolveBaseURL(r)
	input.Tunneled = strings.HasSuffix(strings.ToLower(r.Host), ".onion") || strings.HasSuffix(strings.ToLower(r.Host), ".i2p")
	service, err := currentContactService()
	if err != nil {
		writeHTTPError(w, r, tr(r, "contact.error.unavailable"), http.StatusServiceUnavailable)
		return
	}
	if input.Step == "confirm" {
		outcome, err := service.Confirm(r.Context(), input, requestClientIP(r), time.Now())
		if err != nil {
			contactBlocked(r, "review")
			renderContactForm(w, r, &input, tr(r, "contact.error.confirm"))
			return
		}
		renderContactOutcome(w, r, outcome)
		return
	}
	if input.Step == "edit" {
		renderContactForm(w, r, &input, "")
		return
	}
	if input.Step == "" {
		input.Step = "submit"
	}
	if input.Step == "submit" {
		if err := validateReported(input, r.Host); err != nil {
			contactBlocked(r, "content")
			renderContactForm(w, r, &input, tr(r, "contact.error.failure"))
			return
		}
	}
	review, err := service.Prepare(r.Context(), input, requestClientIP(r), time.Now())
	if err != nil {
		contactBlocked(r, reasonFor(err))
		renderContactForm(w, r, &input, tr(r, "contact.error.failure"))
		return
	}
	if review.Silent {
		review.Silent = true
	}
	renderContactReview(w, r, input, review)
}

func apiContact(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if !config.Get().Server.Pages.Contact.Enabled {
		http.NotFound(w, r)
		return
	}
	service, err := currentContactService()
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "CONTACT_UNAVAILABLE", tr(r, "contact.error.unavailable"))
		return
	}
	if r.Method == http.MethodGet {
		token, err := service.IssueFormToken(time.Now())
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "CONTACT_UNAVAILABLE", tr(r, "contact.error.unavailable"))
			return
		}
		captchaDescriptor, err := contactCaptchaDescriptor(r, service)
		if err != nil {
			writeError(w, r, http.StatusInternalServerError, "CONTACT_UNAVAILABLE", tr(r, "contact.error.unavailable"))
			return
		}
		kind := r.URL.Query().Get("type")
		security := service.ValidSecurityID(r.URL.Query().Get("security_id"), time.Now())
		if kind == "security" && !security {
			writeError(w, r, http.StatusForbidden, "SECURITY_ID_REQUIRED", tr(r, "contact.error.security_id"))
			return
		}
		fields := contact.Fields(kind, security)
		types := []string{"abuse", "dmca", "privacy"}
		if security {
			types = append(types, "security")
		}
		if kind == "" {
			fields = append(contact.Fields("abuse", false), contact.Fields("dmca", false)...)
			fields = append(fields, contact.Fields("privacy", false)...)
		}
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"fields": fields, "captcha": captchaDescriptor, "form_token": token, "types": types, "security_url": buildURL(r, "/server/security"), "abuse_email": publicAbuseEmail()}})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		writeError(w, r, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", tr(r, "api.method_not_allowed"))
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256<<10)
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&raw); err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", tr(r, "contact.error.invalid"))
		return
	}
	values := url.Values{}
	for key, value := range raw {
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			var boolean bool
			if err := json.Unmarshal(value, &boolean); err == nil {
				text = strconv.FormatBool(boolean)
			} else {
				var number json.Number
				if err := json.Unmarshal(value, &number); err != nil {
					writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", tr(r, "contact.error.invalid"))
					return
				}
				text = number.String()
			}
		}
		values.Set(key, text)
	}
	if values.Has("website") {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", tr(r, "contact.error.invalid"))
		return
	}
	input := messageFromValues(values)
	input.RequestHost = r.Host
	input.BaseURL = resolveBaseURL(r)
	input.Tunneled = strings.HasSuffix(strings.ToLower(r.Host), ".onion") || strings.HasSuffix(strings.ToLower(r.Host), ".i2p")
	if input.Step == "confirm" {
		outcome, err := service.Confirm(r.Context(), input, requestClientIP(r), time.Now())
		if err != nil {
			contactBlocked(r, "review")
			writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", tr(r, "contact.error.confirm"))
			return
		}
		if outcome.Silent {
			writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"tracking_id": ""}})
			return
		}
		if outcome.TrackingID != "" {
			message := i18n.Format(r.Header.Get("X-GitMessages-Language"), "contact.received_tracking", map[string]string{"tracking_id": outcome.TrackingID}) + ackMessage(r, outcome.Sent)
			writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"tracking_id": outcome.TrackingID, "message": message}})
			return
		}
		writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"message": contactSuccessMessage(r)}})
		return
	}
	if err := validateReported(input, r.Host); err != nil {
		contactBlocked(r, "content")
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", tr(r, "contact.error.failure"))
		return
	}
	review, err := service.Prepare(r.Context(), input, requestClientIP(r), time.Now())
	if err != nil {
		reason := reasonFor(err)
		contactBlocked(r, reason)
		if reason == "captcha" {
			writeContactCaptchaError(w, r, service)
			return
		}
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", tr(r, "contact.error.failure"))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"review": map[string]string{"type": review.Type, "email": review.Email, "subject": review.Subject}, "review_token": review.Token, "confirm_after_seconds": review.ConfirmAfter}})
}

func writeContactCaptchaError(w http.ResponseWriter, r *http.Request, service *contact.Service) {
	descriptor, err := contactCaptchaDescriptor(r, service)
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "VALIDATION_FAILED", tr(r, "contact.error.failure"))
		return
	}
	writeJSON(w, r, http.StatusBadRequest, map[string]any{"ok": false, "error": "VALIDATION_FAILED", "message": tr(r, "contact.error.failure"), "details": map[string]any{"field": "captcha", "captcha": descriptor}})
}

func getContactStore() *database.Store {
	contactStoreMu.RLock()
	defer contactStoreMu.RUnlock()
	return contactStore
}

func messageFromValues(values url.Values) contact.Message {
	fields := map[string]string{}
	metadata := map[string]bool{"csrf_token": true, "type": true, "name": true, "email": true, "subject": true, "message": true, "website": true, "form_token": true, "captcha_id": true, "captcha_answer": true, "captcha_response": true, "g-recaptcha-response": true, "h-captcha-response": true, "step": true, "review_token": true, "confirmed": true, "security_id": true}
	for key, items := range values {
		if len(items) > 0 && !metadata[key] {
			fields[key] = items[0]
		}
	}
	response := values.Get("captcha_response")
	if response == "" {
		response = values.Get("g-recaptcha-response")
	}
	if response == "" {
		response = values.Get("h-captcha-response")
	}
	return contact.Message{Type: values.Get("type"), Name: values.Get("name"), Email: values.Get("email"), Subject: values.Get("subject"), Body: values.Get("message"), Fields: fields, Website: values.Get("website"), FormToken: values.Get("form_token"), CaptchaID: values.Get("captcha_id"), CaptchaAnswer: values.Get("captcha_answer"), CaptchaResponse: response, Step: values.Get("step"), ReviewToken: values.Get("review_token"), Confirmed: values.Get("confirmed") == "true" || values.Get("confirmed") == "on", SecurityID: values.Get("security_id")}
}

func contactCaptchaProvider(r *http.Request) string {
	provider := strings.ToLower(strings.TrimSpace(config.Get().Server.Pages.Contact.Captcha))
	host := strings.ToLower(r.URL.Hostname())
	if host == "" {
		host = strings.ToLower(r.Host)
	}
	if strings.HasSuffix(host, ".onion") || strings.HasSuffix(host, ".i2p") || provider == "" {
		return "simple"
	}
	return provider
}

func contactCaptchaDescriptor(r *http.Request, service *contact.Service) (map[string]any, error) {
	id, question, err := service.NewCaptcha(r.Context())
	if err != nil {
		return nil, err
	}
	fallback := map[string]any{"provider": "simple", "captcha_id": id, "question": question}
	provider := contactCaptchaProvider(r)
	if provider == "simple" {
		return fallback, nil
	}
	return map[string]any{"provider": provider, "site_key": config.Get().Server.Pages.Contact.CaptchaSiteKey, "fallback": fallback}, nil
}

func validContactOrigin(r *http.Request) bool {
	value := r.Header.Get("Origin")
	if value == "" {
		value = r.Header.Get("Referer")
	}
	if value == "" && r.Header.Get("Sec-Fetch-Site") == "" {
		return false
	}
	if value != "" {
		parsed, err := url.Parse(value)
		if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
			return false
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return false
		}
	}
	return r.Header.Get("Sec-Fetch-Site") == "" || r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

func validateReported(input contact.Message, host string) error {
	if input.Type != "abuse" && input.Type != "dmca" {
		return nil
	}
	if !contact.ValidReportedURL(input.Fields["reported_url"], host) {
		return errors.New("reported URL invalid")
	}
	return nil
}

func reasonFor(err error) string {
	switch {
	case strings.Contains(err.Error(), "captcha"):
		return "captcha"
	case strings.Contains(err.Error(), "replay"):
		return "replay"
	case strings.Contains(err.Error(), "rate"):
		return "rate_global"
	case strings.Contains(err.Error(), "banned"):
		return "banned"
	case strings.Contains(err.Error(), "content"):
		return "content"
	default:
		return "shape"
	}
}

func contactBlocked(r *http.Request, reason string) {
	log.Printf("contact.spam_blocked reason=%s ip=%s", reason, requestClientIP(r))
}

func publicAbuseEmail() string {
	cfg := config.Get().Server.Contact
	if cfg.Abuse.Email != "" {
		return cfg.Abuse.Email
	}
	return cfg.General.Email
}

func renderContactForm(w http.ResponseWriter, r *http.Request, input *contact.Message, errorMessage string) {
	service, err := currentContactService()
	if err != nil {
		writeHTTPError(w, r, tr(r, "contact.error.unavailable"), http.StatusServiceUnavailable)
		return
	}
	kind := r.URL.Query().Get("type")
	if input != nil && input.Type != "" {
		kind = input.Type
	}
	securityID := r.URL.Query().Get("security_id")
	security := service.ValidSecurityID(securityID, time.Now())
	if kind != "" && kind != "abuse" && kind != "dmca" && kind != "privacy" {
		kind = ""
	}
	if security {
		kind = "security"
	} else {
		securityID = ""
	}
	if kind == "" {
		body := `<section class="section-card" aria-labelledby="contact-types"><h2 id="contact-types">` + template.HTMLEscapeString(tr(r, "contact.choose_type")) + `</h2><p>` + template.HTMLEscapeString(tr(r, "contact.intro")) + `</p><ul><li><a href="/server/contact?type=abuse">` + template.HTMLEscapeString(tr(r, "contact.abuse")) + `</a></li><li><a href="/server/contact?type=dmca">` + template.HTMLEscapeString(tr(r, "contact.dmca")) + `</a></li><li><a href="/server/contact?type=privacy">` + template.HTMLEscapeString(tr(r, "contact.privacy")) + `</a></li></ul></section><section class="section-card" aria-labelledby="contact-security"><h2 id="contact-security">` + template.HTMLEscapeString(tr(r, "contact.security_heading")) + `</h2><p>` + template.HTMLEscapeString(tr(r, "security.body")) + ` <a href="/server/security">` + template.HTMLEscapeString(tr(r, "security.title")) + `</a>.</p></section><section class="section-card" aria-labelledby="contact-abuse"><h2 id="contact-abuse">` + template.HTMLEscapeString(tr(r, "contact.abuse_heading")) + `</h2><p>`
		body += template.HTMLEscapeString(tr(r, "contact.abuse_details"))
		if address := publicAbuseEmail(); address != "" {
			body += ` ` + template.HTMLEscapeString(tr(r, "contact.email_abuse_prefix")) + ` <a href="mailto:` + template.HTMLEscapeString(address) + `">` + template.HTMLEscapeString(address) + `</a>.`
		}
		body += `</p></section>`
		page(w, r, tr(r, "contact.title"), template.HTML(body))
		return
	}
	token, err := service.IssueFormToken(time.Now())
	if err != nil {
		writeHTTPError(w, r, tr(r, "contact.error.unavailable"), http.StatusServiceUnavailable)
		return
	}
	captchaDescriptor, err := contactCaptchaDescriptor(r, service)
	if err != nil {
		writeHTTPError(w, r, tr(r, "contact.error.unavailable"), http.StatusServiceUnavailable)
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, `<p>%s</p>`, template.HTMLEscapeString(tr(r, "contact.intro")))
	if errorMessage != "" {
		fmt.Fprintf(&b, `<p role="alert">%s</p>`, template.HTMLEscapeString(errorMessage))
	}
	if input == nil {
		input = &contact.Message{Type: kind, Fields: map[string]string{}}
	}
	if input.Fields == nil {
		input.Fields = map[string]string{}
	}
	fallback := captchaDescriptor["fallback"]
	if fallback == nil {
		fallback = captchaDescriptor
	}
	fallbackMap := fallback.(map[string]any)
	captchaID := fallbackMap["captcha_id"].(string)
	question := fallbackMap["question"].(string)
	csrf := csrfTokenForPage(w, r)
	fmt.Fprintf(&b, `<form method="post" action="/server/contact"><input type="hidden" name="csrf_token" value="%s"><input type="hidden" name="type" value="%s"><input type="hidden" name="form_token" value="%s"><input type="hidden" name="captcha_id" value="%s"><input type="hidden" name="security_id" value="%s"><div class="contact-honeypot" hidden aria-hidden="true"><label>Leave this field empty<input type="text" name="website" tabindex="-1" autocomplete="off"></label></div>`, template.HTMLEscapeString(csrf), template.HTMLEscapeString(kind), template.HTMLEscapeString(token), template.HTMLEscapeString(captchaID), template.HTMLEscapeString(securityID))
	addInput(r, &b, "name", tr(r, "contact.name"), "text", input.Name, 100, true)
	addInput(r, &b, "email", tr(r, "contact.email"), "email", input.Email, 254, true)
	if kind == "security" {
		for _, field := range []struct {
			k, l string
			max  int
		}{{"component", tr(r, "contact.field.component"), 255}, {"endpoint", tr(r, "contact.field.endpoint"), 2048}, {"severity", tr(r, "contact.field.severity"), 0}, {"summary", tr(r, "contact.field.summary"), 200}, {"reproduce", tr(r, "contact.field.reproduce"), 10000}, {"impact", tr(r, "contact.field.impact"), 5000}, {"suggested_fix", tr(r, "contact.field.suggested_fix"), 5000}, {"credit_preference", tr(r, "contact.field.credit_preference"), 0}, {"credit_handle", tr(r, "contact.field.credit_handle"), 100}} {
			addInput(r, &b, field.k, field.l, "text", input.Fields[field.k], field.max, field.k != "suggested_fix" && field.k != "endpoint" && field.k != "credit_handle")
		}
		addCheckbox(&b, "cve_requested", tr(r, "contact.field.cve_requested"), input.Fields["cve_requested"])
		addCheckbox(&b, "coordinated_disclosure", tr(r, "contact.field.coordinated_disclosure"), input.Fields["coordinated_disclosure"])
	} else {
		switch kind {
		case "abuse":
			addInput(r, &b, "reported_url", tr(r, "contact.field.reported_url"), "url", input.Fields["reported_url"], 2048, true)
			addInput(r, &b, "reason", tr(r, "contact.field.reason"), "text", input.Fields["reason"], 0, true)
		case "dmca":
			addInput(r, &b, "reported_url", tr(r, "contact.field.reported_url"), "url", input.Fields["reported_url"], 2048, true)
			addInput(r, &b, "copyrighted_work", tr(r, "contact.field.copyrighted_work"), "text", input.Fields["copyrighted_work"], 2000, true)
			addCheckbox(&b, "good_faith", tr(r, "contact.field.good_faith"), input.Fields["good_faith"])
			addCheckbox(&b, "accuracy", tr(r, "contact.field.accuracy"), input.Fields["accuracy"])
			addInput(r, &b, "signature", tr(r, "contact.field.signature"), "text", input.Fields["signature"], 100, true)
		case "privacy":
			addInput(r, &b, "request_kind", tr(r, "contact.field.request_kind"), "text", input.Fields["request_kind"], 0, true)
		}
	}
	addInput(r, &b, "subject", tr(r, "contact.subject"), "text", input.Subject, 200, true)
	fmt.Fprintf(&b, `<label for="contact-message">%s <span aria-hidden="true">*</span></label><textarea id="contact-message" name="message" required maxlength="5000">%s</textarea><fieldset><legend>%s</legend>`, template.HTMLEscapeString(tr(r, "contact.message")), template.HTMLEscapeString(input.Body), template.HTMLEscapeString(tr(r, "contact.captcha")))
	provider := captchaDescriptor["provider"].(string)
	switch provider {
	case "recaptcha":
		fmt.Fprintf(&b, `<div class="g-recaptcha" data-sitekey="%s"></div><noscript><label for="captcha-answer">%s</label><input id="captcha-answer" name="captcha_answer" inputmode="numeric" required></noscript><script src="https://www.google.com/recaptcha/api.js" async defer></script>`, template.HTMLEscapeString(config.Get().Server.Pages.Contact.CaptchaSiteKey), template.HTMLEscapeString(i18n.Format(r.Header.Get("X-GitMessages-Language"), "contact.captcha_question", map[string]string{"question": question})))
	case "hcaptcha":
		fmt.Fprintf(&b, `<div class="h-captcha" data-sitekey="%s"></div><noscript><label for="captcha-answer">%s</label><input id="captcha-answer" name="captcha_answer" inputmode="numeric" required></noscript><script src="https://js.hcaptcha.com/1/api.js" async defer></script>`, template.HTMLEscapeString(config.Get().Server.Pages.Contact.CaptchaSiteKey), template.HTMLEscapeString(i18n.Format(r.Header.Get("X-GitMessages-Language"), "contact.captcha_question", map[string]string{"question": question})))
	default:
		fmt.Fprintf(&b, `<label for="captcha-answer">%s</label><input id="captcha-answer" name="captcha_answer" inputmode="numeric" required>`, template.HTMLEscapeString(i18n.Format(r.Header.Get("X-GitMessages-Language"), "contact.captcha_question", map[string]string{"question": question})))
	}
	b.WriteString(`</fieldset><button type="submit">` + template.HTMLEscapeString(tr(r, "contact.send")) + `</button></form><p>` + template.HTMLEscapeString(tr(r, "security.body")) + ` <a href="/server/security">` + template.HTMLEscapeString(tr(r, "security.title")) + `</a>.</p>`)
	page(w, r, tr(r, "contact.title"), template.HTML(b.String()))
}

func addInput(r *http.Request, b *strings.Builder, name, label, kind, value string, max int, required bool) {
	mark := ` <span aria-hidden="true">*</span>`
	if !required {
		mark = ` <span>(` + template.HTMLEscapeString(tr(r, "contact.optional")) + `)</span>`
	}
	fmt.Fprintf(b, `<label for="contact-%s">%s%s</label><input id="contact-%s" type="%s" name="%s" value="%s"`, name, template.HTMLEscapeString(label), mark, name, kind, name, template.HTMLEscapeString(value))
	if max > 0 {
		fmt.Fprintf(b, ` maxlength="%d"`, max)
	}
	if required {
		b.WriteString(` required`)
	}
	b.WriteString(`>`)
}

func addCheckbox(b *strings.Builder, name, label, value string) {
	checked := ""
	if value == "true" || value == "on" {
		checked = " checked"
	}
	fmt.Fprintf(b, `<label><input type="checkbox" name="%s" value="true"%s required>%s <span aria-hidden="true">*</span></label>`, name, checked, label)
}

func renderContactReview(w http.ResponseWriter, r *http.Request, input contact.Message, review contact.Review) {
	csrf := csrfTokenForPage(w, r)
	fields := hiddenFields(input, csrf)
	typeLabel := input.Type
	switch input.Type {
	case "abuse":
		typeLabel = tr(r, "contact.abuse")
	case "dmca":
		typeLabel = tr(r, "contact.dmca")
	case "privacy":
		typeLabel = tr(r, "contact.privacy")
	case "security":
		typeLabel = tr(r, "contact.security_heading")
	}
	body := fmt.Sprintf(`<section class="section-card contact-review" aria-labelledby="contact-review-title"><h2 id="contact-review-title">%s</h2><p><strong>%s:</strong> %s</p><p><strong>%s:</strong> %s</p><p><strong>%s:</strong> %s</p><form method="post" action="/server/contact">%s<input type="hidden" name="step" value="confirm"><input type="hidden" name="review_token" value="%s"><label><input type="checkbox" name="confirmed" value="true" required>%s</label><button type="submit">%s</button></form><form method="post" action="/server/contact">%s<input type="hidden" name="step" value="edit"><button type="submit">%s</button></form></section>`, template.HTMLEscapeString(tr(r, "contact.review_details")), template.HTMLEscapeString(tr(r, "contact.type")), template.HTMLEscapeString(typeLabel), template.HTMLEscapeString(tr(r, "contact.email_label")), template.HTMLEscapeString(input.Email), template.HTMLEscapeString(tr(r, "contact.subject")), template.HTMLEscapeString(input.Subject), fields, template.HTMLEscapeString(review.Token), template.HTMLEscapeString(tr(r, "contact.confirm_statement")), template.HTMLEscapeString(tr(r, "contact.confirm")), fields, template.HTMLEscapeString(tr(r, "contact.edit")))
	page(w, r, tr(r, "contact.review_title"), template.HTML(body))
}

func hiddenFields(input contact.Message, csrf string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<input type="hidden" name="csrf_token" value="%s">`, template.HTMLEscapeString(csrf))
	vals := map[string]string{"type": input.Type, "name": input.Name, "email": input.Email, "subject": input.Subject, "message": input.Body, "security_id": input.SecurityID, "website": input.Website}
	for k, v := range input.Fields {
		vals[k] = v
	}
	for k, v := range vals {
		fmt.Fprintf(&b, `<input type="hidden" name="%s" value="%s">`, template.HTMLEscapeString(k), template.HTMLEscapeString(v))
	}
	return b.String()
}

func renderContactSuccess(w http.ResponseWriter, r *http.Request) {
	message := contactSuccessMessage(r)
	page(w, r, tr(r, "contact.received"), template.HTML(`<section class="section-card" aria-labelledby="contact-received"><h2 id="contact-received">`+template.HTMLEscapeString(tr(r, "contact.received"))+`</h2><p role="status">`+template.HTMLEscapeString(message)+`</p></section>`))
}

func contactSuccessMessage(r *http.Request) string {
	message := config.Get().Server.Pages.Contact.SuccessMessage
	if message == "" || message == "Thank you for your report." {
		return tr(r, "contact.thank_you")
	}
	return message
}

func renderContactOutcome(w http.ResponseWriter, r *http.Request, outcome contact.Outcome) {
	if outcome.Silent {
		renderContactSuccess(w, r)
		return
	}
	if outcome.TrackingID != "" {
		message := i18n.Format(r.Header.Get("X-GitMessages-Language"), "contact.received_tracking", map[string]string{"tracking_id": outcome.TrackingID}) + ackMessage(r, outcome.Sent)
		page(w, r, tr(r, "contact.received"), template.HTML(`<section class="section-card" aria-labelledby="contact-received"><h2 id="contact-received">`+template.HTMLEscapeString(tr(r, "contact.received"))+`</h2><p role="status">`+template.HTMLEscapeString(message)+`</p></section>`))
		return
	}
	renderContactSuccess(w, r)
}

func ackMessage(r *http.Request, sent bool) string {
	if sent {
		return tr(r, "contact.email_ack")
	}
	return ""
}

func contactSecurityPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/security" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	var body strings.Builder
	body.WriteString(`<section class="section-card" aria-labelledby="security-reporting-details"><h2 id="security-reporting-details">` + template.HTMLEscapeString(tr(r, "page.details")) + `</h2><dl class="security-details">`)
	for _, line := range strings.Split(securityTextBody(r), "\n") {
		field, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		field, value = strings.TrimSpace(field), strings.TrimSpace(value)
		fmt.Fprintf(&body, "<dt>%s</dt><dd>", template.HTMLEscapeString(field))
		parsed, err := url.Parse(value)
		if err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https" || parsed.Scheme == "mailto") {
			fmt.Fprintf(&body, `<a href="%s">%s</a>`, template.HTMLEscapeString(value), template.HTMLEscapeString(value))
		} else {
			body.WriteString(template.HTMLEscapeString(value))
		}
		body.WriteString("</dd>")
	}
	body.WriteString("</dl><p>" + template.HTMLEscapeString(tr(r, "security.body")) + "</p>")
	fmt.Fprintf(&body, `<p><a href="%s">%s</a></p>`, template.HTMLEscapeString(BuildURL(r, "/server/security/policy")), template.HTMLEscapeString(tr(r, "security.policy")))
	body.WriteString(`</section>`)
	if config.Get().Server.Pages.Contact.Enabled {
		body.WriteString(`<section class="section-card" aria-labelledby="security-other-reports"><h2 id="security-other-reports">` + template.HTMLEscapeString(tr(r, "security.other_reports")) + `</h2><p>` + template.HTMLEscapeString(tr(r, "security.other_reports_body")) + ` <a href="` + template.HTMLEscapeString(BuildURL(r, "/server/contact")) + `">` + template.HTMLEscapeString(tr(r, "contact.title")) + `</a>.</p></section>`)
	}
	page(w, r, tr(r, "security.title"), template.HTML(body.String()))
}

func wellKnownOverrideAt(dataDir, name string) ([]byte, bool) {
	if name != "security.txt" {
		return nil, false
	}
	path := filepath.Join(dataDir, "web", ".well-known", name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, false
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer func() { _ = file.Close() }()
	body, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
	if err != nil || len(body) > 1<<20 {
		return nil, false
	}
	return body, true
}

func wellKnownOverride(name string) ([]byte, bool) {
	return wellKnownOverrideAt(paths.GetDirectories().Data, name)
}

func validSecurityText(body []byte) bool {
	contact, policy, expires := false, false, false
	for _, line := range strings.Split(string(body), "\n") {
		field, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(field)) {
		case "contact":
			parsed, err := url.Parse(strings.TrimSpace(value))
			valid := err == nil && ((parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != "" || parsed.Scheme == "mailto" && parsed.Opaque != "")
			contact = contact || valid
		case "policy":
			parsed, err := url.Parse(strings.TrimSpace(value))
			policy = policy || err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && parsed.Host != ""
		case "expires":
			_, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
			expires = expires || err == nil
		}
	}
	return contact && policy && expires
}

func securityTextBody(r *http.Request) string {
	cfg := config.Get()
	server := cfg.Server
	_, fqdn, _ := GetURLVars(r)
	onion := server.Tor.OnionAddress != "" && strings.EqualFold(fqdn, server.Tor.OnionAddress)
	if !onion {
		if body, ok := wellKnownOverride("security.txt"); ok && validSecurityText(body) {
			return string(body)
		}
	}
	reportURL := strings.TrimSpace(cfg.Web.Security.ReportURL)
	if reportURL == "" {
		reportURL = "https://github.com/apimgr/gitmessages/security/advisories/new"
	}
	var body strings.Builder
	fmt.Fprintf(&body, "Contact: %s\n", reportURL)
	if server.Pages.Contact.Enabled {
		if store := getContactStore(); store != nil {
			if secret, err := contact.InstallationSecret(r.Context(), store); err == nil {
				fmt.Fprintf(&body, "Contact: %s\n", BuildURL(r, "/server/contact?security_id="+url.QueryEscape(contact.SecurityID(secret, time.Now()))))
			}
		}
	}
	securityEmail := strings.TrimSpace(cfg.Web.Security.Contact)
	if securityEmail == "" {
		securityEmail = strings.TrimSpace(server.Contact.Security.Email)
	}
	if onion {
		securityEmail = ""
	}
	if securityEmail != "" {
		fmt.Fprintf(&body, "Contact: mailto:%s\n", securityEmail)
	}
	if cfg.Web.Security.PublishPGPKey {
		if _, fingerprint, available := pgpPublicInfo(); available {
			fmt.Fprintf(&body, "Encryption: %s\n# PGP-Fingerprint: %s\n", BuildURL(r, "/.well-known/pgp-key.asc"), fingerprint)
		}
	}
	fmt.Fprintf(&body, "Policy: %s\nExpires: %s\n", BuildURL(r, "/server/security/policy"), time.Now().UTC().AddDate(1, 0, 0).Format("2006-01-02T15:04:05Z"))
	if !onion {
		fmt.Fprintf(&body, "Preferred-Languages: %s\n", strings.Join(i18n.Supported(), ", "))
	}
	return body.String()
}

func contactSecurityTxt(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/.well-known/security.txt" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeText(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, securityTextBody(r))
	}
}

func llmsTextBody(r *http.Request) string {
	cfg := config.Get()
	base := BuildURL(r, APIBasePath())
	var body strings.Builder
	fmt.Fprintf(&body, "# %s API\n> Curated Git commit conventions, examples, and a stateless commit message builder.\n\n## API\nBase URL: %s\nAuthentication: Public REST reads and stateless message building need no token. An owner token is required for token management and MCP. Create an owner token with POST %s; linked tokens can be created at the token management endpoint.\nMCP: %s (Streamable HTTP; Bearer token required)\nAliases: %s, %s\n", seoBrandingTitle(), base, BuildURL(r, APIBasePath()+"/server/preferences/tokens/primary"), BuildURL(r, APIBasePath()+"/server/mcp"), BuildURL(r, "/api/mcp"), BuildURL(r, "/mcp"))
	limit := cfg.Server.RateLimit.Read
	if limit.Requests <= 0 {
		limit.Requests = 120
	}
	if limit.Window <= 0 {
		limit.Window = 60
	}
	fmt.Fprintf(&body, "Rate limit: %d requests/minute for reads; write limits follow server.rate_limit.write\n", limit.Requests*60/limit.Window)
	body.WriteString("\n## Endpoints\n")
	fmt.Fprintf(&body, "- GET %s - Health check page (no auth)\n- GET %s - Server information page (no auth)\n", BuildURL(r, "/server/healthz"), BuildURL(r, "/server/about"))
	for _, operation := range apiOperationDefinitions(r) {
		if !apiOperationListedNow(operation) {
			continue
		}
		if operation.Path == APIBasePath()+"/server/contact" || strings.HasPrefix(operation.OperationID, "mcpEndpoint") {
			continue
		}
		description := strings.TrimSpace(operation.Summary)
		if description == "" {
			description = operation.OperationID
		}
		if operation.Auth != "" && operation.Auth != "none" {
			description += " (" + operation.Auth + " authentication)"
		}
		fmt.Fprintf(&body, "- %s %s - %s\n", operation.Method, BuildURL(r, operation.Path), description)
	}
	fmt.Fprintf(&body, "- POST %s - MCP endpoint (Bearer token required)\n", BuildURL(r, APIBasePath()+"/server/mcp"))
	body.WriteString("\n## Capabilities\n- Browse commit types, conventions, and templates\n- Retrieve random examples\n- Build a commit message without submitting it to the server\n\n## Contact\nAPI issues: consult the API documentation\n")
	_, fqdn, _ := GetURLVars(r)
	securityEmail := strings.TrimSpace(cfg.Web.Security.Contact)
	if securityEmail == "" {
		securityEmail = strings.TrimSpace(cfg.Server.Contact.Security.Email)
	}
	if cfg.Server.Tor.OnionAddress != "" && strings.EqualFold(fqdn, cfg.Server.Tor.OnionAddress) {
		securityEmail = ""
	}
	if securityEmail != "" {
		fmt.Fprintf(&body, "Security: mailto:%s\n", securityEmail)
	} else {
		body.WriteString("Security: use the private vulnerability reporting URL in security.txt\n")
	}
	return body.String()
}

func llmsTxt(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/.well-known/llms.txt" && r.URL.Path != "/llms.txt" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeText(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, llmsTextBody(r))
	}
}

func apiContactSecurity(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != APIBasePath()+"/server/security" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	cfg := config.Get()
	server := cfg.Server
	contacts := []map[string]string{{"type": "github", "value": cfg.Web.Security.ReportURL}}
	if contacts[0]["value"] == "" {
		contacts[0]["value"] = "https://github.com/apimgr/gitmessages/security/advisories/new"
	}
	if server.Pages.Contact.Enabled {
		if store := getContactStore(); store != nil {
			if secret, err := contact.InstallationSecret(r.Context(), store); err == nil {
				securityURL := BuildURL(r, "/server/contact?security_id="+url.QueryEscape(contact.SecurityID(secret, time.Now())))
				contacts = append(contacts, map[string]string{"type": "contact_form", "value": securityURL})
			}
		}
	}
	_, fqdn, _ := GetURLVars(r)
	securityEmail := strings.TrimSpace(cfg.Web.Security.Contact)
	if securityEmail == "" {
		securityEmail = strings.TrimSpace(server.Contact.Security.Email)
	}
	if server.Tor.OnionAddress != "" && strings.EqualFold(fqdn, server.Tor.OnionAddress) {
		securityEmail = ""
	}
	if securityEmail != "" {
		contacts = append(contacts, map[string]string{"type": "email", "value": securityEmail})
	}
	var pgpURL, fingerprint any
	pgpAvailable := false
	if _, value, available := pgpPublicInfo(); available {
		fingerprint = value
		pgpAvailable = true
		if cfg.Web.Security.PublishPGPKey {
			pgpURL = BuildURL(r, "/.well-known/pgp-key.asc")
		}
	}
	data := map[string]any{
		"contacts":   contacts,
		"expires":    time.Now().UTC().AddDate(1, 0, 0).Format(time.RFC3339),
		"pgp":        map[string]any{"available": pgpAvailable, "url": pgpURL, "fingerprint": fingerprint},
		"policy_url": BuildURL(r, "/server/security/policy"),
		"thanks_url": BuildURL(r, "/server/security/thanks"),
	}
	if server.Pages.Contact.Enabled {
		data["report_url"] = BuildURL(r, "/server/contact")
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": data})
}

func buildURL(r *http.Request, path string) string { return BuildURL(r, path) }
func resolveBaseURL(r *http.Request) string        { return BuildURL(r, "") }

func pgpKeyPath() string { return filepath.Join(config.ConfigDirectory(), "security", "pgp.pub.asc") }

func pgpPublicInfo() ([]byte, string, bool) {
	key, err := os.ReadFile(pgpKeyPath())
	if err != nil {
		return nil, "", false
	}
	ring, err := openpgp.ReadArmoredKeyRing(strings.NewReader(string(key)))
	if err != nil || len(ring) == 0 {
		return nil, "", false
	}
	fingerprint := strings.ToUpper(hex.EncodeToString(ring[0].PrimaryKey.Fingerprint[:]))
	return key, fingerprint, true
}

func pgpKeyPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/.well-known/pgp-key.asc" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeText(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	key, _, available := pgpPublicInfo()
	if !config.Get().Web.Security.PublishPGPKey || !available {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/pgp-keys; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodGet {
		_, _ = w.Write(key)
	}
}

func disclosurePolicy(r *http.Request) string {
	if configured := strings.TrimSpace(config.Get().Web.Security.Policy); configured != "" {
		return configured
	}
	_, host, _ := GetURLVars(r)
	return i18n.Format(r.Header.Get("X-GitMessages-Language"), "security.policy.default", map[string]string{"host": host})
}

func securityPolicyPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/security/policy" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	content := string(normalizeOperatorHeadings(string(renderMarkdown(disclosurePolicy(r)))))
	body := `<section class="section-card" aria-labelledby="security-policy-details"><h2 id="security-policy-details">` + template.HTMLEscapeString(tr(r, "page.details")) + `</h2>` + content + `</section>`
	page(w, r, tr(r, "security.policy"), template.HTML(body))
}

func apiSecurityPolicy(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != APIBasePath()+"/server/security/policy" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"policy": disclosurePolicy(r)}})
}

type thanksEntry struct {
	Credit   string `json:"credit"`
	Date     string `json:"date"`
	Severity string `json:"severity"`
}

func securityThanks(ctx context.Context) ([]thanksEntry, error) {
	store := getContactStore()
	if store == nil {
		return []thanksEntry{}, nil
	}
	rows, err := store.Read().QueryContext(ctx, "SELECT credit, disclosed_on, severity FROM security_thanks ORDER BY disclosed_on DESC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	entries := []thanksEntry{}
	for rows.Next() {
		var entry thanksEntry
		if err := rows.Scan(&entry.Credit, &entry.Date, &entry.Severity); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func apiSecurityThanks(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != APIBasePath()+"/server/security/thanks" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	entries, err := securityThanks(r.Context())
	if err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "SECURITY_UNAVAILABLE", tr(r, "contact.error.security_unavailable"))
		return
	}
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]any{"entries": entries}})
}

func securityThanksPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/security/thanks" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	entries, err := securityThanks(r.Context())
	if err != nil {
		writeHTTPError(w, r, tr(r, "contact.error.security_unavailable"), http.StatusServiceUnavailable)
		return
	}
	var body strings.Builder
	body.WriteString(`<section class="section-card" aria-labelledby="security-thanks-details"><h2 id="security-thanks-details">` + template.HTMLEscapeString(tr(r, "page.details")) + `</h2><p>` + template.HTMLEscapeString(tr(r, "security.thanks.intro")) + `</p><div class="table-wrapper"><table class="data-table"><caption>` + template.HTMLEscapeString(tr(r, "security.thanks.title")) + `</caption><thead><tr><th scope="col">` + template.HTMLEscapeString(tr(r, "security.thanks.researcher")) + `</th><th scope="col">` + template.HTMLEscapeString(tr(r, "security.thanks.date")) + `</th><th scope="col">` + template.HTMLEscapeString(tr(r, "security.thanks.severity")) + `</th></tr></thead><tbody>`)
	if len(entries) == 0 {
		body.WriteString(`<tr><td colspan="3"><div class="empty-state">` + template.HTMLEscapeString(tr(r, "security.thanks.empty")) + ` <a href="/server/security/policy">` + template.HTMLEscapeString(tr(r, "security.policy")) + `</a></div></td></tr>`)
	}
	for _, entry := range entries {
		fmt.Fprintf(&body, "<tr><td>%s</td><td>%s</td><td>%s</td></tr>", template.HTMLEscapeString(entry.Credit), template.HTMLEscapeString(entry.Date), template.HTMLEscapeString(entry.Severity))
	}
	body.WriteString("</tbody></table></div></section>")
	page(w, r, tr(r, "security.thanks.title"), template.HTML(body.String()))
}

func securityReportPage(w http.ResponseWriter, r *http.Request) {
	if !strings.HasPrefix(r.URL.Path, "/server/security/report/") {
		renderSecurityReportNotFound(w, r)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	if !method(w, r, http.MethodGet) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/server/security/report/")
	if id == "" || strings.Contains(id, "/") {
		renderSecurityReportNotFound(w, r)
		return
	}
	token := r.URL.Query().Get("token")
	digest := sha256.Sum256([]byte(token))
	store := getContactStore()
	if store == nil {
		renderSecurityReportNotFound(w, r)
		return
	}
	var expected, status, comment string
	var lastUsed, closed, disclosureDate sql.NullString
	err := store.Read().QueryRowContext(r.Context(), "SELECT token_hash,status,researcher_comment,token_last_used,closed_at,disclosure_date FROM security_reports WHERE tracking_id = ?", id).Scan(&expected, &status, &comment, &lastUsed, &closed, &disclosureDate)
	if err != nil {
		renderSecurityReportNotFound(w, r)
		return
	}
	actual := hex.EncodeToString(digest[:])
	if token == "" || subtle.ConstantTimeCompare([]byte(expected), []byte(actual)) != 1 {
		renderSecurityReportNotFound(w, r)
		return
	}
	today := time.Now().UTC().Format("2006-01-02")
	if lastUsed.Valid && lastUsed.String >= today {
		renderSecurityReportNotFound(w, r)
		return
	}
	if closed.Valid {
		closedAt, parseErr := time.Parse(time.RFC3339, closed.String)
		if parseErr == nil && time.Since(closedAt) > 30*24*time.Hour {
			renderSecurityReportNotFound(w, r)
			return
		}
	}
	var affected int64
	err = store.Write(r.Context(), func(tx *sql.Tx) error {
		result, updateErr := tx.ExecContext(r.Context(), "UPDATE security_reports SET token_last_used = ? WHERE tracking_id = ? AND (token_last_used IS NULL OR token_last_used < ?)", today, id, today)
		if updateErr != nil {
			return updateErr
		}
		affected, updateErr = result.RowsAffected()
		return updateErr
	})
	if err != nil || affected != 1 {
		renderSecurityReportNotFound(w, r)
		return
	}
	labels := map[string]string{"received": "contact.status_received", "triaged": "contact.status_triaged", "confirmed": "contact.status_confirmed", "patching": "contact.status_patching", "disclosed": "contact.status_disclosed", "wont_fix": "contact.status_wont_fix"}
	labelKey := labels[status]
	label := tr(r, labelKey)
	if label == "" {
		renderSecurityReportNotFound(w, r)
		return
	}
	body := `<section class="section-card" aria-labelledby="security-status"><h2 id="security-status">` + template.HTMLEscapeString(tr(r, "contact.status_title")) + `</h2><p><strong>` + template.HTMLEscapeString(tr(r, "contact.status")) + `:</strong> ` + template.HTMLEscapeString(label) + `</p>`
	if comment != "" {
		body += `<p><strong>` + template.HTMLEscapeString(tr(r, "contact.maintainer_comment")) + `:</strong> ` + template.HTMLEscapeString(comment) + `</p>`
	}
	if disclosureDate.Valid {
		body += `<p><strong>` + template.HTMLEscapeString(tr(r, "contact.disclosure_date")) + `:</strong> <time datetime="` + template.HTMLEscapeString(disclosureDate.String) + `">` + template.HTMLEscapeString(disclosureDate.String) + `</time></p>`
	}
	body += `</section>`
	page(w, r, tr(r, "contact.status_title"), template.HTML(body))
}

func renderSecurityReportNotFound(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNotFound)
	message := tr(r, "errors.not_found")
	page(w, r, "404", template.HTML(`<section class="section-card" aria-labelledby="error-message"><h2 id="error-message">`+template.HTMLEscapeString(message)+`</h2></section>`))
}

func dpoPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/server/dpo" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	cfg := config.Get().Server.Contact.DPO
	emailAddress := cfg.Email
	if emailAddress == "" {
		emailAddress = config.Get().Server.Contact.General.Email
	}
	var body strings.Builder
	if cfg.Name != "" {
		fmt.Fprintf(&body, "<p>%s</p>", template.HTMLEscapeString(cfg.Name))
	}
	if emailAddress != "" {
		fmt.Fprintf(&body, `<p><a href="mailto:%s">%s</a></p>`, template.HTMLEscapeString(emailAddress), template.HTMLEscapeString(emailAddress))
	}
	body.WriteString(`<section class="section-card" aria-labelledby="dpo-links"><h2 id="dpo-links">` + template.HTMLEscapeString(tr(r, "contact.privacy_requests")) + `</h2><p><a href="/server/privacy">` + template.HTMLEscapeString(tr(r, "contact.privacy_policy")) + `</a></p><p><a href="/server/security">` + template.HTMLEscapeString(tr(r, "contact.privacy_requests")) + `</a></p></section>`)
	page(w, r, tr(r, "contact.privacy_requests"), template.HTML(body.String()))
}

func apiDPO(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != APIBasePath()+"/server/dpo" {
		http.NotFound(w, r)
		return
	}
	if !method(w, r, http.MethodGet) {
		return
	}
	cfg := config.Get().Server.Contact.DPO
	emailAddress := cfg.Email
	if emailAddress == "" {
		emailAddress = config.Get().Server.Contact.General.Email
	}
	base := resolveBaseURL(r)
	writeJSON(w, r, http.StatusOK, map[string]any{"ok": true, "data": map[string]string{"name": cfg.Name, "email": emailAddress, "privacy_url": base + "/server/privacy"}})
}
