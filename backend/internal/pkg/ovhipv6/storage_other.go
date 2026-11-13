//go:build !linux

package ovhipv6

import "os"

func storageDiskBytes(os.FileInfo) *int64 {
	// Do not present logical file length or a rounded estimate as disk usage.
	return nil
}
