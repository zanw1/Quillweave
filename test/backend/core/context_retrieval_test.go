//go:build quillweave_whitebox

package core

import (
	"fmt"
	"strings"
	"testing"
)

// TestRetiredMemoryDoesNotReturnAsChapterHistory distinguishes revoked interpretations from valid historical intervals.
func TestRetiredMemoryDoesNotReturnAsChapterHistory(t *testing.T) {
	for _, test := range []struct {
		name, status, state string
		through, end, next  uint32
		visible             bool
	}{
		{"retired at opening", "superseded", "superseded_by_rebuild", 1, 1400, 0, false},
		{"retired at latest chapter", "superseded", "superseded_by_rebuild", 260, 1400, 0, false},
		{"normalized retirement marker", "superseded", " SUPERSEDED_BY_REBUILD ", 1, 1400, 0, false},
		{"revoked interpretation with later replacement", "superseded", "superseded_by_rebuild", 1, 1400, 10, false},
		{"confirmed rule mentions retirement", "confirmed", "active", 1, 1400, 0, true},
		{"bounded historical state", "superseded", "alive", 3, 9, 0, true},
		{"before linked replacement", "superseded", "alive", 3, 19, 20, true},
		{"at linked replacement", "superseded", "alive", 20, 1400, 20, false},
		{"rejected rule", "rejected", "active", 1, 1400, 0, false},
		{"unaccepted rule", "proposed", "active", 1, 1400, 0, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			project := testProject()
			project.Chapters[0].Number = test.through
			marker := "MEMORY_VISIBLE_MARKER"
			project.Studio.Memories = []StoryMemory{{ID: "old-memory", Kind: "rule", Title: "A rule", Content: marker + " mentions superseded_by_rebuild without revoking this text", Status: test.status, State: test.state, Chapter: 1, ThroughChapter: test.end}}
			if test.next > 0 {
				project.Studio.Memories = append(project.Studio.Memories, StoryMemory{ID: "replacement", Kind: "rule", Title: "New rule", Content: "CURRENT_RULE", Status: "confirmed", State: "active", Chapter: test.next, Supersedes: "old-memory"})
			}
			for path, value := range map[string]string{
				"writing recall": jsonText(memoryContext(&project, test.through, marker)),
				"knowledge view": jsonText(storyKnowledgeView(&project, test.through, marker, false)),
				"agent snapshot": buildStoryBibleLayer1(&project, 64000, nil, test.through),
			} {
				assert(t, strings.Contains(value, marker) == test.visible, "A revoked interpretation or legitimate history was misclassified in "+path)
			}
			filtered := filterStoryKnowledgeContext(&project, AIRequest{ChapterID: project.Chapters[0].ID, Action: "ask"}, []ContextItem{{ID: "N:old-memory", Kind: "rule", Text: marker}})
			assert(t, strings.Contains(jsonText(filtered), marker) == test.visible, "A previously retrieved item bypassed retirement or lost its historical interval")
		})
	}
}

// TestChapterSynopsisDoesNotReviveReplacedPlan keeps derived snapshots out while preserving distinct author notes.
func TestChapterSynopsisDoesNotReviveReplacedPlan(t *testing.T) {
	project := testProject()
	old := StoryPlan{ID: "chapter-plan", Level: "chapter", Title: "The decision", StartChapter: 1, EndChapter: 1, Status: "confirmed", Goal: "OBSOLETE_SCENE_REQUIREMENT"}
	project.Chapters[0].Synopsis = planSynopsis(&project, old)
	current := old
	current.Goal = "CURRENT_SCENE_REQUIREMENT"
	project.Studio.Plans = []StoryPlan{current}
	project.Studio.ChangeSets = []ChangeSet{{ID: "updated-plan", Status: ChangeSetApplied, Operations: []ChangeOperation{{TargetKind: TargetPlan, TargetID: old.ID, OpType: OpUpsert, BeforeValue: rawMessage(old), AfterValue: rawMessage(current)}}}}
	request := AIRequest{ChapterID: project.Chapters[0].ID, Action: "continue", Instruction: "Continue", WorkspaceContext: &WorkspaceContext{ActiveChapterID: project.Chapters[0].ID, ActivePlanID: current.ID}}
	for _, action := range []string{"continue", "rewrite", "agent", "ask"} {
		request.Action = action
		prompt, err := prepareAI(&project, request, 24000)
		assert(t, err == nil, fmt.Sprint(err))
		text := jsonText(prompt.Messages)
		assert(t, strings.Contains(text, current.Goal) && !strings.Contains(text, old.Goal), "A copied old synopsis competed with the updated structured chapter plan: "+action)
	}
	engine := testEngine(t)
	for _, tool := range []string{"get_chapter_window", "read_chapters"} {
		text, err := engine.executeScopedAgentTool(&project, Job{ProjectID: project.ID}, agentStepEnvelope{Tool: tool, Chapter: 1})
		assert(t, err == nil && !strings.Contains(text, old.Goal) && strings.Contains(text, project.Chapters[0].Body), "A chapter read restored the old generated plan or lost the saved prose")
	}
	assert(t, project.Chapters[0].Synopsis == planSynopsis(&project, old), "Context deduplication changed the author's stored text")
	project.Chapters[0].Synopsis = "AUTHOR_NOTE: Keep the chipped cup on the table."
	prompt, err := prepareAI(&project, request, 24000)
	assert(t, err == nil && strings.Contains(jsonText(prompt.Messages), project.Chapters[0].Synopsis), "Distinct author notes were discarded with the generated plan snapshot")
}

// TestUnscopedSceneUsesParentChapter keeps chapterless child plans inside their inherited narrative window.
func TestUnscopedSceneUsesParentChapter(t *testing.T) {
	project := testProject()
	project.Studio.Plans = []StoryPlan{
		{ID: "one", Level: "chapter", Title: "First", StartChapter: 1, EndChapter: 1, Status: "confirmed"},
		{ID: "two", Level: "chapter", Title: "Second", StartChapter: 2, EndChapter: 2, Status: "confirmed"},
		{ID: "scene-one", ParentID: "one", Level: "scene", Title: "First scene", Goal: "FIRST_CHAPTER_ONLY", Status: "confirmed"},
		{ID: "scene-two", ParentID: "two", Level: "scene", Title: "Second scene", Goal: "SECOND_CHAPTER_ONLY", Status: "confirmed"},
	}
	assert(t, validateStudio(&project.Studio) == nil, "Inherited scene ranges were rejected before they could be saved")
	for _, test := range []struct {
		name, focus string
		chapter     uint32
		omitted     []string
		first, next bool
	}{
		{name: "first chapter", chapter: 1, first: true},
		{name: "second chapter", chapter: 2, next: true},
		{name: "outside both chapters", chapter: 3},
		{name: "explicit other scene", chapter: 1, focus: "scene-two", first: true, next: true},
		{name: "omit first scene", chapter: 1, omitted: []string{"scene-one"}},
		{name: "omit second scene", chapter: 2, omitted: []string{"scene-two"}},
		{name: "omit inherited first scope", chapter: 1, omitted: []string{"one"}},
		{name: "omit inherited second scope", chapter: 2, omitted: []string{"two"}},
		{name: "explicit scene without parent context", chapter: 1, focus: "scene-one", omitted: []string{"one"}, first: true},
		{name: "other parent does not select its children", chapter: 2, focus: "one", next: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			project.Chapters[0].Number = test.chapter
			workspace := &WorkspaceContext{ActivePlanID: test.focus, OmitPlanIDs: test.omitted}
			prompt, err := prepareAI(&project, AIRequest{ChapterID: project.Chapters[0].ID, Action: "continue", Instruction: "Continue", WorkspaceContext: workspace}, 24000)
			assert(t, err == nil, fmt.Sprint(err))
			text := jsonText(prompt.Messages)
			assert(t, strings.Contains(text, "FIRST_CHAPTER_ONLY") == test.first && strings.Contains(text, "SECOND_CHAPTER_ONLY") == test.next, "A scene without its own chapter range leaked or lost its explicit selection")
		})
	}
}

// TestRetrievalFiltersFuturePendingAndStale excludes future, pending, and stale facts from retrieval.
func TestRetrievalFiltersFuturePendingAndStale(t *testing.T) {
	project := ExampleProject()
	for i := range project.Facts {
		project.Facts[i].Status = "confirmed"
	}
	items := retrieve(&project, "Lin Wu", 1, 30)
	assert(t, hasKind(items, "wiki"), "Missing valid evidence")
	for _, fact := range project.Facts {
		if fact.Chapter > 1 {
			for _, item := range items {
				assert(t, item.ID != "W:"+fact.ID, "Future facts leaked into retrieval")
			}
		}
	}
	for i := range project.Facts {
		project.Facts[i].Status = "pending"
	}
	assert(t, !hasKind(retrieve(&project, "Lin Wu", 3, 30), "wiki"), "Pending facts were retrieved")
}

// TestContextBudgetRefusesSilentTruncation rejects over-budget context and retains complete text when the budget permits.
func TestContextBudgetRefusesSilentTruncation(t *testing.T) {
	project := testProject()
	project.Chapters[0].Body = strings.Repeat("\u4e2d\u6587\u6b63\u6587", 4000)
	request := AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: "review", Instruction: "Review consistency", History: []Message{}}
	_, err := prepareAI(&project, request, 4000)
	assert(t, err != nil && strings.Contains(err.Error(), "exceeds"), "Over-budget context was silently truncated")
	prompt, err := prepareAI(&project, request, 24000)
	assert(t, err == nil && strings.Contains(prompt.Messages[len(prompt.Messages)-1].Content, project.Chapters[0].Body), "The prompt did not include the complete chapter")
}

// TestRelationshipRetrievalObeysReviewAndChapterScope keeps relationship expansion within reviewed evidence and chapter scope.
func TestRelationshipRetrievalObeysReviewAndChapterScope(t *testing.T) {
	project := NewProject("Graph test", "", " ")
	bodies := []string{"Mara knows Theo and hands over the key.", "Theo reads the sailing log alone.", "Mara exchanges letters with Iris."}
	for index, body := range bodies {
		chapter := NewChapter(uint32(index+1), "Chapter")
		chapter.Body = body
		project.Chapters = append(project.Chapters, chapter)
	}
	for index, item := range [][3]string{{"Mara", "Theo", "Hands over the key"}, {"Theo", "", "Reads the log"}, {"Mara", "Iris", "Exchanges letters"}} {
		chapter := project.Chapters[index]
		_, err := ingest(&project, chapter.ID, hashText(chapter.Body), []Candidate{{Entity: item[0], RelatedEntity: item[1], Claim: item[2], Quote: chapter.Body, Kind: "relationship", Epistemic: "explicit"}}, "mock")
		if err != nil {
			t.Fatal(err)
		}
	}
	for i := range project.Facts {
		project.Facts[i].Status = "confirmed"
	}
	items := retrieve(&project, "Mara", 2, 30)
	found := false
	for _, item := range items {
		found = found || item.Reason == "Character relationship" && strings.Contains(item.Text, "Reads the log")
		assert(t, !strings.Contains(item.Text, "Exchanges letters"), "Retrieval expanded into future evidence")
	}
	assert(t, found, "Relationship expansion was missing")
	project.Facts[0].Status = "pending"
	for _, item := range retrieve(&project, "Mara", 2, 30) {
		assert(t, !(item.Kind == "wiki" && strings.Contains(item.Text, "Reads the log")), "Retrieval expanded through a pending relationship")
	}
}

// TestCompilationApprovalMatchesEveryContextPath revokes generation access while retaining reusable compilation work.
func TestCompilationApprovalMatchesEveryContextPath(t *testing.T) {
	engine := testEngine(t)
	project, config := memoryProject(t)
	project.Facts[0].Status = "confirmed"
	project.Compilations[0].Status = "confirmed"
	insertProject(t, engine, project)
	request := AIRequest{Action: "rewrite", ChapterID: project.Chapters[0].ID, Instruction: "Key memory"}
	items, err := engine.contextPassages(&project, request)
	assert(t, err == nil && hasKind(items, "memory"), "Confirmed evidence did not admit the compiled memory")
	project = invoke[Project](t, engine, "review_fact", map[string]any{"project_id": project.ID, "revision": project.Revision, "fact_id": project.Facts[0].ID, "status": "pending"})
	items, err = engine.contextPassages(&project, request)
	assert(t, err == nil && !hasKind(items, "memory"), "The indexed context retained memory after its fact approval was withdrawn")
	assert(t, !hasKind(retrieve(&project, "Key memory", 1, 30), "memory"), "Keyword context disagreed about withdrawn evidence")
	fingerprint, err := compilationFingerprint(&project, project.Chapters[0].ID, config)
	assert(t, err == nil && cachedCompilation(&project, project.Chapters[0].ID, fingerprint) != nil, "Withdrawing approval unnecessarily discarded reusable extraction work")
	project = invoke[Project](t, engine, "review_fact", map[string]any{"project_id": project.ID, "revision": project.Revision, "fact_id": project.Facts[0].ID, "status": "confirmed"})
	items, err = engine.contextPassages(&project, request)
	assert(t, err == nil && hasKind(items, "memory"), "Reconfirming current evidence did not restore the summary")
}

// TestAgentBibleUsesTheRequestedNarrativeWindow excludes later observations even with a stored all-book baseline.
func TestAgentBibleUsesTheRequestedNarrativeWindow(t *testing.T) {
	project := testProject()
	later := NewChapter(260, "Later event")
	later.Body = "The teacher dies after the final lesson."
	project.Chapters = append(project.Chapters, later)
	project.Studio.Memories = []StoryMemory{
		{ID: "future-fact", Kind: "fact", Status: "confirmed", Chapter: 260, Content: "FUTURE_OBSERVATION_MARKER", SourceID: later.ID, SourceHash: hashText(later.Body), Quote: later.Body},
		{ID: "author-rule", Kind: "rule", Status: "confirmed", ThroughChapter: 1400, Content: "AUTHOR_RULE_MARKER"},
		{ID: "author-plan", Kind: "plan", Status: "confirmed", Content: "AUTHOR_PLAN_MARKER"},
	}
	baseline := buildStoryBibleLayer1(&project, 64000, nil)
	assert(t, strings.Contains(baseline, "FUTURE_OBSERVATION_MARKER"), "The control baseline did not contain the later observation")
	assert(t, strings.Contains(baseline, "AUTHOR_RULE_MARKER"), "An unscoped discussion dropped rules valid through the current manuscript")
	for _, workspaceOnly := range []bool{false, true} {
		request := AIRequest{Action: "agent", Instruction: "Discuss the selected chapter", ChapterID: project.Chapters[0].ID, conversation: &Conversation{BibleBaseline: baseline, LastBibleHash: hashText(baseline)}}
		if workspaceOnly {
			request.ChapterID = ""
			request.WorkspaceContext = &WorkspaceContext{ActiveChapterID: project.Chapters[0].ID}
		}
		prompt, err := prepareAI(&project, request, 64000)
		assert(t, err == nil, "The scoped Agent request could not be prepared")
		text := jsonText(prompt.Messages)
		assert(t, !strings.Contains(text, "FUTURE_OBSERVATION_MARKER"), "A stored all-book baseline restored a future observation")
		assert(t, strings.Contains(text, "AUTHOR_RULE_MARKER") && strings.Contains(text, "AUTHOR_PLAN_MARKER"), "Scoping removed the author's standing rules or planned story")
	}
	late, err := prepareAI(&project, AIRequest{Action: "agent", ChapterID: later.ID, Instruction: "Discuss the later chapter"}, 64000)
	assert(t, err == nil && strings.Contains(jsonText(late.Messages), "FUTURE_OBSERVATION_MARKER"), "The later chapter lost its own confirmed observation")
}

// TestKnowledgeCandidatesExposeOnlyCurrentQuotes keeps unreviewed interpretations useful as original-source navigation.
func TestKnowledgeCandidatesExposeOnlyCurrentQuotes(t *testing.T) {
	project := testProject()
	chapter := project.Chapters[0]
	project.Entities = []Entity{{ID: "lin", Name: "Lin Wu", Kind: "character", Aliases: []string{"The visitor"}}}
	project.Studio.Memories = []StoryMemory{{ID: "candidate", Kind: "knowledge", Entity: "Lin Wu", Title: "Letter", Content: "UNSUPPORTED_INTERPRETATION", Status: "proposed", Chapter: 1, SourceID: chapter.ID, SourceHash: hashText(chapter.Body), Quote: "Lin Wu found a letter.", KnownBy: []string{"Lin Wu"}, Epistemic: "inferred"}}
	items := retrieve(&project, "The visitor", 1, 30)
	assert(t, hasKind(items, "candidate_source"), "A current candidate did not expose its original excerpt")
	assert(t, !strings.Contains(jsonText(items), "UNSUPPORTED_INTERPRETATION"), "The unreviewed claim was promoted into prompt text")
	assert(t, strings.Contains(jsonText(items), chapter.ID) && strings.Contains(jsonText(items), "Lin Wu found a letter."), "Candidate navigation lost its exact source")
	assert(t, !hasKind(retrieve(&project, "The visitor", 0, 30), "candidate_source"), "A future candidate was recalled")
	project.Chapters[0].Body += " Revised."
	assert(t, !hasKind(retrieve(&project, "The visitor", 1, 30), "candidate_source"), "A stale candidate remained retrievable")
}

// TestKnowledgeFilteringPrecedesRanking refills context after omitted entities and sources are excluded.
func TestKnowledgeFilteringPrecedesRanking(t *testing.T) {
	project := testProject()
	chapter := project.Chapters[0]
	project.Entities = []Entity{{ID: "lin", Name: "Lin Wu", Kind: "character", Aliases: []string{}, Description: "Private profile"}}
	project.Studio.Memories = []StoryMemory{
		{ID: "omitted", Kind: "knowledge", Entity: "Lin Wu", Title: "Secret", Content: "OMITTED_ENTITY_SECRET", Status: "confirmed", Chapter: 1, SourceID: chapter.ID, SourceHash: hashText(chapter.Body), Quote: chapter.Body},
		{ID: "global", Kind: "rule", Title: "Rule", Content: "GLOBAL_AUTHOR_RULE", Status: "confirmed"},
	}
	request := AIRequest{Action: "review", ChapterID: chapter.ID, WorkspaceContext: &WorkspaceContext{OmitEntityIDs: []string{"lin"}}}
	items := mergeKnowledgeContext(&project, "Lin Wu", 1, nil, 1, request)
	assert(t, strings.Contains(jsonText(items), "GLOBAL_AUTHOR_RULE") && !strings.Contains(jsonText(items), "OMITTED_ENTITY_SECRET"), "Excluded records consumed the recall budget")
	request.WorkspaceContext = &WorkspaceContext{OmitChapter: true}
	items = inspectableContext(&project, request, storyKnowledgeContext(&project, "Lin Wu", 1))
	for _, item := range items {
		assert(t, item.SourceID != chapter.ID, "A source-backed record bypassed chapter exclusion")
	}
}

// TestKnowledgeRelationsAndMapRespectTheWindow covers alias seeds, one-hop graph expansion and future-only places.
func TestKnowledgeRelationsAndMapRespectTheWindow(t *testing.T) {
	project := testProject()
	first := project.Chapters[0]
	second := NewChapter(2, "Later")
	second.Body = "Theo visits the future island."
	project.Chapters = append(project.Chapters, second)
	project.Entities = []Entity{{ID: "lin", Name: "Lin Wu", Aliases: []string{"The visitor"}, Kind: "character"}, {ID: "theo", Name: "Theo", Kind: "character"}}
	project.Facts = []Fact{
		{ID: "link", Entity: "Lin Wu", RelatedEntity: "Theo", Kind: "relationship", Claim: "Meets Theo", Status: "confirmed", Chapter: 1, SourceID: first.ID, SourceHash: hashText(first.Body), Quote: first.Body},
		{ID: "theo-state", Entity: "Theo", Kind: "knowledge", Claim: "RELATED_STATE", Status: "confirmed", Chapter: 1, SourceID: first.ID, SourceHash: hashText(first.Body), Quote: first.Body},
		{ID: "future-spoof", Entity: "Theo", Kind: "event", Claim: "FUTURE_SOURCE_MARKER", Status: "confirmed", Chapter: 1, SourceID: second.ID, SourceHash: hashText(second.Body), Quote: second.Body},
	}
	project.Studio.Locations = []WorldLocation{{ID: "port", Name: "Port", EntityIDs: []string{"theo"}, ChapterIDs: []string{first.ID}}, {ID: "island", Name: "Future island", ChapterIDs: []string{second.ID}}}
	project.Studio.Routes = []WorldRoute{{ID: "future-route", FromID: "port", ToID: "island", TravelHours: 12}}
	items := retrieve(&project, "The visitor", 1, 30)
	text := jsonText(items)
	assert(t, strings.Contains(text, "RELATED_STATE") && strings.Contains(text, "L:port"), "The one-hop entity state or linked place was missing")
	assert(t, !strings.Contains(text, "FUTURE_SOURCE_MARKER") && !strings.Contains(text, "future-route"), "Source chapter or future map links escaped scope checking")
}

// TestChineseShortNamesSupplementNonemptyBM25 keeps two-character names available even when longer terms match.
func TestChineseShortNamesSupplementNonemptyBM25(t *testing.T) {
	engine := testEngine(t)
	project := NewProject("Recall", "", "")
	for i, body := range []string{"城里正在讨论失踪案件。", "林舟把钥匙藏进抽屉。"} {
		chapter := NewChapter(uint32(i+1), "Scene")
		chapter.Body = body
		project.Chapters = append(project.Chapters, chapter)
	}
	insertProject(t, engine, project)
	items, err := engine.store.SearchPassages(project.ID, "失踪案件 林舟", 2)
	assert(t, err == nil, "Hybrid local recall failed")
	assert(t, strings.Contains(jsonText(items), "林舟把钥匙"), "A successful trigram match suppressed the short-name candidate")
	previous := 1000
	for _, item := range items {
		if strings.Contains(item.Reason, "BM25 rank") {
			assert(t, item.Score < previous, "BM25 order was flattened")
			previous = item.Score
		}
	}
}

// TestScopedOutlineUsesCurrentPlans prevents a stale duplicated overview from competing with the selected chapter plan.
func TestScopedOutlineUsesCurrentPlans(t *testing.T) {
	project := testProject()
	project.Outline = strings.Repeat("OBSOLETE_GLOBAL_CHAPTER_PLAN ", 300)
	project.Premise, project.Style = "UNIQUE_PREMISE", "UNIQUE_AUTHOR_STYLE"
	project.Studio.NarrativeFocus = project.Premise
	project.Studio.Preferences = []string{project.Premise, project.Style, "Retain the quiet conversation", "Retain the quiet conversation"}
	project.Studio.Plans = []StoryPlan{{ID: "current-outline", Level: "chapter", Title: "Current chapter", StartChapter: 1, EndChapter: 1, Goal: "CURRENT_SCENE_GOAL", Status: "confirmed"}}
	prompt, err := prepareAI(&project, AIRequest{ChapterID: project.Chapters[0].ID, Action: "continue", Instruction: "Continue the current scene"}, 7000)
	assert(t, err == nil, fmt.Sprint(err))
	text := jsonText(prompt.Messages)
	assert(t, strings.Contains(text, "CURRENT_SCENE_GOAL") && !strings.Contains(text, "OBSOLETE_GLOBAL_CHAPTER_PLAN"), "The local task revived the old global chapter overview")
	assert(t, strings.Count(text, "UNIQUE_PREMISE") == 1 && strings.Count(text, "UNIQUE_AUTHOR_STYLE") == 1 && strings.Count(text, "Retain the quiet conversation") == 1, "Duplicated premise, style or preferences crowded the scene context")
	omitted, err := prepareAI(&project, AIRequest{ChapterID: project.Chapters[0].ID, Action: "continue", Instruction: "Continue", WorkspaceContext: &WorkspaceContext{OmitPlanIDs: []string{"current-outline"}}}, 7000)
	assert(t, err == nil && !strings.Contains(jsonText(omitted.Messages), "CURRENT_SCENE_GOAL") && !strings.Contains(jsonText(omitted.Messages), "OBSOLETE_GLOBAL_CHAPTER_PLAN"), "A removed plan leaked back through the legacy overview")
	project.Studio.Plans = nil
	legacy, err := prepareAI(&project, AIRequest{ChapterID: project.Chapters[0].ID, Action: "continue", Instruction: "Continue"}, 24000)
	assert(t, err == nil && strings.Contains(jsonText(legacy.Messages), "OBSOLETE_GLOBAL_CHAPTER_PLAN"), "An author without structured plans lost the free-form outline")
}

// TestAgentKeepsCompleteHardRules reserves full author constraints outside the optional, excerpted story index.
func TestAgentKeepsCompleteHardRules(t *testing.T) {
	project := testProject()
	for i := 0; i < 30; i++ {
		project.Entities = append(project.Entities, Entity{ID: fmt.Sprint(i), Name: fmt.Sprintf("Person %d", i), Kind: "character", Description: strings.Repeat("Unrelated biography. ", 20)})
	}
	project.Studio.Memories = []StoryMemory{{ID: "complete-rule", Kind: "rule", Title: "Ability limit", Content: strings.Repeat("The ability has a cost. ", 50) + "MANDATORY_RULE_END", Status: "confirmed"}}
	request := AIRequest{Action: "agent", ChapterID: project.Chapters[0].ID, Instruction: "Discuss the next choice"}
	prompt, err := prepareAI(&project, request, 12000)
	assert(t, err == nil && strings.Contains(jsonText(prompt.Messages), "MANDATORY_RULE_END"), "The optional story index silently clipped a hard rule")
	found := false
	for _, item := range prompt.Context {
		found = found || item.ID == "N:complete-rule" && item.Required && strings.Contains(item.Text, "MANDATORY_RULE_END")
	}
	assert(t, found, "Complete rules were not exposed as required inspectable evidence")
	request.WorkspaceContext = &WorkspaceContext{OmitMemoryIDs: []string{"complete-rule"}}
	omitted, err := prepareAI(&project, request, 12000)
	assert(t, err == nil && !strings.Contains(jsonText(omitted.Messages), "MANDATORY_RULE_END"), "An explicitly omitted hard rule re-entered the prompt")
	request.WorkspaceContext = nil
	_, err = prepareAI(&project, request, 3000)
	assert(t, err != nil, "A budget too small for required author constraints silently dropped them")
}
