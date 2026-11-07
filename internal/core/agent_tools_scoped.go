package core

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
)

type agentStepEnvelope struct {
	Mode        string            `json:"mode"`
	Tool        string            `json:"tool"`
	Query       string            `json:"query"`
	Text        string            `json:"text"`
	EntityID    string            `json:"entity_id"`
	EntityName  string            `json:"entity_name"`
	ChapterID   string            `json:"chapter_id"`
	Chapter     uint32            `json:"chapter"`
	Offset      int               `json:"offset_chars"`
	Limit       int               `json:"limit_chars"`
	Through     uint32            `json:"through_chapter"`
	PlanID      string            `json:"plan_id"`
	SourceID    string            `json:"source_id"`
	TurnID      string            `json:"turn_id"`
	JobID       string            `json:"job_id"`
	StepID      string            `json:"step_id"`
	ItemOffset  int               `json:"offset"`
	RouteOffset int               `json:"route_offset"`
	Level       string            `json:"level"`
	Proposal    *StudioProposal   `json:"proposal"`
	Proposals   []StudioProposal  `json:"proposals"`
	ChangeSet   *ChangeSet        `json:"changeset"`
	Operations  []ChangeOperation `json:"operations"`
}

// extractBalancedJSON finds the first balanced top-level JSON object in text.
func extractBalancedJSON(text string) (string, int, int, bool) {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return "", -1, -1, false
	}
	depth := 0
	inString := false
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if inString {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == '"' {
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start : i+1], start, i + 1, true
			}
		}
	}
	return "", -1, -1, false
}

// stripModelToolJSON removes raw tool/proposal JSON envelopes from assistant prose so JSON never leaks into chat turns.
func stripModelToolJSON(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return ""
	}
	cleaned := cleanModelJSON(trimmed)
	var env agentStepEnvelope
	if decodeJSON([]byte(cleaned), &env) == nil && (env.Tool != "" || env.Proposal != nil || len(env.Proposals) > 0 || env.ChangeSet != nil || len(env.Operations) > 0) {
		if strings.TrimSpace(env.Text) != "" {
			return strings.TrimSpace(env.Text)
		}
		if env.Proposal != nil && strings.TrimSpace(env.Proposal.Content) != "" {
			if strings.TrimSpace(env.Proposal.Title) != "" {
				return "### " + strings.TrimSpace(env.Proposal.Title) + "\n\n" + strings.TrimSpace(env.Proposal.Content)
			}
			return strings.TrimSpace(env.Proposal.Content)
		}
		if len(env.Proposals) > 0 && strings.TrimSpace(env.Proposals[0].Content) != "" {
			parts := []string{}
			for _, p := range env.Proposals {
				if strings.TrimSpace(p.Title) != "" && strings.TrimSpace(p.Content) != "" {
					parts = append(parts, "### "+strings.TrimSpace(p.Title)+"\n\n"+strings.TrimSpace(p.Content))
				} else if strings.TrimSpace(p.Content) != "" {
					parts = append(parts, strings.TrimSpace(p.Content))
				}
			}
			if len(parts) > 0 {
				return strings.Join(parts, "\n\n")
			}
		}
		if env.ChangeSet != nil && strings.TrimSpace(env.ChangeSet.Summary) != "" {
			return strings.TrimSpace(env.ChangeSet.Summary)
		}
		return ""
	}
	if obj, start, end, ok := extractBalancedJSON(trimmed); ok {
		if decodeJSON([]byte(obj), &env) == nil && (env.Tool != "" || env.Proposal != nil || len(env.Proposals) > 0 || env.ChangeSet != nil || len(env.Operations) > 0) {
			before := strings.TrimSpace(trimmed[:start])
			after := strings.TrimSpace(trimmed[end:])
			after = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(after, "```"), "json"))
			before = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(before, "json"), "```"))
			combined := strings.TrimSpace(before + "\n\n" + after)
			if combined != "" {
				return combined
			}
			if strings.TrimSpace(env.Text) != "" {
				return strings.TrimSpace(env.Text)
			}
			if env.Proposal != nil && strings.TrimSpace(env.Proposal.Content) != "" {
				if strings.TrimSpace(env.Proposal.Title) != "" {
					return "### " + strings.TrimSpace(env.Proposal.Title) + "\n\n" + strings.TrimSpace(env.Proposal.Content)
				}
				return strings.TrimSpace(env.Proposal.Content)
			}
			return ""
		}
	}
	return text
}

// executeScopedAgentTool reads bounded story records and attributable sources within the current work.
func (engine *Engine) executeScopedAgentTool(project *Project, job Job, step agentStepEnvelope) (string, error) {
	switch step.Tool {
	case "get_conversation_excerpt", "get_task_step":
		return engine.readConversationSource(job, step)
	case "get_world_map":
		return readWorldMap(project, step)
	case "get_decision_detail":
		for _, decision := range project.Studio.Decisions {
			if decision.ID == step.SourceID || step.SourceID == "" && step.Query != "" && strings.Contains(lowerText(decision.Title+" "+decision.Content), lowerText(step.Query)) {
				return jsonText(decision), nil
			}
		}
		return "", errors.New("Select an existing author decision from this project")
	case "get_source_detail":
		id := strings.TrimSpace(step.SourceID)
		if id == "" && job.WorkspaceContext != nil {
			id = job.WorkspaceContext.ActiveSourceID
		}
		for _, source := range project.Sources {
			if source.ID != id {
				continue
			}
			content := []rune(source.Content)
			offset := max(0, min(step.Offset, len(content)))
			limit := step.Limit
			if limit < 1 || limit > 4000 {
				limit = 3000
			}
			end := min(len(content), offset+limit)
			return jsonText(map[string]any{"id": source.ID, "title": source.Title, "url": source.URL, "excerpt": string(content[offset:end]), "offset_chars": offset, "next_offset_chars": end, "total_chars": len(content)}), nil
		}
		return "", errors.New("Select an existing source from this project")
	case "get_entity_detail":
		query := strings.TrimSpace(step.EntityName)
		if query == "" {
			query = strings.TrimSpace(step.EntityID)
		}
		if query == "" {
			query = strings.TrimSpace(step.Query)
		}
		if query == "" {
			return "", errors.New("Provide entity_name, entity_id, or query")
		}
		var matched *Entity
		for i := range project.Entities {
			e := &project.Entities[i]
			if e.ID == query || strings.EqualFold(e.Name, query) || containsFold(e.Aliases, query) {
				matched = e
				break
			}
		}
		if matched == nil {
			for i := range project.Entities {
				e := &project.Entities[i]
				if strings.Contains(lowerText(e.Name+" "+strings.Join(e.Aliases, " ")), lowerText(query)) {
					matched = e
					break
				}
			}
		}
		entityName := query
		if matched != nil {
			entityName = matched.Name
		}
		memories := []StoryMemory{}
		through := uint32(math.MaxUint32)
		if chapter := findChapter(project, job.ChapterID); chapter != nil {
			through = chapter.Number
		}
		if step.Through > 0 {
			through = min(through, step.Through)
		}
		successors := memorySuccessors(project.Studio.Memories)
		for _, m := range project.Studio.Memories {
			if memoryVisibleAt(m, through, successors) && memoryCurrent(project, m) && (strings.EqualFold(m.Entity, entityName) || strings.Contains(lowerText(m.Title+" "+m.Content), lowerText(entityName))) {
				memories = append(memories, m)
			}
		}
		facts := []Fact{}
		for _, f := range project.Facts {
			_, chapter, _ := project.SourceText(f.SourceID)
			if f.Status == "confirmed" && chapter <= through && factCurrent(project, &f) && (strings.EqualFold(f.Entity, entityName) || strings.EqualFold(f.RelatedEntity, entityName)) {
				facts = append(facts, f)
			}
		}
		return jsonText(map[string]any{
			"entity":   matched,
			"memories": memories,
			"facts":    facts,
		}), nil

	case "get_chapter_window":
		var chapter *Chapter
		targetID := strings.TrimSpace(step.ChapterID)
		if targetID == "" && step.Chapter == 0 {
			targetID = strings.TrimSpace(step.Query)
		}
		if targetID != "" {
			chapter = findChapter(project, targetID)
		}
		if chapter == nil && step.Chapter > 0 {
			for i := range project.Chapters {
				if project.Chapters[i].Number == step.Chapter {
					chapter = &project.Chapters[i]
					break
				}
			}
		}
		if chapter == nil && job.ChapterID != "" {
			chapter = findChapter(project, job.ChapterID)
		}
		if chapter == nil && len(project.Chapters) > 0 {
			chapter = &project.Chapters[len(project.Chapters)-1]
		}
		if chapter == nil {
			return "", errors.New("Chapter not found")
		}
		runes := []rune(chapter.Body)
		offset := max(0, min(step.Offset, len(runes)))
		limit := step.Limit
		if limit <= 0 || limit > 4000 {
			limit = 3000
		}
		end := min(len(runes), offset+limit)
		return jsonText(map[string]any{
			"id":           chapter.ID,
			"number":       chapter.Number,
			"title":        chapter.Title,
			"synopsis":     chapter.Synopsis,
			"total_chars":  len(runes),
			"offset_chars": offset,
			"excerpt":      string(runes[offset:end]),
		}), nil

	case "search_story_facts":
		query := strings.TrimSpace(step.Query)
		if query == "" {
			return "", errors.New("Provide a non-empty search query")
		}
		through := step.Through
		if through == 0 {
			through = uint32(math.MaxUint32)
			if ch := findChapter(project, job.ChapterID); ch != nil {
				through = ch.Number
			}
		}
		passages, err := engine.store.SearchPassages(project.ID, query, through, job.SessionID)
		if err != nil {
			return "", err
		}
		if len(passages) > 8 {
			passages = passages[:8]
		}
		memories := memoryContext(project, through, query)
		if len(memories) > 8 {
			memories = memories[:8]
		}
		return jsonText(map[string]any{
			"passages": passages,
			"memories": memories,
		}), nil

	case "get_plan_node":
		target := strings.TrimSpace(step.PlanID)
		if target == "" {
			target = strings.TrimSpace(step.Query)
		}
		matched := []StoryPlan{}
		children := []StoryPlan{}
		for _, p := range project.Studio.Plans {
			if p.Status == "rejected" {
				continue
			}
			if (target != "" && (p.ID == target || strings.Contains(lowerText(p.Title), lowerText(target)))) || (step.Level != "" && p.Level == step.Level) {
				matched = append(matched, p)
			}
		}
		if len(matched) == 0 {
			for _, p := range project.Studio.Plans {
				if p.Status != "rejected" {
					matched = append(matched, p)
				}
			}
		} else {
			parentIDs := map[string]bool{}
			for _, m := range matched {
				parentIDs[m.ID] = true
			}
			for _, p := range project.Studio.Plans {
				if parentIDs[p.ParentID] {
					children = append(children, p)
				}
			}
		}
		linkedLocations := []WorldLocation{}
		for _, place := range project.Studio.Locations {
			for _, plan := range matched {
				if contains(place.PlanIDs, plan.ID) {
					linkedLocations = append(linkedLocations, place)
					break
				}
			}
		}
		return jsonText(map[string]any{
			"plans":     matched,
			"children":  children,
			"locations": linkedLocations,
		}), nil
	}
	return "", errors.New("Unknown scoped tool")
}

// containsFold matches author aliases without making capitalization a separate entity identity.
func containsFold(items []string, target string) bool {
	for _, item := range items {
		if strings.EqualFold(item, target) {
			return true
		}
	}
	return false
}

// stageAgentChangeSet records a model-proposed ChangeSet or operations list on the active job.
func (engine *Engine) stageAgentChangeSet(jobID string, project *Project, cs *ChangeSet, ops []ChangeOperation) int {
	if project == nil {
		return 0
	}
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	job := engine.jobs[jobID]
	if job == nil {
		return 0
	}
	var next ChangeSet
	if cs != nil {
		next = *cs
	} else if len(ops) > 0 {
		next = ChangeSet{
			Summary:    ops[0].Title,
			Operations: ops,
		}
	} else {
		return 0
	}
	next.ID = newID()
	next.ProjectID = project.ID
	next.JobID = job.ID
	next.ProposalLinksVersion = 1
	next.SessionID = job.SessionID
	next.Source = "agent"
	next.Status = ChangeSetStaged
	next.BaseRevision = job.BaseRevision
	next.ReviewToken = ""
	next.PreviewRevision = 0
	next.AppliedRevision = 0
	next.Skipped = nil
	next.CommitPolicy = "review"
	next.CreatedAt = now()
	next.UpdatedAt = next.CreatedAt
	if err := validateChangeSet(&next); err != nil {
		return 0
	}
	for i := range next.Operations {
		op := &next.Operations[i]
		op.ProposalID = ""
		if op.OpType != OpUpsert {
			continue
		}
		if op.TargetKind == TargetPlan {
			var plan StoryPlan
			if json.Unmarshal(op.AfterValue, &plan) == nil && plan.Title != "" && (plan.Level != "chapter" || plan.StartChapter > 0) {
				op.ProposalID = newID()
				job.Proposals = append(job.Proposals, StudioProposal{
					ID:      op.ProposalID,
					Kind:    "plan",
					Title:   op.Title,
					Content: op.Summary,
					Status:  "proposed",
					Plan:    &plan,
				})
			}
		} else if op.TargetKind == TargetDecision {
			var dec StoryDecision
			if json.Unmarshal(op.AfterValue, &dec) == nil && dec.Title != "" {
				op.ProposalID = newID()
				job.Proposals = append(job.Proposals, StudioProposal{
					ID:      op.ProposalID,
					Kind:    "decision",
					Title:   dec.Title,
					Content: dec.Content,
					Status:  "proposed",
				})
			}
		}
	}
	job.ChangeSets = append(job.ChangeSets, next)
	return len(next.Operations)
}
