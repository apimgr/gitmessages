//go:build !windows

package privilege

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSelectServiceID(t *testing.T) {
	id, err := selectServiceID("linux", func(id int) bool { return id == 899 }, func(int) bool { return false })
	if err != nil {
		t.Fatalf("selectServiceID() error = %v", err)
	}
	if id != 898 {
		t.Fatalf("selectServiceID() = %d, want 898", id)
	}
	macID, err := selectServiceID("darwin", func(int) bool { return false }, func(int) bool { return false })
	if err != nil {
		t.Fatalf("selectServiceID(darwin) error = %v", err)
	}
	if macID != 399 {
		t.Fatalf("selectServiceID(darwin) = %d, want 399", macID)
	}
	if _, err := selectServiceID("linux", func(int) bool { return true }, func(int) bool { return true }); err == nil {
		t.Fatal("selectServiceID() with all IDs occupied returned no error")
	}
}

func TestEnsureServiceAccountUsesMatchingIDsAndCommands(t *testing.T) {
	home := projectTestDir(t)
	lookup := accountLookup{
		userByName:  func(string) (int, int, bool, error) { return 0, 0, false, nil },
		groupByName: func(string) (int, bool, error) { return 0, false, nil },
		uidInUse:    func(id int) bool { return id == 899 },
		gidInUse:    func(int) bool { return false },
	}
	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}
	uid, gid, err := ensureServiceAccount("linux", home, lookup, run)
	if err != nil {
		t.Fatalf("ensureServiceAccount() error = %v", err)
	}
	if uid != 898 || gid != 898 {
		t.Fatalf("ensureServiceAccount() IDs = %d:%d, want 898:898", uid, gid)
	}
	if len(calls) != 2 || calls[0][0] != "groupadd" || calls[1][0] != "useradd" {
		t.Fatalf("account creation commands = %#v, want groupadd then useradd", calls)
	}
	if !reflect.DeepEqual(calls[0][1:], []string{"--system", "--gid", "898", serviceName}) {
		t.Errorf("groupadd args = %#v", calls[0][1:])
	}
	if !containsArgumentSequence(calls[1], []string{"--uid", "898", "--gid", "898", "--home-dir", home}) {
		t.Errorf("useradd command does not set matching IDs and home: %#v", calls[1])
	}
	if _, err := os.Stat(filepath.Join(home, accountOwnershipDirectory, accountOwnershipMarker)); err != nil {
		t.Errorf("created account ownership marker: %v", err)
	}
}

func TestEnsureServiceAccountReusesExistingGroup(t *testing.T) {
	home := projectTestDir(t)
	lookup := accountLookup{
		userByName:  func(string) (int, int, bool, error) { return 0, 0, false, nil },
		groupByName: func(string) (int, bool, error) { return 700, true, nil },
		uidInUse:    func(int) bool { return false },
		gidInUse:    func(int) bool { return true },
	}
	var calls [][]string
	run := func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	}
	uid, gid, err := ensureServiceAccount("linux", home, lookup, run)
	if err != nil {
		t.Fatalf("ensureServiceAccount() error = %v", err)
	}
	if uid != 700 || gid != 700 {
		t.Fatalf("ensureServiceAccount() IDs = %d:%d, want 700:700", uid, gid)
	}
	if len(calls) != 1 || calls[0][0] != "useradd" {
		t.Fatalf("commands for existing service group = %#v, want useradd only", calls)
	}
	marker, err := os.ReadFile(filepath.Join(home, accountOwnershipDirectory, accountOwnershipMarker))
	if err != nil {
		t.Fatalf("read created-account marker: %v", err)
	}
	if string(marker) != accountOwnershipContents+"group-created=false\n" {
		t.Fatalf("marker contents = %q, want pre-existing group recorded", marker)
	}
}

func TestEnsureServiceAccountDoesNotMarkPreexistingAccount(t *testing.T) {
	home := projectTestDir(t)
	lookup := accountLookup{
		userByName:  func(string) (int, int, bool, error) { return 700, 700, true, nil },
		groupByName: func(string) (int, bool, error) { return 700, true, nil },
	}
	if _, _, err := ensureServiceAccount("linux", home, lookup, func(string, ...string) error {
		t.Fatal("must not run commands for an existing account")
		return nil
	}); err != nil {
		t.Fatalf("ensureServiceAccount() error = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(home, accountOwnershipDirectory)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pre-existing account ownership marker stat error = %v, want not-exist", err)
	}
}

func TestDeleteServiceAccountPlansAndRemovesMarker(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "freebsd", "openbsd", "netbsd"} {
		home := projectTestDir(t)
		if err := writeAccountOwnershipMarker(home, true); err != nil {
			t.Fatalf("write marker for %s: %v", goos, err)
		}
		var calls [][]string
		run := func(name string, args ...string) error {
			calls = append(calls, append([]string{name}, args...))
			return nil
		}
		if err := deleteServiceAccount(goos, home, run); err != nil {
			t.Errorf("deleteServiceAccount(%s) error = %v", goos, err)
			continue
		}
		if len(calls) != 2 {
			t.Errorf("deleteServiceAccount(%s) commands = %#v, want user and created group deletion", goos, calls)
		}
		if _, err := os.Lstat(filepath.Join(home, accountOwnershipDirectory)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("deleteServiceAccount(%s) marker stat error = %v, want not-exist", goos, err)
		}
	}
}

func TestDeleteServiceAccountPreservesPreexistingGroup(t *testing.T) {
	home := projectTestDir(t)
	if err := writeAccountOwnershipMarker(home, false); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	err := deleteServiceAccount("linux", home, func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		return nil
	})
	if err != nil {
		t.Fatalf("deleteServiceAccount() error = %v", err)
	}
	if len(calls) != 1 || calls[0][0] != "userdel" {
		t.Fatalf("delete commands = %#v, want user deletion only", calls)
	}
}

type accountCommandExitError int

func (err accountCommandExitError) Error() string { return "account command failed" }

func (err accountCommandExitError) ExitCode() int { return int(err) }

func TestDeleteServiceAccountAllowsUserdelToRemovePrivateGroup(t *testing.T) {
	home := projectTestDir(t)
	if err := writeAccountOwnershipMarker(home, true); err != nil {
		t.Fatal(err)
	}
	var calls [][]string
	err := deleteServiceAccount("linux", home, func(name string, args ...string) error {
		calls = append(calls, append([]string{name}, args...))
		if name == "groupdel" {
			return accountCommandExitError(6)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("deleteServiceAccount() error = %v, want missing private group to be tolerated", err)
	}
	if len(calls) != 2 || calls[0][0] != "userdel" || calls[1][0] != "groupdel" {
		t.Fatalf("delete commands = %#v, want userdel then groupdel", calls)
	}
	if _, err := os.Lstat(filepath.Join(home, accountOwnershipDirectory)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ownership marker stat error = %v, want not-exist", err)
	}
}

func TestDeleteServiceAccountDoesNotDeleteWithoutMarker(t *testing.T) {
	called := false
	if err := deleteServiceAccount("linux", projectTestDir(t), func(string, ...string) error {
		called = true
		return nil
	}); err != nil {
		t.Fatalf("deleteServiceAccount() error = %v", err)
	}
	if called {
		t.Fatal("deleteServiceAccount() ran a command without ownership marker")
	}
}

func TestEnsureServiceAccountRejectsExistingMismatchedAccount(t *testing.T) {
	lookup := accountLookup{
		userByName:  func(string) (int, int, bool, error) { return 700, 701, true, nil },
		groupByName: func(string) (int, bool, error) { return 701, true, nil },
	}
	_, _, err := ensureServiceAccount("linux", projectTestDir(t), lookup, func(string, ...string) error {
		t.Fatal("must not mutate account after detecting mismatched UID/GID")
		return nil
	})
	if err == nil {
		t.Fatal("ensureServiceAccount() accepted a mismatched UID/GID")
	}
}

func TestEnsureServiceAccountRequiresExistingHomeDirectory(t *testing.T) {
	home := filepath.Join(projectTestDir(t), "missing")
	_, _, err := ensureServiceAccount("linux", home, accountLookup{}, func(string, ...string) error {
		t.Fatal("must not create a service account when its home is missing")
		return nil
	})
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ensureServiceAccount() error = %v, want missing-home error", err)
	}
}

func TestEnsureServiceAccountReportsCommandFailure(t *testing.T) {
	lookup := accountLookup{
		userByName:  func(string) (int, int, bool, error) { return 0, 0, false, nil },
		groupByName: func(string) (int, bool, error) { return 0, false, nil },
		uidInUse:    func(int) bool { return false },
		gidInUse:    func(int) bool { return false },
	}
	wantErr := errors.New("groupadd failed")
	_, _, err := ensureServiceAccount("linux", projectTestDir(t), lookup, func(string, ...string) error { return wantErr })
	if !errors.Is(err, wantErr) {
		t.Fatalf("ensureServiceAccount() error = %v, want wrapped command error", err)
	}
}

func TestBSDAccountCommands(t *testing.T) {
	for _, goos := range []string{"openbsd", "netbsd"} {
		commands, err := serviceAccountCommands(goos, 899, "/var/lib/gitmessages")
		if err != nil {
			t.Fatalf("serviceAccountCommands(%q) error = %v", goos, err)
		}
		if len(commands) != 2 || commands[0][0] != "groupadd" || commands[1][0] != "useradd" {
			t.Fatalf("serviceAccountCommands(%q) = %#v, want groupadd and useradd", goos, commands)
		}
	}
}

func containsArgumentSequence(arguments, sequence []string) bool {
	for start := 0; start+len(sequence) <= len(arguments); start++ {
		if reflect.DeepEqual(arguments[start:start+len(sequence)], sequence) {
			return true
		}
	}
	return false
}
