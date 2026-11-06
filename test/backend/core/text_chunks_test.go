//go:build quillweave_whitebox

package core

import (
	"strings"
	"testing"
)

// TestChunkingCoversUnicodeTail checks complete reconstruction of long Unicode and emoji text.
func TestChunkingCoversUnicodeTail(t *testing.T) {
	text := strings.Repeat("\u4e2d\u6587\u6bb5\u843d。", 1600) + "\u6700\u540e\u7684\u5173\u952e\u7ebf\u7d22🙂"
	parts := chunks(text, 6000, 150)
	assert(t, len(parts) > 1 && strings.HasSuffix(parts[len(parts)-1], "\u6700\u540e\u7684\u5173\u952e\u7ebf\u7d22🙂"), "The tail was lost")
	restored := parts[0]
	for _, part := range parts[1:] {
		restored += string([]rune(part)[150:])
	}
	assert(t, restored == text, "Chunks could not reconstruct the full text")
	assert(t, len(chunks(text, 0, 0)) == 0, "Invalid chunk parameters were accepted")
}
