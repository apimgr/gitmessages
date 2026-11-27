//go:build linux || darwin || freebsd || openbsd || netbsd

package privilege

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func IsElevated() bool {
	return os.Geteuid() == 0
}

// CanEscalate detects usable sudo/doas access, administrative group membership, or graphical pkexec.
func CanEscalate() bool {
	return escalationMethodFor(runtime.GOOS, os.Getenv("DISPLAY"), os.Getenv("WAYLAND_DISPLAY"), os.Geteuid(), exec.LookPath, exec.CommandContext, user.Current) != ""
}

func canEscalate(display, wayland string, euid int, lookPath func(string) (string, error), command func(context.Context, string, ...string) *exec.Cmd, currentUser func() (*user.User, error)) bool {
	return escalationMethod(display, wayland, euid, lookPath, command, currentUser) != ""
}

func escalationMethod(display, wayland string, euid int, lookPath func(string) (string, error), command func(context.Context, string, ...string) *exec.Cmd, currentUser func() (*user.User, error)) string {
	return escalationMethodFor("linux", display, wayland, euid, lookPath, command, currentUser)
}

func escalationMethodFor(goos, display, wayland string, euid int, lookPath func(string) (string, error), command func(context.Context, string, ...string) *exec.Cmd, currentUser func() (*user.User, error)) string {
	if euid == 0 {
		return ""
	}
	probe := func(ctx context.Context, name string, args ...string) bool {
		if _, err := lookPath(name); err != nil {
			return false
		}
		return command(ctx, name, args...).Run() == nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	memberOf := func(names ...string) bool {
		current, err := currentUser()
		if err != nil {
			return false
		}
		groups, err := current.GroupIds()
		if err != nil {
			return false
		}
		for _, gid := range groups {
			group, err := user.LookupGroupId(gid)
			if err != nil {
				continue
			}
			for _, name := range names {
				if group.Name == name {
					return true
				}
			}
		}
		return false
	}
	if goos == "darwin" {
		if path, err := lookPath("sudo"); err == nil && (probe(ctx, "sudo", "-n", "true") || memberOf("admin")) {
			return path
		}
		if path, err := lookPath("osascript"); err == nil && hasActiveMacGUI(command) {
			return path
		}
		return ""
	}
	if goos == "freebsd" || goos == "openbsd" || goos == "netbsd" {
		if path, err := lookPath("doas"); err == nil && (probe(ctx, "doas", "-n", "true") || probe(ctx, "doas", "-C", "/etc/doas.conf", "true")) {
			return path
		}
		if path, err := lookPath("sudo"); err == nil && (probe(ctx, "sudo", "-n", "true") || memberOf("wheel", "admin", "sudo")) {
			return path
		}
		return ""
	}
	if path, err := lookPath("sudo"); err == nil {
		if probe(ctx, "sudo", "-n", "true") || memberOf("sudo", "wheel", "admin") {
			return path
		}
	}
	if path, err := lookPath("doas"); err == nil && (probe(ctx, "doas", "-n", "true") || probe(ctx, "doas", "-C", "/etc/doas.conf", "true")) {
		return path
	}
	if display != "" || wayland != "" {
		if path, err := lookPath("pkexec"); err == nil {
			return path
		}
	}
	return ""
}

func hasActiveMacGUI(command func(context.Context, string, ...string) *exec.Cmd) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	current, err := user.Current()
	if err != nil {
		return false
	}
	output, err := command(ctx, "stat", "-f", "%Su", "/dev/console").Output()
	if err != nil {
		return false
	}
	consoleUser := strings.TrimSpace(string(output))
	return consoleUser != "" && consoleUser != "root" && consoleUser != "loginwindow" && consoleUser == current.Username
}

// ExecElevated re-executes args using the first detected Unix escalation method.
func ExecElevated(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("elevated command is empty")
	}
	path := escalationMethodFor(runtime.GOOS, os.Getenv("DISPLAY"), os.Getenv("WAYLAND_DISPLAY"), os.Geteuid(), exec.LookPath, exec.CommandContext, user.Current)
	if path == "" {
		return fmt.Errorf("no detected privilege escalation method is available")
	}
	if runtime.GOOS == "darwin" && filepath.Base(path) == "osascript" {
		commandLine := "exec " + shellQuote(args[0])
		for _, arg := range args[1:] {
			commandLine += " " + shellQuote(arg)
		}
		script := `do shell script "` + strings.ReplaceAll(strings.ReplaceAll(commandLine, `\`, `\\`), `"`, `\"`) + `" with administrator privileges`
		cmd := exec.Command(path, "-e", script)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		return cmd.Run()
	}
	cmd := exec.Command(path, args...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func DropPrivileges(uid, gid int) error {
	return dropPrivilegesWith(uid, gid, syscall.Setgroups, syscall.Setgid, syscall.Setuid, os.Getuid, os.Geteuid, os.Getgid)
}

func dropPrivilegesWith(uid, gid int, setgroups func([]int) error, setgid, setuid func(int) error, getuid, geteuid, getgid func() int) error {
	if uid < 0 || gid < 0 || uid == 0 {
		return fmt.Errorf("invalid service UID/GID %d:%d", uid, gid)
	}
	if err := setgroups([]int{gid}); err != nil {
		return fmt.Errorf("set supplementary service group: %w", err)
	}
	if err := setgid(gid); err != nil {
		return fmt.Errorf("set service group: %w", err)
	}
	if err := setuid(uid); err != nil {
		return fmt.Errorf("set service user: %w", err)
	}
	if getuid() == 0 || geteuid() == 0 {
		return fmt.Errorf("privilege drop failed: process still has root identity")
	}
	if getuid() != uid || geteuid() != uid || getgid() != gid {
		return fmt.Errorf("privilege drop verification failed for service UID/GID %d:%d", uid, gid)
	}
	return nil
}

func ChownTree(paths []string, uid, gid int) error {
	return chownTree(paths, os.Chown, uid, gid)
}

func chownTree(paths []string, chown func(string, int, int) error, uid, gid int) error {
	for _, root := range paths {
		if root == "" {
			continue
		}
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				return nil
			}
			if entry.IsDir() && filepath.Clean(path) == filepath.Join(filepath.Clean(root), accountOwnershipDirectory) {
				return fs.SkipDir
			}
			if err := chown(path, uid, gid); err != nil {
				return fmt.Errorf("change ownership of %s: %w", path, err)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("change ownership below %s: %w", root, err)
		}
	}
	return nil
}
