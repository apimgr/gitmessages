package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"html/template"
	"math"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/common/i18n"
	"github.com/apimgr/gitmessages/src/config"
	"github.com/apimgr/gitmessages/src/maintenance"
	"github.com/apimgr/gitmessages/src/mode"
)

type healthProject struct {
	Name        string `json:"name"`
	Tagline     string `json:"tagline"`
	Description string `json:"description"`
}

type healthBuild struct {
	Commit string `json:"commit"`
	Date   string `json:"date"`
}

type HealthStats struct {
	RequestsTotal     int64 `json:"requests_total"`
	Requests24Hours   int64 `json:"requests_24h"`
	ActiveConnections int64 `json:"active_connections"`
}

type HealthRequestHourBucket struct {
	HourStart time.Time `json:"hour_start"`
	Count     int64     `json:"count"`
}

type HealthRequestCounterBatch struct {
	Total int64                     `json:"total"`
	Hours []HealthRequestHourBucket `json:"hours"`
}

type healthResponse struct {
	Project        healthProject     `json:"project"`
	Status         string            `json:"status"`
	PendingRestart *bool             `json:"pending_restart,omitempty"`
	RestartReason  []string          `json:"restart_reason,omitempty"`
	Maintenance    *maintenance.Info `json:"maintenance,omitempty"`
	Version        string            `json:"version"`
	GoVersion      string            `json:"go_version"`
	Build          healthBuild       `json:"build"`
	Mode           string            `json:"mode"`
	Uptime         string            `json:"uptime"`
	Timestamp      string            `json:"timestamp"`
	Features       map[string]any    `json:"features"`
	Checks         map[string]string `json:"checks"`
	Stats          HealthStats       `json:"stats"`
}

func buildHealthResponse() (healthResponse, int) {
	branding := config.Get().Server.Branding
	features := overlayStatuses()
	if features == nil {
		features = map[string]any{}
	}
	checks := map[string]string{"database": "ok", "disk": "ok", "config": "ok"}
	status := "healthy"
	for name, feature := range features {
		encoded, err := json.Marshal(feature)
		if err != nil {
			continue
		}
		var state struct {
			Enabled bool   `json:"enabled"`
			Status  string `json:"status"`
		}
		if json.Unmarshal(encoded, &state) != nil || !state.Enabled || state.Status == "disabled" {
			continue
		}
		check := "ok"
		if state.Status == "unavailable" {
			check = "degraded"
			status = "degraded"
		}
		checks[name] = check
	}
	for _, check := range checks {
		if check == "error" {
			status = "unhealthy"
			break
		}
	}
	restartReasons := currentHealthRestartReasons()
	sort.Strings(restartReasons)
	if len(restartReasons) > 0 && status == "healthy" {
		status = "restart_required"
	}
	var pendingRestart *bool
	if len(restartReasons) > 0 {
		pending := true
		pendingRestart = &pending
	}
	maintenanceInfo, inMaintenance := currentMaintenance()
	var maintenanceDetails *maintenance.Info
	statusCode := http.StatusOK
	if status == "unhealthy" {
		statusCode = http.StatusServiceUnavailable
	}
	if inMaintenance {
		status = "maintenance"
		statusCode = http.StatusServiceUnavailable
		maintenanceDetails = &maintenanceInfo
		check := "error"
		switch maintenanceInfo.Reason {
		case "database_connection", "database_write":
			checks["database"] = check
		case "disk_full":
			checks["disk"] = check
		case "file_read", "file_write", "file_permission":
			checks["config"] = check
		}
	}
	if currentHealthShuttingDown() {
		status = "shutting_down"
		statusCode = http.StatusServiceUnavailable
	}
	tagline := strings.TrimSpace(branding.Tagline)
	description := strings.TrimSpace(branding.Description)
	if tagline == "" {
		tagline = config.DefaultConfig().Server.Branding.Tagline
	}
	if description == "" {
		description = config.DefaultConfig().Server.Branding.Description
	}
	buildDate := serverBuildDate
	if buildDate == "" {
		buildDate = "N/A"
	}
	stats := currentHealthStats()
	if stats.RequestsTotal < 0 {
		stats.RequestsTotal = 0
	}
	if stats.Requests24Hours < 0 {
		stats.Requests24Hours = 0
	}
	stats.ActiveConnections = activeRequests.Load()
	if stats.ActiveConnections < 1 {
		stats.ActiveConnections = 1
	}
	return healthResponse{
		Project: healthProject{Name: seoBrandingTitle(), Tagline: tagline, Description: description},
		Status:  status, PendingRestart: pendingRestart, RestartReason: restartReasons, Maintenance: maintenanceDetails,
		Version: serverVersion, GoVersion: runtime.Version(),
		Build: healthBuild{Commit: serverCommit, Date: buildDate},
		Mode:  string(mode.Get()), Uptime: compactHealthUptime(time.Since(serverStartedAt)),
		Timestamp: time.Now().UTC().Truncate(time.Second).Format(time.RFC3339),
		Features:  features, Checks: checks,
		Stats: stats,
	}, statusCode
}

func compactHealthUptime(uptime time.Duration) string {
	if uptime < time.Minute {
		seconds := int(math.Floor(uptime.Seconds()))
		if seconds < 0 {
			seconds = 0
		}
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := int(uptime.Minutes())
	days := minutes / (24 * 60)
	minutes %= 24 * 60
	hours := minutes / 60
	minutes %= 60
	parts := make([]string, 0, 3)
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if minutes > 0 {
		parts = append(parts, fmt.Sprintf("%dm", minutes))
	}
	return strings.Join(parts, " ")
}

func healthHTMLContent(r *http.Request, value healthResponse) string {
	var output strings.Builder
	fmt.Fprintf(&output, `<section class="section-card" aria-labelledby="health-project"><h2 id="health-project">%s</h2><p>%s</p><p>%s</p></section>`, html.EscapeString(value.Project.Name), html.EscapeString(value.Project.Tagline), html.EscapeString(value.Project.Description))
	statusLabel := healthValueLabel(r, value.Status)
	fmt.Fprintf(&output, `<section class="section-card status-%s" aria-labelledby="health-status"><h2 id="health-status">%s</h2><p role="status">%s</p>`, html.EscapeString(value.Status), html.EscapeString(tr(r, "health.field.status")), html.EscapeString(statusLabel))
	if value.PendingRestart != nil {
		writeHealthHTMLField(&output, r, "pending_restart", "true")
		if len(value.RestartReason) > 0 {
			writeHealthHTMLField(&output, r, "restart_reason", strings.Join(value.RestartReason, ", "))
		}
	}
	if value.Maintenance != nil {
		info := value.Maintenance
		guidanceKey := "health.maintenance.guidance.database"
		switch info.Reason {
		case "disk_full":
			guidanceKey = "health.maintenance.guidance.disk"
		case "file_read", "file_write", "file_permission":
			guidanceKey = "health.maintenance.guidance.files"
		}
		fmt.Fprintf(&output, `<h3>%s</h3><p><strong>%s:</strong> %s</p><p>%s</p><p>%s</p><p><strong>%s:</strong> %t</p><p><strong>%s:</strong> %t</p><p><strong>%s:</strong> %d</p>`, html.EscapeString(tr(r, "health.maintenance.title")), html.EscapeString(tr(r, "health.maintenance.reason")), html.EscapeString(info.Reason), html.EscapeString(info.Message), html.EscapeString(tr(r, guidanceKey)), html.EscapeString(tr(r, "health.field.enabled")), info.SelfHealing.Enabled, html.EscapeString(tr(r, "health.field.exhausted")), info.SelfHealing.Exhausted, html.EscapeString(tr(r, "health.maintenance.attempts")), info.SelfHealing.Attempts)
		if info.SelfHealing.LastAttempt != nil {
			fmt.Fprintf(&output, `<p><strong>%s:</strong> %s</p>`, html.EscapeString(tr(r, "health.field.last_attempt")), html.EscapeString(*info.SelfHealing.LastAttempt))
		}
		if info.SelfHealing.NextAttempt != nil {
			fmt.Fprintf(&output, `<p><strong>%s:</strong> %s</p>`, html.EscapeString(tr(r, "health.field.next_attempt")), html.EscapeString(*info.SelfHealing.NextAttempt))
		}
	}
	output.WriteString(`</section>`)

	output.WriteString(healthSectionStart(r, "version"))
	writeHealthHTMLField(&output, r, "version", value.Version)
	writeHealthHTMLField(&output, r, "build_commit", value.Build.Commit)
	writeHealthHTMLField(&output, r, "build_date", value.Build.Date)
	output.WriteString(`</section>`)

	output.WriteString(healthSectionStart(r, "runtime"))
	writeHealthHTMLField(&output, r, "mode", healthValueLabel(r, value.Mode))
	writeHealthHTMLField(&output, r, "uptime", humanHealthUptime(r, time.Since(serverStartedAt)))
	writeHealthHTMLField(&output, r, "timestamp", time.Now().In(healthTimezone()).Format("January 02, 2006 at 15:04:05 MST"))
	output.WriteString(`</section>`)

	output.WriteString(healthSectionStart(r, "features"))
	featureNames := sortedKeys(value.Features)
	if len(featureNames) == 0 {
		output.WriteString(`<p>—</p>`)
	}
	for _, name := range featureNames {
		encoded, _ := json.Marshal(value.Features[name])
		var feature struct {
			Enabled  bool   `json:"enabled"`
			Status   string `json:"status"`
			Hostname string `json:"hostname"`
			Address  string `json:"address"`
			Provider string `json:"provider"`
			State    string `json:"state"`
		}
		_ = json.Unmarshal(encoded, &feature)
		state := feature.Status
		if state == "" {
			if feature.Enabled {
				state = "enabled"
			} else {
				state = "disabled"
			}
		}
		label := healthValueLabel(r, state)
		address := feature.Hostname
		if address == "" {
			address = feature.Address
		}
		fmt.Fprintf(&output, `<p><strong>%s:</strong> %s`, html.EscapeString(healthFeatureLabel(r, name)), html.EscapeString(label))
		if address != "" {
			fmt.Fprintf(&output, ` <code>%s</code>`, html.EscapeString(address))
		}
		if feature.Provider != "" {
			fmt.Fprintf(&output, ` %s`, html.EscapeString(feature.Provider))
		}
		if feature.State != "" {
			fmt.Fprintf(&output, ` %s`, html.EscapeString(healthValueLabel(r, feature.State)))
		}
		output.WriteString(`</p>`)
	}
	output.WriteString(`</section>`)

	output.WriteString(healthSectionStart(r, "checks"))
	checkNames := sortedKeys(value.Checks)
	for _, name := range checkNames {
		fmt.Fprintf(&output, `<p><strong>%s:</strong> %s</p>`, html.EscapeString(healthKeyLabel(r, "health.check.unknown", name)), html.EscapeString(healthValueLabel(r, value.Checks[name])))
	}
	output.WriteString(`</section>`)

	output.WriteString(healthSectionStart(r, "stats"))
	writeHealthHTMLField(&output, r, "stats.requests_total", formatHealthCount(value.Stats.RequestsTotal))
	writeHealthHTMLField(&output, r, "stats.requests_24h", formatHealthCount(value.Stats.Requests24Hours))
	writeHealthHTMLField(&output, r, "stats.active_connections", formatHealthCount(value.Stats.ActiveConnections))
	output.WriteString(`</section>`)
	return output.String()
}

func healthSectionStart(r *http.Request, name string) string {
	title := tr(r, "health.section."+name)
	return `<section class="section-card" aria-labelledby="health-section-` + name + `"><h2 id="health-section-` + name + `">` + html.EscapeString(title) + `</h2>`
}

func writeHealthHTMLField(output *strings.Builder, r *http.Request, key, value string) {
	label := tr(r, "health.field."+key)
	if strings.HasPrefix(key, "health.") {
		label = tr(r, key)
	}
	if strings.HasPrefix(key, "stats.") {
		label = healthKeyLabel(r, "health.stat.unknown", strings.TrimPrefix(key, "stats."))
	}
	fmt.Fprintf(output, `<p><strong>%s:</strong> %s</p>`, html.EscapeString(label), html.EscapeString(value))
}

func healthBooleanValue(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
}

func healthValueLabel(r *http.Request, value string) string {
	key := "health.value." + strings.ReplaceAll(value, "-", "_")
	return tr(r, key)
}

func healthFeatureLabel(r *http.Request, key string) string {
	return healthKeyLabel(r, "health.feature.unknown", key)
}

func healthKeyLabel(r *http.Request, translationKey, key string) string {
	return strings.ReplaceAll(tr(r, translationKey), "{key}", key)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func formatHealthCount(value int64) string {
	text := strconv.FormatInt(value, 10)
	if len(text) <= 3 {
		return text
	}
	var output strings.Builder
	for index, digit := range text {
		if index > 0 && (len(text)-index)%3 == 0 {
			output.WriteByte(',')
		}
		output.WriteRune(digit)
	}
	return output.String()
}

func humanHealthUptime(r *http.Request, uptime time.Duration) string {
	if uptime < 0 {
		uptime = 0
	}
	minutes := int(uptime.Minutes())
	seconds := int(uptime.Seconds())
	days := minutes / (24 * 60)
	minutes %= 24 * 60
	hours := minutes / 60
	minutes %= 60
	parts := make([]string, 0, 3)
	appendUnit := func(amount int, unit string) {
		if amount == 0 {
			return
		}
		unitKey := "health.duration." + unit
		if amount != 1 {
			unitKey += "s"
		}
		parts = append(parts, fmt.Sprintf("%d %s", amount, tr(r, unitKey)))
	}
	appendUnit(days, "day")
	appendUnit(hours, "hour")
	appendUnit(minutes, "minute")
	if len(parts) == 0 {
		appendUnit(seconds, "second")
	}
	return strings.Join(parts, " ")
}

func healthTimezone() *time.Location {
	name := strings.TrimSpace(config.Get().Server.Scheduler.Timezone)
	if name == "" {
		name = "America/New_York"
	}
	location, err := time.LoadLocation(name)
	if err != nil {
		location, _ = time.LoadLocation("America/New_York")
	}
	if location == nil {
		return time.UTC
	}
	return location
}

func humanHealthText(r *http.Request, value healthResponse) string {
	var output strings.Builder
	fmt.Fprintf(&output, "%s\n%s: %s\n", tr(r, "health.title"), tr(r, "health.field.status"), healthValueLabel(r, value.Status))
	if value.PendingRestart != nil {
		writeHealthTextField(&output, r, "pending_restart", tr(r, "health.value.enabled"))
		if len(value.RestartReason) > 0 {
			writeHealthTextField(&output, r, "restart_reason", strings.Join(value.RestartReason, ", "))
		}
	}
	if value.Maintenance != nil {
		info := value.Maintenance
		fmt.Fprintf(&output, "%s\n", tr(r, "health.maintenance.title"))
		writeHealthTextField(&output, r, "health.maintenance.reason", info.Reason)
		writeHealthTextField(&output, r, "health.maintenance.message", info.Message)
		writeHealthTextField(&output, r, "health.field.enabled", healthValueLabel(r, healthBooleanValue(info.SelfHealing.Enabled)))
		writeHealthTextField(&output, r, "health.field.exhausted", healthValueLabel(r, healthBooleanValue(info.SelfHealing.Exhausted)))
		writeHealthTextField(&output, r, "health.maintenance.attempts", strconv.Itoa(info.SelfHealing.Attempts))
		if info.SelfHealing.LastAttempt != nil {
			writeHealthTextField(&output, r, "health.field.last_attempt", *info.SelfHealing.LastAttempt)
		}
		if info.SelfHealing.NextAttempt != nil {
			writeHealthTextField(&output, r, "health.field.next_attempt", *info.SelfHealing.NextAttempt)
		}
	}
	fmt.Fprintf(&output, "%s\n%s: %s\n%s: %s\n%s: %s\n", tr(r, "health.section.version"), tr(r, "health.field.version"), value.Version, tr(r, "health.field.build_commit"), value.Build.Commit, tr(r, "health.field.build_date"), value.Build.Date)
	fmt.Fprintf(&output, "%s\n%s: %s\n%s: %s\n%s: %s\n", tr(r, "health.section.runtime"), tr(r, "health.field.mode"), healthValueLabel(r, value.Mode), tr(r, "health.field.uptime"), humanHealthUptime(r, time.Since(serverStartedAt)), tr(r, "health.field.timestamp"), time.Now().In(healthTimezone()).Format("January 02, 2006 at 15:04:05 MST"))
	output.WriteString(tr(r, "health.section.features") + "\n")
	for _, name := range sortedKeys(value.Features) {
		encoded, _ := json.Marshal(value.Features[name])
		var feature struct {
			Status   string `json:"status"`
			Enabled  bool   `json:"enabled"`
			Hostname string `json:"hostname"`
			Address  string `json:"address"`
		}
		_ = json.Unmarshal(encoded, &feature)
		status := feature.Status
		if status == "" {
			if feature.Enabled {
				status = "enabled"
			} else {
				status = "disabled"
			}
		}
		address := feature.Hostname
		if address == "" {
			address = feature.Address
		}
		fmt.Fprintf(&output, "%s: %s", healthFeatureLabel(r, name), healthValueLabel(r, status))
		if address != "" {
			fmt.Fprintf(&output, " %s", address)
		}
		output.WriteByte('\n')
	}
	output.WriteString(tr(r, "health.section.checks") + "\n")
	for _, name := range sortedKeys(value.Checks) {
		fmt.Fprintf(&output, "%s: %s\n", healthKeyLabel(r, "health.check.unknown", name), healthValueLabel(r, value.Checks[name]))
	}
	fmt.Fprintf(&output, "%s\n", tr(r, "health.section.stats"))
	writeHealthTextField(&output, r, "stats.requests_total", formatHealthCount(value.Stats.RequestsTotal))
	writeHealthTextField(&output, r, "stats.requests_24h", formatHealthCount(value.Stats.Requests24Hours))
	writeHealthTextField(&output, r, "stats.active_connections", formatHealthCount(value.Stats.ActiveConnections))
	return strings.TrimRight(output.String(), "\n") + "\n"
}

func writeHealthTextField(output *strings.Builder, r *http.Request, key, value string) {
	label := tr(r, "health.field."+key)
	if strings.HasPrefix(key, "health.") {
		label = tr(r, key)
	}
	if strings.HasPrefix(key, "stats.") {
		label = healthKeyLabel(r, "health.stat.unknown", strings.TrimPrefix(key, "stats."))
	}
	fmt.Fprintf(output, "%s: %s\n", label, value)
}

func healthText(value healthResponse) string {
	var output strings.Builder
	fmt.Fprintf(&output, "project.name: %s\nproject.tagline: %s\nproject.description: %s\nstatus: %s\n", value.Project.Name, value.Project.Tagline, value.Project.Description, value.Status)
	if value.PendingRestart != nil {
		fmt.Fprintf(&output, "pending_restart: %t\n", *value.PendingRestart)
	}
	if len(value.RestartReason) > 0 {
		fmt.Fprintf(&output, "restart_reason: %s\n", strings.Join(value.RestartReason, ", "))
	}
	if value.Maintenance != nil {
		fmt.Fprintf(&output, "maintenance.reason: %s\nmaintenance.message: %s\nmaintenance.since: %s\nmaintenance.self_healing.enabled: %t\nmaintenance.self_healing.exhausted: %t\nmaintenance.self_healing.attempts: %d\n", value.Maintenance.Reason, value.Maintenance.Message, value.Maintenance.Since, value.Maintenance.SelfHealing.Enabled, value.Maintenance.SelfHealing.Exhausted, value.Maintenance.SelfHealing.Attempts)
		if value.Maintenance.SelfHealing.LastAttempt != nil {
			fmt.Fprintf(&output, "maintenance.self_healing.last_attempt: %s\n", *value.Maintenance.SelfHealing.LastAttempt)
		}
		if value.Maintenance.SelfHealing.NextAttempt != nil {
			fmt.Fprintf(&output, "maintenance.self_healing.next_attempt: %s\n", *value.Maintenance.SelfHealing.NextAttempt)
		}
	}
	fmt.Fprintf(&output, "version: %s\ngo_version: %s\nbuild.commit: %s\nbuild.date: %s\nuptime: %s\nmode: %s\ntimestamp: %s\n", value.Version, value.GoVersion, value.Build.Commit, value.Build.Date, value.Uptime, value.Mode, value.Timestamp)
	featureNames := make([]string, 0, len(value.Features))
	for name := range value.Features {
		featureNames = append(featureNames, name)
	}
	sort.Strings(featureNames)
	for _, name := range featureNames {
		encoded, _ := json.Marshal(value.Features[name])
		var feature struct {
			Enabled  bool   `json:"enabled"`
			Status   string `json:"status"`
			Hostname string `json:"hostname"`
			Address  string `json:"address"`
			Provider string `json:"provider"`
			State    string `json:"state"`
		}
		_ = json.Unmarshal(encoded, &feature)
		label := feature.Status
		if label == "" {
			if feature.Enabled {
				label = "enabled"
			} else {
				label = "disabled"
			}
		}
		if feature.Hostname != "" {
			label += " " + feature.Hostname
		}
		if feature.Address != "" && feature.Hostname == "" {
			label += " " + feature.Address
		}
		if feature.Provider != "" {
			label += " provider=" + feature.Provider
		}
		if feature.State != "" {
			label += " state=" + feature.State
		}
		fmt.Fprintf(&output, "features.%s: %s\n", name, label)
	}
	checkNames := make([]string, 0, len(value.Checks))
	for name := range value.Checks {
		checkNames = append(checkNames, name)
	}
	sort.Strings(checkNames)
	for _, name := range checkNames {
		fmt.Fprintf(&output, "checks.%s: %s\n", name, value.Checks[name])
	}
	fmt.Fprintf(&output, "stats.requests_total: %d\nstats.requests_24h: %d\nstats.active_connections: %d\n", value.Stats.RequestsTotal, value.Stats.Requests24Hours, value.Stats.ActiveConnections)
	return strings.TrimRight(output.String(), "\n") + "\n"
}

func requestStatsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		activeRequests.Add(1)
		defer activeRequests.Add(-1)
		next.ServeHTTP(w, r)
		if !excludedHealthCounterPath(r.URL.Path) {
			countHealthRequest(time.Now().UTC())
		}
	})
}

func excludedHealthCounterPath(path string) bool {
	if path == "/server/healthz" || path == "/healthz" || path == "/api/healthz" || strings.HasSuffix(path, "/server/healthz") {
		return true
	}
	return path == "/metrics" || path == "/server/metrics" || strings.HasPrefix(path, "/metrics/") || strings.HasSuffix(path, "/server/metrics") || strings.HasPrefix(path, "/server/metrics/")
}

func SnapshotHealthRequestCounters() HealthRequestCounterBatch {
	requestCounterMu.Lock()
	defer requestCounterMu.Unlock()
	buckets := make([]HealthRequestHourBucket, 0, len(pendingRequestHours))
	for hour, count := range pendingRequestHours {
		if count <= 0 {
			continue
		}
		buckets = append(buckets, HealthRequestHourBucket{HourStart: time.Unix(hour, 0).UTC(), Count: count})
	}
	sort.Slice(buckets, func(i, j int) bool { return buckets[i].HourStart.Before(buckets[j].HourStart) })
	return HealthRequestCounterBatch{Total: pendingRequestTotal, Hours: buckets}
}

func AcknowledgeHealthRequestCounters(batch HealthRequestCounterBatch) {
	requestCounterMu.Lock()
	defer requestCounterMu.Unlock()
	acknowledgeHealthRequestCountersLocked(batch)
}

// CommitHealthRequestCounters acknowledges a persisted batch and updates its cache atomically to health readers.
func CommitHealthRequestCounters(batch HealthRequestCounterBatch, updateCache func()) {
	requestCounterMu.Lock()
	defer requestCounterMu.Unlock()
	if updateCache != nil {
		updateCache()
	}
	acknowledgeHealthRequestCountersLocked(batch)
}

func acknowledgeHealthRequestCountersLocked(batch HealthRequestCounterBatch) {
	if batch.Total < 0 {
		return
	}
	pendingRequestTotal -= batch.Total
	if pendingRequestTotal < 0 {
		pendingRequestTotal = 0
	}
	for _, bucket := range batch.Hours {
		hour := bucket.HourStart.UTC().Truncate(time.Hour).Unix()
		pendingRequestHours[hour] -= bucket.Count
		if pendingRequestHours[hour] <= 0 {
			delete(pendingRequestHours, hour)
		}
	}
}

func countHealthRequest(at time.Time) {
	hour := at.UTC().Truncate(time.Hour).Unix()
	requestCounterMu.Lock()
	pendingRequestTotal++
	pendingRequestHours[hour]++
	requestCounterMu.Unlock()
}

func writeHealthResponse(w http.ResponseWriter, r *http.Request) {
	response, status := buildHealthResponse()
	if wantsText(r) {
		writeText(w, status, healthText(response))
		return
	}
	writeJSON(w, r, status, response)
}

func maintenanceMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isHealthEndpoint(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		info, active := currentMaintenance()
		if isWriteMethod(r.Method) && active {
			writeMaintenanceError(w, r, info)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		available, configured := canServeMaintenanceReads(ctx)
		cancel()
		if available || !configured {
			next.ServeHTTP(w, r)
			return
		}
		if !active {
			info = maintenance.Info{Reason: "database_connection", Message: "Database connection failed"}
		}
		writeMaintenanceError(w, r, info)
	})
}

func isHealthEndpoint(path string) bool {
	return path == "/server/healthz" || path == "/healthz" || path == "/api/healthz" || strings.HasSuffix(path, "/server/healthz") || strings.HasSuffix(path, "/server/healthz.txt") || path == "/api/healthz.txt"
}

func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func writeMaintenanceError(w http.ResponseWriter, r *http.Request, info maintenance.Info) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Maintenance-Mode", "true")
	w.Header().Set("X-Maintenance-Reason", info.Reason)
	retryAfter := 30
	if next := info.SelfHealing.NextAttempt; next != nil {
		if at, err := time.Parse(time.RFC3339, *next); err == nil {
			retryAfter = int(math.Ceil(time.Until(at).Seconds()))
			if retryAfter < 1 {
				retryAfter = 1
			}
		}
	}
	w.Header().Set("Retry-After", fmt.Sprintf("%d", retryAfter))
	message := i18n.Format(languageCode(r), "health.maintenance.status", map[string]string{"message": info.Message})
	if wantsText(r) {
		writeText(w, http.StatusServiceUnavailable, "ERROR: MAINTENANCE: "+message+"\ndetails.reason: "+info.Reason)
		return
	}
	if htmlPageRequest(r) {
		content := `<section class="section-card" aria-labelledby="maintenance-message"><h2 id="maintenance-message">` + html.EscapeString(tr(r, "health.maintenance.title")) + `</h2><p>` + html.EscapeString(message) + `</p><p><strong>` + html.EscapeString(tr(r, "health.maintenance.reason")) + `:</strong> ` + html.EscapeString(info.Reason) + `</p></section>`
		pageResponse := &capturedPageResponse{header: make(http.Header)}
		page(pageResponse, r, tr(r, "health.maintenance.title"), template.HTML(content))
		for name, values := range pageResponse.header {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(pageResponse.body.Bytes())
		return
	}
	response := map[string]any{
		"ok": false, "error": "MAINTENANCE", "message": message,
		"details":    map[string]any{"reason": info.Reason, "self_healing": info.SelfHealing.Enabled},
		"request_id": w.Header().Get("X-Request-ID"),
	}
	var body bytes.Buffer
	encoder := json.NewEncoder(&body)
	encoder.SetIndent("", "  ")
	if encoder.Encode(response) != nil {
		http.Error(w, "Server is in maintenance mode", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write(body.Bytes())
}
