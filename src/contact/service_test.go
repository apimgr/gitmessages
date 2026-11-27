package contact

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/database"
)

func TestVerifyCaptchaAtPostsBoundedProviderProof(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
			t.Errorf("provider request method/content-type = %s/%q", r.Method, r.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatal(err)
		}
		form, err := url.ParseQuery(string(body))
		if err != nil {
			t.Fatal(err)
		}
		if form.Get("secret") != "private-test-secret" || form.Get("response") != "provider-proof" || form.Get("remoteip") != "192.0.2.4" {
			t.Errorf("provider form = %#v", form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"success":true}`)
	}))
	defer server.Close()
	if err := verifyCaptchaAt(context.Background(), server.URL, "private-test-secret", "provider-proof", "192.0.2.4"); err != nil {
		t.Fatalf("valid provider proof rejected: %v", err)
	}
}

func TestVerifyCaptchaAtFailsClosed(t *testing.T) {
	for name, body := range map[string]string{"rejected": `{"success":false}`, "invalid response": `not-json`} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, body) }))
			defer server.Close()
			if err := verifyCaptchaAt(context.Background(), server.URL, "secret", "proof", ""); err == nil {
				t.Fatal("unverified provider response was accepted")
			}
		})
	}
	if err := verifyCaptchaAt(context.Background(), "http://127.0.0.1:1", "secret", "proof", ""); err == nil {
		t.Fatal("unreachable provider was accepted")
	}
}

type testSender struct {
	contacts, notices, acknowledgments int
}

func (s *testSender) SendContact(_, _, _, _, _ string, _ bool) error {
	s.contacts++
	return nil
}

func (s *testSender) SendResearcher(_, _, _ string) error {
	s.acknowledgments++
	return nil
}

func (s *testSender) SendSecurityNotification(_, _, _, _ string, _ []byte, _ bool, _, _, _ string) error {
	s.notices++
	return nil
}

func testService(t *testing.T, settings Settings, sender Sender) (*Service, *database.Store) {
	t.Helper()
	store, err := database.OpenSQLite(filepath.Join(projectTestDir(t), "contact.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	secret := []byte("0123456789abcdef0123456789abcdef")
	service, err := New(store, secret, []byte("0123456789abcdef0123456789abcdef"), settings, sender)
	if err != nil {
		t.Fatal(err)
	}
	return service, store
}

func validContactInput(t *testing.T, service *Service, now time.Time) Message {
	t.Helper()
	id, question, err := service.NewCaptcha(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Fields(question)
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[2])
	answer := a + b
	if parts[1] == "-" {
		answer = a - b
	}
	token, err := service.IssueFormToken(now)
	if err != nil {
		t.Fatal(err)
	}
	return Message{Type: "abuse", Name: "Reporter", Email: "reporter@example.test", Subject: "Abuse report", Body: "A report about https://example.test/post/1", Fields: map[string]string{"reported_url": "https://example.test/post/1", "reason": "malware_phishing"}, FormToken: token, CaptchaID: id, CaptchaAnswer: strconv.Itoa(answer), RequestHost: "example.test"}
}

func TestSecurityIDWindows(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	id := SecurityID(secret, now)
	if !ValidSecurityID(secret, id, now) {
		t.Fatal("current security ID was rejected")
	}
	previous := securityIDForWindow(secret, now.Unix()/172800-1)
	if !ValidSecurityID(secret, previous, now) {
		t.Fatal("previous security ID window was rejected")
	}
	if ValidSecurityID(secret, "invalid", now) {
		t.Fatal("invalid security ID was accepted")
	}
}

func TestFormTokenRequiresValidMACAndAge(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	token, err := FormToken(secret, now.Add(-10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{secret: secret, settings: Settings{MinFill: 2, MaxAge: 60}, used: map[string]time.Time{}}
	if err := service.consumeFormToken(token, now); err != nil {
		t.Fatalf("valid form token rejected: %v", err)
	}
	if err := service.consumeFormToken(token, now); err == nil {
		t.Fatal("replayed form token was accepted")
	}
	future, err := FormToken(secret, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.consumeFormToken(future, now); err == nil {
		t.Fatal("future form token was accepted")
	}
	parts := []byte(token)
	parts[len(parts)-1] ^= 1
	if err := service.consumeFormToken(string(parts), now); err == nil {
		t.Fatal("forged form token was accepted")
	}
}

func TestValidateTypeFields(t *testing.T) {
	valid := Message{Type: "abuse", Fields: map[string]string{"reported_url": "https://example.test/content", "reason": "malware_phishing"}}
	if err := validateTypeFields(valid); err != nil {
		t.Fatalf("valid abuse fields rejected: %v", err)
	}
	valid.Fields["extra"] = "unexpected"
	if err := validateTypeFields(valid); err == nil {
		t.Fatal("unknown field was accepted")
	}
	if validReportedURL("https://evil.test/item", "example.test") {
		t.Fatal("cross-site reported URL was accepted")
	}
	if validReportedURL("https://example.test/", "example.test") {
		t.Fatal("site root was accepted as a reported content URL")
	}
	if !validReportedURL("https://example.test/content", "example.test") {
		t.Fatal("same-site content URL was rejected")
	}
}

func TestServiceValidationAndFieldDescriptors(t *testing.T) {
	if _, err := New(nil, make([]byte, 32), make([]byte, 32), Settings{}, nil); err == nil {
		t.Fatal("nil store was accepted")
	}
	store, err := database.OpenSQLite(filepath.Join(projectTestDir(t), "constructor.db"), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if _, err := New(store, []byte("short"), make([]byte, 32), Settings{}, nil); err == nil {
		t.Fatal("short installation secret was accepted")
	}
	if _, err := New(store, make([]byte, 32), []byte("short"), Settings{}, nil); err == nil {
		t.Fatal("short encryption key was accepted")
	}
	service, err := New(store, make([]byte, 32), make([]byte, 32), Settings{}, nil)
	if err != nil || service.settings.Window != time.Hour {
		t.Fatalf("default settings not applied: service=%v err=%v", service, err)
	}
	if got := Fields("unknown", false); got != nil {
		t.Fatalf("unknown form fields = %#v, want nil", got)
	}
	if got := Fields("security", true); len(got) < 10 || got[len(got)-3].Name != "credit_handle" {
		t.Fatalf("security fields missing conditional fields: %#v", got)
	}
	if got := Fields("abuse", false); len(got) < 6 || got[3].Name != "reported_url" {
		t.Fatalf("abuse fields incomplete: %#v", got)
	}
}

func TestCaptchaAndContactReviewFlow(t *testing.T) {
	settings := Settings{MaxAge: 600, ReviewMax: 600, RejectMarkup: true, MaxLinks: 3, DuplicateHours: 24}
	sender := &testSender{}
	service, _ := testService(t, settings, sender)
	now := time.Now().UTC()
	input := validContactInput(t, service, now)
	review, err := service.Prepare(context.Background(), input, "192.0.2.10", now)
	if err != nil {
		t.Fatalf("prepare valid report: %v", err)
	}
	if review.Token == "" || review.Type != "abuse" || sender.contacts != 0 {
		t.Fatalf("review=%+v, contact sends=%d", review, sender.contacts)
	}
	input.Step, input.Confirmed, input.ReviewToken = "confirm", true, review.Token
	if _, err := service.Confirm(context.Background(), input, "192.0.2.10", now); err != nil {
		t.Fatalf("confirm valid report: %v", err)
	}
	if sender.contacts != 1 {
		t.Fatalf("contact sends = %d, want 1", sender.contacts)
	}
	if _, err := service.Confirm(context.Background(), input, "192.0.2.10", now); err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("review replay error = %v", err)
	}
	if err := service.verifyCaptcha(context.Background(), input.CaptchaID, input.CaptchaAnswer); err == nil {
		t.Fatal("single-use CAPTCHA was accepted twice")
	}
	if err := service.verifyCaptcha(context.Background(), "missing", "1"); err == nil {
		t.Fatal("unknown CAPTCHA was accepted")
	}
}

func TestContactValidationContentRateAndHoneypot(t *testing.T) {
	now := time.Now().UTC()
	settings := Settings{MinFill: 2, MaxAge: 30, RejectMarkup: true, MaxLinks: 1, BlockedWords: []string{"spamword"}, BlockedEmailDomains: []string{"blocked.test"}, PerIP: 1, Strikes: 2, BanHours: 1, Window: time.Hour, Honeypot: true, ReviewMax: 60}
	service, _ := testService(t, settings, nil)
	input := validContactInput(t, service, now.Add(-time.Second))
	if err := service.checkContent(Message{Type: "abuse", Email: "a@blocked.test"}); err == nil {
		t.Fatal("blocked email domain was accepted")
	}
	if err := service.checkContent(Message{Type: "abuse", Email: "a@example.test", Body: "<script>alert(1)</script>"}); err == nil {
		t.Fatal("markup was accepted")
	}
	if err := service.checkContent(Message{Type: "abuse", Email: "a@example.test", Body: "spamword"}); err == nil {
		t.Fatal("blocked word was accepted")
	}
	if err := validateLinks([]string{"https://example.test/a https://example.test/b"}, 1, "example.test"); err == nil {
		t.Fatal("too many links were accepted")
	}
	if err := validateLinks([]string{"https://outside.test/a"}, 3, "example.test"); err == nil {
		t.Fatal("external link was accepted")
	}
	if err := service.consumeFormToken(input.FormToken, now); err == nil {
		t.Fatal("too-fast form token was accepted")
	}
	service.strike("192.0.2.1", now)
	service.strike("192.0.2.1", now)
	if err := service.checkRate("192.0.2.1", "reporter@example.test", now, false); err == nil {
		t.Fatal("temporarily banned IP was accepted")
	}
	input = validContactInput(t, service, now.Add(-3*time.Second))
	input.Website = "https://bot.invalid"
	input.Body = "A report."
	review, err := service.Prepare(context.Background(), input, "192.0.2.2", now)
	if err != nil || !review.Silent {
		t.Fatalf("honeypot review = %+v, err=%v", review, err)
	}
	input.Step, input.Confirmed, input.ReviewToken = "confirm", true, review.Token
	outcome, err := service.Confirm(context.Background(), input, "192.0.2.2", now)
	if err != nil || !outcome.Silent {
		t.Fatalf("honeypot outcome = %+v, err=%v", outcome, err)
	}
}

func TestSecurityReportEncryptsStoredDataAndAcknowledges(t *testing.T) {
	sender := &testSender{}
	service, store := testService(t, Settings{ReviewMax: 600}, sender)
	now := time.Now().UTC()
	input := Message{Type: "security", Name: "Researcher", Email: "researcher@example.test", SecurityID: SecurityID(service.secret, now), BaseURL: "https://example.test", Fields: map[string]string{"component": "api", "severity": "high", "summary": "Information leak", "reproduce": "Request the endpoint", "impact": "Private metadata is exposed", "credit_preference": "name", "coordinated_disclosure": "true"}}
	token, err := ReviewToken(service.secret, now, normalizedMessage(input), false)
	if err != nil {
		t.Fatal(err)
	}
	input.Step, input.Confirmed, input.ReviewToken = "confirm", true, token
	outcome, err := service.Confirm(context.Background(), input, "192.0.2.3", now)
	if err != nil {
		t.Fatalf("confirm security report: %v", err)
	}
	if !strings.HasPrefix(outcome.TrackingID, "sec_") || !outcome.Sent || sender.notices != 1 || sender.acknowledgments != 1 {
		t.Fatalf("security outcome=%+v notices=%d acknowledgments=%d", outcome, sender.notices, sender.acknowledgments)
	}
	var algorithm string
	var encrypted []byte
	var contact []byte
	if err := store.Read().QueryRow("SELECT body_alg, body_enc, contact_enc FROM security_reports WHERE tracking_id = ?", outcome.TrackingID).Scan(&algorithm, &encrypted, &contact); err != nil {
		t.Fatal(err)
	}
	if algorithm != "aes-256-gcm" || len(encrypted) <= 12 || len(contact) <= 12 {
		t.Fatalf("stored security report algorithm=%q encrypted=%d contact=%d", algorithm, len(encrypted), len(contact))
	}
	if strings.Contains(string(encrypted), input.Email) || strings.Contains(string(contact), input.Email) {
		t.Fatal("plaintext researcher address was stored")
	}
	if _, err := hex.DecodeString(InvalidSecurityIDFingerprint("bad")); err != nil {
		t.Fatalf("invalid security ID fingerprint is not hex: %v", err)
	}
}
