package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
)

type WritingSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Version     string   `json:"version"`
	Modes       []string `json:"modes"`
}

// writingSkills supplies optional craft approaches rather than mandatory story formulas.
func writingSkills() []WritingSkill {
	return []WritingSkill{
		{"continuity", "Continuity", "Read current plans and relevant prose. Separate saved events, author plans, viewpoint knowledge and tentative interpretations. Trace choices to evidence and consequences.", "builtin://writing-craft", writingCraftVersion, []string{"all"}},
		{"snowflake", "Snowflake", "Expand an idea gradually; revisit character motivation and scene purpose.", "https://www.advancedfictionwriting.com/articles/snowflake-method/", "1", []string{"traditional", "hybrid"}},
		{"serial", "Serial rhythm", "Tie local wants to actions, meaningful payoffs and continuing effects. Vary payoffs across chapters. Quiet work can fulfill promises and change relationships.", "builtin://writing-craft", writingCraftVersion, []string{"serial", "hybrid"}},
		{"character_arc", "Character arc", "Give participants their own wants, knowledge and responses. Show changes in judgment, responsibility and relationships through choices; allow refusal and negotiation.", "builtin://writing-craft", writingCraftVersion, []string{"all"}},
		{"world_rules", "World through action", "Establish ability limits, preparation, travel and resources before decisive use. Retain costs and consequences. Let earlier actions reshape later circumstances.", "builtin://writing-craft", writingCraftVersion, []string{"all"}},
		{"outline_control", "Detailed outlines", "Keep distant milestones flexible. Detail near-term starting conditions, character choices, observable actions, changed circumstances and scene handoffs.", "builtin://writing-craft", writingCraftVersion, []string{"all"}},
		{"originality", "Originality", "Offer different causal premises; explain trade-offs; avoid banned names and stock title patterns.", "https://doi.org/10.1126/sciadv.adn5290", "1", []string{"all"}},
	}
}

// modelTask retains the existing provider routing contract for new studio activities.
func modelTask(action string) string {
	switch action {
	case "agent", "research":
		return "ask"
	case "naming":
		return "architecture"
	case "memory_update":
		return "wiki_compile"
	}
	return action
}

// studioTask describes additional model tasks through the same six supported transports.
func studioTask(project *Project, action string) (string, bool) {
	switch action {
	case "memory_update":
		return localText(project, `Extract source-grounded updates from the current chapter for author review. Return JSON {"text":"summary","proposals":[{"kind":"memory","title":"...","content":"...","memory":{"kind":"fact|inference|character|relationship|knowledge|clue|promise","entity":"...","title":"...","content":"...","state":"active|alive|dead|planted|reinforced|resolved|fulfilled","chapter":1,"story_time":"...","known_by":[],"due_chapter":0,"supersedes":"existing memory ID when updating the same attribute, otherwise empty","source_id":"current source ID","quote":"contiguous verbatim passage"}}]}. Distinguish interpretation from explicit evidence. Do not infer public knowledge from an absent known_by list.`, `从当前章节中整理有原文依据的人物状态、关系、知情范围、事件、伏笔与承诺变化，供作者审阅。只返回JSON {"text":"中文摘要","proposals":[{"kind":"memory","title":"...","content":"...","memory":{"kind":"fact|inference|character|relationship|knowledge|clue|promise","entity":"...","title":"...","content":"...","state":"active|alive|dead|planted|reinforced|resolved|fulfilled","chapter":1,"story_time":"...","known_by":[],"due_chapter":0,"supersedes":"更新同一属性时填已有记忆ID，否则留空","source_id":"当前原文ID","quote":"连续逐字引文"}}]}。推断与明确事实分开，未提供知情范围不等于所有人知情。`), true
	case "agent":
		return "Work with the author, use the supplied tool protocol, and finish with a useful creative response.", true
	case "naming":
		return localText(project, "Propose at least five distinctive book titles and five protagonist names. Explain meaning, cultural fit and pitfalls. Respect forbidden names. Treat originality as a proposal; do not claim worldwide uniqueness.", "至少提出五个有区分度的书名及五个主角名，说明寓意、时代文化适配和可能问题。遵守禁用词与命名偏好，不声称全网唯一。"), true
	case "research":
		return localText(project, "Explain the supplied research with citations, source disagreements and unresolved gaps. Do not claim to have searched unless results are supplied.", "结合提供的资料分析问题，逐条给出来源、分歧与缺口；没有搜索结果时不声称已经查证。"), true
	}
	return "", false
}

// studioGuidance carries shared author decisions into every stage without promoting plans to facts.
func studioGuidance(project *Project, action string, chapter *Chapter, workspaces ...*WorkspaceContext) string {
	if action == "agent" {
		return agentCraftGuidance()
	}
	s := project.Studio
	if s.Language == "" {
		s.Language = "inherit"
	}
	if s.Mode == "" {
		s.Mode = "undecided"
	}
	lines := []string{fmt.Sprintf("Story form: %s. Stage: %s. Audience: %s. Pace: %s. Narrative emphasis: %s. Targets: %d characters, %d chapters, %d volumes.", s.Mode, s.Stage, s.Audience, s.Pacing, narrativeEmphasis(project), s.TargetCharacters, s.TargetChapters, s.TargetVolumes)}
	if s.Mode == "traditional" {
		lines = append(lines, "Prioritize character arcs, theme, scene consequences and the author's preferred structure. Do not impose web-serial jargon, power systems or frequent cliffhangers.")
	}
	if s.Mode == "serial" {
		lines = append(lines, "Discuss serial reader expectations, sustainable conflicts, chapter payoffs and pacing; do not assume every serial must be fast or use a special ability.")
	}
	for _, d := range s.Decisions[max(0, len(s.Decisions)-12):] {
		if d.Status == "accepted" {
			content := []rune(d.Content)
			if len(content) > 240 {
				content = content[:240]
			}
			lines = append(lines, "Author decision excerpt: "+d.Title+" — "+string(content))
		} else if d.Status == "rejected" {
			content := []rune(d.Content)
			if len(content) > 240 {
				content = content[:240]
			}
			lines = append(lines, "Rejected alternative; do not silently revive it: "+d.Title+" — "+string(content))
		}
	}
	planCount := 0
	var workspace *WorkspaceContext
	if len(workspaces) > 0 {
		workspace = workspaces[0]
	}
	plans := planningPath(project, chapter, workspace)
	if chapter == nil && len(plans) == 0 {
		plans = s.Plans
	}
	for _, p := range plans {
		if workspace != nil && omittedIDs(workspace.OmitPlanIDs)[p.ID] {
			continue
		}
		if p.Status == "confirmed" {
			if chapter != nil && p.Level != "book" && !(p.StartChapter <= chapter.Number+5 && (p.EndChapter == 0 || p.EndChapter+5 >= chapter.Number)) {
				continue
			}
			if planCount >= 12 {
				break
			}
			planCount++
			goal, conflict, choice, consequence := p.Goal, p.Conflict, p.Choice, p.Consequence
			if charCount(goal) > 240 {
				goal = string([]rune(goal)[:240])
			}
			if charCount(conflict) > 160 {
				conflict = string([]rune(conflict)[:160])
			}
			if charCount(choice) > 240 {
				choice = string([]rune(choice)[:240])
			}
			if charCount(consequence) > 160 {
				consequence = string([]rune(consequence)[:160])
			}
			lines = append(lines, fmt.Sprintf("Plan index (future; complete selected nodes follow) [%s]: %s. Chapters %d–%d. Goal: %s. Conflict: %s. Choice: %s. Consequence: %s.", p.ID, p.Title, p.StartChapter, p.EndChapter, goal, conflict, choice, consequence))
		}
	}
	activeSkills := map[string]bool{}
	for _, id := range s.Skills {
		activeSkills[id] = true
	}
	for _, skill := range resolveBuiltinSkills(s.Mode, s.Stage) {
		activeSkills[skill.ID] = true
	}
	for _, skill := range writingSkills() {
		if activeSkills[skill.ID] && (contains(skill.Modes, "all") || (action == "agent" && (s.Mode == "undecided" || s.Mode == "hybrid")) || contains(skill.Modes, s.Mode)) {
			lines = append(lines, "Selected craft approach: "+skill.Name)
		}
	}
	lines = append(lines, "Naming preference: "+s.NamingStyle+". Forbidden names or title patterns: "+strings.Join(s.ForbiddenNames, ", ")+". Author preferences: "+strings.Join(distinctAuthorPreferences(project), "; "))
	if action == "outline" {
		lines = append(lines, "Narrative emphasis describes the recurring reading experience. Plan flexibility=fixed locks the author's choice; flexible permits proposing changes. An unspecified flexibility retains the confirmed plan's meaning. Keep open questions unresolved and review lasting consequences, character choices and repeated payoffs at unit boundaries.")
		lines = append(lines, "Respect requested chapter counts and parent budgets. Detail the next 5–10 chapters first when writing an ongoing long serial. You may return {\"text\":\"explanation\",\"proposals\":[{\"kind\":\"plan\",\"title\":\"...\",\"content\":\"...\",\"plan\":{\"level\":\"volume|unit|chapter|scene\",\"parent_id\":\"existing ID or empty\",\"title\":\"...\",\"start_chapter\":1,\"end_chapter\":5,\"target_characters\":15000,\"goal\":\"...\",\"conflict\":\"...\"}}]}. These remain proposals until accepted.")
	}
	return strings.Join(lines, "\n")
}

// contextPassages combines indexed prose with reviewed evidence and bounded current memory.
func (engine *Engine) contextPassages(project *Project, request AIRequest) ([]ContextItem, error) {
	through := uint32(math.MaxUint32)
	query := request.Instruction
	chapterID := request.ChapterID
	if chapterID == "" && request.WorkspaceContext != nil {
		chapterID = request.WorkspaceContext.ActiveChapterID
	}
	if chapter := findChapter(project, chapterID); chapter != nil {
		through = chapter.Number
		if request.Action == "continue" && through > 0 {
			through--
		}
		query = creationQuery(project, chapter, request.Instruction)
	}
	items, err := engine.store.SearchPassages(project.ID, query, through, request.SessionID)
	if err != nil {
		return nil, err
	}
	for _, memory := range latestCompilations(project) {
		_, number, ok := project.SourceText(memory.SourceID)
		if !ok || number > through || !compilationApproved(project, memory) {
			continue
		}
		for i, section := range memory.Sections {
			items = append(items, ContextItem{ID: fmt.Sprintf("M:%s:%d", memory.ID, i+1), Title: "Story memory", Kind: "memory", Text: section.Summary + "\nEvidence: " + strings.Join(section.EvidenceIDs, ", "), Score: 5, SourceID: memory.SourceID, SourceHash: memory.SourceHash})
		}
	}
	return mergeKnowledgeContext(project, query, through, items, 24, request), nil
}

// loadStoredJob makes durable interrupted and completed tasks reviewable after a restart.
func (engine *Engine) loadStoredJob(id string) (Job, error) {
	var raw string
	var job Job
	if err := engine.store.db.QueryRow("SELECT document FROM runs WHERE id=?", id).Scan(&raw); err != nil {
		return job, errors.New("Task not found")
	}
	job, err := decodeStoredJob(raw)
	if err != nil {
		return job, err
	}
	return job, nil
}

// loadJob returns an idempotent response only within the same project ownership boundary.
func (engine *Engine) loadJob(id, projectID string) (Job, error) {
	engine.jobsMu.Lock()
	job := engine.jobs[id]
	if job != nil && (job.Status == "running" || job.pendingFinal != nil) {
		value := job.snapshot()
		engine.jobsMu.Unlock()
		if value.ProjectID != projectID {
			return Job{}, errors.New("Task belongs to another project")
		}
		return value, nil
	}
	engine.jobsMu.Unlock()
	value, err := engine.loadStoredJob(id)
	if value.ProjectID != projectID {
		return Job{}, errors.New("Task belongs to another project")
	}
	return value, err
}

// AllConversations exports all chapter scopes, keeping backups independent from live task state.
func (store *Store) AllConversations(projectID string) ([]Conversation, error) {
	rows, err := store.db.Query("SELECT id FROM conversations WHERE project_id=? ORDER BY rowid", projectID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	result := []Conversation{}
	for _, id := range ids {
		session, err := readConversation(store.db, projectID, id)
		if err != nil {
			return nil, err
		}
		result = append(result, session)
	}
	return result, nil
}

// studioFindings reports review hints with evidence rather than asserting narrative interpretations.
func studioFindings(project *Project, chapterID string) []LintFinding {
	findings := []LintFinding{}
	chapter := findChapter(project, chapterID)
	through := uint32(0)
	for _, c := range project.Chapters {
		through = max(through, c.Number)
	}
	if chapter != nil {
		through = chapter.Number
	}
	plantings := map[string]uint32{}
	for _, prior := range project.Studio.Memories {
		if prior.Kind != "clue" || !contains([]string{"confirmed", "superseded"}, prior.Status) || !contains([]string{"planted", "reinforced"}, prior.State) || !memoryCurrent(project, prior) {
			continue
		}
		plantings["id:"+prior.ID] = prior.Chapter
		if old, ok := plantings["title:"+prior.Title]; !ok || prior.Chapter < old {
			plantings["title:"+prior.Title] = prior.Chapter
		}
	}
	for _, m := range project.Studio.Memories {
		if m.Status != "confirmed" {
			continue
		}
		if !memoryCurrent(project, m) {
			findings = append(findings, LintFinding{Kind: "stale", Severity: "warning", Title: m.Title, Detail: localText(project, "Source changed; review this memory before reuse.", "来源已修改，请审阅这条记忆后再使用。"), Entity: m.Entity, SourceID: m.SourceID})
			continue
		}
		if (m.Kind == "clue" || m.Kind == "promise") && m.DueChapter > 0 && m.DueChapter < through && !contains([]string{"resolved", "fulfilled", "abandoned"}, m.State) {
			findings = append(findings, LintFinding{Kind: "overdue", Severity: "warning", Title: m.Title, Detail: fmt.Sprintf(localText(project, "Planned payoff at chapter %d has not been confirmed. Current chapter: %d.", "计划在第 %d 章回收，当前写到第 %d 章，尚未确认兑现。"), m.DueChapter, through), Entity: m.Entity, SourceID: m.SourceID})
		}
		if chapter != nil && m.Kind == "character" && m.State == "dead" && m.Chapter < chapter.Number && m.Entity != "" && strings.Contains(chapter.Body, m.Entity) {
			findings = append(findings, LintFinding{Kind: "character_state", Severity: "review", Title: m.Entity, Detail: localText(project, "A character recorded as dead is mentioned. Check whether this is a flashback, reference, or continuity issue. Evidence: ", "本章提到了已记为死亡的人物，请检查是否为回忆、转述或连续性问题。原文依据：") + m.Quote, Entity: m.Entity, SourceID: m.SourceID})
		}
		if m.Kind == "clue" && m.State == "resolved" && m.Chapter <= through {
			byID, hasID := plantings["id:"+m.Supersedes]
			byTitle, hasTitle := plantings["title:"+m.Title]
			planted := (hasID && byID <= m.Chapter) || (m.Title != "" && hasTitle && byTitle <= m.Chapter)
			if !planted {
				findings = append(findings, LintFinding{Kind: "clue_setup", Severity: "review", Title: m.Title, Detail: localText(project, "This resolved clue has no earlier confirmed setup record. Check the source before treating the payoff as unearned.", "这条已回收伏笔缺少先前确认的埋设记录，请核对原文是否已有铺垫。"), Entity: m.Entity, SourceID: m.SourceID})
			}
		}
	}
	if chapter != nil {
		for _, phrase := range []string{"命运的齿轮", "不禁陷入了沉思", "little did they know", "a testament to"} {
			if strings.Contains(strings.ToLower(chapter.Body), phrase) {
				findings = append(findings, LintFinding{Kind: "style", Severity: "suggestion", Title: phrase, Detail: localText(project, "Consider whether this familiar phrase suits the author's voice; this is not an AI detector.", "请判断这处常见表达是否符合作者文风；此提示不能判断文本是否由 AI 创作。"), SourceID: chapter.ID})
			}
		}
	}
	return findings
}

// researchSnapshot copies external settings before releasing the engine lock for network work.
func (engine *Engine) researchSnapshot() (IntegrationSettings, context.Context, error) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return IntegrationSettings{}, engine.ctx, errors.New("The application is closing")
	}
	if err := engine.refreshIntegrationsLocked(); err != nil {
		return IntegrationSettings{}, engine.ctx, err
	}
	return engine.integrations, engine.ctx, nil
}

// sortChapters preserves narrative numbering after materializing selected chapter plans.
func sortChapters(project *Project) {
	sort.SliceStable(project.Chapters, func(i, j int) bool { return project.Chapters[i].Number < project.Chapters[j].Number })
}
