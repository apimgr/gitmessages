package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/apimgr/gitmessages/src/maintenance"
)

func testMaintenanceInfo() maintenance.Info {
	last := "2026-11-26T12:00:00Z"
	next := "2026-11-26T12:00:30Z"
	return maintenance.Info{
		Reason: "database_connection", Message: "Database connection failed", Since: "2026-11-26T11:59:00Z",
		SelfHealing: maintenance.SelfHealingInfo{Enabled: true, Attempts: 2, LastAttempt: &last, NextAttempt: &next},
	}
}

func TestMaintenanceHealthUses503AndCanonicalFields(t *testing.T) {
	SetMaintenanceProvider(func() (maintenance.Info, bool) { return testMaintenanceInfo(), true })
	t.Cleanup(func() { SetMaintenanceProvider(nil) })
	request := httptest.NewRequest(http.MethodGet, "/api/v1/server/healthz", nil)
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()
	apiHealth(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("health status = %d, want 503", response.Code)
	}
	var body struct {
		Status      string            `json:"status"`
		Maintenance maintenance.Info  `json:"maintenance"`
		Checks      map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "maintenance" || body.Maintenance.Reason != "database_connection" || body.Maintenance.SelfHealing.Attempts != 2 || body.Checks["database"] != "error" {
		t.Fatalf("maintenance health payload = %+v", body)
	}
}

func TestMaintenanceMiddlewareRejectsWritesAndAllowsReads(t *testing.T) {
	SetMaintenanceProvider(func() (maintenance.Info, bool) { return testMaintenanceInfo(), true })
	t.Cleanup(func() { SetMaintenanceProvider(nil) })
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := maintenanceMiddleware(next)

	read := httptest.NewRecorder()
	handler.ServeHTTP(read, httptest.NewRequest(http.MethodGet, "/api/v1/types", nil))
	if read.Code != http.StatusNoContent {
		t.Fatalf("maintenance read status = %d, want 204", read.Code)
	}

	writeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/messages", nil)
	writeRequest.Header.Set("Accept", "application/json")
	write := httptest.NewRecorder()
	handler.ServeHTTP(write, writeRequest)
	if write.Code != http.StatusServiceUnavailable {
		t.Fatalf("maintenance write status = %d, want 503", write.Code)
	}
	if write.Header().Get("Retry-After") == "" || write.Header().Get("X-Maintenance-Mode") != "true" || write.Header().Get("X-Maintenance-Reason") != "database_connection" {
		t.Fatalf("maintenance response headers = %v", write.Header())
	}
	var body map[string]any
	if err := json.Unmarshal(write.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "MAINTENANCE" {
		t.Fatalf("maintenance error body = %v", body)
	}
	details, ok := body["details"].(map[string]any)
	if !ok || details["reason"] != "database_connection" || details["self_healing"] != true {
		t.Fatalf("maintenance details = %v", body["details"])
	}
}

func TestMaintenanceMiddlewareKeepsHealthAvailableWithoutDatabase(t *testing.T) {
	SetMaintenanceProvider(func() (maintenance.Info, bool) { return testMaintenanceInfo(), true })
	SetMaintenanceReadProvider(func(context.Context) bool { return false })
	t.Cleanup(func() {
		SetMaintenanceProvider(nil)
		SetMaintenanceReadProvider(nil)
	})
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := maintenanceMiddleware(next)

	for _, path := range []string{"/api/v1/server/healthz", "/server/healthz", "/api/v1/server/healthz.txt"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent {
			t.Errorf("health endpoint %s status = %d, want 204", path, response.Code)
		}
	}
	for _, path := range []string{"/api/v1/types", "/server/about"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable {
			t.Errorf("non-health endpoint %s status = %d, want 503", path, response.Code)
		}
	}
}

func TestCommitHealthRequestCountersAcknowledgesAtomically(t *testing.T) {
	requestCounterMu.Lock()
	previousTotal := pendingRequestTotal
	previousHours := pendingRequestHours
	pendingRequestTotal = 5
	pendingRequestHours = map[int64]int64{time.Now().UTC().Truncate(time.Hour).Unix(): 5}
	requestCounterMu.Unlock()
	t.Cleanup(func() {
		requestCounterMu.Lock()
		pendingRequestTotal = previousTotal
		pendingRequestHours = previousHours
		requestCounterMu.Unlock()
	})
	updated := false
	CommitHealthRequestCounters(SnapshotHealthRequestCounters(), func() { updated = true })
	if !updated {
		t.Fatal("counter cache update callback was not called")
	}
	if got := SnapshotHealthRequestCounters(); got.Total != 0 || len(got.Hours) != 0 {
		t.Fatalf("pending counters after commit = %+v, want empty", got)
	}
}

func TestCompactHealthUptime(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{45 * time.Second, "45s"},
		{24 * time.Minute, "24m"},
		{2*time.Hour + 16*time.Minute, "2h 16m"},
		{25*time.Hour + 4*time.Minute, "1d 1h 4m"},
	}
	for _, test := range tests {
		if got := compactHealthUptime(test.duration); got != test.want {
			t.Errorf("compactHealthUptime(%s) = %q, want %q", test.duration, got, test.want)
		}
	}
}

func TestRequestCountersExcludeHealthAndMetricsAndAcknowledgeBatches(t *testing.T) {
	if !excludedHealthCounterPath("/api/v1/server/healthz") || !excludedHealthCounterPath("/server/metrics/requests") || excludedHealthCounterPath("/api/v1/types") {
		t.Fatal("health and metrics counter exclusions do not match the health contract")
	}
	before := SnapshotHealthRequestCounters()
	handler := requestStatsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/types", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("request status = %d, want 204", response.Code)
	}
	batch := SnapshotHealthRequestCounters()
	if batch.Total-before.Total != 1 {
		t.Fatalf("pending total delta = %d, want 1", batch.Total-before.Total)
	}
	AcknowledgeHealthRequestCounters(batch)
	after := SnapshotHealthRequestCounters()
	if after.Total != 0 || len(after.Hours) != 0 {
		t.Fatalf("pending counters after acknowledgement = %+v, want empty", after)
	}
}
