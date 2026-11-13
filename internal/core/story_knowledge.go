package core

import (
	"fmt"
	"sort"
	"strings"
)

// knowledgeSource caches one source revision during a projection or retrieval request.
type knowledgeSource struct {
	Text    string
	Hash    string
	Chapter uint32
}

// knowledgeSources hashes each saved source once for the current request.
func knowledgeSources(project *Project) map[string]knowledgeSource {
	result := map[string]knowledgeSource{}
	for _, chapter := range project.Chapters {
		result[chapter.ID] = knowledgeSource{chapter.Body, hashText(chapter.Body), chapter.Number}
	}
	for _, source := range project.Sources {
		result[source.ID] = knowledgeSource{source.Content, hashText(source.Content), 0}
	}
	return result
}

// knowledgeSourceCurrent checks provenance without treating a located quote as semantic approval.
func knowledgeSourceCurrent(sources map[string]knowledgeSource, id, hash, quote string, required bool) bool {
	source, exists := sources[id]
	return exists && hash != "" && source.Hash == hash && (!required || strings.TrimSpace(quote) != "") && (quote == "" || strings.Contains(source.Text, quote))
}

// knowledgeTextKey normalizes spacing and casing while retaining every word and punctuation mark.
func knowledgeTextKey(text string) string {
	return lowerText(strings.Join(strings.Fields(text), " "))
}

// knowledgeMatchScore measures lexical relevance without requiring a tokenizer or remote embedding service.
func knowledgeMatchScore(text, query string) int {
	score := 0
	text = lowerText(text)
	for term := range terms(query) {
		if strings.Contains(text, term) {
			score += 3
		}
	}
	return score
}

// knowledgeEntityScores expands exact names and aliases by one reviewed, current relationship hop.
func knowledgeEntityScores(project *Project, query string, through uint32, sources map[string]knowledgeSource) map[string]int {
	scores := map[string]int{}
	normalized := lowerText(query)
	for _, entity := range project.Entities {
		for _, name := range append([]string{entity.Name}, entity.Aliases...) {
			if name != "" && strings.Contains(normalized, lowerText(name)) {
				scores[entity.Name] = 2000
				break
			}
		}
	}
	seeds := map[string]bool{}
	for name := range scores {
		seeds[name] = true
	}
	for _, fact := range project.Facts {
		if fact.Status != "confirmed" || fact.Chapter > through || sources[fact.SourceID].Chapter > through || fact.RelatedEntity == "" || !knowledgeSourceCurrent(sources, fact.SourceID, fact.SourceHash, fact.Quote, true) {
			continue
		}
		from, to := project.CanonicalName(fact.Entity), project.CanonicalName(fact.RelatedEntity)
		if seeds[from] {
			scores[to] = max(scores[to], 300)
		}
		if seeds[to] {
			scores[from] = max(scores[from], 300)
		}
	}
	return scores
}

// storyMemoryContext selects effective rules, relevant state and approaching payoffs from the same narrative window.
func storyMemoryContext(project *Project, through uint32, query string, limits ...int) []ContextItem {
	sources := knowledgeSources(project)
	scores := knowledgeEntityScores(project, query, through, sources)
	successors := memorySuccessors(project.Studio.Memories)
	current := through
	if current == ^uint32(0) {
		current = 0
		for _, chapter := range project.Chapters {
			current = max(current, chapter.Number)
		}
	}
	items := []ContextItem{}
	for _, memory := range project.Studio.Memories {
		if memory.Status == "proposed" {
			if memory.Chapter <= current && knowledgeSourceCurrent(sources, memory.SourceID, memory.SourceHash, memory.Quote, true) && sources[memory.SourceID].Chapter <= through {
				score := scores[project.CanonicalName(memory.Entity)]/20 + knowledgeMatchScore(memory.Entity+" "+memory.Quote, query)
				if score > 0 {
					items = append(items, candidateKnowledgeContext("Q:"+memory.ID, memory.Entity, memory.Quote, memory.SourceID, memory.SourceHash, score))
				}
			}
			continue
		}
		if !memoryVisibleAt(memory, current, successors) {
			continue
		}
		if memory.SourceID != "" && (!knowledgeSourceCurrent(sources, memory.SourceID, memory.SourceHash, memory.Quote, contains([]string{"fact", "inference"}, memory.Kind)) || sources[memory.SourceID].Chapter > through) {
			continue
		}
		if memory.SourceID == "" && contains([]string{"fact", "inference"}, memory.Kind) {
			continue
		}
		score := scores[project.CanonicalName(memory.Entity)] + knowledgeMatchScore(memory.Title+" "+memory.Content+" "+memory.StoryTime, query)
		if memory.Kind == "rule" {
			score += 10000
		}
		open := contains([]string{"clue", "promise"}, memory.Kind) && !contains([]string{"resolved", "fulfilled", "abandoned"}, memory.State)
		if open && memory.DueChapter > 0 && memory.DueChapter <= current+10 {
			score += 400
		}
		if memory.Chapter <= current && current-memory.Chapter <= 8 && memory.Kind != "summary" {
			score += 40
		}
		if score == 0 && strings.TrimSpace(query) != "" {
			continue
		}
		known := strings.Join(memory.KnownBy, ", ")
		if known == "" {
			known = "not specified; do not assume public knowledge"
		}
		text := fmt.Sprintf("%s: %s\nState: %s. Effective chapters: %d–%d. Story time: %s. Known by: %s. Due chapter: %d\nEvidence type: %s\nQuote: %s", memory.Title, memory.Content, memory.State, memory.Chapter, memory.ThroughChapter, memory.StoryTime, known, memory.DueChapter, memory.Epistemic, memory.Quote)
		reason := "Author-confirmed story memory; respect time and character knowledge"
		if memory.Kind == "plan" {
			text = "Future planning approved by the author; its events have not been established by saved prose.\n" + text
			reason = "Author-approved future plan; distinguish it from observed story events"
		}
		items = append(items, ContextItem{ID: "N:" + memory.ID, Title: memory.Entity + " · " + memory.Title, Kind: memory.Kind, Text: text, Score: score, SourceID: memory.SourceID, SourceHash: memory.SourceHash, Reason: reason})
	}
	limit := 32
	if len(limits) > 0 {
		limit = limits[0]
	}
	if limit < 0 {
		return items
	}
	return rankKnowledgeContext(items, limit)
}

// candidateKnowledgeContext offers an original excerpt for verification without copying the candidate claim into canon.
func candidateKnowledgeContext(id, entity, quote, sourceID, sourceHash string, score int) ContextItem {
	return ContextItem{ID: id, Title: entity + " · unreviewed source excerpt", Kind: "candidate_source", Text: "Unreviewed extraction. Read the original excerpt before drawing conclusions. It does not establish the extracted interpretation or shared character knowledge.\n" + truncateSummary(quote, 900), Score: score, SourceID: sourceID, SourceHash: sourceHash, Reason: "Current source navigation from a candidate extraction"}
}

// knowledgeLocationVisible excludes places linked exclusively to later manuscript chapters.
func knowledgeLocationVisible(project *Project, location WorldLocation, through uint32) bool {
	if len(location.ChapterIDs) == 0 {
		return true
	}
	for _, id := range location.ChapterIDs {
		if chapter := findChapter(project, id); chapter != nil && chapter.Number <= through {
			return true
		}
	}
	return false
}

// storyKnowledgeContext joins current evidence, state, entity setup and linked map constraints.
func storyKnowledgeContext(project *Project, query string, through uint32) []ContextItem {
	sources := knowledgeSources(project)
	scores := knowledgeEntityScores(project, query, through, sources)
	items := storyMemoryContext(project, through, query, -1)
	for _, fact := range project.Facts {
		if fact.Status == "rejected" || fact.Chapter > through || sources[fact.SourceID].Chapter > through || !knowledgeSourceCurrent(sources, fact.SourceID, fact.SourceHash, fact.Quote, true) {
			continue
		}
		score := scores[project.CanonicalName(fact.Entity)] + knowledgeMatchScore(fact.Entity+" "+fact.Claim, query)
		if score == 0 {
			continue
		}
		if fact.Status == "pending" {
			items = append(items, candidateKnowledgeContext("V:"+fact.ID, fact.Entity, fact.Quote, fact.SourceID, fact.SourceHash, score/20+1))
			continue
		}
		reason := "Current author-confirmed source evidence"
		if scores[project.CanonicalName(fact.Entity)] == 300 {
			reason = "Character relationship"
		}
		text := fmt.Sprintf("%s: %s\nRelated entity: %s. Time: %s. Location: %s\nEvidence type: %s. This record does not establish that every character knows it.\nQuote: %s", fact.Entity, fact.Claim, fact.RelatedEntity, fact.StoryTime, fact.Location, fact.Epistemic, fact.Quote)
		items = append(items, ContextItem{ID: "W:" + fact.ID, Title: fmt.Sprintf("%s · Chapter %d", fact.Entity, fact.Chapter), Kind: "wiki", Text: text, Score: score, SourceID: fact.SourceID, SourceHash: fact.SourceHash, Reason: reason})
	}
	for _, entity := range project.Entities {
		if scores[entity.Name] == 0 || strings.TrimSpace(entity.Description) == "" {
			continue
		}
		items = append(items, ContextItem{ID: "E:" + entity.ID, Title: entity.Name, Kind: "entity", Text: "Author setup. Planned traits do not establish events in saved prose.\n" + entity.Description, Score: scores[entity.Name], Reason: "Relevant entity or one-hop relationship"})
	}
	places := map[string]bool{}
	visiblePlaces := map[string]bool{}
	for _, location := range project.Studio.Locations {
		if !knowledgeLocationVisible(project, location, through) {
			continue
		}
		visiblePlaces[location.ID] = true
		relevant := location.Name != "" && strings.Contains(lowerText(query), lowerText(location.Name))
		for _, entityID := range location.EntityIDs {
			for _, entity := range project.Entities {
				relevant = relevant || entity.ID == entityID && scores[entity.Name] > 0
			}
		}
		if relevant {
			places[location.ID] = true
			items = append(items, ContextItem{ID: "L:" + location.ID, Title: location.Name, Kind: "location", Text: "Author-reviewed map constraints. Linked chapter and plan references do not establish a visit.\n" + jsonText(location), Score: 350, Reason: "Place linked to the scene's entities"})
		}
	}
	for _, route := range project.Studio.Routes {
		if (places[route.FromID] || places[route.ToID]) && visiblePlaces[route.FromID] && visiblePlaces[route.ToID] {
			items = append(items, ContextItem{ID: "R:" + route.ID, Title: route.Transport, Kind: "route", Text: "Author-reviewed travel constraint.\n" + jsonText(route), Score: 340, Reason: "Route connected to a relevant place"})
		}
	}
	return items
}

// rankKnowledgeContext deduplicates identities and preserves lexical ordering beneath narrative priorities.
func rankKnowledgeContext(items []ContextItem, ordinaryLimit int) []ContextItem {
	byID := map[string]ContextItem{}
	for _, item := range items {
		old, exists := byID[item.ID]
		if !exists || item.Score > old.Score || item.Required && !old.Required {
			byID[item.ID] = item
		}
	}
	result := make([]ContextItem, 0, len(byID))
	for _, item := range byID {
		result = append(result, item)
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Score != result[j].Score {
			return result[i].Score > result[j].Score
		}
		return result[i].ID < result[j].ID
	})
	kept, ordinary := []ContextItem{}, 0
	for _, item := range result {
		if item.Kind == "rule" || item.Required {
			kept = append(kept, item)
		} else if ordinary < ordinaryLimit {
			kept = append(kept, item)
			ordinary++
		}
	}
	return kept
}

// mergeKnowledgeContext promotes named-entity passages and removes repeated evidence across memory and Wiki projections.
func mergeKnowledgeContext(project *Project, query string, through uint32, passages []ContextItem, limit int, requests ...AIRequest) []ContextItem {
	projection := *project
	if len(requests) > 0 {
		projection = knowledgeContextProject(project, requests[0])
	}
	items := storyKnowledgeContext(&projection, query, through)
	scores := knowledgeEntityScores(&projection, query, through, knowledgeSources(&projection))
	for _, passage := range passages {
		for _, entity := range project.Entities {
			if scores[entity.Name] != 2000 {
				continue
			}
			for _, name := range append([]string{entity.Name}, entity.Aliases...) {
				if name != "" && strings.Contains(lowerText(passage.Title+" "+passage.Text), lowerText(name)) {
					passage.Score += 60
					break
				}
			}
		}
		items = append(items, passage)
	}
	keys := map[string]string{}
	for _, memory := range project.Studio.Memories {
		if memory.SourceID != "" {
			keys["N:"+memory.ID] = memory.SourceID + "|" + knowledgeTextKey(memory.Content)
		}
	}
	for _, fact := range project.Facts {
		keys["W:"+fact.ID] = fact.SourceID + "|" + knowledgeTextKey(fact.Claim)
	}
	if len(requests) > 0 {
		items = filterStoryKnowledgeContext(project, requests[0], items)
	}
	seen, result := map[string]bool{}, []ContextItem{}
	for _, item := range rankKnowledgeContext(items, len(items)) {
		key := keys[item.ID]
		if key != "" && seen[key] {
			continue
		}
		seen[key] = key != ""
		result = append(result, item)
	}
	return rankKnowledgeContext(result, limit)
}

// knowledgeContextProject removes explicit omissions before they can seed relationship expansion.
func knowledgeContextProject(project *Project, request AIRequest) Project {
	view := *project
	workspace := request.WorkspaceContext
	if workspace == nil {
		return view
	}
	omittedNames, omittedSources := map[string]bool{}, map[string]bool{}
	view.Entities = []Entity{}
	for _, entity := range project.Entities {
		if contains(workspace.OmitEntityIDs, entity.ID) {
			omittedNames[project.CanonicalName(entity.Name)] = true
		} else {
			view.Entities = append(view.Entities, entity)
		}
	}
	if workspace.OmitChapter {
		for _, chapter := range project.Chapters {
			omittedSources[chapter.ID] = true
		}
	}
	if workspace.OmitSource {
		omittedSources[workspace.ActiveSourceID] = true
	}
	view.Facts = []Fact{}
	for _, fact := range project.Facts {
		if !omittedNames[project.CanonicalName(fact.Entity)] && !omittedNames[project.CanonicalName(fact.RelatedEntity)] && !omittedSources[fact.SourceID] {
			view.Facts = append(view.Facts, fact)
		}
	}
	view.Studio.Memories = []StoryMemory{}
	for _, memory := range project.Studio.Memories {
		if !contains(workspace.OmitMemoryIDs, memory.ID) && !omittedNames[project.CanonicalName(memory.Entity)] && !omittedSources[memory.SourceID] {
			view.Studio.Memories = append(view.Studio.Memories, memory)
		}
	}
	return view
}

// filterStoryKnowledgeContext applies chapter, source and explicit omission constraints at every prompt entry point.
func filterStoryKnowledgeContext(project *Project, request AIRequest, items []ContextItem) []ContextItem {
	through := ^uint32(0)
	chapterID := request.ChapterID
	workspace := request.WorkspaceContext
	if chapterID == "" && workspace != nil {
		chapterID = workspace.ActiveChapterID
	}
	if chapter := findChapter(project, chapterID); chapter != nil {
		through = chapter.Number
		if request.Action == "continue" && through > 0 {
			through--
		}
	}
	omittedEntities, omittedMemories := map[string]bool{}, map[string]bool{}
	if workspace != nil {
		omittedMemories = omittedIDs(workspace.OmitMemoryIDs)
		for _, entity := range project.Entities {
			if contains(workspace.OmitEntityIDs, entity.ID) {
				omittedEntities[project.CanonicalName(entity.Name)] = true
			}
		}
	}
	excluded := map[string]bool{}
	bindings := map[string]string{}
	memoryThrough := through
	if memoryThrough == ^uint32(0) {
		memoryThrough = 0
		for _, chapter := range project.Chapters {
			memoryThrough = max(memoryThrough, chapter.Number)
		}
	}
	for _, fact := range project.Facts {
		omit := fact.Chapter > through || omittedEntities[project.CanonicalName(fact.Entity)] || omittedEntities[project.CanonicalName(fact.RelatedEntity)] || !factCurrent(project, &fact)
		excluded["W:"+fact.ID] = omit || fact.Status != "confirmed"
		excluded["V:"+fact.ID] = omit || fact.Status != "pending"
		bindings["W:"+fact.ID], bindings["V:"+fact.ID] = fact.SourceID, fact.SourceID
	}
	successors := memorySuccessors(project.Studio.Memories)
	for _, memory := range project.Studio.Memories {
		omit := memory.Chapter > through || omittedMemories[memory.ID] || omittedEntities[project.CanonicalName(memory.Entity)] || !memoryCurrent(project, memory)
		excluded["N:"+memory.ID] = omit || !memoryVisibleAt(memory, memoryThrough, successors)
		excluded["Q:"+memory.ID] = omit || memory.Status != "proposed"
		bindings["N:"+memory.ID], bindings["Q:"+memory.ID] = memory.SourceID, memory.SourceID
	}
	for _, location := range project.Studio.Locations {
		excluded["L:"+location.ID] = !knowledgeLocationVisible(project, location, through)
		if workspace != nil {
			for _, id := range location.EntityIDs {
				excluded["L:"+location.ID] = excluded["L:"+location.ID] || contains(workspace.OmitEntityIDs, id)
			}
		}
	}
	for _, route := range project.Studio.Routes {
		excluded["R:"+route.ID] = excluded["L:"+route.FromID] || excluded["L:"+route.ToID]
	}
	sources, result := knowledgeSources(project), []ContextItem{}
	for _, item := range items {
		if excluded[item.ID] {
			continue
		}
		parts := strings.Split(item.ID, ":")
		if len(parts) > 1 && contains([]string{"W", "V", "N", "Q"}, parts[0]) {
			bound, exists := bindings[item.ID]
			if !exists || item.SourceID != "" && item.SourceID != bound {
				continue
			}
			item.SourceID = bound
		}
		if len(parts) > 1 && parts[0] == "P" && workspace != nil && contains(workspace.OmitPlanIDs, parts[1]) {
			continue
		}
		if len(parts) > 1 && parts[0] == "E" && workspace != nil && contains(workspace.OmitEntityIDs, parts[1]) {
			continue
		}
		if item.SourceID == "" && len(parts) > 1 && contains([]string{"C", "S"}, parts[0]) {
			item.SourceID = parts[1]
		}
		if item.SourceID != "" {
			source, exists := sources[item.SourceID]
			if !exists || source.Chapter > through || item.SourceHash != "" && item.SourceHash != source.Hash {
				continue
			}
			if workspace != nil && (workspace.OmitChapter && source.Chapter > 0 || workspace.OmitSource && item.SourceID == workspace.ActiveSourceID) {
				continue
			}
			if len(parts) > 1 && contains([]string{"C", "S"}, parts[0]) && !strings.Contains(source.Text, item.Text) {
				continue
			}
			item.SourceHash = source.Hash
		}
		result = append(result, item)
	}
	return result
}

// writingMemoryKinds is the shared contract for source-supported chapter memory candidates.
func writingMemoryKinds() []string {
	return []string{"character", "relationship", "knowledge", "clue", "promise", "fact", "inference"}
}

// writingMemoryCandidates binds original evidence to a saved chapter and retains only genuinely new proposed changes.
func writingMemoryCandidates(project *Project, chapter *Chapter, body string, reviewMemories []StoryMemory) []StoryMemory {
	if chapter == nil || strings.TrimSpace(body) == "" {
		return nil
	}
	result := []StoryMemory{}
	seen := map[string]bool{}
	for _, memory := range project.Studio.Memories {
		fromSavedBody := memory.SourceID == chapter.ID && memory.SourceHash == hashText(body) && (memory.Quote == "" || strings.Contains(body, memory.Quote))
		if memory.Status != "rejected" && memory.Chapter <= chapter.Number && (fromSavedBody || memoryCurrent(project, memory)) {
			seen[knowledgeTextKey(memory.Kind+"|"+memory.Entity+"|"+memory.Title+"|"+memory.Content+"|"+memory.State+"|"+strings.Join(uniqueSorted(memory.KnownBy), ","))] = true
		}
	}
	for _, memory := range reviewMemories {
		if !contains(writingMemoryKinds(), memory.Kind) || strings.TrimSpace(memory.Title) == "" || strings.TrimSpace(memory.Content) == "" || charCount(strings.TrimSpace(memory.Quote)) < 4 || !strings.Contains(body, memory.Quote) {
			continue
		}
		memory.Entity = project.CanonicalName(strings.TrimSpace(memory.Entity))
		key := knowledgeTextKey(memory.Kind + "|" + memory.Entity + "|" + memory.Title + "|" + memory.Content + "|" + memory.State + "|" + strings.Join(uniqueSorted(memory.KnownBy), ","))
		if seen[key] {
			continue
		}
		seen[key] = true
		memory.ID, memory.Status, memory.Epistemic = newID(), "proposed", "inferred"
		memory.Chapter, memory.ThroughChapter = chapter.Number, 0
		memory.SourceID, memory.SourceHash = chapter.ID, hashText(body)
		memory.Supersedes, memory.UpdatedAt = "", now()
		candidates := memoryUpdateCandidates(project, memory)
		if len(candidates) == 1 && candidates[0].Chapter <= chapter.Number {
			memory.Supersedes = candidates[0].ID
		}
		result = append(result, memory)
		if len(result) == 20 {
			break
		}
	}
	return result
}

// storyKnowledgeView exposes the same scoped records and maintenance preview used by writing and review.
func storyKnowledgeView(project *Project, through uint32, query string, includeCandidates bool) map[string]any {
	items := mergeKnowledgeContext(project, query, through, nil, 96)
	if !includeCandidates {
		kept := []ContextItem{}
		for _, item := range items {
			if item.Kind != "candidate_source" {
				kept = append(kept, item)
			}
		}
		items = kept
	}
	return map[string]any{"through_chapter": through, "items": items, "maintenance": knowledgeMaintenancePreview(project), "maintenance_receipts": project.Studio.KnowledgeMaintenance}
}
