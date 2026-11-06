//go:build quillweave_whitebox

package core

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestExtractionRejectsInvalidEvidence rejects fabricated quotes, invalid kinds, and malformed analysis.
func TestExtractionRejectsInvalidEvidence(t *testing.T) {
	source := "Lin Wu found a letter."
	valid := jsonText(map[string]any{"facts": []Candidate{testCandidate()}})
	facts, err := parseCandidates(valid, source)
	assert(t, err == nil && len(facts) == 1, "A valid fact was not parsed")
	for _, raw := range []string{"I think the character is happy", strings.ReplaceAll(valid, "explicit", "certain"), strings.ReplaceAll(valid, "Lin Wu found a letter.", "A quote absent from the source."), `{"facts":null}`} {
		_, err := parseCandidates(raw, source)
		assert(t, err != nil, "Invalid analysis was accepted")
	}
	_, err = parseCandidates("```json\n"+valid+"\n```", source)
	assert(t, err == nil, "A valid fenced JSON block was rejected")
}

// TestWikiIsIdempotentAndPreservesReview checks that repeated compilation preserves deduplication and author review.
func TestWikiIsIdempotentAndPreservesReview(t *testing.T) {
	project := testProject()
	ingestCandidate(t, &project)
	project.Facts[0].Status = "rejected"
	count, err := ingest(&project, project.Chapters[0].ID, hashText(project.Chapters[0].Body), []Candidate{testCandidate()}, "mock")
	assert(t, err == nil && count == 0 && project.Facts[0].Status == "rejected", "Repeated compilation changed review status")
}

// TestStaleEvidenceCannotBeConfirmed checks that changed sources invalidate evidence confirmation and publication.
func TestStaleEvidenceCannotBeConfirmed(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	oldHash := hashText(project.Chapters[0].Body)
	ingestCandidate(t, &project)
	project.Chapters[0].Body += "The chapter has changed."
	assert(t, !factCurrent(&project, &project.Facts[0]), "Old evidence remained current")
	_, err := ingest(&project, project.Chapters[0].ID, oldHash, []Candidate{testCandidate()}, "mock")
	assert(t, err != nil, "A stale asynchronous result was accepted")
	insertProject(t, engine, project)
	reject(t, engine, "review_fact", map[string]any{"project_id": project.ID, "revision": 0, "fact_id": project.Facts[0].ID, "status": "confirmed"})
}

// TestMemoryRejectsFabricatedReferences rejects memories with fabricated evidence IDs or absent support.
func TestMemoryRejectsFabricatedReferences(t *testing.T) {
	project, _ := memoryProject(t)
	for _, ids := range [][]string{{"missing"}, {}} {
		_, err := parseMemory(jsonText(map[string]any{"summary": "Claim", "evidence_ids": ids, "questions": []string{}}), project.Facts)
		assert(t, err != nil, "A fabricated memory was accepted")
	}
	_, err := parseMemory(jsonText(project.Compilations[0].Sections[0]), project.Facts)
	assert(t, err == nil, "A valid memory was rejected")
}

// TestOnlyReviewedMemoryEntersContext requires approved memory and evidence before adding memory to generation context.
func TestOnlyReviewedMemoryEntersContext(t *testing.T) {
	project, _ := memoryProject(t)
	assert(t, !hasKind(retrieve(&project, "Key memory", 1, 30), "memory"), "Pending memory was retrieved")
	project.Compilations[0].Status = "confirmed"
	assert(t, !hasKind(retrieve(&project, "Key memory", 1, 30), "memory"), "Memory with unreviewed evidence was retrieved")
	project.Facts[0].Status = "confirmed"
	assert(t, hasKind(retrieve(&project, "Key memory", 1, 30), "memory"), "Confirmed memory was not retrieved")
	assert(t, !hasKind(retrieve(&project, "Key memory", 0, 30), "memory"), "Future memory was retrieved")
}

// TestCompilationCacheChecksAllDependencies checks source, rules, model, and evidence dependencies in compilation caching.
func TestCompilationCacheChecksAllDependencies(t *testing.T) {
	project, config := memoryProject(t)
	source := project.Chapters[0].ID
	fingerprint, _ := compilationFingerprint(&project, source, config)
	assert(t, cachedCompilation(&project, source, fingerprint) != nil, "A valid cache entry was missed")
	changed := cloneProject(project)
	changed.Chapters[0].Body += "New clue"
	assert(t, cachedCompilation(&changed, source, fingerprint) == nil, "A source change did not invalidate the cache")
	changed = cloneProject(project)
	changed.WikiPurpose = "Focus on character knowledge"
	assert(t, cachedCompilation(&changed, source, fingerprint) == nil, "A rule change did not invalidate the cache")
	changed = cloneProject(project)
	changed.Facts = []Fact{}
	assert(t, cachedCompilation(&changed, source, fingerprint) == nil, "Missing evidence did not invalidate the cache")
	config.Model = "another-model"
	next, _ := compilationFingerprint(&project, source, config)
	assert(t, cachedCompilation(&project, source, next) == nil, "A model change did not invalidate the cache")
}

// TestMemoryReviewRequiresCurrentApprovedEvidence requires current, author-approved evidence before confirming memory.
func TestMemoryReviewRequiresCurrentApprovedEvidence(t *testing.T) {
	engine := testEngine(t)
	project, _ := memoryProject(t)
	insertProject(t, engine, project)
	args := map[string]any{"project_id": project.ID, "revision": 0, "memory_id": project.Compilations[0].ID, "status": "confirmed"}
	reject(t, engine, "review_memory", args)
	invoke[Project](t, engine, "review_fact", map[string]any{"project_id": project.ID, "revision": 0, "fact_id": project.Facts[0].ID, "status": "confirmed"})
	args["revision"] = 1
	approved := invoke[Project](t, engine, "review_memory", args)
	assert(t, approved.Compilations[0].Status == "confirmed", "Memory confirmation did not take effect")
	invoke[Project](t, engine, "save_wiki_purpose", map[string]any{"project_id": project.ID, "revision": 2, "purpose": "Track information concealed by characters"})
	args["revision"] = 3
	reject(t, engine, "review_memory", args)
}

// TestWikiExportContainsStoryStructure checks Wiki indexes, memory, source IDs, and compilation history in exports.
func TestWikiExportContainsStoryStructure(t *testing.T) {
	project, _ := memoryProject(t)
	markdown := renderWiki(&project)
	for _, expected := range []string{"Tracking focus", "Entry index", "Story memory", "Compilation history", "pending", project.Chapters[0].ID, "Lin Wu"} {
		assert(t, strings.Contains(markdown, expected), "Wiki export is missing "+expected)
	}
}

// TestWikiCacheDependsOnBothModels checks cache invalidation after changing only the memory compilation model.
func TestWikiCacheDependsOnBothModels(t *testing.T) {
	project := ExampleProject()
	extraction, synthesis := testProfile("extract").Config, testProfile("compile").Config
	first, err := modelsFingerprint(&project, project.Chapters[0].ID, extraction, extraction)
	if err != nil {
		t.Fatal(err)
	}
	second, err := modelsFingerprint(&project, project.Chapters[0].ID, extraction, synthesis)
	assert(t, err == nil && first != second, "The compilation model was absent from the cache identity")
}

// TestWikiJobRunsEndToEnd checks both Wiki stages, pending review, durable history, and cache reuse.
func TestWikiJobRunsEndToEnd(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	staged := cloneProject(project)
	ingestCandidate(t, &staged)
	memory := jsonText(MemorySection{Summary: "Lin Wu found a letter, beginning the investigation.", EvidenceIDs: []string{staged.Facts[0].ID}, Questions: []string{}})
	server, calls := sequenceModel(t, []string{jsonText(map[string]any{"facts": []Candidate{testCandidate()}}), memory})
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL + "/v1"
	configureModel(t, engine, config)
	args := map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""}
	job := invoke[Job](t, engine, "start_ai", args)
	done := finishJob(t, engine, job)
	assert(t, done.Status == "done", "Wiki task failed: "+done.Error)
	updated := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(updated.Facts) == 1 && updated.Facts[0].Status == "pending" && len(updated.Compilations) == 1 && updated.Compilations[0].Status == "pending", "Pending knowledge was incomplete")
	history := invoke[[]Job](t, engine, "get_runs", map[string]any{"project_id": project.ID})
	assert(t, len(history) == 1 && calls.Load() == 2, "Incorrect task history or call count")
	reused := invoke[Job](t, engine, "start_ai", args)
	assert(t, reused.Status == "done" && strings.Contains(reused.Progress, "Reused") && calls.Load() == 2, "The cache was not reused")
}

// TestFailedSecondStageCommitsNothing checks atomic failure when the second compilation stage is invalid.
func TestFailedSecondStageCommitsNothing(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	server, _ := sequenceModel(t, []string{jsonText(map[string]any{"facts": []Candidate{testCandidate()}}), "Not JSON"})
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL + "/v1"
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""})
	done := finishJob(t, engine, job)
	assert(t, done.Status == "error", "Invalid memory did not fail")
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(latest.Facts) == 0 && len(latest.Compilations) == 0 && latest.Revision == 0, "Failure produced a partial write")
}

// TestWikiRevalidatesRulesBeforeCommit checks tracking-rule freshness before Wiki publication.
func TestWikiRevalidatesRulesBeforeCommit(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	staged := cloneProject(project)
	ingestCandidate(t, &staged)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		index := calls.Add(1)
		text := jsonText(map[string]any{"facts": []Candidate{testCandidate()}})
		if index == 2 {
			close(entered)
			select {
			case <-release:
			case <-request.Context().Done():
				return
			}
			text = jsonText(MemorySection{Summary: "Finds a letter", EvidenceIDs: []string{staged.Facts[0].ID}, Questions: []string{}})
		}
		fmt.Fprint(writer, streamPayload(text))
	})
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL + "/v1"
	configureModel(t, engine, config)
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": project.Chapters[0].ID, "action": "wiki", "instruction": ""})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("Stage two was not reached")
	}
	invoke[Project](t, engine, "save_wiki_purpose", map[string]any{"project_id": project.ID, "revision": 0, "purpose": "New tracking focus"})
	close(release)
	done := finishJob(t, engine, job)
	assert(t, done.Status == "error" && strings.Contains(done.Error, "Tracking focus changed"), "Analysis used outdated tracking rules")
	latest := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, latest.Revision == 1 && len(latest.Facts) == 0 && len(latest.Compilations) == 0, "Concurrent revision left partial knowledge")
}
