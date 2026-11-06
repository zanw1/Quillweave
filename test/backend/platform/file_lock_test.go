//go:build quillweave_whitebox

package platform

import (
	"path/filepath"
	"testing"
)

// TestFileLockOwnership uses independent handles to test the same mechanism used by concurrent app instances.
func TestFileLockOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lock")
	first, acquired, err := TryFileLock(path)
	if err != nil || !acquired {
		t.Fatalf("Initial lock failed: %v", err)
	}
	defer first.Close()
	second, acquired, err := TryFileLock(path)
	if err != nil || acquired || second != nil {
		t.Fatal("Independent handles simultaneously acquired ownership")
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, acquired, err = TryFileLock(path)
	if err != nil || !acquired {
		t.Fatal("Released ownership could not be acquired")
	}
	defer second.Close()
}
