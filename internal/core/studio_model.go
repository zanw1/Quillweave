package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// StudioState keeps author decisions distinct from prose-derived evidence.
type StudioState struct {
	Language         string          `json:"language"`
	Mode             string          `json:"mode"`
	Stage            string          `json:"stage"`
	TargetCharacters uint32          `json:"target_characters"`
	TargetChapters   uint32          `json:"target_chapters"`
	TargetVolumes    uint32          `json:"target_volumes"`
	Pacing           string          `json:"pacing"`
	NarrativeFocus   string          `json:"narrative_focus"`
	Audience         string          `json:"audience"`
	NamingStyle      string          `json:"naming_style"`
	ForbiddenNames   []string        `json:"forbidden_names"`
	Skills           []string        `json:"skills"`
	Preferences      []string        `json:"preferences"`
	Plans            []StoryPlan     `json:"plans"`
	Memories         []StoryMemory   `json:"memories"`
	Decisions        []StoryDecision `json:"decisions"`
}

type StoryPlan struct {
	ID               string   `json:"id"`
	ParentID         string   `json:"parent_id"`
	Level            string   `json:"level"`
	Title            string   `json:"title"`
	Theme            string   `json:"theme"`
	Goal             string   `json:"goal"`
	Conflict         string   `json:"conflict"`
	Choice           string   `json:"choice"`
	Consequence      string   `json:"consequence"`
	Pacing           string   `json:"pacing"`
	POV              string   `json:"pov"`
	Characters       []string `json:"characters"`
	Clues            []string `json:"clues"`
	StartChapter     uint32   `json:"start_chapter"`
	EndChapter       uint32   `json:"end_chapter"`
	TargetCharacters uint32   `json:"target_characters"`
	Status           string   `json:"status"`
}

type StoryMemory struct {
	ID             string   `json:"id"`
	Kind           string   `json:"kind"`
	Entity         string   `json:"entity"`
	Title          string   `json:"title"`
	Content        string   `json:"content"`
	State          string   `json:"state"`
	Status         string   `json:"status"`
	Chapter        uint32   `json:"chapter"`
	ThroughChapter uint32   `json:"through_chapter"`
	DueChapter     uint32   `json:"due_chapter"`
	StoryTime      string   `json:"story_time"`
	KnownBy        []string `json:"known_by"`
	SourceID       string   `json:"source_id"`
	SourceHash     string   `json:"source_hash"`
	Quote          string   `json:"quote"`
	SessionID      string   `json:"session_id"`
	Supersedes     string   `json:"supersedes"`
	UpdatedAt      string   `json:"updated_at"`
}

type StoryDecision struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Content   string `json:"content"`
	Status    string `json:"status"`
	SessionID string `json:"session_id"`
	CreatedAt string `json:"created_at"`
}

type AgentStep struct {
	ID     string `json:"id"`
	Tool   string `json:"tool"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

type StudioProposal struct {
	Mode    string       `json:"mode,omitempty"`
	ID      string       `json:"id"`
	Kind    string       `json:"kind"`
	Title   string       `json:"title"`
	Content string       `json:"content"`
	Status  string       `json:"status"`
	Plan    *StoryPlan   `json:"plan,omitempty"`
	Memory  *StoryMemory `json:"memory,omitempty"`
}

// defaultStudio provides safe defaults for new stories and pre-studio archives.
func defaultStudio() StudioState {
	return StudioState{Language: "inherit", Mode: "undecided", Stage: "idea", NamingStyle: "original", ForbiddenNames: []string{}, Skills: []string{"continuity"}, Preferences: []string{}, Plans: []StoryPlan{}, Memories: []StoryMemory{}, Decisions: []StoryDecision{}}
}

// UnmarshalJSON fills omitted studio fields in old backups while rejecting malformed explicit values.
func (state *StudioState) UnmarshalJSON(data []byte) error {
	type record StudioState
	value := record(defaultStudio())
	if err := decodeRecord(json.RawMessage(data), &value); err != nil {
		return err
	}
	*state = StudioState(value)
	return nil
}

// responseLanguage applies explicit task language, work language, then interface language.
func responseLanguage(project *Project, requested string) string {
	for _, language := range []string{requested, project.taskLanguage, project.Studio.Language, project.requestLanguage} {
		if language == "zh-CN" || language == "en" {
			return language
		}
	}
	return "en"
}

// localText chooses application-owned task text without translating author material.
func localText(project *Project, english, chinese string) string {
	if responseLanguage(project, "") == "zh-CN" {
		return chinese
	}
	return english
}

// validateStudio checks identifiers, hierarchy and hard bounds before any persistence.
func validateStudio(state *StudioState) error {
	if !contains([]string{"inherit", "en", "zh-CN"}, state.Language) || !contains([]string{"undecided", "serial", "traditional", "hybrid"}, state.Mode) || !contains([]string{"idea", "direction", "setting", "world", "planning", "writing", "review"}, state.Stage) {
		return errors.New("Invalid story language, form or stage")
	}
	if state.TargetCharacters > 100_000_000 || state.TargetChapters > 100_000 || state.TargetVolumes > 1000 {
		return errors.New("Story targets exceed the supported limits")
	}
	if len(state.Plans) > 100_000 || len(state.Memories) > 100_000 || len(state.Decisions) > 20_000 {
		return errors.New("Too many studio records")
	}
	ids := map[string]bool{}
	plans := map[string]StoryPlan{}
	for _, plan := range state.Plans {
		if plan.Level == "chapter" {
			if plan.StartChapter == 0 || plan.EndChapter != plan.StartChapter {
				return errors.New("A chapter plan must identify one chapter number")
			}
			if plan.TargetCharacters > 0 {
				if err := validateTarget(plan.TargetCharacters); err != nil {
					return err
				}
			}
		}
		if plan.ID == "" || ids[plan.ID] || !contains([]string{"book", "volume", "unit", "chapter", "scene"}, plan.Level) || strings.TrimSpace(plan.Title) == "" || charCount(plan.Title) > 120 || charCount(jsonText(plan)) > 16000 || plan.EndChapter < plan.StartChapter {
			return errors.New("Invalid or duplicate plan")
		}
		ids[plan.ID] = true
		plans[plan.ID] = plan
	}
	for _, plan := range state.Plans {
		seen := map[string]bool{plan.ID: true}
		if plan.ParentID != "" {
			parent, ok := plans[plan.ParentID]
			ranks := map[string]int{"book": 0, "volume": 1, "unit": 2, "chapter": 3, "scene": 4}
			if !ok || ranks[parent.Level] >= ranks[plan.Level] {
				return errors.New("Child plan must have a more detailed level than its parent")
			}
			if parent.EndChapter > 0 && (plan.StartChapter < parent.StartChapter || plan.EndChapter > parent.EndChapter) {
				return errors.New("Child chapter range exceeds its parent")
			}
		}
		parent := plan.ParentID
		for parent != "" {
			ancestor, ok := plans[parent]
			if !ok || seen[parent] {
				return errors.New("Planning hierarchy contains a missing parent or cycle")
			}
			seen[parent] = true
			parent = ancestor.ParentID
		}
	}
	ids = map[string]bool{}
	for _, memory := range state.Memories {
		if memory.ThroughChapter > 0 && memory.ThroughChapter < memory.Chapter {
			return errors.New("Memory validity ends before it begins")
		}
		if memory.ID == "" || ids[memory.ID] || strings.TrimSpace(memory.Content) == "" || charCount(jsonText(memory)) > 20000 || !contains([]string{"rule", "setting", "fact", "inference", "plan", "character", "relationship", "knowledge", "clue", "promise", "summary"}, memory.Kind) || !contains([]string{"proposed", "confirmed", "rejected", "superseded"}, memory.Status) {
			return errors.New("Invalid or duplicate memory")
		}
		ids[memory.ID] = true
	}
	for _, decision := range state.Decisions {
		if decision.ID == "" || charCount(decision.Content) > 16000 || !contains([]string{"proposed", "accepted", "rejected"}, decision.Status) {
			return errors.New("Invalid author decision")
		}
	}
	return validateMemoryLinks(state.Memories)
}

// studioWarnings reports revisable planning conflicts without inventing creative correctness.
func studioWarnings(project *Project) []string {
	result := []string{}
	state := project.Studio
	if state.TargetChapters > 0 && state.TargetCharacters > 0 && uint64(state.TargetChapters)*uint64(project.DefaultChapterWords) != uint64(state.TargetCharacters) {
		result = append(result, localText(project, "Total length differs from chapter count × chapter target. Choose which target to adjust.", "总字数与章节数×单章字数不一致，请选择调整哪个目标。"))
	}
	allocations := map[string]uint64{}
	for _, plan := range state.Plans {
		if plan.Status != "rejected" {
			allocations[plan.ParentID] += uint64(plan.TargetCharacters)
		}
		if state.TargetChapters > 0 && plan.EndChapter > state.TargetChapters {
			result = append(result, fmt.Sprintf("%s: %s", plan.Title, localText(project, "chapter range exceeds the book target", "章节范围超过全书目标")))
		}
	}
	for _, plan := range state.Plans {
		if plan.TargetCharacters > 0 && allocations[plan.ID] > uint64(plan.TargetCharacters) {
			result = append(result, plan.Title+": "+localText(project, "child budgets exceed the parent budget", "下级字数预算超过上级预算"))
		}
	}
	return result
}

// memoryCurrent binds a memory to the exact current source revision and quote.
func memoryCurrent(project *Project, memory StoryMemory) bool {
	if memory.SourceID == "" {
		return memory.Kind != "fact" && memory.Kind != "inference"
	}
	text, _, ok := project.SourceText(memory.SourceID)
	return ok && memory.SourceHash == hashText(text) && (memory.Quote == "" || strings.Contains(text, memory.Quote))
}

// memoryContext prevents future observations from leaking into earlier chapter writing.
func memoryContext(project *Project, through uint32, queries ...string) []ContextItem {
	items := []ContextItem{}
	query := strings.Join(queries, " ")
	needles := terms(query)
	normalized := lowerText(query)
	current := through
	successors := memorySuccessors(project.Studio.Memories)
	if current == ^uint32(0) {
		current = 0
		for _, chapter := range project.Chapters {
			current = max(current, chapter.Number)
		}
	}
	for i := len(project.Studio.Memories) - 1; i >= 0; i-- {
		m := project.Studio.Memories[i]
		if !memoryVisibleAt(m, through, successors) || !memoryCurrent(project, m) {
			continue
		}
		score := 4
		if m.Kind == "rule" {
			score = 10000
		}
		if (m.Kind == "clue" || m.Kind == "promise") && m.State != "resolved" && m.State != "fulfilled" && m.State != "abandoned" {
			score = 100
			if m.DueChapter > 0 && m.DueChapter <= current+10 {
				score += 300
			}
		}
		if m.Chapter <= current && m.Chapter+8 >= current {
			score += 40
		}
		if m.Entity != "" && strings.Contains(normalized, lowerText(m.Entity)) {
			score += 2000
		}
		textForMatch := lowerText(m.Title + " " + m.Content + " " + m.StoryTime)
		for term := range needles {
			if strings.Contains(textForMatch, term) {
				score += 3
			}
		}
		knowledge := strings.Join(m.KnownBy, ", ")
		if knowledge == "" {
			knowledge = "not specified; do not assume public knowledge"
		}
		text := fmt.Sprintf("%s: %s\nState: %s. Story time: %s. Knowledge scope: %s. Due chapter: %d\nQuote: %s", m.Title, m.Content, m.State, m.StoryTime, knowledge, m.DueChapter, m.Quote)
		items = append(items, ContextItem{ID: "N:" + m.ID, Title: m.Entity + " · " + m.Title, Kind: m.Kind, Text: text, Score: score, Reason: "Author-confirmed story memory; respect time and character knowledge"})
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].Score > items[j].Score })
	result := []ContextItem{}
	ordinary := 0
	for _, item := range items {
		if item.Kind == "rule" {
			result = append(result, item)
		} else if ordinary < 32 {
			result = append(result, item)
			ordinary++
		}
	}
	return result
}
