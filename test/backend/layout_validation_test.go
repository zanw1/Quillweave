package backend_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestModuleOverlayLineEndings keeps the direct Go entry compatible with CRLF without accepting missing or changed tags.
func TestModuleOverlayLineEndings(t *testing.T) {
	base := "//go:build quillweave_whitebox\n\npackage core\n\nimport \"testing\"\n\nfunc TestFixture(t *testing.T) {}\n"
	for _, candidate := range []struct {
		name, source string
		valid        bool
	}{
		{"LF", base, true},
		{"CRLF", strings.ReplaceAll(base, "\n", "\r\n"), true},
		{"mixed", strings.Replace(base, "\n", "\r\n", 1), true},
		{"missing", strings.TrimPrefix(base, "//go:build quillweave_whitebox\n"), false},
		{"wrong", strings.ReplaceAll(base, "quillweave_whitebox", "another_tag"), false},
	} {
		t.Run(candidate.name, func(t *testing.T) {
			root := t.TempDir()
			directory := filepath.Join(root, "test", "backend", "core")
			if err := os.MkdirAll(directory, 0700); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(directory, "fixture_test.go")
			if err := os.WriteFile(source, []byte(candidate.source), 0600); err != nil {
				t.Fatal(err)
			}
			overlay, names, err := moduleOverlay(root, "core", "internal/core", "")
			if !candidate.valid {
				if err == nil || !strings.Contains(err.Error(), "missing white-box source tag") {
					t.Fatalf("Invalid tag accepted or failed for another reason: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(overlay) != 1 || overlay[filepath.Join(root, "internal", "core", "fixture_test.go")] != source || len(names) != 1 || !names["TestFixture"] {
				t.Fatalf("Missing overlay source or executable test: %v / %v", overlay, names)
			}
		})
	}
}
