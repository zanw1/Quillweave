package platform

import (
	"errors"
	"os"
	"sync"
)

// FileLock holds an OS-owned lock that is released even when its process is killed.
type FileLock struct {
	file *os.File
	once sync.Once
	err  error
}

// TryFileLock acquires an exclusive nonblocking lock without replacing an existing lock file.
func TryFileLock(path string) (*FileLock, bool, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	acquired, err := lockFile(file)
	if err != nil || !acquired {
		file.Close()
		return nil, acquired, err
	}
	return &FileLock{file: file}, true, nil
}

// Close releases the ownership claim once and preserves any unlock or close error.
func (lock *FileLock) Close() error {
	lock.once.Do(func() { lock.err = errors.Join(unlockFile(lock.file), lock.file.Close()) })
	return lock.err
}
