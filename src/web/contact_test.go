package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/database"
	htmlnode "golang.org/x/net/html"
)

func TestContactPagesExposeProviderFallbackAndFreshCaptchaErrors(t *testing.T) {
	configDir := projectTestDir(t)
	configPath := filepath.Join(configDir, "server.yml")
	if err := os.WriteFile(configPath, []byte("server:\n  pages:\n    contact:\n      captcha: recaptcha\n      captcha_site_key: public-site-key\n      captcha_secret_key: private-test-secret\n      antispam:\n        min_fill_seconds: 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretData := "server:\n  token: adm_test-token-for-contact-routes\n  security:\n    encryption_key: MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=\n    encryption_key_version: 1\n  metrics:\n    auth:\n      tokens:\n        prometheus: test-prometheus-token\n        grafana: test-grafana-token\n        loki: test-loki-token\n"
	if err := os.WriteFile(filepath.Join(configDir, "secrets.yml"), []byte(secretData), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(configPath); err != nil {
		t.Fatal(err)
	}
	store, err := database.OpenSQLite(filepath.Join(configDir, "contact.db"), 4)
	if err != nil {
		t.Fatal(err)
	}
	ConfigureContact(store)
	t.Cleanup(func() {
		ConfigureContact(nil)
		_ = store.Close()
		if err := os.RemoveAll(filepath.Join(configDir, "yaml.d")); err != nil {
			t.Errorf("remove temporary contact config area: %v", err)
		}
		if err := os.WriteFile(configPath, []byte("server:\n"), 0o600); err != nil {
			t.Errorf("reset temporary server config: %v", err)
			return
		}
		if _, err := config.Load(configPath); err != nil {
			t.Errorf("reload default test config: %v", err)
		}
	})
	handler := Handler("contact-route-test")

	apiRequest := httptest.NewRequest(http.MethodGet, "https://messages.example.test/api/v1/server/contact", nil)
	apiResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiResponse, apiRequest)
	if apiResponse.Code != http.StatusOK {
		t.Fatalf("contact API GET status = %d: %s", apiResponse.Code, apiResponse.Body.String())
	}
	var apiBody struct {
		Data struct {
			Captcha   map[string]any `json:"captcha"`
			FormToken string         `json:"form_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(apiResponse.Body.Bytes(), &apiBody); err != nil {
		t.Fatal(err)
	}
	if apiBody.Data.Captcha["provider"] != "recaptcha" || apiBody.Data.Captcha["site_key"] != "public-site-key" || strings.Contains(apiResponse.Body.String(), "private-test-secret") {
		t.Fatalf("provider descriptor leaked or omitted configured data: %s", apiResponse.Body.String())
	}
	if apiBody.Data.Captcha["fallback"] == nil {
		t.Fatal("external CAPTCHA descriptor omitted its simple fallback")
	}
	externalFallback, ok := apiBody.Data.Captcha["fallback"].(map[string]any)
	if !ok {
		t.Fatalf("external CAPTCHA fallback has unexpected type: %#v", apiBody.Data.Captcha["fallback"])
	}
	question, _ := externalFallback["question"].(string)
	challengeID, _ := externalFallback["captcha_id"].(string)
	answer, err := solveSimpleCaptcha(question)
	if err != nil {
		t.Fatal(err)
	}
	formValues := url.Values{
		"type": {"abuse"}, "name": {"Reporter"}, "email": {"reporter@example.test"},
		"reported_url": {"https://messages.example.test/post/1"}, "reason": {"terms_violation"},
		"subject": {"Terms report"}, "message": {"A report for https://messages.example.test/post/1"},
		"form_token": {apiBody.Data.FormToken}, "captcha_id": {challengeID}, "captcha_answer": {answer},
	}
	csrfPage := httptest.NewRecorder()
	formRequest := httptest.NewRequest(http.MethodGet, "https://messages.example.test/server/contact?type=abuse", nil)
	formRequest.Header.Set("Accept-Language", "en")
	handler.ServeHTTP(csrfPage, formRequest)
	var csrfCookie *http.Cookie
	for _, cookie := range csrfPage.Result().Cookies() {
		if cookie.Name == csrfCookieName {
			csrfCookie = cookie
			break
		}
	}
	if csrfCookie == nil || !strings.Contains(csrfPage.Body.String(), `name="csrf_token"`) {
		t.Fatal("HTML contact form omitted its CSRF cookie or field")
	}
	formDocument, err := htmlnode.Parse(strings.NewReader(csrfPage.Body.String()))
	if err != nil {
		t.Fatal(err)
	}
	var formToken, formCaptchaID, captchaQuestion string
	var readFormFields func(*htmlnode.Node)
	readFormFields = func(node *htmlnode.Node) {
		if node.Type == htmlnode.ElementNode && node.Data == "input" {
			switch attrValue(node, "name") {
			case "form_token":
				formToken = attrValue(node, "value")
			case "captcha_id":
				formCaptchaID = attrValue(node, "value")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			readFormFields(child)
		}
	}
	readFormFields(formDocument)
	captchaQuestion = regexp.MustCompile(`[0-9]+\s*[+-]\s*[0-9]+`).FindString(csrfPage.Body.String())
	if formToken == "" || formCaptchaID == "" || captchaQuestion == "" {
		t.Fatal("HTML form omitted its current submission token or fallback CAPTCHA")
	}
	formAnswer, err := solveSimpleCaptcha(captchaQuestion)
	if err != nil {
		t.Fatal(err)
	}
	formValues.Set("form_token", formToken)
	formValues.Set("captcha_id", formCaptchaID)
	formValues.Set("captcha_answer", formAnswer)
	formValues.Set("csrf_token", csrfCookie.Value)
	reviewRequest := httptest.NewRequest(http.MethodPost, "https://messages.example.test/server/contact", strings.NewReader(formValues.Encode()))
	reviewRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	reviewRequest.Header.Set("Origin", "https://messages.example.test")
	reviewRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	reviewRequest.AddCookie(csrfCookie)
	reviewResponse := httptest.NewRecorder()
	handler.ServeHTTP(reviewResponse, reviewRequest)
	if reviewResponse.Code != http.StatusOK || !strings.Contains(reviewResponse.Body.String(), tr(webTestRequest(http.MethodGet, "/server/contact", nil), "contact.review_title")) || strings.Count(reviewResponse.Body.String(), `<h1 class="page-title">`) != 1 {
		t.Fatalf("valid HTML report did not reach review: status=%d body=%s", reviewResponse.Code, reviewResponse.Body.String())
	}
	if strings.Count(reviewResponse.Body.String(), `name="csrf_token"`) != 5 {
		t.Fatalf("contact review forms omitted CSRF tokens: %s", reviewResponse.Body.String())
	}
	missingCSRF := make(url.Values, len(formValues))
	for key, values := range formValues {
		missingCSRF[key] = append([]string(nil), values...)
	}
	missingCSRF.Del("csrf_token")
	invalidRequest := httptest.NewRequest(http.MethodPost, "https://messages.example.test/server/contact", strings.NewReader(missingCSRF.Encode()))
	invalidRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	invalidRequest.Header.Set("Origin", "https://messages.example.test")
	invalidRequest.Header.Set("Sec-Fetch-Site", "same-origin")
	invalidRequest.Header.Set("Accept", "text/html")
	invalidResponse := httptest.NewRecorder()
	handler.ServeHTTP(invalidResponse, invalidRequest)
	if invalidResponse.Code != http.StatusForbidden || !strings.Contains(invalidResponse.Body.String(), `<h1 class="page-title">`) {
		t.Fatalf("HTML contact POST without CSRF returned %d without a themed 403 page", invalidResponse.Code)
	}
	if strings.Contains(reviewResponse.Body.String(), "captcha_answer") || !strings.Contains(reviewResponse.Body.String(), `name="reported_url"`) {
		t.Fatalf("review form retained CAPTCHA metadata or omitted report data: %s", reviewResponse.Body.String())
	}
	apiRefresh := httptest.NewRecorder()
	handler.ServeHTTP(apiRefresh, httptest.NewRequest(http.MethodGet, "https://messages.example.test/api/v1/server/contact", nil))
	var refreshed struct {
		Data struct {
			Captcha   map[string]any `json:"captcha"`
			FormToken string         `json:"form_token"`
		} `json:"data"`
	}
	if apiRefresh.Code != http.StatusOK || json.Unmarshal(apiRefresh.Body.Bytes(), &refreshed) != nil {
		t.Fatalf("could not refresh API form descriptor: status=%d body=%s", apiRefresh.Code, apiRefresh.Body.String())
	}
	apiFallback, ok := refreshed.Data.Captcha["fallback"].(map[string]any)
	if !ok {
		t.Fatalf("refreshed API descriptor omitted simple fallback: %#v", refreshed.Data.Captcha)
	}
	apiQuestion, _ := apiFallback["question"].(string)
	apiCaptchaID, _ := apiFallback["captcha_id"].(string)
	apiAnswer, err := solveSimpleCaptcha(apiQuestion)
	if err != nil {
		t.Fatal(err)
	}
	apiSubmission, err := json.Marshal(map[string]string{
		"type": "abuse", "name": "API Reporter", "email": "api-reporter@example.test", "subject": "API terms report",
		"message": "A unique API report body", "reported_url": "https://messages.example.test/post/2", "reason": "terms_violation",
		"form_token": refreshed.Data.FormToken, "captcha_id": apiCaptchaID, "captcha_answer": apiAnswer,
	})
	if err != nil {
		t.Fatal(err)
	}
	apiSubmitRequest := httptest.NewRequest(http.MethodPost, "https://messages.example.test/api/v1/server/contact", strings.NewReader(string(apiSubmission)))
	apiSubmitResponse := httptest.NewRecorder()
	handler.ServeHTTP(apiSubmitResponse, apiSubmitRequest)
	var apiReview struct {
		Data struct {
			Review      map[string]string `json:"review"`
			ReviewToken string            `json:"review_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(apiSubmitResponse.Body.Bytes(), &apiReview); err != nil {
		t.Fatal(err)
	}
	if apiSubmitResponse.Code != http.StatusOK || apiReview.Data.Review["email"] != "api-reporter@example.test" || apiReview.Data.ReviewToken == "" {
		t.Fatalf("valid API report did not return a review token: status=%d body=%s", apiSubmitResponse.Code, apiSubmitResponse.Body.String())
	}

	formRequest = httptest.NewRequest(http.MethodGet, "https://messages.example.test/server/contact?type=abuse", nil)
	formResponse := httptest.NewRecorder()
	handler.ServeHTTP(formResponse, formRequest)
	form := formResponse.Body.String()
	if formResponse.Code != http.StatusOK || !strings.Contains(form, `class="g-recaptcha"`) || !strings.Contains(form, `<noscript>`) || !strings.Contains(form, "captcha_answer") {
		t.Fatalf("provider contact form omitted its widget or no-JavaScript fallback: status=%d body=%s", formResponse.Code, form)
	}
	policy := formResponse.Header().Get("Content-Security-Policy")
	if !strings.Contains(policy, "https://www.google.com/recaptcha/") || !strings.Contains(policy, "frame-src") || !strings.Contains(policy, "connect-src") {
		t.Fatalf("reCAPTCHA CSP sources are missing: %s", policy)
	}

	onionRequest := httptest.NewRequest(http.MethodGet, "http://messages.onion/api/v1/server/contact", nil)
	onionResponse := httptest.NewRecorder()
	handler.ServeHTTP(onionResponse, onionRequest)
	var onionBody struct {
		Data struct {
			Captcha   map[string]any `json:"captcha"`
			FormToken string         `json:"form_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(onionResponse.Body.Bytes(), &onionBody); err != nil {
		t.Fatal(err)
	}
	if onionResponse.Code != http.StatusOK || onionBody.Data.Captcha["provider"] != "simple" {
		t.Fatalf("onion contact API did not select simple CAPTCHA: status=%d body=%s", onionResponse.Code, onionResponse.Body.String())
	}
	if strings.Contains(onionResponse.Header().Get("Content-Security-Policy"), "recaptcha") {
		t.Fatalf("onion CSP included clearnet CAPTCHA hosts: %s", onionResponse.Header().Get("Content-Security-Policy"))
	}
	fallbackID, ok := onionBody.Data.Captcha["captcha_id"].(string)
	if !ok || fallbackID == "" {
		t.Fatalf("onion CAPTCHA descriptor omitted its challenge id: %#v", onionBody.Data.Captcha)
	}
	badSubmission, err := json.Marshal(map[string]string{
		"type": "abuse", "name": "Reporter", "email": "reporter@example.test", "subject": "Abuse report", "message": "This is a report",
		"reported_url": "http://messages.onion/post/1", "reason": "terms_violation", "form_token": onionBody.Data.FormToken,
		"captcha_id": fallbackID, "captcha_answer": "wrong",
	})
	if err != nil {
		t.Fatal(err)
	}
	postRequest := httptest.NewRequest(http.MethodPost, "http://messages.onion/api/v1/server/contact", strings.NewReader(string(badSubmission)))
	postResponse := httptest.NewRecorder()
	handler.ServeHTTP(postResponse, postRequest)
	var errorBody struct {
		Details struct {
			Field   string         `json:"field"`
			Captcha map[string]any `json:"captcha"`
		} `json:"details"`
	}
	if err := json.Unmarshal(postResponse.Body.Bytes(), &errorBody); err != nil {
		t.Fatal(err)
	}
	newCaptchaID, _ := errorBody.Details.Captcha["captcha_id"].(string)
	if postResponse.Code != http.StatusBadRequest || errorBody.Details.Field != "captcha" || newCaptchaID == "" || newCaptchaID == fallbackID {
		t.Fatalf("failed CAPTCHA response omitted its fresh descriptor: status=%d body=%s", postResponse.Code, postResponse.Body.String())
	}
}

func solveSimpleCaptcha(question string) (string, error) {
	parts := strings.Fields(question)
	if len(parts) != 3 {
		return "", fmt.Errorf("unexpected simple CAPTCHA question %q", question)
	}
	left, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", err
	}
	right, err := strconv.Atoi(parts[2])
	if err != nil {
		return "", err
	}
	switch parts[1] {
	case "+":
		return strconv.Itoa(left + right), nil
	case "-":
		return strconv.Itoa(left - right), nil
	default:
		return "", fmt.Errorf("unexpected simple CAPTCHA operator %q", parts[1])
	}
}

func TestMessageFromValuesSeparatesCaptchaMetadataFromReportFields(t *testing.T) {
	values := url.Values{
		"type":                 {"security"},
		"name":                 {"Researcher"},
		"email":                {"researcher@example.test"},
		"form_token":           {"form"},
		"captcha_id":           {"challenge"},
		"g-recaptcha-response": {"provider-proof"},
		"component":            {"web"},
	}
	input := messageFromValues(values)
	if input.CaptchaResponse != "provider-proof" {
		t.Fatalf("captcha response = %q", input.CaptchaResponse)
	}
	if input.Fields["component"] != "web" {
		t.Fatalf("report field was lost: %#v", input.Fields)
	}
	for _, key := range []string{"type", "name", "email", "form_token", "captcha_id", "g-recaptcha-response"} {
		if _, found := input.Fields[key]; found {
			t.Errorf("metadata field %q was retained in report fields", key)
		}
	}
}

func TestContactAndSecurityRoutesAreRegistered(t *testing.T) {
	handler := Handler("contact-test")
	for _, path := range []string{
		"/server/contact", "/server/security", "/server/security/policy", "/server/security/thanks",
		"/server/dpo", "/.well-known/security.txt", "/api/v1/server/security",
		"/.well-known/llms.txt", "/llms.txt",
		"/api/v1/server/security/policy", "/api/v1/server/security/thanks", "/api/v1/server/dpo",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code == http.StatusNotFound {
			t.Errorf("GET %s returned 404", path)
		}
	}
}

func TestWellKnownFilesSupportHead(t *testing.T) {
	handler := Handler("contact-test")
	for _, path := range []string{"/.well-known/security.txt", "/.well-known/llms.txt", "/llms.txt"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, path, nil))
		if response.Code != http.StatusOK || response.Body.Len() != 0 {
			t.Errorf("HEAD %s returned %d with body length %d", path, response.Code, response.Body.Len())
		}
	}
}

func TestContactFormIsNoIndex(t *testing.T) {
	handler := Handler("contact-test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/server/contact", nil))
	if response.Header().Get("X-Robots-Tag") != "noindex, nofollow" {
		t.Fatalf("contact page X-Robots-Tag = %q", response.Header().Get("X-Robots-Tag"))
	}
	pageResponse := httptest.NewRecorder()
	page(pageResponse, httptest.NewRequest(http.MethodGet, "/server/contact", nil), "Contact", template.HTML("<p>form</p>"))
	if !strings.Contains(pageResponse.Body.String(), `<meta name="robots" content="noindex,nofollow">`) {
		t.Fatal("contact page omitted the robots noindex metadata")
	}
}

func TestWellKnownSecurityOverrideIsAllowlistedAndBounded(t *testing.T) {
	dataDir := projectTestDir(t)
	path := filepath.Join(dataDir, "web", ".well-known", "security.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("Contact: mailto:security@example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, ok := wellKnownOverrideAt(dataDir, "security.txt"); !ok || string(body) != "Contact: mailto:security@example.test\n" {
		t.Fatalf("override was not served: ok=%t body=%q", ok, body)
	}
	if _, ok := wellKnownOverrideAt(dataDir, "../server.yml"); ok {
		t.Fatal("unallowlisted override was served")
	}
	if err := os.WriteFile(path, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := wellKnownOverrideAt(dataDir, "security.txt"); ok {
		t.Fatal("oversized override was served")
	}
	if validSecurityText([]byte("Contact: https://example.test/report\n")) {
		t.Fatal("security.txt missing mandatory fields was accepted")
	}
	if !validSecurityText([]byte("Contact: https://example.test/report\nPolicy: https://example.test/security\nExpires: 2027-01-01T00:00:00Z\n")) {
		t.Fatal("valid security.txt was rejected")
	}
}

func TestLLMSTextUsesRegisteredPublicOperations(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "http://messages.example.test/.well-known/llms.txt", nil)
	body := llmsTextBody(r)
	for _, endpoint := range []string{"GET http://messages.example.test/api/v1/types", "POST http://messages.example.test/api/v1/messages", "GET http://messages.example.test/server/healthz", "MCP: http://messages.example.test/api/v1/server/mcp (Streamable HTTP; Bearer token required)", "Aliases: http://messages.example.test/api/mcp, http://messages.example.test/mcp"} {
		if !strings.Contains(body, endpoint) {
			t.Errorf("llms.txt omitted %q", endpoint)
		}
	}
	if strings.Contains(body, "/server/contact") {
		t.Fatal("llms.txt advertised the private contact form")
	}
}

func TestSecurityURLsUseTrustedProxyBuildURL(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://internal/.well-known/security.txt", nil)
	request.RemoteAddr = "127.0.0.1:443"
	request.Header.Set("X-Forwarded-Host", "public.example.test")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Prefix", "/app")
	body := securityTextBody(request)
	if !strings.Contains(body, "Policy: https://public.example.test/app/server/security/policy\n") {
		t.Fatalf("security.txt policy URL did not use BuildURL: %s", body)
	}
	pageResponse := httptest.NewRecorder()
	pageRequest := httptest.NewRequest(http.MethodGet, "http://internal/server/security", nil)
	pageRequest.RemoteAddr = request.RemoteAddr
	pageRequest.Header = request.Header.Clone()
	contactSecurityPage(pageResponse, pageRequest)
	if !strings.Contains(pageResponse.Body.String(), `href="https://public.example.test/app/server/security/policy"`) {
		t.Fatal("security page link did not use BuildURL")
	}
}
