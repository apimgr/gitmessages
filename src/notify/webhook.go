package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// WebhookConfig contains transport endpoints and their receiver verification secrets.
type WebhookConfig map[string]string

// Event is the summary delivered to a configured webhook. Body must never contain user-submitted content.
type Event struct {
	Role       string
	Name       string
	Version    string
	AppURL     string
	Event      string
	Subject    string
	Body       string
	Severity   string
	Timestamp  time.Time
	TrackingID string
	WebhookID  string
}

var failureLogMu sync.Mutex
var lastFailureLog = make(map[string]time.Time)
var retryDelays = [...]time.Duration{0, time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}

// DispatchAsync queues delivery attempts without delaying the triggering request.
func DispatchAsync(role string, hooks WebhookConfig, event Event) bool {
	if role != "admin" && role != "security" && role != "abuse" && role != "general" {
		return false
	}
	keys := make([]string, 0, len(hooks))
	for name, endpoint := range hooks {
		if !strings.HasSuffix(name, "_secret") && strings.TrimSpace(endpoint) != "" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC().Truncate(time.Second)
	}
	if event.WebhookID == "" {
		event.WebhookID = uuid.Must(uuid.NewV7()).String()
	}
	if len(keys) == 0 {
		return false
	}
	for _, transport := range keys {
		endpoint, secret := hooks[transport], hooks[transport+"_secret"]
		go func() {
			for attempt, delay := range retryDelays {
				if delay > 0 {
					time.Sleep(delay)
				}
				if err := deliver(context.Background(), transport, endpoint, secret, role, event); err == nil {
					return
				} else if attempt == 6 {
					logWebhookFailure(role, transport, err)
				}
			}
		}()
	}
	return true
}

func logWebhookFailure(role, transport string, err error) {
	key := role + "." + transport
	now := time.Now()
	failureLogMu.Lock()
	if last := lastFailureLog[key]; now.Sub(last) < time.Minute {
		failureLogMu.Unlock()
		return
	}
	lastFailureLog[key] = now
	failureLogMu.Unlock()
	log.Printf("notify.webhook_failed role=%s transport=%s error=%v", role, transport, err)
}

// Dispatch sends one summary to each configured transport for a role.
func Dispatch(ctx context.Context, role string, hooks WebhookConfig, event Event) error {
	if role != "admin" && role != "security" && role != "abuse" && role != "general" {
		return fmt.Errorf("unsupported webhook role %q", role)
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC().Truncate(time.Second)
	} else {
		event.Timestamp = event.Timestamp.UTC().Truncate(time.Second)
	}
	if event.Severity == "" {
		event.Severity = "info"
	}
	if event.Severity != "info" && event.Severity != "warning" && event.Severity != "critical" {
		return fmt.Errorf("unsupported webhook severity %q", event.Severity)
	}
	if event.WebhookID == "" {
		event.WebhookID = uuid.Must(uuid.NewV7()).String()
	}
	keys := make([]string, 0, len(hooks))
	for name, endpoint := range hooks {
		if !strings.HasSuffix(name, "_secret") && strings.TrimSpace(endpoint) != "" {
			keys = append(keys, name)
		}
	}
	sort.Strings(keys)
	var failures []error
	for _, name := range keys {
		endpoint := strings.TrimSpace(hooks[name])
		if endpoint == "" {
			continue
		}
		if err := deliver(ctx, name, endpoint, hooks[name+"_secret"], role, event); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

func deliver(ctx context.Context, transport, endpoint, secret, role string, event Event) error {
	client := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return deliverWithClient(ctx, transport, endpoint, secret, role, event, client)
}

func deliverWithClient(ctx context.Context, transport, endpoint, secret, role string, event Event, client *http.Client) error {
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil || parsedEndpoint.Scheme != "https" || parsedEndpoint.Host == "" {
		return errors.New("webhook endpoint must be an HTTPS URL")
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC().Truncate(time.Second)
	} else {
		event.Timestamp = event.Timestamp.UTC().Truncate(time.Second)
	}
	if event.WebhookID == "" {
		event.WebhookID = uuid.Must(uuid.NewV7()).String()
	}
	var body []byte
	method := http.MethodPost
	contentType := "application/json"
	requestURL := endpoint
	switch transport {
	case "telegram":
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return errors.New("invalid Telegram endpoint")
		}
		query := parsed.Query()
		query.Set("text", summary(event))
		parsed.RawQuery = query.Encode()
		requestURL = parsed.String()
		body = []byte("{}")
	case "discord":
		body, _ = json.Marshal(map[string]string{"content": summary(event), "username": event.Name})
	case "slack", "mattermost":
		body, _ = json.Marshal(map[string]string{"text": summary(event)})
	case "pushover":
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return errors.New("invalid Pushover endpoint")
		}
		fields := parsed.Query()
		parsed.RawQuery = ""
		requestURL = parsed.String()
		fields.Set("title", event.Subject)
		fields.Set("message", summary(event))
		fields.Set("priority", pushoverPriority(event.Severity))
		body = []byte(fields.Encode())
		contentType = "application/x-www-form-urlencoded"
	case "gotify":
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return errors.New("invalid Gotify endpoint")
		}
		token := parsed.Query().Get("token")
		parsed.RawQuery = ""
		requestURL = strings.TrimRight(parsed.String(), "/") + "/message"
		query := url.Values{"token": []string{token}}
		requestURL += "?" + query.Encode()
		body, _ = json.Marshal(map[string]any{"title": event.Subject, "message": summary(event), "priority": gotifyPriority(event.Severity)})
	case "generic":
		payload := map[string]any{"role": role, "event": event.Event, "subject": event.Subject, "body": event.Body, "severity": event.Severity, "timestamp": event.Timestamp.Format(time.RFC3339), "project_name": event.Name, "project_version": event.Version, "app_url": event.AppURL}
		if event.TrackingID != "" {
			payload["tracking_id"] = event.TrackingID
		}
		body, _ = json.Marshal(payload)
	default:
		return fmt.Errorf("unsupported transport %q", transport)
	}
	if body == nil {
		body = []byte{}
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid webhook request")
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("User-Agent", event.Name+"/"+event.Version+" (+"+event.AppURL+")")
	request.Header.Set("X-Webhook-Timestamp", fmt.Sprint(time.Now().Unix()))
	request.Header.Set("X-Webhook-ID", event.WebhookID)
	request.Header.Set("X-Webhook-Event", event.Event)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	request.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	response, err := client.Do(request)
	if err != nil {
		return errors.New("webhook request failed")
	}
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		_ = response.Body.Close()
		return errors.New("read webhook response failed")
	}
	if err := response.Body.Close(); err != nil {
		return errors.New("close webhook response failed")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}

func summary(event Event) string {
	if event.Body != "" {
		return event.Body
	}
	return event.Subject
}

func pushoverPriority(severity string) string {
	if severity == "critical" {
		return "1"
	}
	return "0"
}

func gotifyPriority(severity string) int {
	switch severity {
	case "warning":
		return 5
	case "critical":
		return 8
	default:
		return 2
	}
}
