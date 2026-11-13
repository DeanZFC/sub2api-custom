//go:build linux

package ovhipv6

import (
	"os"
	"syscall"
)

func storageDiskBytes(info os.FileInfo) *int64 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks < 0 || stat.Blocks > int64(^uint64(0)>>1)/512 {
		return nil
	}
	// Linux st_blocks is expressed in 512-byte units, not st_blksize units.
	bytes := stat.Blocks * 512
	return &bytes
}
