package platform

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestDataDirectoryCompatibility checks legacy desktop locations and the explicit directory override.
func TestDataDirectoryCompatibility(t *testing.T) {
	temporary := t.TempDir()
	t.Setenv("QUILLWEAVE_DATA_DIR", temporary)
	directory, err := DataDirectory()
	if err != nil || directory != temporary {
		t.Fatal("Isolated data directory override failed")
	}
	t.Setenv("QUILLWEAVE_DATA_DIR", "")
	var base string
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		base, err = os.UserConfigDir()
	} else {
		t.Setenv("XDG_DATA_HOME", temporary)
		base = temporary
	}
	if err != nil {
		t.Fatal(err)
	}
	directory, err = DataDirectory()
	if err != nil || directory != filepath.Join(base, "studio.quillweave.desktop") {
		t.Fatal("Legacy desktop data directory compatibility failed")
	}
}
