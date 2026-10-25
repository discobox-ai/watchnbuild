package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigLockExcludesSecondInstance(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".wnb.yaml")
	if err := os.WriteFile(path, []byte("build: {command: make}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if heldLock != nil {
			heldLock.Close()
			heldLock = nil
		}
	}()

	if err := lockConfig(path); err != nil {
		t.Fatal(err)
	}
	first := heldLock

	// A lock is per open file, so a second open in this process stands
	// in for a second watchnbuild.
	err := lockConfig(path)
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("second lock: %v", err)
	}
	if heldLock != first {
		t.Fatal("failed lock replaced the held one")
	}

	// The locked config must still be readable.
	if _, err := LoadConfig(path); err != nil {
		t.Fatalf("locked config unreadable: %v", err)
	}

	first.Close()
	heldLock = nil
	if err := lockConfig(path); err != nil {
		t.Fatalf("lock not released on close: %v", err)
	}
}
