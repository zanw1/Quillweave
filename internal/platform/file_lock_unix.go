//go:build !windows

package platform

import (
	"errors"
	"os"
	"syscall"
)

// lockFile uses a descriptor-owned advisory lock on supported Unix desktop platforms.
func lockFile(file *os.File) (bool, error) {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return false, nil
	}
	return err == nil, err
}

// unlockFile releases only the lock associated with this open descriptor.
func unlockFile(file *os.File) error { return syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }
