package core

import (
	"fmt"
	"sort"
	"strings"
)

type CacheControl struct {
	Type string `json:"type"`
}

// resolveBuiltinSkills automatically selects craft skills matching the story mode and stage.
func resolveBuiltinSkills(mode, stage string) []WritingSkill {
	if mode == "" {
		mode = "undecided"
	}
	all := writingSkills()
	selected := []WritingSkill{}
	for _, skill := range all {
		if skill.ID == "outline_control" && stage == "idea" || skill.ID == "snowflake" && stage == "review" {
			continue
		}
		if contains(skill.Modes, "all") {
			selected = append(selected, skill)
			continue
		}
		if mode == "undecided" || mode == "hybrid" || contains(skill.Modes, mode) {
			selected = append(selected, skill)
		}
	}
	return selected
}

// agentCraftGuidance keeps craft instructions stable while the current story determines their applicability.
func agentCraftGuidance() string {
	lines := []string{"Choose craft approaches for the current author goal and story form; their names below are an index, not a mandatory sequence. Writing stages supply detailed methods. Build actions from independent character wants, knowledge, feasible choices and continuing consequences, including quiet relationship scenes. Distinguish accepted settings, future plans, source events, rejected alternatives and inference. Current story updates supersede earlier snapshots. Preserve story time and knowledge boundaries; reread original evidence when summaries are insufficient. Follow author preferences and the current save policy."}
	names := []string{}
	for _, skill := range writingSkills() {
		names = append(names, skill.Name)
	}
	lines = append(lines, "Available approaches: "+strings.Join(names, ", "))
	return strings.Join(lines, "\n")
}

// omittedIDs marks records the author removed from this turn.
func omittedIDs(ids []string) map[string]bool {
	skipped := map[string]bool{}
	for _, id := range ids {
		if id != "" {
			skipped[id] = true
		}
	}
	return skipped
}

// narrativeEmphasis avoids repeating the entire premise when an imported focus is an exact copy.
func narrativeEmphasis(project *Project) string {
	focus := strings.TrimSpace(project.Studio.NarrativeFocus)
	if focus != "" && focus == strings.TrimSpace(project.Premise) {
		return "Use the story premise supplied above."
	}
	return project.Studio.NarrativeFocus
}

// distinctAuthorPreferences keeps ordered author notes while omitting identical fields already supplied.
func distinctAuthorPreferences(project *Project) []string {
	seen := map[string]bool{strings.TrimSpace(project.Premise): true, strings.TrimSpace(project.Style): true, strings.TrimSpace(project.Studio.NarrativeFocus): true}
	result := []string{}
	for _, preference := range project.Studio.Preferences {
		key := strings.TrimSpace(preference)
		if key != "" && !seen[key] {
			result = append(result, preference)
			seen[key] = true
		}
	}
	return result
}

// buildStoryBibleLayer1 constructs a deterministic ID-sorted Story Bible snapshot for Layer 1 prefix caching.
func buildStoryBibleLayer1(project *Project, budget int, ctx *WorkspaceContext, chapterScope ...uint32) string {
	through := ^uint32(0)
	if ctx != nil {
		if chapter := findChapter(project, ctx.ActiveChapterID); chapter != nil {
			through = chapter.Number
		}
	}
	if len(chapterScope) > 0 {
		through = chapterScope[0]
	}
	if through == ^uint32(0) {
		through = 0
		for _, chapter := range project.Chapters {
			through = max(through, chapter.Number)
		}
	}
	s := project.Studio
	mode := s.Mode
	if mode == "" {
		mode = "undecided"
	}
	stage := s.Stage
	if stage == "" {
		stage = "idea"
	}
	lines := []string{
		"<story_bible>",
		fmt.Sprintf("Story time boundary: chapter %d. Future plans describe intended events only.", through),
		fmt.Sprintf("Project ID: %s", project.ID),
		fmt.Sprintf("Title: %s | Genre: %s | Form: %s | Stage: %s", project.Title, project.Genre, mode, stage),
	}
	if strings.TrimSpace(project.Premise) != "" {
		lines = append(lines, "Premise: "+strings.TrimSpace(project.Premise))
	}
	if strings.TrimSpace(project.Style) != "" {
		lines = append(lines, "Style notes: "+strings.TrimSpace(project.Style))
	}
	var scopeChapter *Chapter
	if ctx != nil {
		scopeChapter = findChapter(project, ctx.ActiveChapterID)
	}
	if scopeChapter == nil && len(chapterScope) > 0 && chapterScope[0] != ^uint32(0) {
		for i := range project.Chapters {
			if project.Chapters[i].Number == chapterScope[0] {
				scopeChapter = &project.Chapters[i]
				break
			}
		}
	}
	if summary := scopedOutlineContext(project, scopeChapter, ctx); strings.TrimSpace(summary) != "" {
		outline := []rune(strings.TrimSpace(summary))
		if len(outline) > 1200 {
			outline = outline[:1200]
		}
		lines = append(lines, "Author outline: "+string(outline))
	}
	if mode != "undecided" && (s.TargetCharacters > 0 || s.TargetChapters > 0 || s.TargetVolumes > 0) {
		lines = append(lines, fmt.Sprintf("Targets: %d characters, %d chapters, %d volumes.", s.TargetCharacters, s.TargetChapters, s.TargetVolumes))
	}
	lines = append(lines, fmt.Sprintf("Audience: %s | Pace: %s | Narrative emphasis: %s", s.Audience, s.Pacing, narrativeEmphasis(project)))
	lines = append(lines, "Naming preference: "+s.NamingStyle+" | Forbidden names: "+strings.Join(s.ForbiddenNames, ", ")+" | Author preferences: "+strings.Join(distinctAuthorPreferences(project), "; "))
	decisions := append([]StoryDecision{}, s.Decisions...)
	sort.Slice(decisions, func(i, j int) bool { return decisions[i].ID < decisions[j].ID })
	for _, decision := range decisions {
		if decision.Status != "accepted" && decision.Status != "rejected" {
			continue
		}
		content := []rune(decision.Content)
		lines = append(lines, fmt.Sprintf("Decision [%s] (%s) %s: %s", decision.ID, decision.Status, decision.Title, string(content[:min(len(content), 240)])))
	}

	entities := []Entity{}
	skipEntities := omittedIDs(nil)
	skipPlans := omittedIDs(nil)
	skipMemories := omittedIDs(nil)
	if ctx != nil {
		skipEntities = omittedIDs(ctx.OmitEntityIDs)
		skipPlans = omittedIDs(ctx.OmitPlanIDs)
		skipMemories = omittedIDs(ctx.OmitMemoryIDs)
	}
	for _, entity := range project.Entities {
		if !skipEntities[entity.ID] {
			entities = append(entities, entity)
		}
	}
	sort.Slice(entities, func(i, j int) bool { return entities[i].ID < entities[j].ID })
	if len(entities) > 0 {
		limit := min(24, len(entities))
		lines = append(lines, "Entities (sorted by ID):")
		for _, e := range entities[:limit] {
			desc := []rune(strings.TrimSpace(e.Description))
			if len(desc) > 180 {
				desc = desc[:180]
			}
			lines = append(lines, fmt.Sprintf("- [%s] %s (%s): %s", e.ID, e.Name, e.Kind, string(desc)))
		}
	}

	memories := []StoryMemory{}
	successors := memorySuccessors(s.Memories)
	for _, m := range s.Memories {
		if memoryVisibleAt(m, through, successors) && memoryCurrent(project, m) && !skipMemories[m.ID] {
			memories = append(memories, m)
		}
	}
	sort.Slice(memories, func(i, j int) bool {
		if (memories[i].Kind == "rule") != (memories[j].Kind == "rule") {
			return memories[i].Kind == "rule"
		}
		return memories[i].ID < memories[j].ID
	})
	if len(memories) > 0 {
		limit := min(24, len(memories))
		lines = append(lines, "Confirmed story memory & rules (sorted by ID):")
		for _, m := range memories[:limit] {
			if m.Kind == "plan" {
				lines = append(lines, "Future planning: the following memory is an author-approved proposal, not an observed event.")
			}
			content := []rune(strings.TrimSpace(m.Content))
			if len(content) > 180 {
				content = content[:180]
			}
			lines = append(lines, fmt.Sprintf("- [N:%s] (%s, chapters %d–%d, known by %s) %s: %s", m.ID, m.Kind, m.Chapter, m.ThroughChapter, strings.Join(m.KnownBy, ","), m.Title, string(content)))
		}
	}

	plans := []StoryPlan{}
	for _, p := range s.Plans {
		if p.Status == "confirmed" && !skipPlans[p.ID] {
			plans = append(plans, p)
		}
	}
	sort.SliceStable(plans, func(i, j int) bool {
		if plans[i].StartChapter != plans[j].StartChapter {
			return plans[i].StartChapter < plans[j].StartChapter
		}
		return plans[i].ID < plans[j].ID
	})
	if len(plans) > 0 {
		limit := min(16, len(plans))
		lines = append(lines, "Confirmed plan index (narrative order; complete focused plans are supplied separately):")
		for _, p := range plans[:limit] {
			goal := []rune(strings.TrimSpace(p.Goal))
			if len(goal) > 160 {
				goal = goal[:160]
			}
			lines = append(lines, fmt.Sprintf("- [P:%s] (%s %d-%d) %s: %s. Choice: %s", p.ID, p.Level, p.StartChapter, p.EndChapter, p.Title, string(goal), truncateSummary(p.Choice, 160)))
		}
	}

	lines = append(lines, "</story_bible>")
	snapshot := strings.Join(lines, "\n")
	if budget > 0 && charCount(snapshot) > budget/3 {
		runes := []rune(snapshot)
		snapshot = string(runes[:min(len(runes), max(200, budget/3))]) + "\n</story_bible>"
	}
	return snapshot
}

// buildIntentHintDirective translates a one-shot quick intent chip into a turn-scoped directive without locking mode.
func buildIntentHintDirective(project *Project, hint string) string {
	switch strings.TrimSpace(hint) {
	case "writing_trial":
		return localText(project,
			"[Turn Intent: Trial Scene] Write a short original trial of the requested scene now, preserving the supplied voices, facts and knowledge boundaries. Briefly explain the chosen approach. Keep this trial unaccepted and revise only when the author asks.",
			"【本轮意图】直接完成所请求场景的简短原创试写，沿用已有声音、事实和人物知情边界，再简要说明写法取舍。试写保持草案身份。")
	case "writing_recompose":
		return localText(project,
			"[Turn Intent: Recompose Candidate] Reconsider the supplied unaccepted candidate's actions and scene choices before drafting a fresh version. Respect locked author decisions.",
			"【本轮意图】先重新考虑所给未采纳候选中的人物行动与场景安排，再写出新版本，遵守作者锁定的决定。")
	case "brainstorm":
		return localText(project,
			"[Turn Intent: Brainstorm Directions] Propose 2–3 distinct creative directions with clear trade-offs, core conflict, and protagonist agency.",
			"【本轮意图：发散方向】请围绕当前想法提供 2～3 个有明显区分度的故事方向，说明各自的核心冲突、人物动机与长线利弊。")
	case "naming":
		return localText(project,
			"[Turn Intent: Titles & Naming] Propose at least 5 distinctive book titles and 5 character/faction names with meaning and tone fit.",
			"【本轮意图：创意与命名】请提出至少 5 个有辨识度的书名方案与 5 个角色/势力命名建议，说明寓意、题材契合度与避坑原因。")
	case "world_check":
		return localText(project,
			"[Turn Intent: Character & World Check] Examine character motivations, relationship tension, and world rule costs/limits.",
			"【本轮意图：角色与设定梳理】请重点梳理角色欲望、性格矛盾、人物关系张力以及世界规则的代价与边界。")
	case "outline_breakdown":
		return localText(project,
			"[Turn Intent: Volume & Chapter Breakdown] Break down the current arc into concrete volume/chapter goals, obstacles, choices, and consequences.",
			"【本轮意图：细纲拆解】请将当前剧情拆解为分卷/章节细纲，明确每章视角、目标、阻碍、关键选择与后果。")
	case "prose_polish":
		return localText(project,
			"[Turn Intent: Prose Continuation & Polish] Focus on scene immersion, pacing, dialogue voice, and sensory details.",
			"【本轮意图：正文续写与润色】请聚焦当前场景的画面感、人物语气、节奏张力与伏笔衔接。")
	case "commit_extract":
		return localText(project,
			"[Turn Intent: Extract & Stage Settings] Summarize settled decisions from our discussion and call propose or propose_changes so the author can commit them.",
			"【本轮意图：提取设定并入库】请将讨论中已满意的设定整理成结构化提案（调用 propose 或 propose_changes 工具），以便入库保存。")
	}
	return ""
}

// buildWorkspaceContextBlock formats the active UI step and explicit source context.
// Model-aware submissions retain full selected prose; legacy callers keep their established excerpt limits.
func buildWorkspaceContextBlock(project *Project, ctx *WorkspaceContext, chapter *Chapter, fullText ...bool) string {
	includeFullText := len(fullText) > 0 && fullText[0]
	if ctx == nil && chapter == nil {
		return ""
	}
	parts := []string{"<workspace_context>"}
	if ctx != nil {
		if ctx.ActiveStep != "" {
			parts = append(parts, "Active workspace step: "+ctx.ActiveStep)
		}
		skipped := omittedIDs(append(append([]string{}, ctx.OmitEntityIDs...), ctx.OmitPlanIDs...))
		if ctx.ActiveSourceID != "" && !ctx.OmitSource {
			for _, source := range project.Sources {
				if source.ID == ctx.ActiveSourceID {
					parts = append(parts, fmt.Sprintf("Selected source [S:%s]: %s. Read its original text with get_source_detail before making source-based claims.", source.ID, source.Title))
					break
				}
			}
		}
		if ctx.ActiveEntityID != "" && !skipped[ctx.ActiveEntityID] {
			for _, place := range project.Studio.Locations {
				if place.ID == ctx.ActiveEntityID {
					parts = append(parts, "Focused location: "+jsonText(place))
					break
				}
			}
			for _, e := range project.Entities {
				if e.ID == ctx.ActiveEntityID {
					parts = append(parts, fmt.Sprintf("Focused entity [%s]: %s (%s) — %s", e.ID, e.Name, e.Kind, e.Description))
					break
				}
			}
		}
		if ctx.ActivePlanID != "" && !omittedIDs(ctx.OmitPlanIDs)[ctx.ActivePlanID] {
			for _, p := range project.Studio.Plans {
				if p.ID == ctx.ActivePlanID {
					parts = append(parts, fmt.Sprintf("Focused plan [%s] (future planning):\n%s", p.ID, jsonText(p)))
					break
				}
			}
		}
		if strings.TrimSpace(ctx.SelectedText) != "" && !ctx.OmitSelectedText {
			sel := []rune(strings.TrimSpace(ctx.SelectedText))
			if !includeFullText && len(sel) > 2000 {
				sel = sel[:2000]
			}
			parts = append(parts, "Selected text excerpt:\n"+string(sel))
		}
	}
	if chapter != nil && (ctx == nil || !ctx.OmitChapter) {
		runes := []rune(chapter.Body)
		tail := string(runes[max(0, len(runes)-1500):])
		proseLabel := "Recent prose excerpt:"
		if includeFullText {
			tail = chapter.Body
			proseLabel = "Current chapter prose:"
		}
		parts = append(parts, fmt.Sprintf("Active chapter [C:%s]: Chapter %d — %s\nAuthor chapter notes: %s\n%s\n%s", chapter.ID, chapter.Number, chapter.Title, chapterPlanningNotes(project, chapter), proseLabel, tail))
	}
	parts = append(parts, "</workspace_context>")
	if len(parts) == 2 {
		return ""
	}
	return strings.Join(parts, "\n")
}

// compactHistoryBlockwise keeps conversation history strictly append-only until the 80% budget threshold is exceeded,
// then folds the oldest half of turns into a stable compacted summary block.
func compactHistoryBlockwise(history []Message, budget int, priorSummary string, priorCount int) ([]Message, string, int) {
	threshold := budget * 4 / 5
	if threshold < 300 {
		threshold = budget
	}
	if priorCount > 0 && priorCount <= len(history) && priorSummary != "" {
		remaining := history[priorCount:]
		candidate := append([]Message{{Role: "user", Content: priorSummary}}, remaining...)
		if len(candidate) <= maxHistoryMessages && messageChars(candidate) <= threshold {
			return candidate, priorSummary, priorCount
		}
	}
	if len(history) <= maxHistoryMessages && messageChars(history) <= threshold {
		return history, "", 0
	}
	foldCount := max(1, len(history)/2)
	if len(history)-foldCount >= maxHistoryMessages {
		foldCount = len(history) - (maxHistoryMessages - 1)
	}
	snippets := []string{}
	summaryBudget := max(120, budget/3)
	usedSummary := 0
	for i := 0; i < foldCount && usedSummary < summaryBudget; i++ {
		text := []rune(strings.TrimSpace(history[i].Content))
		size := min(len(text), min(180, summaryBudget-usedSummary))
		if size <= 0 {
			break
		}
		snippets = append(snippets, fmt.Sprintf("%s: %s", history[i].Role, string(text[:size])))
		usedSummary += size + 12
	}
	summary := "Earlier discussion excerpts (incomplete; accepted decisions are recorded separately):\n" + strings.Join(snippets, "\n")
	tail := append([]Message{}, history[foldCount:]...)
	for len(tail) > 1 && (len(tail) >= maxHistoryMessages || charCount(summary)+messageChars(tail) > budget) {
		tail = tail[1:]
		foldCount++
	}
	if charCount(summary)+messageChars(tail) > budget {
		avail := max(0, budget-messageChars(tail))
		runes := []rune(summary)
		if len(runes) > avail {
			summary = string(runes[:avail])
		}
	}
	result := append([]Message{{Role: "user", Content: summary}}, tail...)
	return result, summary, foldCount
}
