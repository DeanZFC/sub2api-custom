//go:build linux

package ovhipv6

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOVHIPv6StorageFilesRealLinuxAllocatedBlocks(t *testing.T) {
	dir := t.TempDir()
	writeStorageFixture(t, dir)
	file, err := os.OpenFile(filepath.Join(dir, "counter.json"), os.O_WRONLY|os.O_TRUNC, 0600)
	require.NoError(t, err)
	_, err = file.WriteAt([]byte{1}, 8<<20)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	out := ReadStorageFiles(dir)
	require.True(t, out.Available)
	var total, counter int64
	for i, item := range out.Items {
		info, err := os.Lstat(filepath.Join(dir, item.Name))
		require.NoError(t, err)
		stat, ok := info.Sys().(*syscall.Stat_t)
		require.True(t, ok)
		actual := stat.Blocks * 512
		require.NotNil(t, item.DiskBytes)
		require.Equal(t, actual, *item.DiskBytes)
		total += actual
		if i < 2 {
			counter += actual
		}
	}
	require.Equal(t, counter, *out.CounterDiskBytes)
	require.Equal(t, total, *out.TotalDiskBytes)
}

func TestOVHIPv6StorageFilesTraverseWithoutContentAccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("run as the non-root application UID to verify access restrictions")
	}
	dir := t.TempDir()
	writeStorageFixture(t, dir)
	for _, name := range []string{"counter.json", "counter.guard.json", "leases.json"} {
		require.NoError(t, os.Chmod(filepath.Join(dir, name), 0000))
	}
	require.NoError(t, os.Chmod(dir, 0100))
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	_, err := os.ReadFile(filepath.Join(dir, "leases.json"))
	require.ErrorIs(t, err, os.ErrPermission)
	_, err = os.ReadDir(dir)
	require.ErrorIs(t, err, os.ErrPermission)
	out := ReadStorageFiles(dir)
	require.True(t, out.Available, "directory traversal alone must be enough for metadata")
	require.NotNil(t, out.CounterDiskBytes)
	require.NotNil(t, out.TotalBytes)
}
