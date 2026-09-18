package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestDetectServiceManager verifies the detector always returns one of the
// known ServiceType constants for the current host, never an out-of-range
// value.
func TestDetectServiceManager(t *testing.T) {
	got := DetectServiceManager()

	switch got {
	case ServiceUnknown, ServiceSystemd, ServiceRunit, ServiceLaunchd, ServiceWindows, ServiceBSDRC:
		// Expected.
	default:
		t.Errorf("DetectServiceManager() returned unrecognized ServiceType %v", got)
	}

	// On Linux specifically, DetectServiceManager must never return the
	// launchd/windows values.
	if runtime.GOOS == "linux" && (got == ServiceLaunchd || got == ServiceWindows) {
		t.Errorf("DetectServiceManager() on linux returned %v, want a linux-only type", got)
	}
}

// TestGetBinaryPath verifies the returned path is well-formed for the
// current OS and always includes the app name.
func TestGetBinaryPath(t *testing.T) {
	path := GetBinaryPath()

	if !strings.Contains(path, "gitmessages") {
		t.Errorf("GetBinaryPath() = %q, want it to contain the app name", path)
	}

	switch runtime.GOOS {
	case "windows":
		if !strings.HasPrefix(path, `C:\Program Files\`) {
			t.Errorf("GetBinaryPath() on windows = %q, want C:\\Program Files\\ prefix", path)
		}
		if !strings.HasSuffix(path, ".exe") {
			t.Errorf("GetBinaryPath() on windows = %q, want .exe suffix", path)
		}
	default:
		if path != "/usr/local/bin/gitmessages" {
			t.Errorf("GetBinaryPath() = %q, want /usr/local/bin/gitmessages", path)
		}
	}
}

// TestCopyBinary_HappyPath verifies the source file's contents land at the
// destination, including creating a nonexistent destination directory.
func TestCopyBinary_HappyPath(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "source-binary")
	dst := filepath.Join(tmp, "nested", "dest-binary")

	content := []byte("fake binary contents")
	if err := os.WriteFile(src, content, 0755); err != nil {
		t.Fatalf("failed to seed source file: %v", err)
	}

	if err := copyBinary(src, dst); err != nil {
		t.Fatalf("copyBinary() error: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("failed to read destination file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("copied content = %q, want %q", got, content)
	}

	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("failed to stat destination file: %v", err)
	}
	if info.Mode().Perm() != 0755 {
		t.Errorf("destination file mode = %v, want 0755", info.Mode().Perm())
	}
}

// TestCopyBinary_MissingSource verifies a nonexistent source surfaces an
// error rather than silently creating an empty destination.
func TestCopyBinary_MissingSource(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "does-not-exist")
	dst := filepath.Join(tmp, "dest-binary")

	if err := copyBinary(src, dst); err == nil {
		t.Fatal("expected error copying a nonexistent source, got nil")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("destination should not exist after a failed copy")
	}
}

// TestCapitalize covers the ASCII-first-rune capitalization helper used to
// build the Windows service display name.
func TestCapitalize(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"lowercase word", "gitmessages", "Gitmessages"},
		{"already capitalized", "Gitmessages", "Gitmessages"},
		{"single char", "a", "A"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := capitalize(tt.in); got != tt.want {
				t.Errorf("capitalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
