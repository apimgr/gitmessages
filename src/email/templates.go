package email

import (
	"errors"
	"fmt"
	"html"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/apimgr/gitmessages/src/common/i18n"
	emailtemplates "github.com/apimgr/gitmessages/src/server/template"
)

var (
	templateNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	variablePattern     = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)
)

var emailTemplateNames = []string{
	"security_alert", "backup_complete", "backup_failed", "ssl_expiring", "ssl_renewed",
	"ssl_renewal_failed", "scheduler_error", "startup", "shutdown", "disk_low",
	"database_error", "maintenance_entered", "maintenance_exited", "self_healing_exhausted",
	"update_available", "update_installed", "update_failed", "update_staged", "test",
}

var emailTemplateVariables = map[string]struct{}{
	"app_name": {}, "app_url": {}, "fqdn": {}, "onion_url": {}, "onion_address": {},
	"i2p_url": {}, "i2p_address": {}, "notification_reply_to": {}, "timestamp": {}, "year": {},
	"email_subject": {}, "email_body": {}, "event": {}, "reason": {}, "details": {},
	"filename": {}, "size": {}, "error": {}, "domain": {}, "expires_in": {}, "expiry_date": {},
	"valid_until": {}, "next_retry": {}, "hostname": {}, "version": {}, "mode": {},
	"path": {}, "used_percent": {}, "free": {}, "threshold_percent": {}, "since": {}, "duration": {},
	"current_version": {}, "new_version": {}, "channel": {}, "previous_version": {}, "attempts": {},
	"task_name": {}, "next_run": {}, "ip": {},
}

// TemplateManager reads custom files for every render so operator edits take effect immediately.
type TemplateManager struct {
	customDir string
	defaults  fs.FS
}

func NewTemplateManager(customDir string) *TemplateManager {
	return &TemplateManager{customDir: customDir, defaults: emailtemplates.EmailFiles}
}

// Names returns the supported template identifiers in stable order.
func (m *TemplateManager) Names() []string {
	return append([]string(nil), emailTemplateNames...)
}

// Load returns a validated custom template or its embedded default.
func (m *TemplateManager) Load(name string) (string, error) {
	if !templateNamePattern.MatchString(name) || !containsTemplateName(name) {
		return "", fmt.Errorf("unknown email template %q", name)
	}
	if m.customDir != "" {
		customPath := filepath.Join(m.customDir, name+".txt")
		custom, err := os.ReadFile(customPath)
		if err == nil {
			if _, err := ValidateTemplate(name, string(custom)); err != nil {
				return "", fmt.Errorf("validate custom email template %s: %w", name, err)
			}
			return string(custom), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("read custom email template %s: %w", name, err)
		}
	}
	data, err := fs.ReadFile(m.defaults, "email/"+name+".txt")
	if err != nil {
		return "", fmt.Errorf("read embedded email template %s: %w", name, err)
	}
	if _, err := ValidateTemplate(name, string(data)); err != nil {
		return "", fmt.Errorf("validate embedded email template %s: %w", name, err)
	}
	return string(data), nil
}

// Render validates and renders one plain-text template and creates its HTML alternative.
func (m *TemplateManager) Render(name, language string, values map[string]string) (Message, error) {
	raw, err := m.Load(name)
	if err != nil {
		return Message{}, err
	}
	subject, body, err := splitEmailTemplate(raw)
	if err != nil {
		return Message{}, err
	}
	variables := make(map[string]string, len(values)+14)
	for key, value := range values {
		variables[key] = value
	}
	now := time.Now()
	if variables["app_name"] == "" {
		variables["app_name"] = "Git Messages"
	}
	if variables["fqdn"] == "" {
		variables["fqdn"] = ""
	}
	if variables["app_url"] == "" {
		variables["app_url"] = ""
	}
	if variables["timestamp"] == "" {
		variables["timestamp"] = now.Format(time.RFC3339)
	}
	if variables["year"] == "" {
		variables["year"] = fmt.Sprint(now.Year())
	}
	if variables["event"] == "" {
		variables["event"] = i18n.T(language, "email.events."+name)
	}
	if variables["reason"] == "" {
		variables["reason"] = variables["event"]
	}
	if variables["details"] == "" {
		variables["details"] = emailDetails(values)
	}
	variables["email_subject"] = i18n.Format(language, "email.subjects.operator_alert", map[string]string{
		"event": variables["event"], "app_name": variables["app_name"],
	})
	variables["email_body"] = i18n.Format(language, "email.body.operator_notification", map[string]string{
		"reason": variables["reason"], "app_name": variables["app_name"], "fqdn": variables["fqdn"],
		"timestamp": variables["timestamp"], "details": variables["details"], "app_url": variables["app_url"],
	})
	subject = strings.TrimSpace(replaceEmailVariables(subject, variables))
	body = strings.TrimSpace(replaceEmailVariables(body, variables))
	if subject == "" || body == "" {
		return Message{}, errors.New("rendered email subject and body are required")
	}
	return Message{Subject: subject, Text: body, HTML: "<p>" + strings.ReplaceAll(html.EscapeString(body), "\n", "<br>\n") + "</p>"}, nil
}

// ValidateTemplate checks the required format, known variables and non-empty content.
func ValidateTemplate(name, raw string) ([]string, error) {
	if !templateNamePattern.MatchString(name) || !containsTemplateName(name) {
		return nil, fmt.Errorf("unknown email template %q", name)
	}
	subject, body, err := splitEmailTemplate(raw)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(subject) == "" {
		return nil, errors.New("subject cannot be empty")
	}
	if strings.TrimSpace(body) == "" {
		return nil, errors.New("body cannot be empty")
	}
	if line := invalidTemplateLine(raw); line > 0 {
		return nil, fmt.Errorf("invalid template syntax at line %d", line)
	}
	warnings := []string{}
	for _, match := range variablePattern.FindAllStringSubmatch(raw, -1) {
		if _, ok := emailTemplateVariables[match[1]]; !ok {
			return warnings, fmt.Errorf("unknown variable: {%s}%s", match[1], variableSuggestion(match[1]))
		}
	}
	if len(subject) > 78 {
		warnings = append(warnings, "subject line is longer than 78 characters")
	}
	return warnings, nil
}

func splitEmailTemplate(raw string) (string, string, error) {
	raw = strings.ReplaceAll(raw, "\r\n", "\n")
	lines := strings.Split(raw, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "Subject:") {
		return "", "", errors.New("invalid template syntax at line 1")
	}
	separator := -1
	for index := 1; index < len(lines); index++ {
		if lines[index] == "---" {
			separator = index
			break
		}
	}
	if separator < 0 {
		return "", "", errors.New("invalid template syntax: missing --- separator")
	}
	subject := strings.TrimSpace(strings.TrimPrefix(lines[0], "Subject:"))
	body := strings.TrimSpace(strings.Join(lines[separator+1:], "\n"))
	return subject, body, nil
}

func invalidTemplateLine(raw string) int {
	for index, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		if strings.Contains(line, "{{") || strings.Contains(line, "}}") {
			return index + 1
		}
		withoutVariables := variablePattern.ReplaceAllString(line, "")
		if strings.ContainsAny(withoutVariables, "{}") {
			return index + 1
		}
	}
	return 0
}

func replaceEmailVariables(value string, variables map[string]string) string {
	keys := make([]string, 0, len(variables))
	for key := range variables {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	replacements := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		replacements = append(replacements, "{"+key+"}", variables[key])
	}
	return strings.NewReplacer(replacements...).Replace(value)
}

func emailDetails(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if value != "" && key != "details" && key != "reason" && key != "event" && key != "app_name" && key != "app_url" && key != "fqdn" && key != "timestamp" && key != "year" && key != "notification_reply_to" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, strings.ReplaceAll(key, "_", " ")+": "+values[key])
	}
	return strings.Join(lines, "\n")
}

func variableSuggestion(value string) string {
	best, distance := "", 4
	for candidate := range emailTemplateVariables {
		if current := editDistance(value, candidate); current < distance {
			best, distance = candidate, current
		}
	}
	if best == "" {
		return ""
	}
	return ". Did you mean {" + best + "}?"
}

func editDistance(left, right string) int {
	previous := make([]int, len(right)+1)
	for column := range previous {
		previous[column] = column
	}
	for row, leftChar := range left {
		current := make([]int, len(right)+1)
		current[0] = row + 1
		for column, rightChar := range right {
			cost := 0
			if leftChar != rightChar {
				cost = 1
			}
			current[column+1] = min(current[column]+1, previous[column+1]+1, previous[column]+cost)
		}
		previous = current
	}
	return previous[len(right)]
}

func containsTemplateName(name string) bool {
	for _, candidate := range emailTemplateNames {
		if candidate == name {
			return true
		}
	}
	return false
}
