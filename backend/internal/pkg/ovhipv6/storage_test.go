package ovhipv6

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func writeStorageFixture(t *testing.T, dir string) {
	t.Helper()
	// Deliberately not valid JSON. Statistics must never parse credentials or
	// infer byte counts from the issued sequence returned by the control API.
	for name, body := range map[string]string{
		"counter.json":       "counter metadata only",
		"counter.guard.json": "second counter with a different length",
		"leases.json":        "private proxy password: must-not-appear",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(body), 0600))
	}
}

func TestOVHIPv6StorageFilesActualMetadataAndReplacement(t *testing.T) {
	dir := t.TempDir()
	writeStorageFixture(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "unrelated-private-file"), make([]byte, 512), 0600))
	check := func() StorageFiles {
		out := ReadStorageFiles(dir)
		require.True(t, out.Configured)
		require.True(t, out.Available)
		require.Len(t, out.Items, 3)
		var counters, total int64
		for i, item := range out.Items {
			info, err := os.Stat(filepath.Join(dir, item.Name))
			require.NoError(t, err)
			require.NotNil(t, item.Bytes)
			require.Equal(t, info.Size(), *item.Bytes)
			total += info.Size()
			if i < 2 {
				counters += info.Size()
			}
		}
		require.Equal(t, counters, *out.CounterBytes)
		require.Equal(t, total, *out.TotalBytes)
		if runtime.GOOS != "linux" {
			require.Nil(t, out.CounterDiskBytes, "unknown allocated blocks must not be approximated")
			require.Nil(t, out.TotalDiskBytes)
		}
		encoded, err := json.Marshal(out)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "must-not-appear")
		require.NotContains(t, string(encoded), dir)
		require.NotContains(t, string(encoded), "unrelated-private-file")
		return out
	}
	before := check()
	newPath := filepath.Join(dir, ".atomic-replacement")
	require.NoError(t, os.WriteFile(newPath, []byte("new"), 0600))
	require.NoError(t, os.Rename(newPath, filepath.Join(dir, "counter.json")))
	after := check()
	require.EqualValues(t, 3, *after.Items[0].Bytes)
	require.NotEqual(t, *before.CounterBytes, *after.CounterBytes, "must stat the new inode on every request")
}

func TestOVHIPv6StorageFilesMissingOrInvalidAreUnknown(t *testing.T) {
	for _, dir := range []string{"", "relative-directory", filepath.Join(t.TempDir(), "missing")} {
		out := ReadStorageFiles(dir)
		require.False(t, out.Available)
		require.Nil(t, out.CounterBytes)
		require.Nil(t, out.TotalBytes)
		require.NotEmpty(t, out.Error)
		require.Len(t, out.Items, 3)
	}
	dir := t.TempDir()
	writeStorageFixture(t, dir)
	require.NoError(t, os.Remove(filepath.Join(dir, "leases.json")))
	out := ReadStorageFiles(dir)
	require.False(t, out.Available)
	require.NotNil(t, out.CounterBytes, "readable counters remain useful when leases are unavailable")
	require.Nil(t, out.TotalBytes)
	require.Nil(t, out.Items[2].Bytes)
	require.NotEmpty(t, out.Items[2].Error)
	require.NoError(t, os.Mkdir(filepath.Join(dir, "leases.json"), 0700))
	out = ReadStorageFiles(dir)
	require.False(t, out.Available, "directories cannot masquerade as state files")
	require.Nil(t, out.Items[2].Bytes)
}

func TestOVHIPv6StorageFilesRejectSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeStorageFixture(t, dir)
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("outside state directory"), 0600))
	require.NoError(t, os.Remove(filepath.Join(dir, "counter.json")))
	if err := os.Symlink(outside, filepath.Join(dir, "counter.json")); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	out := ReadStorageFiles(dir)
	require.False(t, out.Available)
	require.Nil(t, out.Items[0].Bytes)
	require.Nil(t, out.CounterBytes)
	require.Nil(t, out.TotalBytes)
	linkDir := filepath.Join(t.TempDir(), "state-link")
	require.NoError(t, os.Symlink(dir, linkDir))
	require.False(t, ReadStorageFiles(linkDir).Available)
}
