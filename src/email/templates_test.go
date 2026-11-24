package email

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedEmailTemplatesRenderTranslatedMessage(t *testing.T) {
	manager := NewTemplateManager("")
	if got := len(manager.Names()); got != 16 {
		t.Fatalf("template count = %d, want 16", got)
	}
	message, err := manager.Render("disk_low", "es", map[string]string{
		"app_name": "Git Messages", "fqdn": "messages.example.test", "path": "/data", "used_percent": "92",
		"free": "8 GiB", "threshold_percent": "85", "timestamp": "2026-11-22T12:00:00Z", "details": "Path: /data\n<script>alert(1)</script>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if message.Subject != "Poco espacio en disco - Git Messages" {
		t.Fatalf("translated subject = %q", message.Subject)
	}
	for _, part := range []string{"Motivo de este mensaje", "Git Messages (messages.example.test)", "/data", "https://messages.example.test"} {
		if !strings.Contains(message.Text, part) {
			t.Errorf("rendered text is missing %q: %s", part, message.Text)
		}
	}
	if strings.Contains(message.HTML, "<script>") || !strings.Contains(message.HTML, "&lt;script&gt;") {
		t.Fatalf("HTML alternative did not escape template data: %s", message.HTML)
	}
}

func TestCustomEmailTemplatesReloadAndValidate(t *testing.T) {
	customDir := emailTestDir(t)
	path := filepath.Join(customDir, "test.txt")
	if err := os.WriteFile(path, []byte("Subject: First {app_name}\n---\nMessage for {fqdn}."), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewTemplateManager(customDir)
	first, err := manager.Render("test", "en", map[string]string{"app_name": "Git Messages", "fqdn": "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Subject != "First Git Messages" || first.Text != "Message for example.test." {
		t.Fatalf("first render = %+v", first)
	}
	if err := os.WriteFile(path, []byte("Subject: Second {app_name}\n---\nChanged for {fqdn}."), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := manager.Render("test", "en", map[string]string{"app_name": "Git Messages", "fqdn": "example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Subject != "Second Git Messages" || second.Text != "Changed for example.test." {
		t.Fatalf("reloaded render = %+v", second)
	}
	if err := os.WriteFile(path, []byte("Subject: Invalid {foo}\n---\nBody"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Load("test"); err == nil || !strings.Contains(err.Error(), "unknown variable: {foo}") {
		t.Fatalf("unknown variable validation error = %v", err)
	}
}

func TestValidateEmailTemplateErrorsAndWarnings(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want string
	}{
		{"empty subject", "Subject: \n---\nBody", "subject cannot be empty"},
		{"empty body", "Subject: Notice\n---\n  ", "body cannot be empty"},
		{"invalid syntax", "Subject: Notice\n---\n{{t \"email.key\"}}", "invalid template syntax at line 3"},
		{"unknown variable suggestion", "Subject: {fqnd}\n---\nBody", "Did you mean {fqdn}?"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ValidateTemplate("test", test.raw); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want %q", err, test.want)
			}
		})
	}
	long := "Subject: " + strings.Repeat("x", 80) + "\n---\nBody"
	warnings, err := ValidateTemplate("test", long)
	if err != nil || len(warnings) != 1 || !strings.Contains(warnings[0], "78") {
		t.Fatalf("long subject validation = %v, %v", warnings, err)
	}
}
