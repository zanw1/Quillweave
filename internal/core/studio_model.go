package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// StudioState keeps author decisions distinct from prose-derived evidence.
type StudioState struct {
	Language             string                 `json:"language"`
	Mode                 string                 `json:"mode"`
	Stage                string                 `json:"stage"`
	TargetCharacters     uint32                 `json:"target_characters"`
	TargetChapters       uint32                 `json:"target_chapters"`
	TargetVolumes        uint32                 `json:"target_volumes"`
	Pacing               string                 `json:"pacing"`
	NarrativeFocus       string                 `json:"narrative_focus"`
	Audience             string                 `json:"audience"`
	NamingStyle          string                 `json:"naming_style"`
	ForbiddenNames       []string               `json:"forbidden_names"`
	Skills               []string               `json:"skills"`
	Preferences          []string               `json:"preferences"`
	Plans                []StoryPlan            `json:"plans"`
	Memories             []StoryMemory          `json:"memories"`
	Decisions            []StoryDecision        `json:"decisions"`
	ChangeSets           []ChangeSet            `json:"changesets,omitempty"`
	Locations            []WorldLocation        `json:"locations,omitempty"`
	Routes               []WorldRoute           `json:"routes,omitempty"`
	KnowledgeMaintenance []KnowledgeMaintenance `json:"knowledge_maintenance,omitempty"`
}

type StoryPlan struct {
	ID               string         `json:"id"`
	ParentID         string         `json:"parent_id"`
	Level            string         `json:"level"`
	Title            string         `json:"title"`
	Theme            string         `json:"theme"`
	Goal             string         `json:"goal"`
	Conflict         string         `json:"conflict"`
	Choice           string         `json:"choice"`
	Consequence      string         `json:"consequence"`
	Pacing           string         `json:"pacing"`
	POV              string         `json:"pov"`
	Characters       []string       `json:"characters"`
	Clues            []string       `json:"clues"`
	StartChapter     uint32         `json:"start_chapter"`
	EndChapter       uint32         `json:"end_chapter"`
	TargetCharacters uint32         `json:"target_characters"`
	Status           string         `json:"status"`
	Flexibility      string         `json:"flexibility,omitempty"`
	OpenQuestions    []string       `json:"open_questions,omitempty"`
	SceneCards       []WritingScene `json:"scene_cards,omitempty"`
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
	Epistemic      string   `json:"epistemic,omitempty"`
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
	Input      string `json:"input,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	ID         string `json:"id"`
	Tool       string `json:"tool"`
	Status     string `json:"status"`
	Detail     string `json:"detail"`
	FullDetail string `json:"full_detail,omitempty"`
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

var errConflictingChapterPlans = errors.New("This chapter has multiple confirmed outlines. Keep one confirmed outline before writing")

// confirmedChapterPlanConflicts counts excess effective outlines without discarding author alternatives.
func confirmedChapterPlanConflicts(plans []StoryPlan) int {
	seen := map[uint32]bool{}
	conflicts := 0
	for _, plan := range plans {
		if plan.Level == "chapter" && plan.Status == "confirmed" {
			if seen[plan.StartChapter] {
				conflicts++
			}
			seen[plan.StartChapter] = true
		}
	}
	return conflicts
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
		if len(plan.SceneCards) > 12 {
			return errors.New("An outline supports at most 12 scene cards")
		}
		if !contains([]string{"", "fixed", "flexible"}, plan.Flexibility) || len(plan.OpenQuestions) > 32 {
			return errors.New("Choose a valid plan flexibility and at most 32 open questions")
		}
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
		if plan.ID == "" || ids[plan.ID] || !contains([]string{"book", "volume", "unit", "chapter", "scene"}, plan.Level) || strings.TrimSpace(plan.Title) == "" || charCount(plan.Title) > 120 || charCount(jsonText(plan)) > 16000 || (plan.EndChapter != 0 && plan.EndChapter < plan.StartChapter) {
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
			inheritsRange := plan.Level == "scene" && plan.StartChapter == 0 && plan.EndChapter == 0
			if !inheritsRange && parent.EndChapter > 0 && (plan.StartChapter < parent.StartChapter || plan.EndChapter == 0 || plan.EndChapter > parent.EndChapter) {
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
		if !contains([]string{"", "explicit", "inferred", "author"}, memory.Epistemic) {
			return errors.New("Unknown memory evidence type")
		}
		if memory.ThroughChapter > 0 && memory.ThroughChapter < memory.Chapter {
			return errors.New("Memory validity ends before it begins")
		}
		if memory.ID == "" {
			return errors.New("Story memory ID is required")
		}
		if ids[memory.ID] {
			return errors.New("Duplicate story memory ID")
		}
		if strings.TrimSpace(memory.Content) == "" {
			return errors.New("Story memory content is required. Use the content field")
		}
		if charCount(jsonText(memory)) > 20000 {
			return errors.New("Story memory exceeds the 20000-character limit")
		}
		if !contains([]string{"rule", "setting", "fact", "inference", "plan", "character", "relationship", "knowledge", "clue", "promise", "summary"}, memory.Kind) {
			return errors.New("Unknown story memory type")
		}
		if !contains([]string{"proposed", "confirmed", "rejected", "superseded"}, memory.Status) {
			return errors.New("Invalid story memory status")
		}
		ids[memory.ID] = true
	}
	for _, decision := range state.Decisions {
		if decision.ID == "" || charCount(decision.Content) > 16000 || !contains([]string{"proposed", "accepted", "rejected"}, decision.Status) {
			return errors.New("Invalid author decision")
		}
	}
	if err := validateMemoryLinks(state.Memories); err != nil {
		return err
	}
	if err := validateKnowledgeMaintenance(state.KnowledgeMaintenance); err != nil {
		return err
	}
	if confirmedChapterPlanConflicts(state.Plans) > 0 {
		return errConflictingChapterPlans
	}
	return nil
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
	requiresQuote := contains([]string{"fact", "inference"}, memory.Kind)
	return ok && memory.SourceHash == hashText(text) && (!requiresQuote || strings.TrimSpace(memory.Quote) != "") && (memory.Quote == "" || strings.Contains(text, memory.Quote))
}

// memoryContext prevents future observations from leaking into earlier chapter writing.
func memoryContext(project *Project, through uint32, queries ...string) []ContextItem {
	return storyMemoryContext(project, through, strings.Join(queries, " "))
}
