package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

const ApplicationID = "studio.quillweave.desktop"

// DataDirectory locates desktop data and supports an explicit isolated test directory.
func DataDirectory() (string, error) {
	if directory := os.Getenv("QUILLWEAVE_DATA_DIR"); directory != "" {
		return directory, nil
	}
	var base string
	var err error
	switch runtime.GOOS {
	case "darwin", "windows":
		base, err = os.UserConfigDir()
	default:
		base = os.Getenv("XDG_DATA_HOME")
		if base == "" {
			var home string
			home, err = os.UserHomeDir()
			base = filepath.Join(home, ".local", "share")
		}
	}
	if err != nil {
		return "", err
	}
	return filepath.Join(base, ApplicationID), nil
}
