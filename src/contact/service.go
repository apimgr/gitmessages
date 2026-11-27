package contact

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/apimgr/gitmessages/src/database"
)

type Settings struct {
	MinFill, MaxAge, ReviewMin, ReviewMax       int
	PerIP, PerEmail, Global                     int
	Window                                      time.Duration
	MaxLinks, Strikes, BanHours, DuplicateHours int
	RejectMarkup, Honeypot                      bool
	BlockedWords, BlockedEmailDomains           []string
	ProjectPGPKey                               []byte
	PreviousSecrets                             [][]byte
	CaptchaProvider, CaptchaSecretKey           string
}

type Message struct {
	Type, Name, Email, Subject, Body                              string
	Fields                                                        map[string]string
	Website, FormToken, CaptchaID, CaptchaAnswer, CaptchaResponse string
	Step, ReviewToken                                             string
	Confirmed                                                     bool
	SecurityID                                                    string
	Tunneled                                                      bool
	RequestHost                                                   string
	BaseURL                                                       string
}

type Review struct {
	Type, Email, Subject, Token string
	ConfirmAfter                int
	Silent                      bool
}
type Outcome struct {
	TrackingID string
	Sent       bool
	Silent     bool
}

type Sender interface {
	SendContact(to, from, name, subject, body string, tunneled bool) error
}
type ResearcherSender interface {
	SendResearcher(to, subject, body string) error
}
type SecurityNotificationSender interface {
	SendSecurityNotification(to, subject, body, filename string, encrypted []byte, tunneled bool, trackingID, component, severity string) error
}
type EncryptedResearcherSender interface {
	SendResearcherEncrypted(to, subject string, encrypted []byte) error
}

type Service struct {
	store                 *database.Store
	secret, encryptionKey []byte
	settings              Settings
	sender                Sender
	mu                    sync.Mutex
	used                  map[string]time.Time
	counts                map[string][]time.Time
	strikes               map[string][]time.Time
	bans                  map[string]time.Time
	duplicates            map[string]time.Time
}

func New(store *database.Store, secret, encryptionKey []byte, settings Settings, sender Sender) (*Service, error) {
	if store == nil || len(secret) < 32 {
		return nil, errors.New("contact database and installation secret are required")
	}
	if len(encryptionKey) != 32 {
		return nil, errors.New("security encryption key must be 32 bytes")
	}
	if settings.Window <= 0 {
		settings.Window = time.Hour
	}
	settings.PreviousSecrets = cloneSecrets(settings.PreviousSecrets)
	return &Service{store: store, secret: append([]byte(nil), secret...), encryptionKey: append([]byte(nil), encryptionKey...), settings: settings, sender: sender, used: map[string]time.Time{}, counts: map[string][]time.Time{}, strikes: map[string][]time.Time{}, bans: map[string]time.Time{}, duplicates: map[string]time.Time{}}, nil
}

func cloneSecrets(values [][]byte) [][]byte {
	result := make([][]byte, 0, len(values))
	for _, value := range values {
		result = append(result, append([]byte(nil), value...))
	}
	return result
}

func (s *Service) installationSecrets() [][]byte {
	return append([][]byte{s.secret}, s.settings.PreviousSecrets...)
}

func (s *Service) validSecurityID(value string, now time.Time) bool {
	valid := 0
	for _, secret := range s.installationSecrets() {
		if ValidSecurityID(secret, value, now) {
			valid = 1
		}
	}
	return valid == 1
}

// ValidSecurityID validates the active or still-graceful security-report link token.
func (s *Service) ValidSecurityID(value string, now time.Time) bool {
	return s.validSecurityID(value, now)
}

type FieldDescriptor struct {
	Name          string `json:"name"`
	Type          string `json:"type"`
	Required      bool   `json:"required"`
	MaxLength     *int   `json:"max_length"`
	RequiredWhen  any    `json:"required_when"`
	MaxLengthWhen any    `json:"max_length_when"`
}

func Fields(kind string, security bool) []FieldDescriptor {
	length := func(n int) *int { return &n }
	base := []FieldDescriptor{{Name: "name", Type: "text", Required: true, MaxLength: length(100)}, {Name: "email", Type: "email", Required: true, MaxLength: length(254)}, {Name: "type", Type: "select", Required: true}}
	if security {
		securityFields := []FieldDescriptor{{Name: "researcher_gpg", Type: "text", MaxLength: length(16384), MaxLengthWhen: []any{map[string]any{"prefix": "https://", "max_length": 2048}}}, {Name: "component", Type: "text", Required: true, MaxLength: length(255)}, {Name: "endpoint", Type: "text", MaxLength: length(2048)}, {Name: "severity", Type: "select", Required: true}, {Name: "summary", Type: "text", Required: true, MaxLength: length(200)}, {Name: "reproduce", Type: "textarea", Required: true, MaxLength: length(10000)}, {Name: "impact", Type: "textarea", Required: true, MaxLength: length(5000)}, {Name: "suggested_fix", Type: "textarea", MaxLength: length(5000)}, {Name: "cve_requested", Type: "checkbox"}, {Name: "disclosure_days", Type: "number"}, {Name: "credit_preference", Type: "select", Required: true}, {Name: "credit_handle", Type: "text", MaxLength: length(100), RequiredWhen: map[string]any{"field": "credit_preference", "equals": "handle"}}, {Name: "coordinated_disclosure", Type: "checkbox", Required: true}, {Name: "captcha", Type: "captcha", Required: true}}
		return append(base, securityFields...)
	}
	var typeSpecific []FieldDescriptor
	switch kind {
	case "abuse":
		typeSpecific = []FieldDescriptor{{Name: "reported_url", Type: "url", Required: true, MaxLength: length(2048), RequiredWhen: map[string]any{"field": "type", "in": []string{"abuse", "dmca"}}}, {Name: "reason", Type: "select", Required: true}}
	case "dmca":
		typeSpecific = []FieldDescriptor{{Name: "reported_url", Type: "url", Required: true, MaxLength: length(2048), RequiredWhen: map[string]any{"field": "type", "in": []string{"abuse", "dmca"}}}, {Name: "copyrighted_work", Type: "textarea", Required: true, MaxLength: length(2000)}, {Name: "good_faith", Type: "checkbox", Required: true}, {Name: "accuracy", Type: "checkbox", Required: true}, {Name: "signature", Type: "text", Required: true, MaxLength: length(100)}}
	case "privacy":
		typeSpecific = []FieldDescriptor{{Name: "request_kind", Type: "select", Required: true}}
	default:
		return nil
	}
	return append(append(append(base, typeSpecific...), FieldDescriptor{Name: "subject", Type: "text", Required: true, MaxLength: length(200)}, FieldDescriptor{Name: "message", Type: "textarea", Required: true, MaxLength: length(5000)}), FieldDescriptor{Name: "captcha", Type: "captcha", Required: true})
}

func (s *Service) IssueFormToken(now time.Time) (string, error) { return FormToken(s.secret, now) }

func (s *Service) NewCaptcha(ctx context.Context) (string, string, error) {
	a, b, op := randomInt(20), randomInt(20), "+"
	if a < b {
		a, b = b, a
	}
	if randomInt(2) == 1 {
		op = "-"
	}
	answer := a + b
	if op == "-" {
		answer = a - b
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(raw)
	digest := sha256.Sum256([]byte(strconv.Itoa(answer)))
	expires := time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano)
	err := s.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO captcha_challenges(id, answer_hash, expires_at) VALUES(?, ?, ?)", id, hex.EncodeToString(digest[:]), expires)
		return err
	})
	return id, fmt.Sprintf("%d %s %d", a, op, b), err
}

func randomInt(max int) int {
	raw := make([]byte, 4)
	if _, err := rand.Read(raw); err != nil {
		return 0
	}
	return int(raw[0])%max + 1
}

func (s *Service) verifyCaptcha(ctx context.Context, id, answer string) error {
	if id == "" || answer == "" {
		return errors.New("captcha required")
	}
	var stored, expires string
	err := s.store.Read().QueryRowContext(ctx, "SELECT answer_hash, expires_at FROM captcha_challenges WHERE id = ?", id).Scan(&stored, &expires)
	if err != nil {
		return errors.New("captcha invalid")
	}
	_ = s.store.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM captcha_challenges WHERE id = ?", id)
		return err
	})
	expiresAt, err := time.Parse(time.RFC3339Nano, expires)
	if err != nil || time.Now().After(expiresAt) {
		return errors.New("captcha expired")
	}
	digest := sha256.Sum256([]byte(strings.TrimSpace(answer)))
	if !hmac.Equal([]byte(stored), []byte(hex.EncodeToString(digest[:]))) {
		return errors.New("captcha invalid")
	}
	return nil
}

func (s *Service) verifyCaptchaForMessage(ctx context.Context, input Message, remoteIP string) error {
	provider := strings.ToLower(strings.TrimSpace(s.settings.CaptchaProvider))
	if provider == "" || provider == "simple" || input.Tunneled || input.CaptchaResponse == "" {
		return s.verifyCaptcha(ctx, input.CaptchaID, input.CaptchaAnswer)
	}
	var endpoint string
	switch provider {
	case "recaptcha":
		endpoint = "https://www.google.com/recaptcha/api/siteverify"
	case "hcaptcha":
		endpoint = "https://api.hcaptcha.com/siteverify"
	default:
		return errors.New("captcha provider is invalid")
	}
	return verifyCaptchaAt(ctx, endpoint, s.settings.CaptchaSecretKey, input.CaptchaResponse, remoteIP)
}

func verifyCaptchaAt(ctx context.Context, endpoint, secret, response, remoteIP string) error {
	if strings.TrimSpace(secret) == "" || strings.TrimSpace(response) == "" {
		return errors.New("captcha required")
	}
	form := url.Values{"secret": {secret}, "response": {response}}
	if remoteIP != "" {
		form.Set("remoteip", remoteIP)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return errors.New("captcha verification unavailable")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	result, err := client.Do(request)
	if err != nil {
		return errors.New("captcha verification unavailable")
	}
	defer func() { _ = result.Body.Close() }()
	if result.StatusCode != http.StatusOK {
		return errors.New("captcha verification unavailable")
	}
	var payload struct {
		Success bool `json:"success"`
	}
	if err := json.NewDecoder(io.LimitReader(result.Body, 1<<20)).Decode(&payload); err != nil || !payload.Success {
		return errors.New("captcha invalid")
	}
	return nil
}

func (s *Service) Prepare(ctx context.Context, input Message, ip string, now time.Time) (Review, error) {
	if err := validateShape(input); err != nil {
		s.strike(rateIP(input, ip), now)
		return Review{}, err
	}
	if input.Step == "edit" {
		return Review{}, errors.New("edit")
	}
	if input.Step == "confirm" {
		return Review{}, errors.New("confirm requires confirmation handler")
	}
	if err := s.consumeFormToken(input.FormToken, now); err != nil {
		s.strike(rateIP(input, ip), now)
		return Review{}, err
	}
	if err := s.verifyCaptchaForMessage(ctx, input, rateIP(input, ip)); err != nil {
		s.strike(rateIP(input, ip), now)
		return Review{}, err
	}
	if err := s.checkContent(input); err != nil {
		s.strike(rateIP(input, ip), now)
		return Review{}, err
	}
	if err := s.checkRate(rateIP(input, ip), input.Email, now, false); err != nil {
		return Review{}, err
	}
	if input.Type == "security" && !s.validSecurityID(input.SecurityID, now) {
		return Review{}, errors.New("invalid security mode")
	}
	bot := s.settings.Honeypot && input.Website != ""
	token, err := ReviewToken(s.secret, now, normalizedMessage(input), bot)
	if err != nil {
		return Review{}, err
	}
	return Review{Type: input.Type, Email: input.Email, Subject: input.Subject, Token: token, ConfirmAfter: s.settings.ReviewMin, Silent: bot}, nil
}

func validateShape(input Message) error {
	if input.Type != "abuse" && input.Type != "dmca" && input.Type != "privacy" && input.Type != "security" {
		return errors.New("invalid type")
	}
	if strings.ContainsAny(input.Name+input.Email+input.Subject, "\r\n\x00") {
		return errors.New("invalid field")
	}
	if len([]rune(strings.TrimSpace(input.Name))) > 100 || len([]rune(strings.TrimSpace(input.Email))) > 254 || len([]rune(strings.TrimSpace(input.Subject))) > 200 {
		return errors.New("field length exceeded")
	}
	parsed, err := mail.ParseAddress(input.Email)
	if err != nil || parsed.Address != strings.TrimSpace(input.Email) || strings.ContainsAny(input.Email, ",;<>\r\n") {
		return errors.New("invalid email")
	}
	for _, value := range allValues(input) {
		for _, char := range value {
			if char == '\u200b' || char == '\u200c' || char == '\u200d' || char == '\u202e' || char == '\u202d' || char == '\u2066' || char == '\u2067' || char == '\u2068' || char == '\u2069' {
				return errors.New("invalid field")
			}
		}
	}
	if input.Type != "security" && len([]rune(input.Body)) > 5000 {
		return errors.New("message too long")
	}
	if input.Type == "security" {
		size := 0
		for _, v := range allValues(input) {
			size += len([]rune(v))
		}
		if size > 40000 {
			return errors.New("security report too large")
		}
	}
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Email) == "" {
		return errors.New("required field missing")
	}
	if input.Type != "security" && (strings.TrimSpace(input.Subject) == "" || strings.TrimSpace(input.Body) == "") {
		return errors.New("required field missing")
	}
	if err := validateTypeFields(input); err != nil {
		return err
	}
	return nil
}

func validateTypeFields(input Message) error {
	allowed := map[string]bool{"reported_url": true, "reason": true, "copyrighted_work": true, "good_faith": true, "accuracy": true, "signature": true, "request_kind": true, "researcher_gpg": true, "component": true, "endpoint": true, "severity": true, "summary": true, "reproduce": true, "impact": true, "suggested_fix": true, "cve_requested": true, "disclosure_days": true, "credit_preference": true, "credit_handle": true, "coordinated_disclosure": true}
	for key := range input.Fields {
		if !allowed[key] {
			return fmt.Errorf("unknown field %s", key)
		}
	}
	required := func(key string) error {
		if strings.TrimSpace(input.Fields[key]) == "" {
			return fmt.Errorf("required field %s", key)
		}
		return nil
	}
	if input.Type == "abuse" || input.Type == "dmca" {
		if err := required("reported_url"); err != nil {
			return err
		}
	}
	switch input.Type {
	case "abuse":
		if err := required("reason"); err != nil {
			return err
		}
		if !oneOf(input.Fields["reason"], "illegal_content", "terms_violation", "harassment", "malware_phishing", "other") {
			return errors.New("invalid reason")
		}
	case "dmca":
		for _, key := range []string{"copyrighted_work", "good_faith", "accuracy", "signature"} {
			if err := required(key); err != nil {
				return err
			}
		}
		if input.Fields["good_faith"] != "true" && input.Fields["good_faith"] != "on" || input.Fields["accuracy"] != "true" && input.Fields["accuracy"] != "on" {
			return errors.New("required DMCA statement missing")
		}
	case "privacy":
		if err := required("request_kind"); err != nil {
			return err
		}
		if !oneOf(input.Fields["request_kind"], "access", "export", "deletion", "correction", "opt_out", "other") {
			return errors.New("invalid request kind")
		}
	case "security":
		for _, key := range []string{"component", "severity", "summary", "reproduce", "impact", "credit_preference", "coordinated_disclosure"} {
			if err := required(key); err != nil {
				return err
			}
		}
		if !oneOf(input.Fields["severity"], "critical", "high", "medium", "low", "informational") {
			return errors.New("invalid severity")
		}
		if !oneOf(input.Fields["credit_preference"], "name", "handle", "no", "anonymous") {
			return errors.New("invalid credit preference")
		}
		if input.Fields["credit_preference"] == "handle" && strings.TrimSpace(input.Fields["credit_handle"]) == "" {
			return errors.New("credit handle required")
		}
		if input.Fields["coordinated_disclosure"] != "true" && input.Fields["coordinated_disclosure"] != "on" {
			return errors.New("coordinated disclosure required")
		}
	}
	return nil
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func allValues(in Message) []string {
	out := []string{in.Type, in.Name, in.Email, in.Subject, in.Body, in.SecurityID}
	for _, v := range in.Fields {
		out = append(out, v)
	}
	return out
}

func normalizedMessage(in Message) any {
	fields := make(map[string]string, len(in.Fields))
	for k, v := range in.Fields {
		fields[k] = normalizedText(v)
	}
	return map[string]any{"type": in.Type, "name": normalizedText(in.Name), "email": normalizedText(in.Email), "subject": normalizedText(in.Subject), "message": normalizedText(in.Body), "fields": fields}
}

func (s *Service) consumeFormToken(token string, now time.Time) error {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return errors.New("invalid form token")
	}
	issued, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return errors.New("invalid form token")
	}
	age := now.Sub(time.Unix(issued, 0))
	if age < time.Duration(s.settings.MinFill)*time.Second || s.settings.MaxAge > 0 && age > time.Duration(s.settings.MaxAge)*time.Second {
		return errors.New("invalid form token")
	}
	valid := 0
	for _, secret := range s.installationSecrets() {
		mac := hmac.New(sha256.New, secret)
		_, _ = fmt.Fprintf(mac, "contact-form|%s|%s", parts[0], parts[1])
		valid |= subtle.ConstantTimeCompare([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(parts[2]))
	}
	if valid != 1 {
		return errors.New("invalid form token")
	}
	key := parts[0] + "." + parts[1]
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireUsed(now)
	if _, ok := s.used[key]; ok {
		return errors.New("replay")
	}
	if len(s.used) >= 50000 {
		for old := range s.used {
			delete(s.used, old)
			break
		}
	}
	expires := time.Time{}
	if s.settings.MaxAge > 0 {
		expires = now.Add(time.Duration(s.settings.MaxAge) * time.Second)
	}
	s.used[key] = expires
	return nil
}

func (s *Service) expireUsed(now time.Time) {
	for key, expires := range s.used {
		if !expires.IsZero() && now.After(expires) {
			delete(s.used, key)
		}
	}
}

func (s *Service) checkContent(input Message) error {
	if input.Type == "security" {
		return nil
	}
	fields := []string{input.Type, input.Name, input.Email, input.Subject, input.Body}
	for key, value := range input.Fields {
		switch key {
		case "reported_url", "reason", "copyrighted_work", "good_faith", "accuracy", "signature", "request_kind":
			fields = append(fields, value)
		}
	}
	if s.settings.RejectMarkup {
		markup := regexp.MustCompile(`(?i)<\s*/?[a-z!]|&(?:lt|#0*60|#x0*3c|gt|#0*62|#x0*3e);|\[(?:/?(?:url|img|b))`)
		for _, v := range fields {
			if markup.MatchString(v) {
				return errors.New("content rejected")
			}
		}
	}
	domain := strings.ToLower(strings.TrimSpace(strings.Split(input.Email, "@")[len(strings.Split(input.Email, "@"))-1]))
	for _, blocked := range s.settings.BlockedEmailDomains {
		if strings.EqualFold(domain, blocked) {
			return errors.New("content rejected")
		}
	}
	for _, word := range s.settings.BlockedWords {
		if strings.Contains(strings.ToLower(strings.Join(fields, " ")), strings.ToLower(word)) {
			return errors.New("content rejected")
		}
	}
	if linkPattern.MatchString(input.Name) || linkPattern.MatchString(input.Subject) {
		return errors.New("content rejected")
	}
	linkFields := []string{input.Body}
	for key, value := range input.Fields {
		switch key {
		case "reported_url", "reason", "copyrighted_work", "good_faith", "accuracy", "signature", "request_kind", "researcher_gpg", "component", "endpoint", "severity", "summary", "reproduce", "impact", "suggested_fix", "disclosure_days", "credit_handle":
			linkFields = append(linkFields, value)
		}
	}
	if err := validateLinks(linkFields, s.settings.MaxLinks, input.RequestHost); err != nil {
		return err
	}
	return nil
}

var linkPattern = regexp.MustCompile(`(?i)(https?://[^\s<>]+|www\.[^\s<>]+|\b(?:[a-z0-9-]+\.)+[a-z]{2,}(?:/[^\s<>]*)?)`)

func validateLinks(values []string, max int, ownHost string) error {
	if max <= 0 {
		return nil
	}
	count := 0
	for _, value := range values {
		hits := linkPattern.FindAllString(value, -1)
		for _, hit := range hits {
			count++
			if count > max {
				return errors.New("content rejected")
			}
			raw := hit
			if strings.HasPrefix(strings.ToLower(raw), "www.") {
				raw = "https://" + raw
			} else if !strings.Contains(raw, "://") {
				raw = "https://" + raw
			}
			parsed, err := url.Parse(raw)
			if err != nil || !allowedHost(parsed.Hostname(), ownHost) {
				return errors.New("content rejected")
			}
		}
	}
	return nil
}

func allowedHost(host, ownHost string) bool {
	if parsed, err := url.Parse("https://" + ownHost); err == nil && parsed.Hostname() != "" {
		ownHost = parsed.Hostname()
	}
	if parsed, err := url.Parse("https://" + host); err == nil && parsed.Hostname() != "" {
		host = parsed.Hostname()
	}
	return strings.EqualFold(host, ownHost)
}

func (s *Service) strike(ip string, now time.Time) {
	if ip == "" || s.settings.Strikes <= 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	recent := recentTimes(s.strikes[ip], now, s.settings.Window)
	recent = append(recent, now)
	s.strikes[ip] = recent
	if len(recent) >= s.settings.Strikes && s.settings.BanHours > 0 {
		s.bans[ip] = now.Add(time.Duration(s.settings.BanHours) * time.Hour)
	}
}

func (s *Service) checkRate(ip, email string, now time.Time, count bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Before(s.bans[ip]) {
		return errors.New("banned")
	}
	limits := map[string]int{}
	if s.settings.PerEmail > 0 {
		limits["email:"+strings.ToLower(email)] = s.settings.PerEmail
	}
	if s.settings.Global > 0 {
		limits["global"] = s.settings.Global
	}
	if ip != "" && s.settings.PerIP > 0 {
		limits["ip:"+ip] = s.settings.PerIP
	}
	for key, limit := range limits {
		recent := recentTimes(s.counts[key], now, s.settings.Window)
		if len(recent) >= limit {
			return errors.New("rate limit")
		}
		if count {
			s.counts[key] = append(recent, now)
		}
	}
	return nil
}

func recentTimes(values []time.Time, now time.Time, window time.Duration) []time.Time {
	out := values[:0]
	for _, v := range values {
		if now.Sub(v) < window {
			out = append(out, v)
		}
	}
	return out
}

func (s *Service) Confirm(ctx context.Context, input Message, ip string, now time.Time) (Outcome, error) {
	if !input.Confirmed || input.Step != "confirm" {
		return Outcome{}, errors.New("review confirmation required")
	}
	if err := validateShape(input); err != nil {
		return Outcome{}, err
	}
	silent, err := s.verifyReviewToken(input.ReviewToken, normalizedMessage(input), now)
	if err != nil {
		return Outcome{}, err
	}
	if input.Type == "security" && !s.validSecurityID(input.SecurityID, now) {
		return Outcome{}, errors.New("invalid security mode")
	}
	if err := s.checkRate(rateIP(input, ip), input.Email, now, true); err != nil {
		return Outcome{}, err
	}
	if silent {
		return Outcome{Silent: true}, nil
	}
	if input.Type != "security" && s.settings.DuplicateHours > 0 {
		duplicate := duplicateKey(input)
		s.mu.Lock()
		for key, until := range s.duplicates {
			if !now.Before(until) {
				delete(s.duplicates, key)
			}
		}
		if until, ok := s.duplicates[duplicate]; ok && now.Before(until) {
			s.mu.Unlock()
			return Outcome{Silent: true}, nil
		}
		if len(s.duplicates) >= 10000 {
			for key := range s.duplicates {
				delete(s.duplicates, key)
				break
			}
		}
		s.duplicates[duplicate] = now.Add(time.Duration(s.settings.DuplicateHours) * time.Hour)
		s.mu.Unlock()
	}
	if input.Type == "security" {
		return s.storeSecurity(ctx, input, now)
	}
	if s.sender != nil {
		if err := s.sender.SendContact(input.Type, input.Email, input.Name, "[contact] "+input.Subject, contactBody(input), input.Tunneled); err != nil {
			return Outcome{}, err
		}
	}
	return Outcome{}, nil
}

func (s *Service) verifyReviewToken(token string, normalized any, now time.Time) (bool, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 4 {
		return false, errors.New("invalid review token")
	}
	issued, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false, errors.New("invalid review token")
	}
	age := now.Sub(time.Unix(issued, 0))
	if age < time.Duration(s.settings.ReviewMin)*time.Second || s.settings.ReviewMax > 0 && age > time.Duration(s.settings.ReviewMax)*time.Second {
		return false, errors.New("invalid review token")
	}
	if parts[2] != "-" && parts[2] != "d" {
		return false, errors.New("invalid review token")
	}
	payload, _ := json.Marshal(normalized)
	digest := sha256.Sum256(payload)
	valid := 0
	for _, secret := range s.installationSecrets() {
		mac := hmac.New(sha256.New, secret)
		_, _ = fmt.Fprintf(mac, "contact-review|%s|%s|%s|%s", parts[0], parts[1], parts[2], hex.EncodeToString(digest[:]))
		valid |= subtle.ConstantTimeCompare([]byte(hex.EncodeToString(mac.Sum(nil))), []byte(parts[3]))
	}
	if valid != 1 {
		return false, errors.New("invalid review token")
	}
	key := parts[0] + "." + parts[1]
	s.mu.Lock()
	defer s.mu.Unlock()
	s.expireUsed(now)
	if _, ok := s.used[key]; ok {
		return false, errors.New("replay")
	}
	expires := time.Time{}
	if s.settings.ReviewMax > 0 {
		expires = now.Add(time.Duration(s.settings.ReviewMax) * time.Second)
	}
	s.used[key] = expires
	return parts[2] == "d", nil
}

func duplicateKey(input Message) string {
	sum := sha256.Sum256([]byte(normalizedText(input.Email) + "\x00" + normalizedText(input.Subject) + "\x00" + normalizedText(input.Body)))
	return hex.EncodeToString(sum[:])
}

func contactBody(in Message) string {
	fields := map[string]string{}
	for key, value := range in.Fields {
		switch key {
		case "reported_url", "reason", "copyrighted_work", "good_faith", "accuracy", "signature", "request_kind", "researcher_gpg", "component", "endpoint", "severity", "summary", "reproduce", "impact", "suggested_fix", "cve_requested", "disclosure_days", "credit_preference", "credit_handle", "coordinated_disclosure":
			fields[key] = value
		}
	}
	b, _ := json.MarshalIndent(map[string]any{"type": in.Type, "name": in.Name, "email": in.Email, "subject": in.Subject, "message": in.Body, "fields": fields}, "", "  ")
	return string(b)
}

func (s *Service) storeSecurity(ctx context.Context, in Message, now time.Time) (Outcome, error) {
	unlock := LockSecurityReportPipeline()
	defer unlock()
	tracking := make([]byte, 8)
	if _, err := rand.Read(tracking); err != nil {
		return Outcome{}, err
	}
	id := "sec_" + hex.EncodeToString(tracking)
	var err error
	body := []byte(contactBody(in))
	bodyAlg := "aes-256-gcm"
	encrypted := []byte(nil)
	if len(s.settings.ProjectPGPKey) > 0 {
		encrypted, err = EncryptPGP(s.settings.ProjectPGPKey, body)
		if err != nil {
			return Outcome{}, err
		}
		bodyAlg = "pgp"
	} else {
		block, blockErr := aes.NewCipher(s.encryptionKey)
		if blockErr != nil {
			return Outcome{}, blockErr
		}
		gcm, gcmErr := cipher.NewGCM(block)
		if gcmErr != nil {
			return Outcome{}, gcmErr
		}
		nonce := make([]byte, gcm.NonceSize())
		if _, err := rand.Read(nonce); err != nil {
			return Outcome{}, err
		}
		encrypted = gcm.Seal(nonce, nonce, body, nil)
	}
	if keyText := strings.TrimSpace(in.Fields["researcher_gpg"]); keyText != "" {
		if strings.HasPrefix(keyText, "https://") {
			if len(keyText) > 2048 {
				return Outcome{}, errors.New("researcher key URL is too long")
			}
			key, fetchErr := DownloadPGPKey(ctx, keyText)
			if fetchErr != nil {
				return Outcome{}, errors.New("invalid researcher public key")
			}
			if _, parseErr := EncryptPGP(key, []byte("key validation")); parseErr != nil {
				return Outcome{}, errors.New("invalid researcher public key")
			}
		} else if _, parseErr := EncryptPGP([]byte(keyText), []byte("key validation")); parseErr != nil {
			return Outcome{}, errors.New("invalid researcher public key")
		}
	}
	contactData, _ := json.Marshal(map[string]string{"email": in.Email, "name": in.Name, "researcher_gpg": in.Fields["researcher_gpg"]})
	contactBlock, err := aes.NewCipher(s.encryptionKey)
	if err != nil {
		return Outcome{}, err
	}
	contactGCM, err := cipher.NewGCM(contactBlock)
	if err != nil {
		return Outcome{}, err
	}
	contactNonce := make([]byte, contactGCM.NonceSize())
	if _, err := rand.Read(contactNonce); err != nil {
		return Outcome{}, err
	}
	contactEncrypted := contactGCM.Seal(contactNonce, contactNonce, contactData, nil)
	severity := strings.ToLower(in.Fields["severity"])
	component := in.Fields["component"]
	credit := in.Fields["credit_preference"]
	creditName := in.Fields["credit_handle"]
	if credit == "name" {
		creditName = in.Name
	}
	if credit == "anonymous" {
		credit = "anon"
	}
	if credit == "no" {
		creditName = ""
	}
	statusTokenRaw := make([]byte, 32)
	if _, err := rand.Read(statusTokenRaw); err != nil {
		return Outcome{}, err
	}
	statusToken := base64.RawURLEncoding.EncodeToString(statusTokenRaw)
	hash := sha256.Sum256([]byte(statusToken))
	tokenHash := hex.EncodeToString(hash[:])
	legacySecurityID, err := s.store.HasColumn(ctx, "security_reports", "security_id")
	if err != nil {
		return Outcome{}, fmt.Errorf("inspect security report schema: %w", err)
	}
	err = s.store.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if legacySecurityID {
			_, err = tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,security_id,id_window,severity,component,status,body_enc,body_alg,contact_enc,credit_pref,credit_name,cve_requested,token_hash) VALUES(?,'',?,?,?,?,?,?,?,?,?,?,?)`, id, now.Unix()/172800, severity, component, "received", encrypted, bodyAlg, contactEncrypted, credit, nullable(creditName), in.Fields["cve_requested"] == "true", tokenHash)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO security_reports(tracking_id,id_window,severity,component,status,body_enc,body_alg,contact_enc,credit_pref,credit_name,cve_requested,token_hash) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, id, now.Unix()/172800, severity, component, "received", encrypted, bodyAlg, contactEncrypted, credit, nullable(creditName), in.Fields["cve_requested"] == "true", tokenHash)
		}
		return err
	})
	if err != nil {
		return Outcome{}, err
	}
	if s.sender != nil {
		to := ""
		notice := "Security report received. The attached report is encrypted with the server security key; use the configured key to decrypt it."
		if bodyAlg == "pgp" {
			notice = "Security report received. The attached report is encrypted with the project OpenPGP key."
		}
		subject := "[security] report " + id + ": " + in.Fields["summary"]
		if notification, ok := s.sender.(SecurityNotificationSender); ok {
			_ = notification.SendSecurityNotification(to, subject, notice, id+".enc", encrypted, in.Tunneled, id, component, severity)
		} else {
			_ = s.sender.SendContact("security", "", "", subject, notice+"\n\nEncrypted report data (AES-256-GCM): "+base64.StdEncoding.EncodeToString(encrypted), in.Tunneled)
		}
	}
	ackSent := false
	ack := fmt.Sprintf("Your report was received. Tracking ID: %s\nStatus: %s", id, strings.TrimRight(in.BaseURL, "/")+"/server/security/report/"+id+"?token="+url.QueryEscape(statusToken))
	if researcher, ok := s.sender.(ResearcherSender); ok {
		if keyText := strings.TrimSpace(in.Fields["researcher_gpg"]); keyText != "" {
			var key []byte
			if strings.HasPrefix(keyText, "https://") {
				key, _ = DownloadPGPKey(ctx, keyText)
			} else {
				key = []byte(keyText)
			}
			if encrypted, encryptErr := EncryptPGP(key, []byte(ack)); encryptErr == nil {
				if encryptedSender, ok := s.sender.(EncryptedResearcherSender); ok && encryptedSender.SendResearcherEncrypted(in.Email, "Security report received", encrypted) == nil {
					ackSent = true
				}
			}
		} else if researcher.SendResearcher(in.Email, "Security report received", ack) == nil {
			ackSent = true
		}
	}
	return Outcome{TrackingID: id, Sent: ackSent}, nil
}

func rateIP(input Message, ip string) string {
	if input.Tunneled {
		return ""
	}
	return ip
}

func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func validReportedURL(raw, requestHost string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return false
	}
	request, err := url.Parse("https://" + requestHost)
	if err != nil || request.Hostname() == "" {
		return false
	}
	if !strings.EqualFold(parsed.Hostname(), request.Hostname()) {
		return false
	}
	port := parsed.Port()
	if port == "" {
		if parsed.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	requestPort := request.Port()
	if requestPort != "" && port != requestPort {
		return false
	}
	if requestPort == "" && port != "80" && port != "443" {
		return false
	}
	path := strings.ToLower(parsed.EscapedPath())
	if path == "" || path == "/" || strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/server/contact") {
		return false
	}
	return true
}

// ValidReportedURL validates a reported resource URL without fetching it.
func ValidReportedURL(raw, requestHost string) bool { return validReportedURL(raw, requestHost) }

// DownloadPGPKey accepts bounded HTTPS keys without following redirects or reaching private networks.
func DownloadPGPKey(ctx context.Context, raw string) ([]byte, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" {
		return nil, errors.New("invalid key URL")
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, parsed.Hostname())
	if err != nil || len(addresses) == 0 {
		return nil, errors.New("key host lookup failed")
	}
	validated := make([]netip.Addr, 0, len(addresses))
	for _, address := range addresses {
		parsedAddress, parseErr := netip.ParseAddr(address.IP.String())
		if parseErr != nil || parsedAddress.IsPrivate() || parsedAddress.IsLoopback() || parsedAddress.IsLinkLocalUnicast() || parsedAddress.IsUnspecified() {
			return nil, errors.New("key host is not public")
		}
		validated = append(validated, parsedAddress)
	}
	port := parsed.Port()
	if port == "" {
		port = "443"
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{ServerName: parsed.Hostname(), MinVersion: tls.VersionTLS12}, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		var lastErr error
		for _, ip := range validated {
			conn, dialErr := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, lastErr
	}}
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, errors.New("key URL returned an error")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16385))
	if err != nil || len(data) > 16384 {
		return nil, errors.New("key is too large")
	}
	if _, err := EncryptPGP(data, []byte("key validation")); err != nil {
		return nil, err
	}
	return data, nil
}
