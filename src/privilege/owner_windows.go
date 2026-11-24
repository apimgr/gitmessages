//go:build windows

package privilege

import "os"

func fileOwnerUID(os.FileInfo) int {
	return -1
}
