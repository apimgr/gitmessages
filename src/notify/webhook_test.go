package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestDispatchGenericWebhookSignsSummary(t *testing.T) {
	var gotBody string
	var gotSignature string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		gotSignature = r.Header.Get("X-Webhook-Signature")
		if r.Header.Get("X-Webhook-Event") != "contact.privacy.received" {
			t.Errorf("X-Webhook-Event = %q", r.Header.Get("X-Webhook-Event"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	err := deliverWithClient(t.Context(), "generic", server.URL, "receiver-secret", "general", Event{
		Name: "gitmessages", Version: "1.2.3", AppURL: "https://example.test", Event: "contact.privacy.received",
		Subject: "Privacy submission received", Body: "A privacy submission was received.", Severity: "info",
		Timestamp: time.Date(2026, 11, 27, 12, 30, 45, 999, time.FixedZone("EST", -5*60*60)),
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotBody, "sender@example.test") || !strings.Contains(gotBody, "2026-11-27T17:30:45Z") {
		t.Fatalf("generic payload contains contact data or wrong timestamp: %s", gotBody)
	}
	mac := hmac.New(sha256.New, []byte("receiver-secret"))
	_, _ = mac.Write([]byte(gotBody))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if gotSignature != want {
		t.Fatalf("signature = %q, want %q", gotSignature, want)
	}
}

func TestNotificationPriorities(t *testing.T) {
	for _, test := range []struct {
		severity string
		push     string
		gotify   int
	}{{"info", "0", 2}, {"warning", "0", 5}, {"critical", "1", 8}} {
		if got := pushoverPriority(test.severity); got != test.push {
			t.Errorf("pushoverPriority(%q) = %q, want %q", test.severity, got, test.push)
		}
		if got := gotifyPriority(test.severity); got != test.gotify {
			t.Errorf("gotifyPriority(%q) = %d, want %d", test.severity, got, test.gotify)
		}
	}
}

func TestWebhookTransportAdapters(t *testing.T) {
	tests := []struct {
		name     string
		endpoint func(string) string
		check    func(*testing.T, *http.Request, []byte)
	}{
		{
			name:     "telegram",
			endpoint: func(base string) string { return base + "?chat_id=42" },
			check: func(t *testing.T, r *http.Request, body []byte) {
				if r.URL.Query().Get("chat_id") != "42" || r.URL.Query().Get("text") != "A short summary." {
					t.Fatalf("Telegram query = %v", r.URL.Query())
				}
				if string(body) != "{}" {
					t.Fatalf("Telegram body = %q, want empty JSON object", body)
				}
			},
		},
		{
			name:     "discord",
			endpoint: func(base string) string { return base },
			check: func(t *testing.T, _ *http.Request, body []byte) {
				var payload map[string]string
				if err := json.Unmarshal(body, &payload); err != nil || payload["content"] != "A short summary." || payload["username"] != "gitmessages" {
					t.Fatalf("Discord payload = %s, err=%v", body, err)
				}
			},
		},
		{
			name:     "slack",
			endpoint: func(base string) string { return base },
			check: func(t *testing.T, _ *http.Request, body []byte) {
				var payload map[string]string
				if err := json.Unmarshal(body, &payload); err != nil || payload["text"] != "A short summary." {
					t.Fatalf("Slack payload = %s, err=%v", body, err)
				}
			},
		},
		{
			name:     "mattermost",
			endpoint: func(base string) string { return base },
			check: func(t *testing.T, _ *http.Request, body []byte) {
				var payload map[string]string
				if err := json.Unmarshal(body, &payload); err != nil || payload["text"] != "A short summary." {
					t.Fatalf("Mattermost payload = %s, err=%v", body, err)
				}
			},
		},
		{
			name: "pushover",
			endpoint: func(base string) string {
				return base + "?user=user-id&token=app-token"
			},
			check: func(t *testing.T, r *http.Request, body []byte) {
				values, err := url.ParseQuery(string(body))
				if err != nil || values.Get("user") != "user-id" || values.Get("token") != "app-token" || values.Get("message") != "A short summary." || values.Get("title") != "Event summary" || values.Get("priority") != "1" {
					t.Fatalf("Pushover form = %q, err=%v", body, err)
				}
				if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
					t.Fatalf("Pushover content type = %q", r.Header.Get("Content-Type"))
				}
			},
		},
		{
			name: "gotify",
			endpoint: func(base string) string {
				return base + "?token=app-token"
			},
			check: func(t *testing.T, r *http.Request, body []byte) {
				var payload map[string]any
				if r.URL.Path != "/message" || r.URL.Query().Get("token") != "app-token" {
					t.Fatalf("Gotify request target = %s?%s", r.URL.Path, r.URL.RawQuery)
				}
				if err := json.Unmarshal(body, &payload); err != nil || payload["title"] != "Event summary" || payload["message"] != "A short summary." || payload["priority"] != float64(8) {
					t.Fatalf("Gotify payload = %s, err=%v", body, err)
				}
			},
		},
		{
			name:     "generic",
			endpoint: func(base string) string { return base },
			check: func(t *testing.T, _ *http.Request, body []byte) {
				var payload map[string]any
				if err := json.Unmarshal(body, &payload); err != nil || payload["role"] != "general" || payload["event"] != "test.event" || payload["body"] != "A short summary." || payload["tracking_id"] != "sec_0123456789abcdef" {
					t.Fatalf("generic payload = %s, err=%v", body, err)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var request *http.Request
			var body []byte
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request = r.Clone(t.Context())
				body, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent)
			}))
			defer server.Close()
			event := Event{
				Name: "gitmessages", Version: "1.2.3", AppURL: "https://example.test", Event: "test.event",
				Subject: "Event summary", Body: "A short summary.", Severity: "critical",
				Timestamp: time.Date(2026, 11, 27, 12, 30, 45, 0, time.UTC), TrackingID: "sec_0123456789abcdef", WebhookID: "0192a2de-8b00-7000-8000-000000000001",
			}
			if err := deliverWithClient(t.Context(), tt.name, tt.endpoint(server.URL), "receiver-secret", "general", event, server.Client()); err != nil {
				t.Fatal(err)
			}
			if request.Method != http.MethodPost {
				t.Fatalf("method = %s, want POST", request.Method)
			}
			if request.Header.Get("X-Webhook-ID") != event.WebhookID || !strings.HasPrefix(request.Header.Get("X-Webhook-Signature"), "sha256=") {
				t.Fatalf("missing webhook verification headers: %#v", request.Header)
			}
			tt.check(t, request, body)
		})
	}
}

func TestWebhookRetrySchedule(t *testing.T) {
	want := []time.Duration{0, time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour, 6 * time.Hour, 24 * time.Hour}
	if len(retryDelays) != len(want) {
		t.Fatalf("retry schedule length = %d, want %d", len(retryDelays), len(want))
	}
	for i, delay := range retryDelays {
		if delay != want[i] {
			t.Errorf("retryDelays[%d] = %s, want %s", i, delay, want[i])
		}
	}
}
