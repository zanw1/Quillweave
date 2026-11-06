//go:build !windows

package platform

import (
	"errors"
	"syscall"
)

// transientFileConflict reports advisory locks and busy files on Unix.
func transientFileConflict(err error) bool {
	return errors.Is(err, syscall.EBUSY) || errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK)
}
