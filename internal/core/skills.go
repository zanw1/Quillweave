package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxSkillBytes = 100_000

// SkillDescriptor exposes a stable resource identity and content version without loading instructions into a prompt.
type SkillDescriptor struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Version     string `json:"version"`
	Source      string `json:"source"`
}

// SkillCatalog separates configured discovery roots from the available instruction resources.
type SkillCatalog struct {
	Skills   []SkillDescriptor `json:"skills"`
	Roots    []string          `json:"roots"`
	Warnings []string          `json:"warnings,omitempty"`
}

// LoadedSkill binds the exact read-only instructions to their resource version and relative-resource directory.
type LoadedSkill struct {
	SkillDescriptor
	Content       string `json:"content"`
	BaseDirectory string `json:"base_directory"`
	Truncated     bool   `json:"truncated"`
	ResourcePath  string `json:"resource_path,omitempty"`
}

// defaultSkillRoots discovers common user skills and project-local skills without scanning unrelated home files.
func defaultSkillRoots() []string {
	candidates := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, ".agents", "skills"))
	}
	if directory, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(directory, ".agents", "skills"))
	}
	roots := []string{}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			roots = append(roots, candidate)
		}
	}
	return roots
}

// configuredSkillRoots reads optional discovery preferences without creating schema during a read-only catalog request.
func (engine *Engine) configuredSkillRoots() ([]string, error) {
	var exists int
	if err := engine.store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='skill_settings'").Scan(&exists); err != nil {
		return nil, err
	}
	roots := defaultSkillRoots()
	if exists == 0 {
		return roots, nil
	}
	var raw string
	err := engine.store.db.QueryRow("SELECT document FROM skill_settings WHERE id=1").Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return roots, nil
	}
	if err != nil {
		return nil, err
	}
	var configured struct {
		Roots []string `json:"roots"`
	}
	if err := decodeJSON([]byte(raw), &configured); err != nil {
		return nil, errors.New("The saved Skill directories are invalid")
	}
	return configured.Roots, nil
}

// setSkillRoots explicitly saves validated directories while leaving model settings and existing user files untouched.
func (engine *Engine) setSkillRoots(roots []string) (SkillCatalog, error) {
	if len(roots) > 20 {
		return SkillCatalog{}, errors.New("Choose at most twenty Skill directories")
	}
	cleaned := []string{}
	seen := map[string]bool{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if strings.HasPrefix(root, "~/") {
			home, err := os.UserHomeDir()
			if err != nil {
				return SkillCatalog{}, err
			}
			root = filepath.Join(home, root[2:])
		}
		if !filepath.IsAbs(root) {
			return SkillCatalog{}, errors.New("Use an absolute Skill directory path")
		}
		canonical, err := filepath.EvalSymlinks(root)
		if err != nil {
			return SkillCatalog{}, fmt.Errorf("Unable to open Skill directory %q", root)
		}
		info, err := os.Stat(canonical)
		if err != nil || !info.IsDir() {
			return SkillCatalog{}, fmt.Errorf("Skill directory %q is not a directory", root)
		}
		if !seen[canonical] {
			seen[canonical] = true
			cleaned = append(cleaned, canonical)
		}
	}
	err := engine.store.withWriteTransaction(func(db *sql.Conn) error {
		if _, err := db.ExecContext(context.Background(), "CREATE TABLE IF NOT EXISTS skill_settings(id INTEGER PRIMARY KEY CHECK(id=1), document TEXT NOT NULL)"); err != nil {
			return err
		}
		_, err := db.ExecContext(context.Background(), "INSERT INTO skill_settings(id,document) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET document=excluded.document", jsonText(map[string]any{"roots": cleaned}))
		return err
	})
	if err != nil {
		return SkillCatalog{}, err
	}
	return engine.listSkills()
}

// readSkillFile bounds local instruction reads and reports truncation explicitly to consumers.
func readSkillFile(path string) (string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return "", false, errors.New("Skill entry is not a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxSkillBytes+1))
	if err != nil {
		return "", false, err
	}
	truncated := len(data) > maxSkillBytes
	if truncated {
		data = data[:maxSkillBytes]
		for removed := 0; removed < utf8.UTFMax && len(data) > 0 && !utf8.Valid(data); removed++ {
			data = data[:len(data)-1]
		}
	}
	if !utf8.Valid(data) {
		return "", false, errors.New("Skill instructions must use UTF-8 text")
	}
	return string(data), truncated, nil
}

// skillMetadata parses common Agent Skills front matter while treating its remaining Markdown as instruction data.
func skillMetadata(content, fallback string) (string, string) {
	name, description := fallback, ""
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return name, description
	}
	field := ""
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			if field == "description" {
				description += " " + strings.TrimSpace(line)
			}
			continue
		}
		key, value, found := strings.Cut(line, ":")
		field = strings.TrimSpace(key)
		if !found {
			continue
		}
		value = strings.TrimSpace(value)
		if decoded, err := strconv.Unquote(value); err == nil {
			value = decoded
		} else {
			value = strings.Trim(value, "'")
		}
		switch field {
		case "name":
			if value != "" {
				name = value
			}
		case "description":
			if value != ">" && value != "|" && value != ">-" && value != "|-" {
				description = value
			}
		}
	}
	return truncateSummary(name, 120), truncateSummary(strings.TrimSpace(description), 600)
}

// scanSkillDirectory follows the selected root while bounding nesting and avoiding dependency trees.
func scanSkillDirectory(root string) ([]string, error) {
	paths := []string{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, _ := filepath.Rel(root, path)
		if entry.IsDir() && path != root && (strings.Count(relative, string(filepath.Separator)) >= 4 || entry.Name() == "node_modules" || strings.HasPrefix(entry.Name(), ".")) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && strings.EqualFold(entry.Name(), "SKILL.md") {
			paths = append(paths, path)
		}
		return nil
	})
	return paths, err
}

// listSkills discovers stable instruction identities and isolates unreadable roots as visible catalog warnings.
func (engine *Engine) listSkills() (SkillCatalog, error) {
	roots, err := engine.configuredSkillRoots()
	if err != nil {
		return SkillCatalog{}, err
	}
	catalog := SkillCatalog{Skills: []SkillDescriptor{}, Roots: roots}
	seen := map[string]bool{}
	for _, root := range roots {
		entries, err := os.ReadDir(root)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			catalog.Warnings = append(catalog.Warnings, fmt.Sprintf("Unable to read Skill directory %q", root))
			continue
		}
		directories := []string{root}
		for _, entry := range entries {
			if entry.Type()&os.ModeSymlink != 0 {
				if target, err := filepath.EvalSymlinks(filepath.Join(root, entry.Name())); err == nil {
					directories = append(directories, target)
				}
			}
		}
		for _, directory := range directories {
			paths, err := scanSkillDirectory(directory)
			if err != nil {
				catalog.Warnings = append(catalog.Warnings, fmt.Sprintf("Unable to scan Skill directory %q", directory))
				continue
			}
			for _, path := range paths {
				canonical, err := filepath.EvalSymlinks(path)
				if err != nil || seen[canonical] {
					continue
				}
				content, _, err := readSkillFile(canonical)
				if err != nil {
					catalog.Warnings = append(catalog.Warnings, fmt.Sprintf("Unable to read Skill %q", path))
					continue
				}
				seen[canonical] = true
				if externalOnlySkill(content) { continue }
				name, description := skillMetadata(content, filepath.Base(filepath.Dir(canonical)))
				catalog.Skills = append(catalog.Skills, SkillDescriptor{ID: "skill-" + hashText(canonical)[:24], Name: name, Description: description, Path: canonical, Version: hashText(content), Source: root})
				if len(catalog.Skills) >= 500 {
					catalog.Warnings = append(catalog.Warnings, "Only the first 500 Skills are available. Narrow the configured directories.")
					return catalog, nil
				}
			}
		}
	}
	sort.Slice(catalog.Skills, func(i, j int) bool {
		if catalog.Skills[i].Name == catalog.Skills[j].Name {
			return catalog.Skills[i].ID < catalog.Skills[j].ID
		}
		return catalog.Skills[i].Name < catalog.Skills[j].Name
	})
	return catalog, nil
}

// loadSkill resolves only a discovered identity and returns the exact version that was read.
func (engine *Engine) loadSkill(id string) (LoadedSkill, error) {
	catalog, err := engine.listSkills()
	if err != nil {
		return LoadedSkill{}, err
	}
	for _, descriptor := range catalog.Skills {
		if descriptor.ID != id {
			continue
		}
		content, truncated, err := readSkillFile(descriptor.Path)
		if err != nil {
			return LoadedSkill{}, fmt.Errorf("Unable to load Skill %q", descriptor.Name)
		}
		descriptor.Version = hashText(content)
		return LoadedSkill{SkillDescriptor: descriptor, Content: content, BaseDirectory: filepath.Dir(descriptor.Path), Truncated: truncated}, nil
	}
	return LoadedSkill{}, errors.New("Select an available Skill from the current catalog")
}

// loadSkillResource reads a relative text resource only inside the discovered Skill's canonical directory.
func (engine *Engine) loadSkillResource(id, resourcePath string) (LoadedSkill, error) {
	skill, err := engine.loadSkill(id)
	if err != nil || strings.TrimSpace(resourcePath) == "" {
		return skill, err
	}
	if filepath.IsAbs(resourcePath) || strings.ContainsRune(resourcePath, 0) {
		return LoadedSkill{}, errors.New("Use a relative resource path inside the selected Skill")
	}
	path, err := filepath.EvalSymlinks(filepath.Join(skill.BaseDirectory, resourcePath))
	if err != nil {
		return LoadedSkill{}, errors.New("The requested Skill resource could not be found")
	}
	relative, err := filepath.Rel(skill.BaseDirectory, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return LoadedSkill{}, errors.New("The resource must stay inside the selected Skill directory")
	}
	content, truncated, err := readSkillFile(path)
	if err != nil {
		return LoadedSkill{}, errors.New("The Skill resource must be a readable UTF-8 text file")
	}
	skill.Path, skill.Content, skill.Version = path, content, hashText(content)
	skill.Truncated, skill.ResourcePath = truncated, filepath.ToSlash(relative)
	return skill, nil
}

// prepareSkills loads selected instructions and exposes a bounded metadata directory for optional model use.
func (engine *Engine) prepareSkills(request AIRequest, metadataBudgets ...uint64) (AIRequest, []SkillDescriptor, error) {
	if request.ModelSelection == nil && len(request.SkillIDs) == 0 {
		return request, nil, nil
	}
	if len(request.SkillIDs) > 12 {
		return request, nil, errors.New("Select at most twelve Skills for one turn")
	}
	catalog, err := engine.listSkills()
	if err != nil {
		return request, nil, err
	}
	loaded := []SkillDescriptor{}
	seen := map[string]bool{}
	for _, id := range request.SkillIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		skill, err := engine.loadSkill(id)
		if err != nil {
			return request, nil, err
		}
		if skill.Truncated {
			return request, nil, fmt.Errorf("Skill %q exceeds the %d-byte instruction limit", skill.Name, maxSkillBytes)
		}
		loaded = append(loaded, skill.SkillDescriptor)
		request.systemAppend += fmt.Sprintf("\n<selected_skill id=%q name=%q version=%q>\nBase directory: %s\nUse these author-selected instructions only where relevant to the current task and requested document form. Explicit author constraints and this application's stage contracts take priority over generic skill advice. Do not import an unrelated document genre or extra review cycle. Use only available application tools; read relative text resources with load_skill using this skill_id and resource_path.\n%s\n</selected_skill>\n", skill.ID, skill.Name, skill.Version, skill.BaseDirectory, skill.Content)
	}
	if request.Action == "agent" && len(catalog.Skills) > 0 {
		index := "\n<available_skills>\nThese are optional workflow instructions. Load a matching Skill only when needed using load_skill with skill_id. Loading does not execute its scripts.\n"
		metadataBudget := uint64(2000)
		if len(metadataBudgets) > 0 {
			metadataBudget = metadataBudgets[0]
		}
		for _, skill := range catalog.Skills {
			line := fmt.Sprintf("- %s | %s | %s\n", skill.ID, skill.Name, truncateSummary(skill.Description, 220))
			if estimateTextTokens(index+line) > metadataBudget {
				line = fmt.Sprintf("- %s | %s\n", skill.ID, skill.Name)
			}
			if estimateTextTokens(index+line) > metadataBudget {
				break
			}
			index += line
		}
		request.systemAppend += index + "</available_skills>\n"
	}
	return request, loaded, nil
}

// skillCatalogTokenBudget keeps automatic discovery metadata proportional to the selected model's usable window.
func skillCatalogTokenBudget(config ProviderConfig) uint64 {
	window := effectiveModelLimits(config).ContextWindowTokens
	if window == 0 {
		// Discovery is optional. Reserve the actual native declarations first and
		// leave room for the chapter, conversation and the next paired tool result.
		available := config.ContextChars
		declarations := uint64(nativeToolChars(config))
		if declarations >= available {
			return 0
		}
		return min(uint64(2000), max(uint64(100), (available-declarations)/64))
	}
	return min(uint64(2000), max(uint64(100), window/50))
}

// selectedSkillContext forwards the submission's already-loaded instruction blocks to a delegated model unchanged.
func selectedSkillContext(prompt Prompt) string {
	if len(prompt.Messages) == 0 || len(prompt.Skills) == 0 {
		return ""
	}
	text := prompt.Messages[0].Content
	start := strings.Index(text, "<selected_skill ")
	end := strings.LastIndex(text, "\n</selected_skill>\n")
	if start < 0 || end < start {
		return ""
	}
	return "\n" + text[start:end+len("\n</selected_skill>\n")]
}
