package privilege

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
)

const serviceName = "gitmessages"

const (
	accountOwnershipDirectory = ".gitmessages-account-ownership"
	accountOwnershipMarker    = "owner"
)

var reservedIDs = map[int]struct{}{
	101: {}, 102: {}, 103: {}, 104: {}, 105: {}, 106: {}, 107: {}, 108: {}, 109: {}, 110: {},
	170: {}, 171: {}, 172: {}, 173: {}, 174: {}, 175: {}, 176: {}, 177: {}, 178: {}, 179: {},
	980: {}, 981: {}, 982: {}, 983: {}, 984: {}, 985: {}, 986: {}, 987: {}, 988: {}, 989: {},
	990: {}, 991: {}, 992: {}, 993: {}, 994: {}, 995: {}, 996: {}, 997: {}, 998: {}, 999: {},
	65534: {},
}

type accountLookup struct {
	userByName  func(string) (uid, gid int, found bool, err error)
	groupByName func(string) (gid int, found bool, err error)
	uidInUse    func(int) bool
	gidInUse    func(int) bool
}

type commandRunner func(string, ...string) error

func systemAccountLookup() accountLookup {
	return accountLookup{
		userByName: func(name string) (int, int, bool, error) {
			entry, err := user.Lookup(name)
			if err != nil {
				if _, ok := err.(user.UnknownUserError); ok {
					return 0, 0, false, nil
				}
				return 0, 0, false, err
			}
			uid, err := strconv.Atoi(entry.Uid)
			if err != nil {
				return 0, 0, false, fmt.Errorf("parse service UID: %w", err)
			}
			gid, err := strconv.Atoi(entry.Gid)
			if err != nil {
				return 0, 0, false, fmt.Errorf("parse service GID: %w", err)
			}
			return uid, gid, true, nil
		},
		groupByName: func(name string) (int, bool, error) {
			entry, err := user.LookupGroup(name)
			if err != nil {
				if _, ok := err.(user.UnknownGroupError); ok {
					return 0, false, nil
				}
				return 0, false, err
			}
			gid, err := strconv.Atoi(entry.Gid)
			if err != nil {
				return 0, false, fmt.Errorf("parse service group GID: %w", err)
			}
			return gid, true, nil
		},
		uidInUse: func(id int) bool {
			_, err := user.LookupId(strconv.Itoa(id))
			return err == nil
		},
		gidInUse: func(id int) bool {
			_, err := user.LookupGroupId(strconv.Itoa(id))
			return err == nil
		},
	}
}

func runCommand(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		if len(output) > 0 {
			return fmt.Errorf("%s failed: %w: %s", name, err, output)
		}
		return fmt.Errorf("%s failed: %w", name, err)
	}
	return nil
}

func selectServiceID(goos string, uidInUse, gidInUse func(int) bool) (int, error) {
	minimum, maximum := 200, 899
	if goos == "darwin" {
		maximum = 399
	}
	for id := maximum; id >= minimum; id-- {
		if _, reserved := reservedIDs[id]; reserved || uidInUse(id) || gidInUse(id) {
			continue
		}
		return id, nil
	}
	return 0, fmt.Errorf("no available UID/GID in safe range %d-%d", minimum, maximum)
}

func validServiceID(id int, goos string) bool {
	maximum := 899
	if goos == "darwin" {
		maximum = 399
	}
	_, reserved := reservedIDs[id]
	return id >= 200 && id <= maximum && !reserved
}

func serviceAccountID(goos string, lookup accountLookup) (int, bool, error) {
	uid, gid, found, err := lookup.userByName(serviceName)
	if err != nil || !found {
		return 0, found, err
	}
	if uid != gid || !validServiceID(uid, goos) {
		return 0, false, fmt.Errorf("existing service account %s does not use a matching UID/GID in the safe range", serviceName)
	}
	groupID, groupFound, err := lookup.groupByName(serviceName)
	if err != nil {
		return 0, false, err
	}
	if !groupFound || groupID != gid {
		return 0, false, fmt.Errorf("existing service account %s does not use its matching primary group", serviceName)
	}
	return uid, true, nil
}

func ensureServiceAccount(goos, homeDir string, lookup accountLookup, run commandRunner) (int, int, error) {
	if homeDir == "" {
		return 0, 0, errors.New("service account home directory is required")
	}
	info, err := os.Stat(homeDir)
	if err != nil {
		return 0, 0, fmt.Errorf("inspect service account home directory %s: %w", homeDir, err)
	}
	if !info.IsDir() {
		return 0, 0, fmt.Errorf("service account home path %s is not a directory", homeDir)
	}
	if goos != "linux" && goos != "darwin" && goos != "freebsd" && goos != "openbsd" && goos != "netbsd" {
		return 0, 0, fmt.Errorf("service account creation is unsupported on %s", goos)
	}
	if id, found, err := serviceAccountID(goos, lookup); err != nil || found {
		return id, id, err
	}
	if _, err := os.Lstat(filepath.Join(homeDir, accountOwnershipDirectory)); err == nil {
		return 0, 0, fmt.Errorf("service account ownership marker already exists in %s", homeDir)
	} else if !errors.Is(err, os.ErrNotExist) {
		return 0, 0, fmt.Errorf("inspect service account ownership marker: %w", err)
	}

	groupID, groupFound, err := lookup.groupByName(serviceName)
	if err != nil {
		return 0, 0, fmt.Errorf("lookup service group: %w", err)
	}
	if groupFound {
		if !validServiceID(groupID, goos) || lookup.uidInUse(groupID) {
			return 0, 0, fmt.Errorf("existing service group %s has an unavailable or unsafe matching UID", serviceName)
		}
	} else {
		groupID, err = selectServiceID(goos, lookup.uidInUse, lookup.gidInUse)
		if err != nil {
			return 0, 0, err
		}
	}

	commands, err := serviceAccountCommands(goos, groupID, homeDir)
	if err != nil {
		return 0, 0, err
	}
	if groupFound {
		switch goos {
		case "linux", "freebsd", "openbsd", "netbsd":
			commands = commands[1:]
		case "darwin":
			commands = commands[3:]
		}
	}
	for _, command := range commands {
		if err := run(command[0], command[1:]...); err != nil {
			return 0, 0, fmt.Errorf("create service account %s: %w", serviceName, err)
		}
	}
	if err := writeAccountOwnershipMarker(homeDir, !groupFound); err != nil {
		return 0, 0, fmt.Errorf("record service account ownership: %w", err)
	}
	return groupID, groupID, nil
}

func serviceAccountCommands(goos string, id int, homeDir string) ([][]string, error) {
	uid := strconv.Itoa(id)
	switch goos {
	case "linux":
		shell := "/sbin/nologin"
		if _, err := os.Stat(shell); err != nil {
			shell = "/usr/sbin/nologin"
		}
		return [][]string{
			{"groupadd", "--system", "--gid", uid, serviceName},
			{"useradd", "--system", "--uid", uid, "--gid", uid, "--home-dir", homeDir, "--shell", shell, "--comment", serviceName + " service account", serviceName},
		}, nil
	case "darwin":
		group := "/Groups/" + serviceName
		account := "/Users/" + serviceName
		return [][]string{
			{"dscl", ".", "-create", group},
			{"dscl", ".", "-create", group, "PrimaryGroupID", uid},
			{"dscl", ".", "-create", group, "Password", "*"},
			{"dscl", ".", "-create", account},
			{"dscl", ".", "-create", account, "UniqueID", uid},
			{"dscl", ".", "-create", account, "PrimaryGroupID", uid},
			{"dscl", ".", "-create", account, "UserShell", "/usr/bin/false"},
			{"dscl", ".", "-create", account, "RealName", serviceName + " service account"},
			{"dscl", ".", "-create", account, "NFSHomeDirectory", homeDir},
			{"dscl", ".", "-create", account, "Password", "*"},
			{"dscl", ".", "-create", account, "IsHidden", "1"},
		}, nil
	case "freebsd":
		return [][]string{
			{"pw", "groupadd", "-n", serviceName, "-g", uid},
			{"pw", "useradd", "-n", serviceName, "-u", uid, "-g", uid, "-d", homeDir, "-s", "/usr/sbin/nologin", "-c", serviceName + " service account"},
		}, nil
	case "openbsd", "netbsd":
		shell := "/sbin/nologin"
		if _, err := os.Stat(shell); err != nil {
			shell = "/usr/sbin/nologin"
		}
		return [][]string{
			{"groupadd", "-g", uid, serviceName},
			{"useradd", "-u", uid, "-g", uid, "-d", homeDir, "-s", shell, "-c", serviceName + " service account", serviceName},
		}, nil
	default:
		return nil, fmt.Errorf("service account creation is unsupported on %s", goos)
	}
}

func EnsureServiceAccount(homeDir string) (int, int, error) {
	if runtime.GOOS == "windows" {
		return 0, 0, nil
	}
	if homeDir == "" {
		return 0, 0, errors.New("service account home directory is required")
	}
	return ensureServiceAccount(runtime.GOOS, filepath.Clean(homeDir), systemAccountLookup(), runCommand)
}
