//go:build unix

package main

import "testing"

func TestStateDirectoryRejectsConcurrentProcesses(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lockStateDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate, err := lockStateDirectory(dir); err == nil {
		duplicate()
		unlock()
		t.Fatal("accepted duplicate runtime for one state directory")
	}
	unlock()
	again, err := lockStateDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	again()
}
