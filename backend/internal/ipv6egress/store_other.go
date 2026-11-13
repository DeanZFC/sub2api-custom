//go:build !linux

package ipv6egress

import "os"

// Tests can run on Windows. Production uses the crash-safe Linux flock.
func lockState(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	return func() error { _ = f.Close(); return os.Remove(path) }, nil
}
func syncDirectory(string) error { return nil }
