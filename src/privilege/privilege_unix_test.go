//go:build linux || darwin || freebsd || openbsd || netbsd

package privilege

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCanEscalateDetectsDoasAndDoesNotDetectSu(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "doas" || name == "su" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	command := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	}
	currentUser := func() (*user.User, error) { return nil, errors.New("no user") }
	if !canEscalate("", "", 1000, lookPath, command, currentUser) {
		t.Fatal("permitted doas should be detected")
	}
	lookPath = func(name string) (string, error) {
		if name == "su" {
			return "/bin/su", nil
		}
		return "", errors.New("not found")
	}
	if canEscalate("", "", 1000, lookPath, command, currentUser) {
		t.Fatal("su availability must not count as detected escalation access")
	}
}

func TestEscalationMethodUsesPlatformOrder(t *testing.T) {
	lookPath := func(name string) (string, error) {
		if name == "sudo" || name == "doas" {
			return "/usr/bin/" + name, nil
		}
		return "", errors.New("not found")
	}
	command := func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "true")
	}
	noUser := func() (*user.User, error) { return nil, errors.New("no user") }
	for _, test := range []struct {
		goos string
		want string
	}{
		{goos: "linux", want: "/usr/bin/sudo"},
		{goos: "freebsd", want: "/usr/bin/doas"},
	} {
		if got := escalationMethodFor(test.goos, "", "", 1000, lookPath, command, noUser); got != test.want {
			t.Errorf("escalationMethodFor(%q) = %q, want %q", test.goos, got, test.want)
		}
	}
}

func TestShellQuoteEscapesSingleQuotes(t *testing.T) {
	if got, want := shellQuote("a'b c"), "'a'\\''b c'"; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestDropPrivilegesOrdersChangesAndVerifiesIdentity(t *testing.T) {
	var calls []string
	uid, gid := 650, 650
	setgroups := func(groups []int) error {
		calls = append(calls, "groups")
		if !reflect.DeepEqual(groups, []int{gid}) {
			t.Errorf("supplementary groups = %v, want [%d]", groups, gid)
		}
		return nil
	}
	setgid := func(value int) error {
		calls = append(calls, "gid")
		if value != gid {
			t.Errorf("setgid(%d), want %d", value, gid)
		}
		return nil
	}
	setuid := func(value int) error {
		calls = append(calls, "uid")
		if value != uid {
			t.Errorf("setuid(%d), want %d", value, uid)
		}
		return nil
	}
	if err := dropPrivilegesWith(uid, gid, setgroups, setgid, setuid, func() int { return uid }, func() int { return uid }, func() int { return gid }); err != nil {
		t.Fatalf("dropPrivilegesWith() error = %v", err)
	}
	if want := []string{"groups", "gid", "uid"}; !reflect.DeepEqual(calls, want) {
		t.Fatalf("privilege change order = %v, want %v", calls, want)
	}
}

func TestDropPrivilegesStopsOnFailure(t *testing.T) {
	wantErr := errors.New("group change failed")
	uidCalled := false
	err := dropPrivilegesWith(650, 650, func([]int) error { return nil }, func(int) error { return wantErr }, func(int) error {
		uidCalled = true
		return nil
	}, func() int { return 650 }, func() int { return 650 }, func() int { return 650 })
	if !errors.Is(err, wantErr) {
		t.Fatalf("dropPrivilegesWith() error = %v, want wrapped group error", err)
	}
	if uidCalled {
		t.Fatal("setuid ran after setgid failed")
	}
}

func TestChownTreeOnlyVisitsRegularTreeEntries(t *testing.T) {
	dir := projectTestDir(t)
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	var visited []string
	err := chownTree([]string{dir}, func(path string, _, _ int) error {
		visited = append(visited, path)
		return nil
	}, 650, 650)
	if err != nil {
		t.Fatalf("chownTree() error = %v", err)
	}
	if len(visited) != 2 {
		t.Fatalf("chownTree() visited %v, want directory and file", visited)
	}
}

func TestChownTreeSkipsOwnershipMarkerSubtree(t *testing.T) {
	dir := projectTestDir(t)
	markerDir := filepath.Join(dir, accountOwnershipDirectory)
	if err := os.Mkdir(markerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(markerDir, accountOwnershipMarker)
	if err := os.WriteFile(marker, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "data")
	if err := os.WriteFile(file, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	var visited []string
	err := chownTree([]string{dir}, func(path string, _, _ int) error {
		visited = append(visited, path)
		return nil
	}, 650, 650)
	if err != nil {
		t.Fatalf("chownTree() error = %v", err)
	}
	for _, path := range visited {
		if path == markerDir || path == marker {
			t.Fatalf("chownTree() visited protected ownership path %s", path)
		}
	}
}
