//go:build linux || darwin || freebsd || openbsd || netbsd

package privilege

import (
	"os"
	"syscall"
)

func fileOwnerUID(info os.FileInfo) int {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return -1
	}
	return int(stat.Uid)
}
