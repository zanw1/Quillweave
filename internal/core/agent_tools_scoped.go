package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type agentStepEnvelope struct {
	Mode         string            `json:"mode"`
	Tool         string            `json:"tool"`
	SkillID      string            `json:"skill_id"`
	ResourcePath string            `json:"resource_path"`
	Query        string            `json:"query"`
	Text         string            `json:"text"`
	EntityID     string            `json:"entity_id"`
	EntityName   string            `json:"entity_name"`
	ChapterID    string            `json:"chapter_id"`
	Chapter      uint32            `json:"chapter"`
	Chapters     []uint32          `json:"chapters"`
	ChapterIDs   []string          `json:"chapter_ids"`
	Count        int               `json:"count"`
	Offset       int               `json:"offset_chars"`
	Limit        int               `json:"limit_chars"`
	Through      uint32            `json:"through_chapter"`
	PlanID       string            `json:"plan_id"`
	PlanIDs      []string          `json:"plan_ids"`
	SourceID     string            `json:"source_id"`
	TurnID       string            `json:"turn_id"`
	JobID        string            `json:"job_id"`
	StepID       string            `json:"step_id"`
	EvidenceIDs  []string          `json:"evidence_ids,omitempty"`
	ItemOffset   int               `json:"offset"`
	RouteOffset  int               `json:"route_offset"`
	Level        string            `json:"level"`
	Proposal     *StudioProposal   `json:"proposal"`
	Proposals    []StudioProposal  `json:"proposals"`
	ChangeSet    *ChangeSet        `json:"changeset"`
	Operations   []ChangeOperation `json:"operations"`
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

// stripModelToolJSON keeps the complete author prose while stripping protocol envelopes.
func stripModelToolJSON(text string) string {
	return visibleAgentOutput(text)
}

// recoverAgentStep reads a tool step even when the model breaks JSON with raw newlines or unescaped quotes.
func recoverAgentStep(text string) (agentStepEnvelope, bool) {
	cleaned := cleanModelJSON(strings.TrimSpace(text))
	if env, ok := decodeAgentStep(cleaned); ok {
		return env, true
	}
	if obj, _, _, found := extractBalancedJSON(cleaned); found {
		for _, repaired := range repairedJSONVariants(obj) {
			if env, ok := decodeAgentStep(repaired); ok {
				return env, true
			}
		}
	}
	for _, repaired := range repairedJSONVariants(cleaned) {
		if env, ok := decodeAgentStep(repaired); ok {
			return env, true
		}
	}
	return scrapeAgentStep(cleaned)
}

// repairedJSONVariants escalates from the raw text to newline control repair and then unescaped-quote repair.
func repairedJSONVariants(text string) []string {
	controls := repairJSONStringControls(text)
	return []string{text, controls, repairJSONQuotes(controls)}
}

// decodeAgentStep accepts one complete tool or proposal envelope.
func decodeAgentStep(text string) (agentStepEnvelope, bool) {
	var env agentStepEnvelope
	if strings.TrimSpace(text) == "" || decodeJSON([]byte(text), &env) != nil {
		return env, false
	}
	if env.Tool == "" && env.Proposal == nil && len(env.Proposals) == 0 && env.ChangeSet == nil && len(env.Operations) == 0 {
		return env, false
	}
	return env, true
}

// repairJSONStringControls escapes raw line breaks inside JSON strings without rewriting unescaped quotes.
func repairJSONStringControls(text string) string {
	var b strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if !inString {
			if ch == '"' {
				inString = true
			}
			b.WriteByte(ch)
			continue
		}
		if escaped {
			b.WriteByte(ch)
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			b.WriteByte(ch)
			escaped = true
		case '"':
			inString = false
			b.WriteByte(ch)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// repairJSONQuotes escapes unescaped quotation marks inside string values: a quote only
// closes a string when the next non-space character is a JSON structural delimiter.
func repairJSONQuotes(text string) string {
	var b strings.Builder
	inString := false
	escaped := false
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if !inString {
			if ch == '"' {
				inString = true
			}
			b.WriteByte(ch)
			continue
		}
		if escaped {
			b.WriteByte(ch)
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			b.WriteByte(ch)
			escaped = true
		case '"':
			next := i + 1
			for next < len(text) && (text[next] == ' ' || text[next] == '\t' || text[next] == '\n' || text[next] == '\r') {
				next++
			}
			if next >= len(text) || strings.IndexByte(":,}]", text[next]) >= 0 {
				inString = false
				b.WriteByte('"')
			} else {
				b.WriteString(`\"`)
			}
		default:
			b.WriteByte(ch)
		}
	}
	return b.String()
}

// scrapeAgentStep recovers the author-facing fields when quotes inside the prose make the envelope invalid JSON.
func scrapeAgentStep(text string) (agentStepEnvelope, bool) {
	tool, ok := scrapeJSONString(text, "tool")
	if !ok || !knownAgentTool(tool) {
		return agentStepEnvelope{}, false
	}
	env := agentStepEnvelope{
		Tool:         tool,
		SkillID:      scrapeField(text, "skill_id"),
		ResourcePath: scrapeField(text, "resource_path"),
		Text:         scrapeField(text, "text"),
		Query:        scrapeField(text, "query"),
		EntityID:     scrapeField(text, "entity_id"),
		EntityName:   scrapeField(text, "entity_name"),
		ChapterID:    scrapeField(text, "chapter_id"),
		PlanID:       scrapeField(text, "plan_id"),
		SourceID:     scrapeField(text, "source_id"),
		TurnID:       scrapeField(text, "turn_id"),
		JobID:        scrapeField(text, "job_id"),
		StepID:       scrapeField(text, "step_id"),
		Mode:         scrapeField(text, "mode"),
		Level:        scrapeField(text, "level"),
	}
	env.Chapter = uint32(max(0, scrapeJSONNumber(text, "chapter")))
	env.Offset = int(scrapeJSONNumber(text, "offset_chars"))
	env.Limit = int(scrapeJSONNumber(text, "limit_chars"))
	env.Through = uint32(max(0, scrapeJSONNumber(text, "through_chapter")))
	env.ItemOffset = int(scrapeJSONNumber(text, "offset"))
	env.RouteOffset = int(scrapeJSONNumber(text, "route_offset"))
	if tool == "propose_changes" {
		scrapeChangeSetPayload(text, &env)
	}
	if tool == "propose" {
		kind := scrapeField(text, "kind")
		title := scrapeField(text, "title")
		content := scrapeField(text, "content")
		if kind == "" {
			kind = "decision"
		}
		if title != "" || content != "" {
			if title == "" {
				title = proposalFallbackTitle(content)
			}
			env.Proposal = &StudioProposal{Kind: kind, Title: title, Content: content}
		}
	}
	return env, true
}

// scrapeChangeSetPayload recovers operations or an embedded changeset from prose-broken JSON.
func scrapeChangeSetPayload(text string, env *agentStepEnvelope) {
	if raw, found := scrapeBalancedValue(text, "operations"); found {
		for _, variant := range repairedJSONVariants(raw) {
			var ops []ChangeOperation
			if decodeJSON([]byte(variant), &ops) == nil && len(ops) > 0 {
				env.Operations = ops
				return
			}
		}
	}
	if raw, found := scrapeBalancedValue(text, "changeset"); found {
		for _, variant := range repairedJSONVariants(raw) {
			var cs ChangeSet
			if decodeJSON([]byte(variant), &cs) == nil && len(cs.Operations) > 0 {
				env.ChangeSet = &cs
				return
			}
		}
	}
}

// scrapeBalancedValue extracts the balanced object or array that follows a JSON key.
func scrapeBalancedValue(text, key string) (string, bool) {
	idx := strings.Index(text, `"`+key+`"`)
	if idx < 0 {
		return "", false
	}
	rest := text[idx+len(key)+2:]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return "", false
	}
	rest = strings.TrimSpace(rest[colon+1:])
	if rest == "" {
		return "", false
	}
	var open, close byte
	switch rest[0] {
	case '{':
		open, close = '{', '}'
	case '[':
		open, close = '[', ']'
	default:
		return "", false
	}
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(rest); i++ {
		ch := rest[i]
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
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return rest[:i+1], true
			}
		}
	}
	return "", false
}

// scrapeJSONNumber reads one numeric JSON field; zero means absent or unparsable.
func scrapeJSONNumber(text, key string) int64 {
	idx := strings.Index(text, `"`+key+`"`)
	if idx < 0 {
		return 0
	}
	rest := text[idx+len(key)+2:]
	colon := strings.IndexByte(rest, ':')
	if colon < 0 {
		return 0
	}
	rest = strings.TrimSpace(rest[colon+1:])
	end := 0
	for end < len(rest) && (rest[end] == '-' || rest[end] == '+' || (rest[end] >= '0' && rest[end] <= '9')) {
		end++
	}
	if end == 0 {
		return 0
	}
	value, err := strconv.ParseInt(rest[:end], 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func scrapeField(text, key string) string {
	value, _ := scrapeJSONString(text, key)
	return value
}

func knownAgentTool(tool string) bool {
	return contains([]string{"load_skill", "recall", "get_entity_detail", "get_chapter_window", "read_chapters", "read_plan_nodes", "search_story_facts", "get_plan_node", "get_world_map", "get_source_detail", "get_decision_detail", "get_conversation_excerpt", "get_task_step", "search", "plan", "propose", "propose_changes", "researcher", "reviewer", "writer", "ask_author", "finish"}, tool)
}

func proposalFallbackTitle(content string) string {
	line := strings.TrimSpace(strings.Split(content, "\n")[0])
	line = strings.TrimLeft(line, "#*> -")
	line = strings.TrimSpace(line)
	if line == "" {
		return "待确认的修改"
	}
	if charCount(line) > 40 {
		return string([]rune(line)[:40])
	}
	return line
}

// scrapeJSONString reads one JSON string field, keeping internal quotation marks that are part of the prose.
func scrapeJSONString(text, key string) (string, bool) {
	needle := `"` + key + `"`
	idx := strings.Index(text, needle)
	if idx < 0 {
		return "", false
	}
	rest := strings.TrimSpace(text[idx+len(needle):])
	if !strings.HasPrefix(rest, ":") {
		return "", false
	}
	rest = strings.TrimSpace(rest[1:])
	if !strings.HasPrefix(rest, `"`) {
		return "", false
	}
	body := rest[1:]
	end := len(body)
	for i := 0; i < len(body); i++ {
		if body[i] != '"' || trailingEscape(body, i) {
			continue
		}
		tail := strings.TrimLeft(body[i+1:], " \t\r\n")
		if tail == "" || strings.HasPrefix(tail, "}") || strings.HasPrefix(tail, ",") || strings.HasPrefix(tail, "`") || strings.HasPrefix(tail, "json") {
			end = i
			break
		}
	}
	return strings.TrimSpace(unescapeJSONString(body[:end])), true
}

func trailingEscape(text string, quote int) bool {
	slashes := 0
	for i := quote - 1; i >= 0 && text[i] == '\\'; i-- {
		slashes++
	}
	return slashes%2 == 1
}

// unescapeJSONString turns JSON string escapes into the characters the author should read.
func unescapeJSONString(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] != '\\' || i+1 >= len(value) {
			b.WriteByte(value[i])
			continue
		}
		switch value[i+1] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case '"', '\\', '/':
			b.WriteByte(value[i+1])
		case 'u':
			if i+5 < len(value) {
				if parsed, err := strconv.ParseUint(value[i+2:i+6], 16, 16); err == nil {
					b.WriteRune(rune(parsed))
					i += 5
					continue
				}
			}
			b.WriteByte(value[i])
			continue
		default:
			b.WriteByte(value[i+1])
		}
		i++
	}
	return b.String()
}

// visibleAgentProse is the only part of a tool envelope that belongs in the chat transcript.
func visibleAgentProse(step agentStepEnvelope) string {
	if step.Tool != "" && step.Tool != "ask_author" && step.Tool != "finish" && step.Tool != "propose" && step.Tool != "propose_changes" {
		return ""
	}
	if text := strings.TrimSpace(step.Text); text != "" && step.Tool != "propose" && step.Tool != "propose_changes" {
		return text
	}
	proposal := step.Proposal
	if proposal == nil && len(step.Proposals) > 0 {
		proposal = &step.Proposals[0]
	}
	if proposal == nil {
		return strings.TrimSpace(step.Text)
	}
	title, content := strings.TrimSpace(proposal.Title), strings.TrimSpace(proposal.Content)
	if title != "" && content != "" {
		return "### " + title + "\n\n" + content
	}
	if content != "" {
		return content
	}
	if title != "" {
		return title
	}
	return strings.TrimSpace(step.Text)
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
			id = strings.TrimSpace(job.WorkspaceContext.ActiveSourceID)
		}
		query := strings.TrimSpace(step.Query)
		reference := normalizeSourceRef(id)
		var matched *Source
		// Exact forms win before any fuzzy fallback so a query can never shadow a valid source_id.
		for i := range project.Sources {
			source := &project.Sources[i]
			if (id != "" && source.ID == id) ||
				(reference != "" && source.ID == reference) ||
				(id != "" && strings.EqualFold(source.Title, id)) {
				matched = source
				break
			}
		}
		if matched == nil && query != "" {
			for i := range project.Sources {
				if strings.Contains(lowerText(project.Sources[i].Title), lowerText(query)) {
					matched = &project.Sources[i]
					break
				}
			}
		}
		if matched != nil {
			content := []rune(matched.Content)
			offset := max(0, min(step.Offset, len(content)))
			limit := step.Limit
			if limit < 1 || limit > 4000 {
				limit = 3000
			}
			end := min(len(content), offset+limit)
			return jsonText(map[string]any{"id": matched.ID, "title": matched.Title, "url": matched.URL, "excerpt": string(content[offset:end]), "offset_chars": offset, "next_offset_chars": end, "total_chars": len(content)}), nil
		}
		return "", noSourceMatchError(project, id, query)
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
		// A chapter is one read: the default returns the whole body so a 4500-character
		// chapter never costs two tool steps. An explicit limit is honored up to the cap.
		limit := step.Limit
		if limit <= 0 {
			limit = len(runes) - offset
		}
		limit = min(limit, 100000)
		end := min(len(runes), offset+limit)
		return jsonText(map[string]any{
			"id":            chapter.ID,
			"number":        chapter.Number,
			"title":         chapter.Title,
			"synopsis":      chapterPlanningNotes(project, chapter),
			"synopsis_role": "Distinct author notes; generated plan copies are omitted. Read current plan nodes for the planned sequence.",
			"total_chars":   len(runes),
			"offset_chars":  offset,
			"excerpt":       string(runes[offset:end]),
			"truncated":     end < len(runes),
		}), nil

	case "read_chapters":
		numbers := append([]uint32{}, step.Chapters...)
		if len(numbers) == 0 && len(step.ChapterIDs) == 0 {
			if step.Chapter > 0 {
				numbers = []uint32{step.Chapter}
			} else if current := findChapter(project, job.ChapterID); current != nil {
				numbers = []uint32{current.Number}
			}
		}
		selected := []*Chapter{}
		for _, number := range numbers {
			if len(selected) >= 8 {
				break
			}
			for i := range project.Chapters {
				if project.Chapters[i].Number == number {
					selected = append(selected, &project.Chapters[i])
					break
				}
			}
		}
		for _, id := range step.ChapterIDs {
			if len(selected) >= 8 {
				break
			}
			if ch := findChapter(project, strings.TrimSpace(id)); ch != nil {
				selected = append(selected, ch)
			}
		}
		if len(selected) == 0 {
			return "", errors.New("Provide chapter numbers or chapter_ids to read")
		}
		batch := []map[string]any{}
		for _, ch := range selected {
			body := []rune(ch.Body)
			truncated := false
			if len(body) > 100000 {
				body, truncated = body[:100000], true
			}
			batch = append(batch, map[string]any{
				"id": ch.ID, "number": ch.Number, "title": ch.Title, "synopsis": chapterPlanningNotes(project, ch),
				"synopsis_role": "Distinct author notes; generated plan copies are omitted. Read current plan nodes for the planned sequence.",
				"total_chars":   len([]rune(ch.Body)), "body": string(body), "truncated": truncated,
			})
		}
		return jsonText(map[string]any{"chapters": batch}), nil

	case "read_plan_nodes":
		ids := append([]string{}, step.PlanIDs...)
		if step.PlanID != "" {
			ids = append(ids, step.PlanID)
		}
		matched := []StoryPlan{}
		for _, p := range project.Studio.Plans {
			if p.Status == "rejected" || len(matched) >= 12 {
				continue
			}
			for _, id := range ids {
				if p.ID == strings.TrimSpace(id) {
					matched = append(matched, p)
					break
				}
			}
		}
		if len(matched) == 0 {
			return "", errors.New("Provide exact plan_ids to read; use get_plan_node to search by title or level")
		}
		rendered := []map[string]any{}
		for i := range matched {
			rendered = append(rendered, map[string]any{"plan": matched[i], "synopsis": planSynopsis(project, matched[i])})
		}
		return jsonText(map[string]any{"plans": rendered}), nil

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

// normalizeSourceRef accepts context reference forms like "S:<id>" or passage IDs "S:<id>:<chunk>" and returns the bare source ID.
func normalizeSourceRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if after, ok := strings.CutPrefix(ref, "S:"); ok {
		id, _, _ := strings.Cut(after, ":")
		return id
	}
	return ref
}

// noSourceMatchError teaches the model the exact reusable IDs instead of leaving it to guess again.
func noSourceMatchError(project *Project, id, query string) error {
	if len(project.Sources) == 0 {
		return errors.New("This project has no sources yet. Import material in Sources & research before calling get_source_detail")
	}
	listed := make([]string, 0, min(10, len(project.Sources)))
	for i, source := range project.Sources {
		if i >= 10 {
			listed = append(listed, fmt.Sprintf("… and %d more", len(project.Sources)-10))
			break
		}
		listed = append(listed, fmt.Sprintf("%s (%s)", source.ID, truncateSummary(source.Title, 60)))
	}
	requested := id
	if requested == "" {
		requested = query
	}
	return fmt.Errorf("No source matches %q. Pass source_id exactly as one of: %s. Passage IDs look like S:<source_id>:<chunk>; reuse only the <source_id> part, or leave source_id empty when a source is selected in the workspace", requested, strings.Join(listed, "; "))
}

// stageAgentChangeSet records a model-proposed ChangeSet or operations list on the active job.
// The error explains the exact rejection so the model can correct the next attempt.
func (engine *Engine) stageAgentChangeSet(jobID string, project *Project, cs *ChangeSet, ops []ChangeOperation) (int, error) {
	if project == nil {
		return 0, errors.New("No project is loaded for this task")
	}
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	job := engine.jobs[jobID]
	if job == nil {
		return 0, errors.New("The task is no longer active")
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
		return 0, errors.New("pass operations: [{\"target_kind\":\"entity|memory|plan|decision|chapter|location|route\",\"op_type\":\"upsert|delete|append_prose|replace_prose\",\"target_id\":\"…\",\"title\":\"…\",\"after_value\":{…}}] or a changeset object with the same operations array")
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
		return 0, err
	}
	if err := normalizeMemoryOperations(&next); err != nil {
		return 0, err
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
	// Mirror only the typed decodes of the confirmation-time apply path, so field-type
	// mistakes fail at staging (and the model can correct them in-task) while semantic
	// details stay repairable by the author at preview time.
	if err := previewChangeSetApply(&next); err != nil {
		return 0, err
	}
	job.ChangeSets = append(job.ChangeSets, next)
	return len(next.Operations), nil
}

// previewChangeSetApply rejects operations whose after_value cannot decode into the
// same struct shapes applyChangeSetToProject uses — the class of error that otherwise
// surfaces only when the author tries to confirm.
func previewChangeSetApply(cs *ChangeSet) error {
	for i := range cs.Operations {
		op := &cs.Operations[i]
		if op.OpType == OpDelete || len(op.AfterValue) == 0 {
			continue
		}
		var target any
		switch op.TargetKind {
		case TargetProjectMeta, TargetStudioGoal:
			var patch map[string]json.RawMessage
			target = &patch
		case TargetDecision:
			target = &StoryDecision{}
		case TargetEntity:
			var rawEnt struct {
				ID          string   `json:"id"`
				Name        string   `json:"name"`
				Kind        string   `json:"kind"`
				Aliases     []string `json:"aliases"`
				Description string   `json:"description"`
			}
			target = &rawEnt
		case TargetMemory:
			target = &StoryMemory{}
		case TargetPlan:
			target = &StoryPlan{}
		case TargetChapter:
			target = &chapterProsePatch{}
		case TargetLocation:
			target = &WorldLocation{}
		case TargetRoute:
			target = &WorldRoute{}
		default:
			continue
		}
		if err := json.Unmarshal(op.AfterValue, target); err != nil {
			return fmt.Errorf("Operation %d (%s %s) has a malformed after_value: %v", i+1, op.TargetKind, op.OpType, err)
		}
	}
	return nil
}
