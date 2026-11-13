//go:build quillweave_whitebox

package core

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestExternalWikiConcurrentUpgrade keeps one intact legacy backup when multiple MCP processes initialize together.
func TestExternalWikiConcurrentUpgrade(t *testing.T) {
	path := filepath.Join(t.TempDir(), "concurrent.sqlite3")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	project := NewProject("Upgrade fixture", "Fantasy", "A keeper opens a gate")
	_, err = store.db.Exec("INSERT INTO projects(id,revision,document) VALUES(?,?,?)", project.ID, project.Revision, jsonText(project))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.db.Exec("CREATE TABLE experiments(id TEXT PRIMARY KEY,project_id TEXT,document TEXT); INSERT INTO experiments VALUES('old','legacy','{}'); PRAGMA user_version=5")
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	var wait sync.WaitGroup
	results := make(chan error, 6)
	for range 6 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			opened, err := OpenStore(path)
			if err == nil {
				err = opened.Close()
			}
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	backup, err := sql.Open("sqlite", path+".before-v6.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	var count int
	if err := backup.QueryRow("SELECT COUNT(*) FROM experiments").Scan(&count); err != nil || count != 1 {
		t.Fatal("Concurrent migration damaged the legacy backup", err)
	}
}

type wikiTestView struct {
	ID              string `json:"wiki_session_id"`
	Revision        uint64 `json:"session_revision"`
	Phase           string `json:"phase"`
	CandidateID     string `json:"candidate_id"`
	EvidenceVersion string `json:"evidence_version"`
}

// wikiMutation binds one test action to its current durable checkpoint.
func wikiMutation(session ExternalWikiSession, request string) WikiMutation {
	return WikiMutation{WikiSessionRequest: WikiSessionRequest{ProjectID: session.ProjectID, WikiSessionID: session.ID}, RequestID: request, SessionRevision: session.Revision}
}

// wikiStored reads the real session table after a public service operation.
func wikiStored(t *testing.T, service *ExternalService, id string) ExternalWikiSession {
	t.Helper()
	session, err := readWikiSession(service.engine.store.db, service.options.ProjectID, id)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

// wikiFixture creates a source with more than fifty independently located evidence records.
func wikiFixture(t *testing.T, count int) (*Engine, *ExternalService, Project, ExternalWikiSession, []Candidate) {
	t.Helper()
	engine, service, project := externalFixture(t)
	candidates := []Candidate{}
	lines := []string{}
	for i := 0; i < count; i++ {
		quote := fmt.Sprintf("林无将第%03d封信放进木匣，随后锁好抽屉。", i)
		lines = append(lines, quote)
		candidates = append(candidates, Candidate{Entity: "林无", EntityKind: "character", Kind: "event", Claim: fmt.Sprintf("林无收好第%d封信。", i), Quote: quote, Epistemic: "explicit"})
	}
	if count == 0 {
		lines = append(lines, "林无静坐片刻。")
	}
	project.Chapters[0].Body = strings.Join(lines, "\n")
	if err := engine.store.Save(&project, project.Revision); err != nil {
		t.Fatal(err)
	}
	view := externalInvoke[wikiTestView](t, service, "wiki_begin", WikiBeginInput{RequestID: "wiki-start", SourceID: project.Chapters[0].ID})
	return engine, service, project, wikiStored(t, service, view.ID), candidates
}

// wikiExtractAll checkpoints every block, including blocks with no matching evidence.
func wikiExtractAll(t *testing.T, service *ExternalService, session ExternalWikiSession, candidates []Candidate) ExternalWikiSession {
	t.Helper()
	for _, chunk := range session.Chunks {
		block := string([]rune(session.SourceText)[chunk.Offset:chunk.End])
		facts := []Candidate{}
		for _, fact := range candidates {
			if strings.Contains(block, fact.Quote) {
				facts = append(facts, fact)
			}
		}
		externalInvoke[wikiTestView](t, service, "wiki_submit_extraction", WikiExtractionInput{WikiMutation: wikiMutation(session, session.ID+chunk.ID), ChunkID: chunk.ID, SourceHash: session.SourceHash, Facts: facts})
		session = wikiStored(t, service, session.ID)
	}
	return session
}

// wikiReviewAll submits exact content hashes through the public review and compilation contracts.
func wikiReviewAll(t *testing.T, service *ExternalService, session ExternalWikiSession) ExternalWikiSession {
	t.Helper()
	for start := 0; start < len(session.Facts); start += 100 {
		reviews := []WikiFactReview{}
		for _, fact := range session.Facts[start:min(start+100, len(session.Facts))] {
			reviews = append(reviews, WikiFactReview{FactID: fact.ID, FactHash: wikiFactHash(fact), Decision: "accept", Reason: "The quoted original passage explicitly supports this fixture fact."})
		}
		externalInvoke[wikiTestView](t, service, "wiki_submit_review", WikiReviewInput{WikiMutation: wikiMutation(session, fmt.Sprintf("%s-review-%d-%d", session.ID, start, session.Revision)), EvidenceVersion: session.EvidenceVersion, Reviews: reviews})
		session = wikiStored(t, service, session.ID)
	}
	if session.Phase == "awaiting_compilation" {
		ids := []string{}
		for _, fact := range wikiAcceptedFacts(&session) {
			ids = append(ids, fact.ID)
		}
		externalInvoke[wikiTestView](t, service, "wiki_submit_compilation", WikiCompilationInput{WikiMutation: wikiMutation(session, session.ID+"-compile"), EvidenceVersion: session.EvidenceVersion, Sections: []MemorySection{{Summary: "林无逐封整理信件并将信件锁入抽屉。", EvidenceIDs: ids, Questions: []string{}}}})
		session = wikiStored(t, service, session.ID)
	}
	return session
}

// wikiReviewToken reads every actual review window and returns only the final version-bound token.
func wikiReviewToken(t *testing.T, service *ExternalService, id string) string {
	t.Helper()
	for offset := 0; ; {
		window := externalInvoke[candidateReviewWindow](t, service, "review_candidate", ExternalReadRequest{ID: id, Offset: offset, MaxChars: 1200})
		if !window.Truncated {
			return window.Token
		}
		if window.Token != "" || window.Next <= offset {
			t.Fatal("Invalid review continuation")
		}
		offset = window.Next
	}
}

// TestExternalWikiFullFlow proves large compound candidates, author confirmation and retrieval integration.
func TestExternalWikiFullFlow(t *testing.T) {
	engine, service, project, session, facts := wikiFixture(t, 70)
	session = wikiExtractAll(t, service, session, facts)
	assert(t, session.Phase == "awaiting_review" && len(session.Facts) == 70, "Extraction lost source records")
	session = wikiReviewAll(t, service, session)
	view := externalInvoke[wikiTestView](t, service, "wiki_finish", wikiMutation(session, "wiki-finish"))
	assert(t, view.Phase == "staged" && view.CandidateID != "", "Wiki did not stage a candidate")
	before := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(before.Facts) == 0 && len(before.Compilations) == 0, "Staging changed live evidence")
	token := wikiReviewToken(t, service, view.CandidateID)
	assert(t, token != "", "Complete Wiki review did not return a confirmation token")
	confirmation := CandidateConfirmation{RequestID: "wiki-confirm", CandidateID: view.CandidateID, ConfirmToken: token}
	first := externalInvoke[map[string]any](t, service, "confirm_candidate", confirmation)
	second := externalInvoke[map[string]any](t, service, "confirm_candidate", confirmation)
	assert(t, jsonText(first) == jsonText(second), "Confirmation replay changed its receipt")
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(saved.Facts) == 70 && len(saved.Compilations) == 1 && compilationApproved(&saved, &saved.Compilations[0]), "Confirmed summaries are unusable by writing retrieval")
	assert(t, wikiStored(t, service, session.ID).Phase == "completed" && len(engine.jobs) == 0, "Wiki session failed to finish without internal model work")
	for _, fact := range saved.Facts {
		assert(t, fact.Provenance != nil && fact.Provenance.WikiSessionID == session.ID, "Fact lost external attribution")
	}
	page := externalInvoke[ExternalPage](t, service, "list_wiki_records", WikiRecordsRequest{Kind: "fact", SourceID: session.SourceID, Limit: 10})
	assert(t, page.Total == 70 && len(page.Items) == 10 && page.NextCursor != "", "Wiki record pagination lost coverage")
	read := externalInvoke[map[string]any](t, service, "get_record", ExternalReadRequest{Kind: "compilation", ID: saved.Compilations[0].ID, MaxChars: 100})
	assert(t, read["truncated"] == true, "Focused compilation read ignored the window budget")
	archive := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": project.ID, "format": "archive"})
	restored := invoke[Project](t, engine, "import_archive", map[string]any{"content": archive["content"]})
	reader, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: restored.ID, Client: "restored"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	restoredSessions := externalInvoke[ExternalPage](t, reader, "list_wiki_records", WikiRecordsRequest{Kind: "session"})
	assert(t, restoredSessions.Total == 1, "Archive omitted durable Wiki sessions")
	assert(t, restored.Facts[0].Provenance.WikiSessionID != session.ID && compilationApproved(&restored, &restored.Compilations[0]), "Restored Wiki attribution or evidence links are invalid")
}

// TestExternalWikiInvalidEmptyAndReviewVersions prevents invalid evidence and stale review promotion.
func TestExternalWikiInvalidEmptyAndReviewVersions(t *testing.T) {
	_, service, _, session, facts := wikiFixture(t, 1)
	broken := facts[0]
	broken.Quote = "A fabricated passage that is absent"
	input := WikiExtractionInput{WikiMutation: wikiMutation(session, "invalid-fact"), ChunkID: session.Chunks[0].ID, SourceHash: session.SourceHash, Facts: []Candidate{broken}}
	externalInvoke[wikiTestView](t, service, "wiki_submit_extraction", input)
	session = wikiStored(t, service, session.ID)
	assert(t, !session.Chunks[0].Submitted && len(session.Chunks[0].Errors) == 1 && len(session.Facts) == 0, "Invalid extraction was marked complete")
	session = wikiExtractAll(t, service, session, facts)
	_, err := service.Execute(context.Background(), "wiki_submit_review", rawMessage(WikiReviewInput{WikiMutation: wikiMutation(session, "stale-review"), EvidenceVersion: "old", Reviews: []WikiFactReview{{FactID: session.Facts[0].ID, FactHash: wikiFactHash(session.Facts[0]), Decision: "accept", Reason: "Evidence"}}}))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "REVIEW_VERSION", "Stale review was accepted")
	_, err = service.Execute(context.Background(), "wiki_finish", rawMessage(wikiMutation(session, "premature-finish")))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "PHASE_ORDER", "Unreviewed facts became a candidate")
	_, emptyService, _, empty, _ := wikiFixture(t, 0)
	empty = wikiExtractAll(t, emptyService, empty, []Candidate{})
	view := externalInvoke[wikiTestView](t, emptyService, "wiki_finish", wikiMutation(empty, "empty-finish"))
	assert(t, view.Phase == "completed" && view.CandidateID == "", "Empty evidence created a false compilation")
}

// TestExternalWikiSourceRefreshAndConcurrency checks source-scoped invalidation, replay and competing revisions.
func TestExternalWikiSourceRefreshAndConcurrency(t *testing.T) {
	engine, service, project, session, facts := wikiFixture(t, 1)
	request := WikiExtractionInput{WikiMutation: wikiMutation(session, "extract-once"), ChunkID: session.Chunks[0].ID, SourceHash: session.SourceHash, Facts: facts}
	first := externalInvoke[wikiTestView](t, service, "wiki_submit_extraction", request)
	second := externalInvoke[wikiTestView](t, service, "wiki_submit_extraction", request)
	assert(t, first == second, "Extraction replay advanced the checkpoint")
	session = wikiStored(t, service, session.ID)
	var wait sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			_, err := service.Execute(context.Background(), "wiki_submit_review", rawMessage(WikiReviewInput{WikiMutation: wikiMutation(session, fmt.Sprintf("competing-%d", index)), EvidenceVersion: session.EvidenceVersion, Reviews: []WikiFactReview{{FactID: session.Facts[0].ID, FactHash: wikiFactHash(session.Facts[0]), Decision: "accept", Reason: "The source supports this claim"}}}))
			errors <- err
		}(i)
	}
	wait.Wait()
	close(errors)
	passed, conflicted := 0, 0
	for err := range errors {
		if err == nil {
			passed++
		} else if ExternalEnvelope(nil, err).ErrorCode == "REVISION_CONFLICT" {
			conflicted++
		} else {
			t.Fatal(err)
		}
	}
	assert(t, passed == 1 && conflicted == 1, "Concurrent review did not compare revisions")
	session = wikiStored(t, service, session.ID)
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	current.Chapters[0].Body += "\n作者补充了新的结尾。"
	if err := engine.store.Save(&current, current.Revision); err != nil {
		t.Fatal(err)
	}
	_, err := service.Execute(context.Background(), "wiki_finish", rawMessage(wikiMutation(session, "changed-finish")))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "SOURCES_CHANGED", "Changed source retained its old review")
	externalInvoke[wikiTestView](t, service, "wiki_refresh", wikiMutation(session, "refresh-source"))
	session = wikiStored(t, service, session.ID)
	assert(t, session.Phase == "awaiting_extraction" && len(session.Reviews) == 0 && strings.Contains(session.SourceText, "新的结尾"), "Refresh retained obsolete evidence")
}

// TestExternalWikiConfirmationRollback injects a receipt failure to verify that domain application rolls back too.
func TestExternalWikiConfirmationRollback(t *testing.T) {
	engine, service, project, session, facts := wikiFixture(t, 1)
	session = wikiReviewAll(t, service, wikiExtractAll(t, service, session, facts))
	view := externalInvoke[wikiTestView](t, service, "wiki_finish", wikiMutation(session, "stage-atomic"))
	token := wikiReviewToken(t, service, view.CandidateID)
	_, err := engine.store.db.Exec("CREATE TRIGGER fail_confirm_receipt BEFORE INSERT ON external_receipts WHEN NEW.operation='confirm_candidate' BEGIN SELECT RAISE(ABORT,'fixture receipt failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.store.db.Exec("DROP TRIGGER IF EXISTS fail_confirm_receipt") })
	input := CandidateConfirmation{RequestID: "atomic-confirm", CandidateID: view.CandidateID, ConfirmToken: token}
	_, err = service.Execute(context.Background(), "confirm_candidate", rawMessage(input))
	assert(t, err != nil, "Receipt failure was ignored")
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(current.Facts) == 0 && findChangeSet(&current, view.CandidateID).Status == ChangeSetStaged && wikiStored(t, service, session.ID).Phase == "staged", "Failed confirmation partially applied Wiki data")
	engine.store.db.Exec("DROP TRIGGER fail_confirm_receipt")
	externalInvoke[map[string]any](t, service, "confirm_candidate", input)
}

// TestExternalWikiUpgradeAndRetirement verifies backup-before-removal and bounded old-archive compatibility.
func TestExternalWikiUpgradeAndRetirement(t *testing.T) {
	engine := testEngine(t)
	project := testProject()
	insertProject(t, engine, project)
	for _, name := range []string{"list_experiments", "record_experiment", "evaluate_policy"} {
		if _, err := DescribeCommand(name); err == nil {
			t.Fatal("Retired command remains discoverable", name)
		}
		if _, err := engine.Dispatch(name, rawMessage(map[string]any{"project_id": project.ID})); err == nil {
			t.Fatal("Retired command remains callable", name)
		}
	}
	path := engine.store.path
	_, err := engine.store.db.Exec("CREATE TABLE experiments(id TEXT PRIMARY KEY,project_id TEXT,document TEXT); INSERT INTO experiments VALUES('old','legacy','{}'); PRAGMA user_version=5")
	if err != nil {
		t.Fatal(err)
	}
	engine.Close()
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='experiments'").Scan(&count); err != nil || count != 0 {
		t.Fatal("Experimental table survived migration", err)
	}
	if _, err := os.Stat(path + ".before-v6.sqlite3"); err != nil {
		t.Fatal(err)
	}
	backup, err := sql.Open("sqlite", path+".before-v6.sqlite3")
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if err := backup.QueryRow("SELECT COUNT(*) FROM experiments").Scan(&count); err != nil || count != 1 {
		t.Fatal("Migration backup lost retired records", err)
	}
	other := testEngine(t)
	var fields map[string]any
	if err := decodeJSON(rawMessage(project), &fields); err != nil {
		t.Fatal(err)
	}
	fields["experiments"] = []any{map[string]any{"legacy": "retired data"}}
	imported := invoke[Project](t, other, "import_archive", map[string]any{"content": jsonText(map[string]any{"format": "quillweave-3", "project": fields})})
	assert(t, len(imported.ImportWarnings) == 1 && len(imported.Chapters) == len(project.Chapters), "Legacy retirement was silent or damaged the work")
}

// TestExternalRecoveryChoices confirms author-selected keep and restore decisions entirely through external tools.
func TestExternalRecoveryChoices(t *testing.T) {
	for _, choice := range []string{"keep", "restore"} {
		t.Run(choice, func(t *testing.T) {
			engine, service, project := externalFixture(t)
			candidate := externalInvoke[ChangeSet](t, service, "stage_changes", StageChangesInput{RequestID: "create-entry", BaseRevision: project.Revision, Summary: "Add a world entry", Operations: []ChangeOperation{{TargetKind: TargetEntity, OpType: OpUpsert, Title: "Ledger", AfterValue: rawMessage(Entity{Name: "Ledger", Kind: "item", Aliases: []string{}, Description: "Original entry"})}}})
			externalInvoke[map[string]any](t, service, "confirm_candidate", CandidateConfirmation{RequestID: "apply-entry", CandidateID: candidate.ID, ConfirmToken: wikiReviewToken(t, service, candidate.ID)})
			current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			entry := current.ResolveEntity("Ledger")
			if entry == nil {
				t.Fatal("Entry was not created")
			}
			entry.Description = "Later author edit"
			if err := engine.store.Save(&current, current.Revision); err != nil {
				t.Fatal(err)
			}
			recovery := externalInvoke[ChangeSet](t, service, "prepare_revert", CandidateMutation{RequestID: "prepare-recovery", CandidateID: candidate.ID})
			view := externalInvoke[struct {
				Hash      string           `json:"review_hash"`
				Revision  uint64           `json:"revision"`
				Conflicts []RevertConflict `json:"conflicts"`
				Blocked   string           `json:"confirm_blocked"`
			}](t, service, "review_candidate", ExternalReadRequest{ID: recovery.ID})
			assert(t, view.Hash != "" && view.Blocked != "" && len(view.Conflicts) == 1, "Recovery did not expose its version-bound conflict")
			input := CandidateConflictInput{CandidateMutation: CandidateMutation{RequestID: "resolve-recovery", CandidateID: recovery.ID}, ExpectedRevision: view.Revision, ReviewHash: view.Hash, Resolutions: []CandidateConflictChoice{{Kind: view.Conflicts[0].Kind, ID: view.Conflicts[0].ID, Resolution: choice}}}
			first := externalInvoke[map[string]any](t, service, "resolve_candidate_conflicts", input)
			second := externalInvoke[map[string]any](t, service, "resolve_candidate_conflicts", input)
			assert(t, jsonText(first) == jsonText(second), "Conflict-choice replay changed its receipt")
			stale := input
			stale.RequestID = "obsolete-choice"
			_, err := service.Execute(context.Background(), "resolve_candidate_conflicts", rawMessage(stale))
			assert(t, ExternalEnvelope(nil, err).ErrorCode == "CANDIDATE_CHANGED", "Obsolete conflict choices were accepted")
			externalInvoke[map[string]any](t, service, "confirm_candidate", CandidateConfirmation{RequestID: "confirm-recovery", CandidateID: recovery.ID, ConfirmToken: wikiReviewToken(t, service, recovery.ID)})
			current = invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
			entry = current.ResolveEntity("Ledger")
			if choice == "keep" {
				assert(t, entry != nil && entry.Description == "Later author edit", "Keep overwrote the later edit")
			} else {
				assert(t, entry == nil, "Restore retained the originally created entry")
			}
		})
	}
}

// TestExternalWikiPreservesAuthorDecisions allows changes only as explicit, reviewable re-review proposals.
func TestExternalWikiPreservesAuthorDecisions(t *testing.T) {
	engine, service, project, session, facts := wikiFixture(t, 1)
	session = wikiExtractAll(t, service, session, facts)
	input := WikiReviewInput{WikiMutation: wikiMutation(session, "reject-fact"), EvidenceVersion: session.EvidenceVersion, Reviews: []WikiFactReview{{FactID: session.Facts[0].ID, FactHash: wikiFactHash(session.Facts[0]), Decision: "reject", Reason: "The author excludes this fixture interpretation"}}}
	externalInvoke[wikiTestView](t, service, "wiki_submit_review", input)
	session = wikiStored(t, service, session.ID)
	view := externalInvoke[wikiTestView](t, service, "wiki_finish", wikiMutation(session, "stage-rejection"))
	externalInvoke[map[string]any](t, service, "confirm_candidate", CandidateConfirmation{RequestID: "confirm-rejection", CandidateID: view.CandidateID, ConfirmToken: wikiReviewToken(t, service, view.CandidateID)})
	view = externalInvoke[wikiTestView](t, service, "wiki_begin", WikiBeginInput{RequestID: "recompile", SourceID: session.SourceID})
	session = wikiExtractAll(t, service, wikiStored(t, service, view.ID), facts)
	input = WikiReviewInput{WikiMutation: wikiMutation(session, "override-rejection"), EvidenceVersion: session.EvidenceVersion, Reviews: []WikiFactReview{{FactID: session.Facts[0].ID, FactHash: wikiFactHash(session.Facts[0]), Decision: "accept", Reason: "Accept again"}}}
	_, err := service.Execute(context.Background(), "wiki_submit_review", rawMessage(input))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "NEEDS_AUTHOR", "Compilation resurrected author-rejected evidence")
	view = externalInvoke[wikiTestView](t, service, "wiki_begin", WikiBeginInput{RequestID: "explicit-rereview", SourceID: session.SourceID, Mode: "review"})
	session = wikiReviewAll(t, service, wikiStored(t, service, view.ID))
	view = externalInvoke[wikiTestView](t, service, "wiki_finish", wikiMutation(session, "stage-rereview"))
	externalInvoke[map[string]any](t, service, "confirm_candidate", CandidateConfirmation{RequestID: "confirm-rereview", CandidateID: view.CandidateID, ConfirmToken: wikiReviewToken(t, service, view.CandidateID)})
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, len(current.Facts) == 1 && current.Facts[0].Status == "confirmed", "Explicitly approved re-review did not update the existing fact")
}

// TestExternalWikiRestoredPendingRefresh rejects resumed archived stages and invalidates their old candidates.
func TestExternalWikiRestoredPendingRefresh(t *testing.T) {
	engine, service, project, session, facts := wikiFixture(t, 1)
	session = wikiReviewAll(t, service, wikiExtractAll(t, service, session, facts))
	externalInvoke[wikiTestView](t, service, "wiki_finish", wikiMutation(session, "archive-stage"))
	archive := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": project.ID, "format": "archive"})
	restored := invoke[Project](t, engine, "import_archive", map[string]any{"content": archive["content"]})
	reader, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: restored.ID, Client: "restored"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	var raw string
	if err := reader.engine.store.db.QueryRow("SELECT document FROM wiki_sessions WHERE project_id=?", restored.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var imported ExternalWikiSession
	if err := decodeJSON([]byte(raw), &imported); err != nil {
		t.Fatal(err)
	}
	assert(t, imported.Phase == "stale" && imported.ID != session.ID, "Active Wiki archive resumed stale creative work")
	_, err = reader.Execute(context.Background(), "wiki_finish", rawMessage(wikiMutation(imported, "obsolete-finish")))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "SOURCES_CHANGED", "Restored source checks were bypassed")
	if token := wikiReviewToken(t, reader, imported.CandidateID); token != "" {
		_, err = reader.Execute(context.Background(), "confirm_candidate", rawMessage(CandidateConfirmation{RequestID: "obsolete-confirm", CandidateID: imported.CandidateID, ConfirmToken: token}))
		assert(t, ExternalEnvelope(nil, err).ErrorCode == "SOURCES_CHANGED", "A stale restored session applied its pending Wiki candidate")
	}
	externalInvoke[wikiTestView](t, reader, "wiki_refresh", wikiMutation(imported, "refresh-import"))
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": restored.ID})
	assert(t, findChangeSet(&current, imported.CandidateID).Status == ChangeSetRejected, "Refresh left the obsolete imported candidate pending")
}

// TestExternalWikiReviewCorrectionAndInvalidRejection supports evidence correction and rejection of legacy bad quotes.
func TestExternalWikiReviewCorrectionAndInvalidRejection(t *testing.T) {
	engine, service, project, session, facts := wikiFixture(t, 1)
	session = wikiReviewAll(t, service, wikiExtractAll(t, service, session, facts))
	view := externalInvoke[wikiTestView](t, service, "wiki_finish", wikiMutation(session, "initial-correction-stage"))
	externalInvoke[map[string]any](t, service, "confirm_candidate", CandidateConfirmation{RequestID: "initial-correction-confirm", CandidateID: view.CandidateID, ConfirmToken: wikiReviewToken(t, service, view.CandidateID)})
	view = externalInvoke[wikiTestView](t, service, "wiki_begin", WikiBeginInput{RequestID: "correct-quote", SourceID: session.SourceID, Mode: "review"})
	session = wikiStored(t, service, view.ID)
	fact := session.Facts[0]
	externalInvoke[wikiTestView](t, service, "wiki_submit_review", WikiReviewInput{WikiMutation: wikiMutation(session, "quote-needs-revision"), EvidenceVersion: session.EvidenceVersion, Reviews: []WikiFactReview{{FactID: fact.ID, FactHash: wikiFactHash(fact), Decision: "needs_revision", Reason: "Use the exact clause supporting the letter-storage action"}}})
	session = wikiStored(t, service, session.ID)
	facts[0].Quote = strings.Split(facts[0].Quote, "，")[0]
	session = wikiExtractAll(t, service, session, facts)
	assert(t, session.Facts[0].Quote == facts[0].Quote && wikiFactHash(session.Facts[0]) != wikiFactHash(fact), "Review-mode extraction did not correct an existing fact")
	session = wikiReviewAll(t, service, session)
	view = externalInvoke[wikiTestView](t, service, "wiki_finish", wikiMutation(session, "corrected-quote-stage"))
	externalInvoke[map[string]any](t, service, "confirm_candidate", CandidateConfirmation{RequestID: "corrected-quote-confirm", CandidateID: view.CandidateID, ConfirmToken: wikiReviewToken(t, service, view.CandidateID)})
	current := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	current.Facts[0].Quote = "旧版本中无法定位的引文"
	if err := engine.store.Save(&current, current.Revision); err != nil {
		t.Fatal(err)
	}
	view = externalInvoke[wikiTestView](t, service, "wiki_begin", WikiBeginInput{RequestID: "reject-bad-legacy", SourceID: session.SourceID, Mode: "review"})
	session = wikiStored(t, service, view.ID)
	fact = session.Facts[0]
	externalInvoke[wikiTestView](t, service, "wiki_submit_review", WikiReviewInput{WikiMutation: wikiMutation(session, "reject-bad-quote"), EvidenceVersion: session.EvidenceVersion, Reviews: []WikiFactReview{{FactID: fact.ID, FactHash: wikiFactHash(fact), Decision: "reject", Reason: "The legacy quote is absent from the source"}}})
	session = wikiStored(t, service, session.ID)
	view = externalInvoke[wikiTestView](t, service, "wiki_finish", wikiMutation(session, "reject-bad-stage"))
	externalInvoke[map[string]any](t, service, "confirm_candidate", CandidateConfirmation{RequestID: "reject-bad-confirm", CandidateID: view.CandidateID, ConfirmToken: wikiReviewToken(t, service, view.CandidateID)})
	current = invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, current.Facts[0].Status == "rejected", "Invalid old evidence could not be explicitly rejected")
}

// TestExternalWikiReadOnlyAndScope confines all session operations to the selected work and granted capability.
func TestExternalWikiReadOnlyAndScope(t *testing.T) {
	engine, service, project, session, _ := wikiFixture(t, 1)
	reader, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: project.ID, ReadOnly: true, Client: "reader"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reader.Close() })
	status := externalInvoke[wikiTestView](t, reader, "wiki_status", WikiSessionRequest{WikiSessionID: session.ID})
	assert(t, status.ID == session.ID, "Read-only session lookup failed")
	_, err = reader.Execute(context.Background(), "wiki_begin", rawMessage(WikiBeginInput{RequestID: "forbidden-begin", SourceID: session.SourceID}))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "READ_ONLY_MODE", "Read-only caller created a session")
	_, err = reader.Execute(context.Background(), "wiki_abandon", rawMessage(wikiMutation(session, "forbidden-abandon")))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "READ_ONLY_MODE", "Read-only caller mutated a session")
	other := testProject()
	insertProject(t, engine, other)
	foreign, err := OpenExternalService(engine.store.path, ExternalOptions{ProjectID: other.ID, Client: "other"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { foreign.Close() })
	_, err = foreign.Execute(context.Background(), "wiki_status", rawMessage(WikiSessionRequest{WikiSessionID: session.ID}))
	assert(t, ExternalEnvelope(nil, err).ErrorCode == "NOT_FOUND", "A session escaped its owning work")
	externalInvoke[wikiTestView](t, service, "wiki_abandon", wikiMutation(session, "end-session"))
	assert(t, wikiStored(t, service, session.ID).Phase == "abandoned", "Abandonment lost its durable state")
}
