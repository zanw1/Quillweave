//go:build quillweave_whitebox

package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"syscall"
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

// TestRetryableFileError keeps missing files distinct from locks Windows may refuse while they are being deleted.
func TestRetryableFileError(t *testing.T) {
	if RetryableFileError(nil) || RetryableFileError(os.ErrNotExist) {
		t.Fatal("absence is not a transient file conflict")
	}
	if !RetryableFileError(&os.PathError{Op: "open", Path: "owner.lock", Err: syscall.EACCES}) {
		t.Fatal("permission denial should be treated as a transient file conflict")
	}
	sharing := &os.PathError{Op: "open", Path: "search.final.json", Err: syscall.Errno(32)}
	pending := &os.PathError{Op: "open", Path: "owner.lock", Err: syscall.Errno(303)}
	if runtime.GOOS == "windows" {
		accessDenied := &os.PathError{Op: "open", Path: "owner.lock", Err: syscall.Errno(5)}
		if !RetryableFileError(sharing) || !RetryableFileError(pending) || !RetryableFileError(accessDenied) {
			t.Fatal("Windows sharing and delete-pending errors should be retryable")
		}
		return
	}
	if RetryableFileError(sharing) || RetryableFileError(pending) {
		t.Fatal("unrelated errno was treated as a file conflict")
	}
}
