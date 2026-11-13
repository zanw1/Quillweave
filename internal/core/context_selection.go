package core

import (
	"fmt"
	"sort"
	"strings"
)

// planningPath selects the focused chapter and its ancestors while respecting explicit omissions.
func planningPath(project *Project, chapter *Chapter, workspace *WorkspaceContext) []StoryPlan {
	omitted := map[string]bool{}
	focus := ""
	if workspace != nil {
		omitted = omittedIDs(workspace.OmitPlanIDs)
		focus = workspace.ActivePlanID
		if chapter == nil {
			chapter = findChapter(project, workspace.ActiveChapterID)
		}
	}
	byID := map[string]StoryPlan{}
	for _, plan := range project.Studio.Plans {
		if plan.Status == "confirmed" && !omitted[plan.ID] {
			byID[plan.ID] = plan
		}
	}
	selected := map[string]bool{}
	for _, plan := range project.Studio.Plans {
		if _, exists := byID[plan.ID]; !exists {
			continue
		}
		inChapter := chapter != nil && planCoversChapter(plan, chapter.Number, byID)
		if plan.ID != focus && !inChapter {
			continue
		}
		for id := plan.ID; id != "" && !selected[id]; {
			parent, exists := byID[id]
			if !exists {
				break
			}
			selected[id] = true
			id = parent.ParentID
		}
	}
	result := []StoryPlan{}
	for _, plan := range project.Studio.Plans {
		if selected[plan.ID] {
			result = append(result, plan)
		}
	}
	ranks := map[string]int{"book": 0, "volume": 1, "unit": 2, "chapter": 3, "scene": 4}
	sort.SliceStable(result, func(i, j int) bool {
		if ranks[result[i].Level] != ranks[result[j].Level] {
			return ranks[result[i].Level] < ranks[result[j].Level]
		}
		return result[i].StartChapter < result[j].StartChapter
	})
	return result
}

// planCoversChapter inherits missing child ranges and checks every available ancestor boundary.
func planCoversChapter(plan StoryPlan, chapter uint32, byID map[string]StoryPlan) bool {
	bounded := false
	global := plan.Level == "book"
	for depth := 0; depth <= len(byID); depth++ {
		end := plan.EndChapter
		if plan.Level == "chapter" && plan.StartChapter > 0 && end == 0 {
			end = plan.StartChapter
		}
		if plan.StartChapter > chapter || end > 0 && end < chapter {
			return false
		}
		bounded = bounded || plan.StartChapter > 0 || plan.EndChapter > 0
		if plan.ParentID == "" {
			return bounded || global
		}
		parent, exists := byID[plan.ParentID]
		if !exists {
			return bounded
		}
		plan = parent
	}
	return false
}

// chapterPlanningNotes removes only exact generated plan snapshots, retaining distinct author text unchanged.
func chapterPlanningNotes(project *Project, chapter *Chapter) string {
	if chapter == nil || strings.TrimSpace(chapter.Synopsis) == "" {
		return ""
	}
	hasPlan := false
	for _, plan := range project.Studio.Plans {
		if plan.Level == "chapter" && plan.Status == "confirmed" && plan.StartChapter == chapter.Number {
			hasPlan = true
			if planSynopsisMatches(project, plan, chapter.Synopsis) {
				return ""
			}
		}
	}
	if hasPlan {
		for _, change := range project.Studio.ChangeSets {
			if change.Status != ChangeSetApplied {
				continue
			}
			for _, operation := range change.Operations {
				if operation.TargetKind != TargetPlan || len(operation.BeforeValue) == 0 {
					continue
				}
				var previous StoryPlan
				if decodeJSON(operation.BeforeValue, &previous) == nil && previous.Level == "chapter" && previous.Status == "confirmed" && previous.StartChapter == chapter.Number && planSynopsisMatches(project, previous, chapter.Synopsis) {
					return ""
				}
			}
		}
	}
	return chapter.Synopsis
}

// planSynopsisMatches recognizes both generated UI languages without treating partial matches as copied text.
func planSynopsisMatches(project *Project, plan StoryPlan, synopsis string) bool {
	copy := *project
	for _, language := range []string{"en", "zh-CN"} {
		copy.taskLanguage = language
		if strings.TrimSpace(synopsis) == strings.TrimSpace(planSynopsis(&copy, plan)) {
			return true
		}
	}
	return false
}

// planSynopsis preserves the action connecting a plan's obstacle to its outcome.
func planSynopsis(project *Project, plan StoryPlan) string {
	parts := []string{}
	for _, field := range []struct{ name, chinese, text string }{
		{"Goal", "目标", plan.Goal}, {"Conflict", "冲突", plan.Conflict}, {"Choice / action", "选择与行动", plan.Choice}, {"Consequence", "后果", plan.Consequence},
		{"Open questions", "未决问题", strings.Join(plan.OpenQuestions, "\n")},
	} {
		if strings.TrimSpace(field.text) != "" {
			parts = append(parts, localText(project, field.name, field.chinese)+"\n"+field.text)
		}
	}
	if plan.Flexibility != "" {
		parts = append(parts, localText(project, "Plan flexibility", "规划弹性")+"\n"+plan.Flexibility)
	}
	return strings.Join(parts, "\n\n")
}

// chapterContextItems pins complete local plans and the previous saved chapter for prose work.
func chapterContextItems(project *Project, request AIRequest, chapter *Chapter) []ContextItem {
	items := []ContextItem{}
	if chapter != nil && contains([]string{"continue", "rewrite", "review"}, request.Action) {
		items = append(items, ContextItem{ID: "project_revision", Title: "Saved project revision", Kind: "project_revision", Text: fmt.Sprint(project.Revision), Required: true, Reason: "Writing stages share this saved version"})
	}
	for _, plan := range planningPath(project, chapter, request.WorkspaceContext) {
		items = append(items, ContextItem{ID: "P:" + plan.ID, Title: plan.Title, Kind: "plan", Text: "Future planning, interpreted only within this chapter's time and character knowledge.\n" + jsonText(plan), Required: true, Reason: "Current chapter planning path; complete goals, choices and consequences"})
	}
	if chapter == nil || !contains([]string{"continue", "rewrite", "review"}, request.Action) || request.WorkspaceContext != nil && request.WorkspaceContext.OmitChapter {
		return items
	}
	var previous *Chapter
	for i := range project.Chapters {
		candidate := &project.Chapters[i]
		if candidate.Number < chapter.Number && strings.TrimSpace(candidate.Body) != "" && (previous == nil || candidate.Number > previous.Number) {
			previous = candidate
		}
	}
	if previous != nil {
		title := fmt.Sprintf(localText(project, "Previous saved chapter %d — %s", "上一章已保存正文 · 第%d章 %s"), previous.Number, previous.Title)
		items = append(items, ContextItem{ID: "C:" + previous.ID, Title: title, Kind: "previous_chapter", Text: previous.Body, Required: true, SourceID: previous.ID, SourceHash: hashText(previous.Body), Reason: "Complete immediate predecessor for scene and relationship continuity"})
	}
	return items
}

// orderedContextItems deduplicates source records and keeps required evidence ahead of optional retrieval.
func orderedContextItems(items []ContextItem) []ContextItem {
	result := []ContextItem{}
	seen := map[string]bool{}
	for _, item := range items {
		if !seen[item.ID] {
			result = append(result, item)
			seen[item.ID] = true
		}
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Required && !result[j].Required })
	return result
}

// inspectableContext filters omitted records and attaches source revisions without promoting references to story facts.
func inspectableContext(project *Project, request AIRequest, items []ContextItem) []ContextItem {
	items = filterStoryKnowledgeContext(project, request, items)
	omittedMemories := map[string]bool{}
	if request.WorkspaceContext != nil {
		omittedMemories = omittedIDs(request.WorkspaceContext.OmitMemoryIDs)
	}
	result := []ContextItem{}
	for _, item := range orderedContextItems(items) {
		if omittedMemories[strings.TrimPrefix(item.ID, "N:")] {
			continue
		}
		parts := strings.Split(item.ID, ":")
		if len(parts) > 1 && parts[0] == "W" {
			for _, fact := range project.Facts {
				if fact.ID == parts[1] {
					item.SourceID, item.SourceHash = fact.SourceID, fact.SourceHash
					break
				}
			}
		}
		if len(parts) > 1 && (parts[0] == "C" || parts[0] == "S") {
			if request.WorkspaceContext != nil && (parts[0] == "C" && request.WorkspaceContext.OmitChapter || parts[0] == "S" && request.WorkspaceContext.OmitSource && parts[1] == request.WorkspaceContext.ActiveSourceID) {
				continue
			}
			if source, _, exists := project.SourceText(parts[1]); exists && item.SourceHash == "" {
				item.SourceID, item.SourceHash = parts[1], hashText(source)
			}
		}
		if item.Kind == "rule" {
			item.Required = true
		}
		if item.SourceID != "" && !contains([]string{"rule", "setting"}, item.Kind) {
			for _, source := range project.Sources {
				if source.ID == item.SourceID && source.Category == "craft" {
					item.Kind = "craft"
					break
				}
			}
		}
		if item.Kind == "craft" {
			if !strings.HasPrefix(item.Text, "Writing reference from another work.") {
				item.Text = "Writing reference from another work. Use its craft analysis and applicable conditions; its characters, events and rules belong to that reference.\n" + item.Text
			}
			item.Reason = "Craft reference with a separate identity from this story's evidence"
		}
		result = append(result, item)
	}
	return orderedContextItems(result)
}

// creationQuery includes local scene context and planned participants when a request is simply "continue".
func creationQuery(project *Project, chapter *Chapter, instruction string) string {
	parts := []string{instruction}
	if chapter != nil {
		parts = append(parts, chapter.Title, chapterPlanningNotes(project, chapter))
		body := []rune(chapter.Body)
		parts = append(parts, string(body[:min(600, len(body))]), string(body[max(0, len(body)-1200):]))
		for _, plan := range planningPath(project, chapter, nil) {
			parts = append(parts, strings.Join(plan.Characters, " "), plan.POV, strings.Join(plan.Clues, " "), plan.Choice)
		}
	}
	return strings.Join(parts, " ")
}

// relevantEntities preserves full selected biographies and global world notes without loading an entire cast.
func relevantEntities(project *Project, query string, strict bool) []Entity {
	type ranked struct {
		entity Entity
		score  int
	}
	rankedEntities := []ranked{}
	worlds := []Entity{}
	needles := terms(query)
	query = lowerText(query)
	for _, entity := range project.Entities {
		if entity.Kind == "world" && !strict {
			worlds = append(worlds, entity)
			continue
		}
		score := 0
		for _, name := range append([]string{entity.Name}, entity.Aliases...) {
			if name != "" && strings.Contains(query, lowerText(name)) {
				score += 1000
			}
		}
		if score == 0 {
			description := lowerText(entity.Description)
			for term := range needles {
				if len(term) > 2 && strings.Contains(description, term) {
					score++
				}
			}
		}
		if score > 0 || (!strict && strings.TrimSpace(query) == "") {
			rankedEntities = append(rankedEntities, ranked{entity: entity, score: score})
		}
	}
	sort.SliceStable(rankedEntities, func(i, j int) bool { return rankedEntities[i].score > rankedEntities[j].score })
	limit := 12
	named := len(rankedEntities) > 0 && rankedEntities[0].score >= 1000
	if !named {
		limit = 4
	}
	for _, item := range rankedEntities[:min(limit, len(rankedEntities))] {
		if named && item.score < 1000 {
			break
		}
		worlds = append(worlds, item.entity)
	}
	return worlds
}
