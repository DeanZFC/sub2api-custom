package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareSocketDirectoryAllowsApplicationAccess(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new"
		if existing {
			name = "existing_private"
		}
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "sockets")
			if existing {
				if err := os.Mkdir(dir, 0o700); err != nil {
					t.Fatal(err)
				}
				// Existing sockets must survive permission repair.
				if err := os.WriteFile(filepath.Join(dir, "existing"), []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := prepareSocketDirectory(dir); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(dir)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o755 {
				t.Fatalf("socket directory mode = %o, want 755", info.Mode().Perm())
			}
			if existing {
				data, err := os.ReadFile(filepath.Join(dir, "existing"))
				if err != nil || string(data) != "keep" {
					t.Fatalf("existing entry changed: data=%q err=%v", data, err)
				}
			}
		})
	}
}

func TestPrepareSocketDirectoryRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareSocketDirectory(path); err == nil {
		t.Fatal("expected an error for a non-directory socket parent")
	}
}
