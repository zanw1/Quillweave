package core

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

const CompilerVersion = "quillweave-story-wiki-2"

var FactKinds = []string{"event", "emotion", "relationship", "foreshadow", "world", "knowledge", "knowledge_state"}
var EntityKinds = []string{"character", "faction", "location", "world", "item", "concept"}

// rulesHash hashes the compilation rules and project tracking focus.
func rulesHash(project *Project) string {
	return hashText(CompilerVersion + "|" + project.WikiPurpose + "|" + strings.Join(FactKinds, ","))
}

// compilationFingerprint binds source revision, extraction contract, tracking rules and selected provider.
func compilationFingerprint(project *Project, sourceID string, config ProviderConfig) (string, error) {
	source, _, ok := project.SourceText(sourceID)
	if !ok {
		return "", errors.New("Compilation source not found")
	}
	parts := []string{sourceID, hashText(source), rulesHash(project), hashText(extractionSystem), config.Protocol, config.BaseURL, config.Model}
	if responseLanguage(project, "") != "en" {
		parts = append(parts, responseLanguage(project, ""))
	}
	if options := providerOptionFingerprint(config); options != "" {
		parts = append(parts, options)
	}
	return hashText(strings.Join(parts, "|")), nil
}

// modelsFingerprint binds both selected models while retaining the single-model legacy fingerprint.
func modelsFingerprint(project *Project, sourceID string, extraction, synthesis ProviderConfig) (string, error) {
	first, err := compilationFingerprint(project, sourceID, extraction)
	if err != nil {
		return "", err
	}
	second, err := compilationFingerprint(project, sourceID, synthesis)
	if err != nil {
		return "", err
	}
	if first == second {
		return first, nil
	}
	return hashText(first + "|" + second), nil
}

// latestCompilations selects the latest compilation for each source while preserving history.
func latestCompilations(project *Project) []*WikiCompilation {
	result := []*WikiCompilation{}
	seen := map[string]bool{}
	for i := len(project.Compilations) - 1; i >= 0; i-- {
		memory := &project.Compilations[i]
		if !seen[memory.SourceID] {
			seen[memory.SourceID] = true
			result = append(result, memory)
		}
	}
	return result
}

// factCurrent checks the complete source hash and verbatim quote.
func factCurrent(project *Project, fact *Fact) bool {
	source, _, ok := project.SourceText(fact.SourceID)
	return ok && fact.SourceHash != "" && strings.TrimSpace(fact.Quote) != "" && hashText(source) == fact.SourceHash && strings.Contains(source, fact.Quote)
}

// compilationCurrent requires current sources, rules, and resolvable unrejected evidence.
func compilationCurrent(project *Project, memory *WikiCompilation) bool {
	if len(memory.Sections) == 0 || memory.RulesHash != rulesHash(project) || memory.CompilerVersion != CompilerVersion {
		return false
	}
	text, _, ok := project.SourceText(memory.SourceID)
	if !ok || hashText(text) != memory.SourceHash {
		return false
	}
	for _, section := range memory.Sections {
		for _, id := range section.EvidenceIDs {
			found := false
			for _, fact := range project.Facts {
				if fact.ID == id && fact.SourceID == memory.SourceID && fact.Status != "rejected" && factCurrent(project, &fact) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// compilationApproved admits compiled memory only while every cited fact remains author-confirmed.
func compilationApproved(project *Project, memory *WikiCompilation) bool {
	if memory.Status != "confirmed" || !compilationCurrent(project, memory) {
		return false
	}
	approved := map[string]bool{}
	for _, fact := range project.Facts {
		approved[fact.ID] = fact.Status == "confirmed"
	}
	for _, section := range memory.Sections {
		for _, id := range section.EvidenceIDs {
			if !approved[id] {
				return false
			}
		}
	}
	return true
}

// renameEntityReferences keeps structured name references together without rewriting source passages.
func renameEntityReferences(project *Project, oldName, newName string) {
	if oldName == newName {
		return
	}
	for i := range project.Facts {
		if project.Facts[i].Entity == oldName {
			project.Facts[i].Entity = newName
		}
		if project.Facts[i].RelatedEntity == oldName {
			project.Facts[i].RelatedEntity = newName
		}
	}
	for i := range project.Studio.Memories {
		memory := &project.Studio.Memories[i]
		if memory.Entity == oldName {
			memory.Entity = newName
		}
		for j, name := range memory.KnownBy {
			if name == oldName {
				memory.KnownBy[j] = newName
			}
		}
	}
	for i := range project.Studio.Plans {
		for j, name := range project.Studio.Plans[i].Characters {
			if name == oldName {
				project.Studio.Plans[i].Characters[j] = newName
			}
		}
	}
}

// cachedCompilation reuses memory only while all source and evidence dependencies remain valid.
func cachedCompilation(project *Project, sourceID, fingerprint string) *WikiCompilation {
	for _, memory := range latestCompilations(project) {
		if memory.SourceID == sourceID && memory.Fingerprint == fingerprint && compilationCurrent(project, memory) {
			return memory
		}
	}
	return nil
}

// parseMemory accepts structured summaries referencing evidence from the current batch.
func parseMemory(response string, facts []Fact) (MemorySection, error) {
	text := strings.TrimSpace(response)
	if strings.HasPrefix(text, "```json") {
		text = strings.TrimPrefix(text, "```json")
	} else {
		text = strings.TrimPrefix(text, "```")
	}
	text = strings.TrimSpace(text)
	for strings.HasSuffix(text, "```") {
		text = strings.TrimSuffix(text, "```")
	}
	text = strings.TrimSpace(text)
	var memory MemorySection
	if err := decodeJSON([]byte(text), &memory); err != nil {
		return memory, errors.New("Story memory is not valid JSON. Compilation was not saved")
	}
	invalid := strings.TrimSpace(memory.Summary) == "" || charCount(memory.Summary) > 3000 || len(memory.EvidenceIDs) == 0 || len(memory.Questions) > 12
	for _, question := range memory.Questions {
		invalid = invalid || charCount(question) > 1000
	}
	if invalid {
		return memory, errors.New("Story memory requires a summary and evidence within the length limits")
	}
	allowed := map[string]bool{}
	for _, fact := range facts {
		allowed[fact.ID] = true
	}
	for _, id := range memory.EvidenceIDs {
		if !allowed[id] {
			return memory, errors.New("Story memory references missing evidence")
		}
	}
	return memory, nil
}

// chunks splits text by Unicode characters with overlap and complete tail coverage.
func chunks(text string, size, overlap int) []string {
	result := []string{}
	chars := []rune(text)
	if len(chars) == 0 || size <= 0 || overlap >= size || overlap < 0 {
		return result
	}
	for start := 0; start < len(chars); {
		end := min(start+size, len(chars))
		result = append(result, string(chars[start:end]))
		if end == len(chars) {
			break
		}
		start = end - overlap
	}
	return result
}

// parseCandidates validates each structured fact and its verbatim quote separately so one
// malformed record no longer discards the whole batch. Only a structurally unusable response
// is fatal; per-candidate problems are returned as skip reasons for the author to review.
func parseCandidates(response, source string) ([]Candidate, []string, error) {
	text := strings.TrimSpace(response)
	if strings.HasPrefix(text, "```") {
		if index := strings.IndexByte(text, '\n'); index >= 0 {
			body := text[index+1:]
			trimmed := strings.TrimRightFunc(body, unicode.IsSpace)
			if strings.HasSuffix(trimmed, "```") {
				body = strings.TrimSuffix(trimmed, "```")
			}
			text = strings.TrimSpace(body)
		}
	}
	var extraction struct {
		Facts []Candidate `json:"facts"`
	}
	if err := decodeRecord([]byte(text), &extraction, "facts"); err != nil {
		return nil, nil, errors.New("Wiki analysis returned invalid JSON. Try again or select a model that supports JSON output.")
	}
	if len(extraction.Facts) > 100 {
		return nil, nil, errors.New("The analysis returned too many facts to save.")
	}
	accepted := []Candidate{}
	skipped := []string{}
	for _, fact := range extraction.Facts {
		if err := validateWikiCandidate(fact, source); err != nil {
			skipped = append(skipped, err.Error())
		} else {
			accepted = append(accepted, fact)
		}
	}
	return accepted, skipped, nil
}

// ingest deduplicates pending evidence by source revision and claim while retaining author review.
func ingest(project *Project, sourceID, expectedHash string, candidates []Candidate, model string) (int, error) {
	source, chapter, ok := project.SourceText(sourceID)
	if !ok {
		return 0, errors.New("The analysis source no longer exists")
	}
	if hashText(source) != expectedHash {
		return 0, errors.New("The source changed during analysis. The result is retained in the task. Analyze the new version.")
	}
	for _, candidate := range candidates {
		if candidate.EntityKind != "" && !contains(EntityKinds, candidate.EntityKind) {
			return 0, errors.New("The analysis contains an unsupported entity type")
		}
	}
	added := 0
	for _, candidate := range candidates {
		name := project.CanonicalName(strings.TrimSpace(candidate.Entity))
		fingerprint := hashText(strings.Join([]string{sourceID, expectedHash, name, candidate.Kind, strings.TrimSpace(candidate.Claim)}, "|"))
		duplicate := false
		for _, fact := range project.Facts {
			if fact.ID == fingerprint || (fact.SourceID == sourceID && fact.SourceHash == expectedHash && fact.Entity == name && fact.Kind == candidate.Kind && strings.TrimSpace(fact.Claim) == strings.TrimSpace(candidate.Claim)) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		found := false
		for _, entity := range project.Entities {
			if entity.Name == name {
				found = true
				break
			}
		}
		if !found {
			kind, description := candidate.EntityKind, ""
			if kind == "" {
				kind = "concept"
				description = localText(project, "Entry type awaits author review.", "条目类型待作者确认。")
			}
			project.Entities = append(project.Entities, Entity{ID: newID(), Name: name, Kind: kind, Aliases: []string{}, Description: description})
		}
		project.Facts = append(project.Facts, Fact{ID: fingerprint, Entity: name, Kind: candidate.Kind, Claim: candidate.Claim, SourceID: sourceID, SourceHash: expectedHash, Quote: candidate.Quote, Chapter: chapter, StoryTime: candidate.StoryTime, Location: candidate.Location, RelatedEntity: project.CanonicalName(candidate.RelatedEntity), Epistemic: candidate.Epistemic, Status: "pending", Model: model, CreatedAt: now()})
		added++
	}
	return added, nil
}

// lintWiki reports stale sources, broken links, and ambiguity for author review.
func lintWiki(project *Project) []LintFinding {
	findings := []LintFinding{}
	aliases := map[string]string{}
	for _, entity := range project.Entities {
		for _, alias := range append([]string{entity.Name}, entity.Aliases...) {
			owner, exists := aliases[alias]
			aliases[alias] = entity.Name
			if exists && owner != entity.Name {
				findings = append(findings, LintFinding{Kind: "ambiguous_alias", Severity: "warning", Title: "Character names or aliases are ambiguous", Detail: fmt.Sprintf("%s refers to both %s and %s", alias, owner, entity.Name), Entity: entity.Name})
			}
		}
		referenced := false
		for _, fact := range project.Facts {
			if (fact.Entity == entity.Name || fact.RelatedEntity == entity.Name) && fact.Status != "rejected" && factCurrent(project, &fact) {
				referenced = true
				break
			}
		}
		if !referenced {
			findings = append(findings, LintFinding{Kind: "unreferenced_entity", Severity: "info", Title: entity.Name + " has no current chapter evidence", Detail: "This entry may describe a planned character or setting that has yet to appear.", Entity: entity.Name})
		}
	}
	positions := map[[2]string]map[string]bool{}
	for _, fact := range project.Facts {
		if fact.Status == "rejected" {
			continue
		}
		current := factCurrent(project, &fact)
		if !current {
			findings = append(findings, LintFinding{Kind: "stale_evidence", Severity: "warning", Title: fact.Entity + " needs updated evidence", Detail: "The source changed or the quote cannot be located. Compile the source again.", Entity: fact.Entity, SourceID: fact.SourceID})
		}
		if project.ResolveEntity(fact.Entity) == nil || (fact.RelatedEntity != "" && project.ResolveEntity(fact.RelatedEntity) == nil) {
			findings = append(findings, LintFinding{Kind: "broken_relation", Severity: "warning", Title: "A relationship points to a missing entry", Detail: fmt.Sprintf("%s → %s. Create the missing entry or review the relationship.", fact.Entity, fact.RelatedEntity), Entity: fact.Entity, SourceID: fact.SourceID})
		}
		if fact.Status == "confirmed" && current && fact.StoryTime != "" && fact.Location != "" {
			key := [2]string{fact.Entity, fact.StoryTime}
			if positions[key] == nil {
				positions[key] = map[string]bool{}
			}
			positions[key][fact.Location] = true
		}
	}
	for key, locations := range positions {
		if len(locations) > 1 {
			places := sortedKeys(locations)
			findings = append(findings, LintFinding{Kind: "possible_time_conflict", Severity: "review", Title: fmt.Sprintf("Review %s's location at \"%s\"", key[0], key[1]), Detail: "Records contain multiple locations: " + strings.Join(places, "、") + ". The time reference may be broad and needs author review.", Entity: key[0]})
		}
	}
	for _, memory := range latestCompilations(project) {
		if memory.Status != "rejected" && !compilationCurrent(project, memory) {
			findings = append(findings, LintFinding{Kind: "stale_memory", Severity: "warning", Title: "Story memory dependencies changed", Detail: "The source, tracking focus, or cited evidence changed. Recompile this memory before retrieval.", SourceID: memory.SourceID})
		}
	}
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		if a.Entity != b.Entity {
			return a.Entity < b.Entity
		}
		if a.SourceID != b.SourceID {
			return a.SourceID < b.SourceID
		}
		return a.Detail < b.Detail
	})
	result := []LintFinding{}
	for _, item := range findings {
		if len(result) > 0 {
			last := result[len(result)-1]
			if last.Kind == item.Kind && last.Entity == item.Entity && last.SourceID == item.SourceID && last.Detail == item.Detail {
				continue
			}
		}
		result = append(result, item)
	}
	return result
}

// sortedKeys sorts keys for stable graph identifiers and export order.
func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// terms uses words and CJK bigrams for deterministic local retrieval scoring.
func terms(query string) map[string]bool {
	query = lowerText(query)
	result := map[string]bool{}
	for _, term := range strings.FieldsFunc(query, func(char rune) bool { return !unicode.IsLetter(char) && !unicode.IsNumber(char) }) {
		if term != "" {
			result[term] = true
		}
	}
	chars := []rune(query)
	for i := 0; i+1 < len(chars); i++ {
		if chars[i] >= 0x4e00 && chars[i] <= 0x9fff && chars[i+1] >= 0x4e00 && chars[i+1] <= 0x9fff {
			result[string(chars[i:i+2])] = true
		}
	}
	return result
}

// retrieve retrieves current evidence, sources, and approved memories within chapter scope.
func retrieve(project *Project, query string, through uint32, limit int) []ContextItem {
	if limit <= 0 {
		return []ContextItem{}
	}
	items := []ContextItem{}
	for _, source := range project.Sources {
		if source.Category == "legacy" {
			continue
		}
		for index, text := range chunks(source.Content, 900, 100) {
			if score := knowledgeMatchScore(source.Title+" "+text, query); score > 0 {
				items = append(items, ContextItem{ID: fmt.Sprintf("S:%s:%d", source.ID, index+1), Title: source.Title, Kind: source.Category, Text: text, Score: score, SourceID: source.ID, SourceHash: hashText(source.Content), Reason: "Source keyword match"})
			}
		}
	}
	for _, chapter := range project.Chapters {
		if chapter.Number > through {
			continue
		}
		for index, text := range chunks(chapter.Body, 900, 100) {
			if score := knowledgeMatchScore(chapter.Title+" "+text, query); score > 0 {
				items = append(items, ContextItem{ID: fmt.Sprintf("C:%s:%d", chapter.ID, index+1), Title: fmt.Sprintf("Chapter %d — %s", chapter.Number, chapter.Title), Kind: "chapter", Text: text, Score: score, SourceID: chapter.ID, SourceHash: hashText(chapter.Body), Reason: "Chapter keyword match"})
			}
		}
	}
	for _, memory := range latestCompilations(project) {
		_, number, exists := project.SourceText(memory.SourceID)
		if !compilationApproved(project, memory) || !exists || number > through {
			continue
		}
		for index, section := range memory.Sections {
			score := knowledgeMatchScore(section.Summary+" "+sourceTitle(project, memory.SourceID), query)
			if score > 0 {
				items = append(items, ContextItem{ID: fmt.Sprintf("M:%s:%d", memory.ID, index+1), Title: "Story memory · " + sourceTitle(project, memory.SourceID), Kind: "memory", Text: section.Summary + "\nCited evidence: " + strings.Join(section.EvidenceIDs, ", "), Score: score, SourceID: memory.SourceID, SourceHash: memory.SourceHash, Reason: "Author-confirmed story memory"})
			}
		}
	}
	return mergeKnowledgeContext(project, query, through, items, min(limit, 30))
}

// sourceTitle uses complete chapter titles independently of canvas abbreviations.
func sourceTitle(project *Project, sourceID string) string {
	for _, chapter := range project.Chapters {
		if chapter.ID == sourceID {
			return fmt.Sprintf("Chapter %d — %s", chapter.Number, chapter.Title)
		}
	}
	for _, source := range project.Sources {
		if source.ID == sourceID {
			return source.Title
		}
	}
	return "Source no longer exists"
}

// renderEntity exports author setup, bidirectional relationships, and source evidence.
func renderEntity(project *Project, entity *Entity) string {
	output := fmt.Sprintf("<a id=\"entity-%s\"></a>\n\n# %s\n\nType: %s\n\nAliases: %s\n\n## Author setup\n\n%s\n\n## Trajectory and evidence\n\n", entity.ID, entity.Name, entity.Kind, strings.Join(entity.Aliases, "、"), entity.Description)
	facts := []Fact{}
	for _, fact := range project.Facts {
		if (fact.Entity == entity.Name || fact.RelatedEntity == entity.Name) && fact.Status != "rejected" {
			facts = append(facts, fact)
		}
	}
	sort.SliceStable(facts, func(i, j int) bool { return facts[i].Chapter < facts[j].Chapter })
	for _, fact := range facts {
		status := fact.Status
		if !factCurrent(project, &fact) {
			status = "stale"
		}
		related := []string{}
		for _, name := range []string{fact.Entity, fact.RelatedEntity} {
			if name == "" {
				continue
			}
			label := name
			for _, target := range project.Entities {
				if target.Name == name {
					label = fmt.Sprintf("[%s](#entity-%s)", name, target.ID)
					break
				}
			}
			related = append(related, label)
		}
		output += fmt.Sprintf("### Chapter %d · %s\n\n%s\n\n- Status: %s. Evidence type: %s\n- Time: %s. Location: %s\n- Related character: %s\n- Source: %s @ %s\n\n> %s\n\n", fact.Chapter, fact.Kind, fact.Claim, status, fact.Epistemic, fact.StoryTime, fact.Location, strings.Join(related, " / "), fact.SourceID, fact.SourceHash, strings.ReplaceAll(fact.Quote, "\n", "\n> "))
	}
	return output
}

// renderWiki exports the entry index, memories, entities, and compilation history as Markdown.
func renderWiki(project *Project) string {
	output := fmt.Sprintf("# %s · Story Wiki\n\n## Tracking focus\n\n%s\n\n## Entry index\n\n", project.Title, project.WikiPurpose)
	for _, entity := range project.Entities {
		output += fmt.Sprintf("- [%s](#entity-%s) · %s\n", entity.Name, entity.ID, entity.Kind)
	}
	output += "\n## Story memory\n\n"
	for _, memory := range latestCompilations(project) {
		status := memory.Status
		if !compilationCurrent(project, memory) {
			status = "stale"
		}
		output += fmt.Sprintf("### %s\n\nStatus: %s. Compiled: %s. Model: %s\n\n", sourceTitle(project, memory.SourceID), status, memory.CreatedAt, memory.Model)
		for _, section := range memory.Sections {
			output += fmt.Sprintf("%s\n\nEvidence: %s\n\n", section.Summary, strings.Join(section.EvidenceIDs, "、"))
		}
	}
	for _, entity := range project.Entities {
		output += "\n---\n\n" + renderEntity(project, &entity)
	}
	output += "\n## Compilation history\n\n"
	for i := len(project.Compilations) - 1; i >= 0; i-- {
		memory := project.Compilations[i]
		output += fmt.Sprintf("- %s · %s · %s · %d source chunks\n", memory.CreatedAt, sourceTitle(project, memory.SourceID), memory.Status, memory.Chunks)
	}
	return output
}

// validateWikiCandidate shares structural and verbatim-evidence checks across internal and external Wiki paths.
func validateWikiCandidate(fact Candidate, source string) error {
	switch {
	case strings.TrimSpace(fact.Entity) == "" || charCount(fact.Entity) > 80 || strings.TrimSpace(fact.Claim) == "" || charCount(fact.Claim) > 2000:
		return fmt.Errorf("invalid entity name or claim (%s)", truncateSummary(fact.Entity, 40))
	case !contains(FactKinds, fact.Kind) || !contains([]string{"explicit", "inferred"}, fact.Epistemic):
		return fmt.Errorf("unsupported fact kind or evidence type for %q", fact.Entity)
	case fact.EntityKind != "" && !contains(EntityKinds, fact.EntityKind):
		return fmt.Errorf("unsupported entity type for %q", fact.Entity)
	case charCount(strings.TrimSpace(fact.Quote)) < 4 || !strings.Contains(source, fact.Quote):
		return fmt.Errorf("quote not found in source for %q", fact.Entity)
	}
	return nil
}
