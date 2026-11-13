package core

import (
	"errors"
	"fmt"
	"strings"
)

type StylePreset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Guidance    string `json:"guidance"`
}

// narrativePovLabel canonicalizes supported labels and legacy enum values without guessing unknown viewpoints.
func narrativePovLabel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "第一人称", "first person", "first_person":
		return "First person"
	case "第三人称有限视角", "third-person limited", "third_limited", "third_person_limited":
		return "Third-person limited"
	case "全知视角", "omniscient", "third_omniscient", "third_person_omniscient":
		return "Omniscient"
	default:
		return strings.TrimSpace(value)
	}
}

// findWritingPreset shares preset validation and preserves the established custom fallback for old records.
func findWritingPreset(id string) (StylePreset, bool) {
	presets := writingPresets()
	for _, preset := range presets {
		if preset.ID == id {
			return preset, true
		}
	}
	return presets[0], false
}

// writingPresets returns the nine writing styles and their complete guidance.
func writingPresets() []StylePreset {
	return []StylePreset{
		{"custom", "Custom style", "Follow the author's style notes", "Follow the author's custom style and narrative boundaries."},
		{"fast_serial", "Fast-paced serial", "Clear goals, immediate obstacles, and visible progress", "Establish the immediate goal and obstacle early. Advance the plot through concrete action. Give gains a cost and prepare reversals with clues. End chapters with a natural reason to continue."},
		{"slow_growth", "Gradual growth", "Develop abilities, relationships, and world knowledge over time", "Make growth cumulative and causally grounded. Use intermediate goals and observable changes. Reveal the world through choices and consequences."},
		{"mystery", "Mystery", "Limited knowledge and traceable clues", "Respect the viewpoint character's knowledge. Plant clues in details that readers can revisit. Make misdirection fair and let later revelations follow from established evidence."},
		{"romance", "Romantic tension", "Relationship changes and subtext", "Show changes in relationships through dialogue, action, and hesitation. Ground emotions in specific situations and vary each character's response."},
		{"ensemble", "Ensemble intrigue", "Conflicting goals, resources, and loyalties", "Clarify each faction's goals, leverage, and changing interests. Build conflict through understandable decisions. Give every character an independent voice and purpose."},
		{"daily", "Slice of life", "Everyday detail, gentle conflict, and natural humor", "Organize scenes around everyday goals and character interactions. Keep the tone relaxed and the humor natural. Let each scene add a small change in information or relationships."},
		{"fantasy", "Progression fantasy", "Defined abilities, costs of growth, and a layered world", "Track the limits of abilities, resources, and the cost of growth. Let goals and decisions drive conflict. Resolve problems within the established world rules."},
		{"realistic", "Contemporary realism", "Specific professions, settings, and social relationships", "Use details consistent with each character's profession and environment. Show social relationships through behavior. Qualify real-world claims when supporting sources are limited."},
	}
}

// validateTarget uses the same length range for project defaults and chapter overrides.
func validateTarget(target uint32) error {
	if target < 100 || target > 50000 {
		return errors.New("Chapter length must be between 100 and 50,000 characters")
	}
	return nil
}

// chapterTarget prefers a chapter override and otherwise inherits the project target.
func chapterTarget(project *Project, chapter *Chapter) uint32 {
	if chapter != nil && chapter.TargetWords != nil {
		return *chapter.TargetWords
	}
	return project.DefaultChapterWords
}

// fictionProseGuidance adapts Humanizer's editing principles to fiction without flattening author voice.
func fictionProseGuidance() string {
	return "Fiction prose: The author's style notes and established character voices take priority over generic editing rules. Match the existing prose's rhythm, vocabulary, and viewpoint. Let actions, specific details, dialogue and the viewpoint character's own thoughts carry emotion and subtext; avoid explaining a feeling the scene already conveys. Reduce repeated sentence openings, stock comparisons, empty intensifiers, forced contrasts or three-part lists, and paragraph endings that summarize or inflate the scene. Keep deliberate repetition, imagery, punctuation, and genre conventions when they serve the author's voice. Return only the requested fiction, without chatbot greetings, editing commentary, decorative headings, or an explanatory moral. Before responding, reread for these habits and for continuity of names, plot facts, relationships, character knowledge, and planted clues."
}

// promptGuidance defers writing constraints while exploring and applies them to concrete writing tasks.
func promptGuidance(project *Project, chapter *Chapter, action string) string {
	if contains([]string{"ask", "agent", "naming", "research"}, action) && chapter == nil && len(project.Chapters) == 0 && strings.TrimSpace(project.Premise) == "" && strings.TrimSpace(project.Outline) == "" && (project.Studio.Mode == "undecided" || project.Studio.Mode == "") {
		return "The author is exploring an undecided story. Genre, title, plot, length, and viewpoint are not prerequisites. Help discover interests through one or two easy questions at a time. Use preferences the author actually expresses, offer possibilities as proposals, and do not treat application writing defaults as confirmed creative choices. Do not require a complete setup or rush into a full outline."
	}
	preset, known := findWritingPreset(project.WritingStyle)
	if !known && strings.TrimSpace(project.WritingStyle) != "" {
		preset.Guidance += "\n" + project.WritingStyle
	}
	target := chapterTarget(project, chapter)
	actual := 0
	if chapter != nil {
		actual = wordCount(chapter.Body)
	}
	length := fmt.Sprintf("Target chapter length is about %d non-whitespace characters. Treat this as a writing goal and prioritize a complete scene. Report a length only when it has been counted.", target)
	if action == "continue" {
		remaining := max(0, int(target)-actual)
		length = fmt.Sprintf("Target chapter length is about %d non-whitespace characters. The saved text has %d characters, leaving about %d. Return the new prose and follow the author's request. Any completion claim must reflect a measured count.", target, actual, remaining)
	}
	guidance := fmt.Sprintf("Selected style: %s\nStyle guidance: %s\nNarrative viewpoint: %s\n%s", preset.Name, preset.Guidance, narrativePovLabel(project.NarrativePov), length)
	if contains([]string{"continue", "rewrite"}, action) {
		guidance += "\n" + fictionProseGuidance()
	}
	return guidance
}
