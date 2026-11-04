package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// readLegacyText reads regular UTF-8 files within the selected directory after validating paths.
func readLegacyText(root, path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(root, resolved)
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("Import only regular files inside the selected directory: %s", path)
	}
	if info.Size() > 5_000_000 {
		return "", fmt.Errorf("File exceeds 5 MB: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil || !utf8.Valid(data) {
		return "", fmt.Errorf("Unable to read UTF-8 text: %s", path)
	}
	return string(data), nil
}

// archiveSource retains historical machine analysis as archived reference material.
func archiveSource(project *Project, title, content, category string) {
	project.Sources = append(project.Sources, Source{ID: newID(), Title: title, Category: category, Content: content, CreatedAt: now()})
}

// fileExists checks whether a filesystem entry exists.
func fileExists(path string) bool { _, err := os.Stat(path); return err == nil }

// isDirectory checks directory existence while file reads separately enforce path boundaries.
func isDirectory(path string) bool { info, err := os.Stat(path); return err == nil && info.IsDir() }

// importLegacy imports legacy prose, setup, chapters, character files, and versions through read-only access.
func importLegacy(path string) (Project, error) {
	root, err := filepath.EvalSymlinks(path)
	if err != nil {
		return Project{}, errors.New("Legacy project directory not found")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return Project{}, err
	}
	if !isDirectory(root) {
		return Project{}, errors.New("Select the legacy project folder")
	}
	architecture, chaptersPath := filepath.Join(root, "Novel_architecture.txt"), filepath.Join(root, "chapters")
	info, statErr := os.Stat(architecture)
	if !isDirectory(chaptersPath) && (statErr != nil || !info.Mode().IsRegular()) {
		return Project{}, errors.New("No chapters/ or Novel_architecture.txt found. Select a project folder")
	}
	title := filepath.Base(root)
	if title == string(filepath.Separator) || title == "." {
		title = "Imported project"
	}
	project := NewProject(title, "Uncategorized", "")
	for _, name := range []string{"Novel_architecture.txt", "Novel_directory.txt", "global_summary.txt", "character_state.txt", "foreshadowing.json", "character_arcs.json", "partial_architecture.json"} {
		file := filepath.Join(root, name)
		if fileExists(file) {
			content, err := readLegacyText(root, file)
			if err != nil {
				return Project{}, err
			}
			if name == "Novel_architecture.txt" {
				project.Premise = content
			}
			if name == "Novel_directory.txt" {
				project.Outline = content
			}
			archiveSource(&project, name, content, "legacy")
		}
	}
	if isDirectory(chaptersPath) {
		entries, err := os.ReadDir(chaptersPath)
		if err != nil {
			return Project{}, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "chapter_") || !strings.HasSuffix(name, ".txt") {
				continue
			}
			number, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimPrefix(name, "chapter_"), ".txt"), 10, 32)
			if err != nil || number == 0 {
				continue
			}
			content, err := readLegacyText(root, filepath.Join(chaptersPath, name))
			if err != nil {
				return Project{}, err
			}
			title := "Untitled chapter"
			line := strings.TrimSuffix(strings.SplitN(content, "\n", 2)[0], "\r")
			if strings.HasPrefix(strings.TrimSpace(line), "\u7b2c") && charCount(line) < 80 {
				title = strings.TrimSpace(line)
			}
			chapter := NewChapter(uint32(number), title)
			chapter.Body = content
			project.Chapters = append(project.Chapters, chapter)
		}
		sort.SliceStable(project.Chapters, func(i, j int) bool { return project.Chapters[i].Number < project.Chapters[j].Number })
	}
	versions := filepath.Join(root, ".versions")
	if isDirectory(versions) {
		entries, err := os.ReadDir(versions)
		if err != nil {
			return Project{}, err
		}
		for _, entry := range entries {
			name := entry.Name()
			if !strings.HasPrefix(name, "chapter_") || !strings.HasSuffix(name, ".json") {
				continue
			}
			pieces := strings.Split(name, "_")
			number, err := strconv.ParseUint(pieces[1], 10, 32)
			if err != nil {
				continue
			}
			for index := range project.Chapters {
				chapter := &project.Chapters[index]
				if chapter.Number != uint32(number) {
					continue
				}
				text, err := readLegacyText(root, filepath.Join(versions, name))
				if err != nil {
					return Project{}, err
				}
				var raw map[string]json.RawMessage
				if err := decodeJSON([]byte(text), &raw); err != nil {
					return Project{}, fmt.Errorf("Invalid version JSON: %s", name)
				}
				var body string
				if data, ok := raw["content"]; !ok || string(data) == "null" || decodeJSON(data, &body) != nil {
					return Project{}, fmt.Errorf("Version is missing its text: %s", name)
				}
				var metadata struct {
					CreatedAt string `json:"created_at"`
				}
				_ = decodeJSON(raw["metadata"], &metadata)
				chapter.Versions = append(chapter.Versions, Version{ID: newID(), CreatedAt: metadata.CreatedAt, Title: chapter.Title, Body: body, Status: "draft"})
				break
			}
		}
		for index := range project.Chapters {
			list := project.Chapters[index].Versions
			sort.SliceStable(list, func(i, j int) bool { return list[i].CreatedAt < list[j].CreatedAt })
		}
	}
	roles := filepath.Join(root, "\u89d2\u8272\u5e93")
	if isDirectory(roles) {
		folders := []string{roles}
		entries, err := os.ReadDir(roles)
		if err != nil {
			return Project{}, err
		}
		for _, entry := range entries {
			if entry.IsDir() {
				folders = append(folders, filepath.Join(roles, entry.Name()))
			}
		}
		for _, folder := range folders {
			entries, err := os.ReadDir(folder)
			if err != nil {
				return Project{}, err
			}
			for _, entry := range entries {
				if filepath.Ext(entry.Name()) != ".txt" {
					continue
				}
				content, err := readLegacyText(root, filepath.Join(folder, entry.Name()))
				if err != nil {
					return Project{}, err
				}
				archiveSource(&project, "\u89d2\u8272\u5e93/"+entry.Name(), content, "legacy")
			}
		}
	}
	if len(jsonText(project)) > 40_000_000 {
		return Project{}, errors.New("The legacy project exceeds the 40 MB import limit. Import it in separate volumes")
	}
	return project, nil
}

// ExampleProject provides the original sample in English with demonstration review states.
func ExampleProject() Project {
	project := NewProject("Letters at Low Tide", "Mystery · Speculative fiction", "While restoring an old lighthouse, Lin Wu receives a letter dated tomorrow. Each low tide reveals a path that should not exist. Before the water rises again, she must decipher the signal left by her missing father.")
	project.Style = "Third-person limited through Lin Wu. Use restrained prose and let concrete details carry emotion."
	project.Outline = "Act I: A letter from tomorrow challenges Lin Wu's explanation for her father's disappearance.\nAct II: She and lighthouse keeper Lu Chuan investigate an old sea route, finding contradictions between lighthouse records and island memories.\nAct III: Lin Wu must decide whether to light the tower again."
	chapters := [][3]string{
		{"After the Tide", "Lin Wu found a letter on the stone steps after the tide went out. The envelope was dry, but a grain of wet salt clung to its seal.\n\nHer father's name was on the front. The date was tomorrow.\n\nShe pocketed it and climbed toward the lighthouse. The sea wind struck the old railing with the same three faint notes she had heard last night.\n\nLu Chuan stood at the door, holding a rusted key. When he saw the corner of the envelope, his smile disappeared.\n\n\"Stay down here today,\" he said.\n\nLin Wu looked past him. Above the tower, the lamp that had been dark for seventeen years was slowly turning toward the sea.", "Goal: Find the letter's source. Obstacle: Lu Chuan blocks the lighthouse entrance. Change: The lamp turns without an operator."},
		{"The Keeper's Map", "Lu Chuan finally unlocked the door. Lin Wu followed him into the circular hall, where a map of old sea routes covered the wall.\n\n\"Your father did not take this route on his last voyage,\" he said, pointing to a faded red line.\n\nLin Wu opened her father's logbook. Its coordinates followed that line exactly.\n\nShe kept the logbook to herself. Lu Chuan offered no explanation. Across the map, each understood that the other was keeping something back.\n\nOutside, the tide receded. A narrow stone path emerged from the sea.", "Goal: Check the sailing log. Obstacle: Lu Chuan's account conflicts with the coordinates. Change: Distrust develops between them."},
		{"The Path Below", "Lin Wu decided to follow the exposed path alone. She left the logbook in the tower and took the letter.\n\nLu Chuan called after her. She paused, but kept facing the water.\n\nDeep channels bordered the stones. At the third numbered slab, the envelope turned wet, and ink spread between her fingers.\n\nOnly then did she see the small line on the back: Do not trust the light.", "Goal: Reach the end of the path. Obstacle: The rising tide and uncertain clues. Change: Lin Wu takes the risk and discovers a warning."},
	}
	for index, item := range chapters {
		chapter := NewChapter(uint32(index+1), item[0])
		chapter.Body = item[1]
		chapter.Synopsis = item[2]
		if index == 0 {
			chapter.Status = "final"
		}
		project.Chapters = append(project.Chapters, chapter)
	}
	entities := [][3]string{
		{"Lin Wu", "character", "A lighthouse restorer investigating her father's disappearance. She wants certainty and gradually learns to act amid uncertainty."},
		{"Lu Chuan", "character", "The island's lighthouse keeper. He knows the tides and old routes, and withholds part of the story about Lin Wu's father."},
		{"North Isle Lighthouse", "location", "A lighthouse dark for seventeen years, linking island memories to a vanished sea route."},
		{"Tomorrow's Letter", "item", "A letter dated tomorrow and addressed to Lin Wu's father."},
	}
	for _, item := range entities {
		project.Entities = append(project.Entities, Entity{ID: newID(), Name: item[0], Kind: item[1], Description: item[2], Aliases: []string{}})
	}
	facts := []struct {
		index                                 int
		entity, kind, claim, quote, epistemic string
	}{
		{0, "Lin Wu", "event", "Finds a letter with an impossible date and investigates its source", "Lin Wu found a letter on the stone steps after the tide went out.", "explicit"},
		{0, "Lu Chuan", "event", "Stops Lin Wu from entering the lighthouse", "\"Stay down here today,\" he said.", "explicit"},
		{1, "Lin Wu", "relationship", "Questions Lu Chuan's account", "She kept the logbook to herself. Lu Chuan offered no explanation.", "inferred"},
		{2, "Lin Wu", "event", "Chooses to follow the path alone", "Lin Wu decided to follow the exposed path alone.", "explicit"},
		{2, "Tomorrow's Letter", "foreshadow", "The warning may suggest danger at the lighthouse", "Do not trust the light.", "inferred"},
	}
	for _, item := range facts {
		chapter := project.Chapters[item.index]
		related := ""
		if item.kind == "relationship" {
			related = "Lu Chuan"
		}
		_, err := ingest(&project, chapter.ID, hashText(chapter.Body), []Candidate{{Entity: item.entity, Kind: item.kind, Claim: item.claim, Quote: item.quote, RelatedEntity: related, Epistemic: item.epistemic}}, "Author-written demonstration data")
		if err != nil {
			panic(err)
		}
	}
	for index := range project.Facts[:min(3, len(project.Facts))] {
		project.Facts[index].Status = "confirmed"
	}
	return project
}
