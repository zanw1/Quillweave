//go:build windows

package platform

import (
	"errors"
	"golang.org/x/sys/windows"
)

// transientFileConflict reports NTFS sharing, lock, and delete-pending failures.
func transientFileConflict(err error) bool {
	return errors.Is(err, windows.ERROR_SHARING_VIOLATION) || errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_DELETE_PENDING)
}
