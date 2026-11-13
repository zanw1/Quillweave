package core

import "strings"

// externalOnlySkill recognizes the portable front-matter marker reserved for host-agent instructions.
func externalOnlySkill(content string) bool {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return false
	}
	metadata := false
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			metadata = strings.TrimSpace(line) == "metadata:"
			continue
		}
		if !metadata {
			continue
		}
		key, value, found := strings.Cut(strings.TrimSpace(line), ":")
		if found && key == "quillweave-surface" && strings.Trim(strings.TrimSpace(value), "\"'") == "external" {
			return true
		}
	}
	return false
}
