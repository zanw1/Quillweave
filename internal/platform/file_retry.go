package platform

import (
	"errors"
	"os"
)

// RetryableFileError reports a lock or sidecar open that can succeed on a later attempt.
// Absence is not retryable: callers recover missing files instead of waiting.
func RetryableFileError(err error) bool {
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return false
	}
	return errors.Is(err, os.ErrPermission) || transientFileConflict(err)
}
