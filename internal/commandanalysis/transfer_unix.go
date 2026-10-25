//go:build linux || darwin

package commandanalysis

import (
	"os"
	"syscall"
)

func transferFileIdentity(info os.FileInfo) (device, links uint64, ok bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return uint64(stat.Dev), uint64(stat.Nlink), true
}
