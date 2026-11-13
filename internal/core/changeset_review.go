package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
)

// captureRenamedStoryReferences freezes only structured names touched by a compound rename.
func captureRenamedStoryReferences(project *Project, cs *ChangeSet, name string) {
	for _, memory := range project.Studio.Memories {
		if memory.Entity != name && !slices.Contains(memory.KnownBy, name) {
			continue
		}
		if !slices.ContainsFunc(cs.BeforeMemoryReferences, func(item MemoryReferenceSnapshot) bool { return item.MemoryID == memory.ID }) {
			cs.BeforeMemoryReferences = append(cs.BeforeMemoryReferences, MemoryReferenceSnapshot{MemoryID: memory.ID, Entity: memory.Entity, KnownBy: slices.Clone(memory.KnownBy)})
		}
	}
	for _, plan := range project.Studio.Plans {
		if slices.Contains(plan.Characters, name) && !slices.ContainsFunc(cs.BeforePlanReferences, func(item PlanReferenceSnapshot) bool { return item.PlanID == plan.ID }) {
			cs.BeforePlanReferences = append(cs.BeforePlanReferences, PlanReferenceSnapshot{PlanID: plan.ID, Characters: slices.Clone(plan.Characters)})
		}
	}
}

// restoreRenamedStoryReferences restores name fields while preserving source text and other edits.
func restoreRenamedStoryReferences(project *Project, cs *ChangeSet) {
	for _, previous := range cs.BeforeMemoryReferences {
		if index := memoryIndex(project, previous.MemoryID); index >= 0 {
			project.Studio.Memories[index].Entity = previous.Entity
			project.Studio.Memories[index].KnownBy = slices.Clone(previous.KnownBy)
		}
	}
	for _, previous := range cs.BeforePlanReferences {
		for i := range project.Studio.Plans {
			if project.Studio.Plans[i].ID == previous.PlanID {
				project.Studio.Plans[i].Characters = slices.Clone(previous.Characters)
			}
		}
	}
}

// changeTargetValue returns the public value shown in the author's final preview.
func changeTargetValue(project *Project, op ChangeOperation) json.RawMessage {
	switch op.TargetKind {
	case TargetWiki:
		return wikiTargetValue(project, op)
	case TargetLocation:
		for _, value := range project.Studio.Locations {
			if value.ID == op.TargetID {
				return rawMessage(value)
			}
		}
	case TargetRoute:
		for _, value := range project.Studio.Routes {
			if value.ID == op.TargetID {
				return rawMessage(value)
			}
		}
	case TargetProjectMeta:
		return rawMessage(projectMetaPatch{Title: &project.Title, Genre: &project.Genre, Premise: &project.Premise, Style: &project.Style, Outline: &project.Outline, WritingStyle: &project.WritingStyle, DefaultChapterWords: &project.DefaultChapterWords, NarrativePov: &project.NarrativePov})
	case TargetStudioGoal:
		return rawMessage(studioGoalSnapshot(project))
	case TargetEntity:
		for _, value := range project.Entities {
			if value.ID == op.TargetID {
				return rawMessage(value)
			}
		}
	case TargetDecision:
		for _, value := range project.Studio.Decisions {
			if value.ID == op.TargetID {
				return rawMessage(value)
			}
		}
	case TargetMemory:
		if i := memoryIndex(project, op.TargetID); i >= 0 {
			return rawMessage(project.Studio.Memories[i])
		}
	case TargetPlan:
		for _, value := range project.Studio.Plans {
			if value.ID == op.TargetID {
				return rawMessage(value)
			}
		}
	case TargetChapter:
		if chapter := findChapter(project, op.TargetID); chapter != nil {
			return rawMessage(chapterProsePatch{Number: &chapter.Number, Title: &chapter.Title, Body: chapter.Body, Synopsis: &chapter.Synopsis})
		}
	}
	return json.RawMessage("null")
}

// changeSetReviewToken binds confirmation to the exact project revision and reviewed operations.
func changeSetReviewToken(cs ChangeSet) string {
	return hashText(jsonText([]any{cs.ProjectID, cs.ID, cs.PreviewRevision, cs.Operations}))
}

// writeChangeSetRecord updates one independently managed candidate inside the current transaction.
func writeChangeSetRecord(db documentDB, projectID string, cs ChangeSet) error {
	result, err := db.ExecContext(context.Background(), "UPDATE project_parts SET document=? WHERE project_id=? AND kind='changeset' AND id=?", jsonText(cs), projectID, cs.ID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count != 1 {
		return errors.New("Changeset not found")
	}
	return err
}

// previewChangeSet validates edited selections on a copy and freezes the displayed before/after values.
func previewChangeSet(project Project, cs ChangeSet, raw json.RawMessage) (ChangeSet, error) {
	if !contains([]string{"staged", "conflicted"}, string(cs.Status)) {
		return cs, errors.New("Only pending changes can be previewed")
	}
	if len(raw) > 0 {
		var selected []ChangeOperation
		if err := decodeJSON(raw, &selected); err != nil {
			return cs, err
		}
		all := append(append([]ChangeOperation{}, cs.Operations...), cs.Skipped...)
		available := map[string]ChangeOperation{}
		for _, op := range all {
			available[op.ID] = op
		}
		for i := range selected {
			op := &selected[i]
			old, found := available[op.ID]
			if op.ProposalID == "" {
				op.ProposalID = old.ProposalID
			}
			if !found || old.TargetKind != op.TargetKind || old.OpType != op.OpType || old.TargetID != op.TargetID || old.ProposalID != op.ProposalID {
				return cs, errors.New("Selected operation changed its target. Create a new proposal")
			}
			delete(available, op.ID)
		}
		cs.Skipped = nil
		for _, op := range all {
			if _, found := available[op.ID]; found {
				cs.Skipped = append(cs.Skipped, op)
			}
		}
		cs.Operations = selected
	}
	copy := cloneProject(project)
	if err := applyChangeSetToProject(&copy, &cs); err != nil {
		return cs, err
	}
	for i := range cs.Operations {
		op := &cs.Operations[i]
		op.PreviewValue = changeTargetValue(&copy, *op)
	}
	cs.Impacts = mapChangeImpacts(project, copy, cs.Operations)
	cs.Impacts = append(cs.Impacts, chapterDeletionImpacts(&project, cs.Operations)...)
	cs.Status = ChangeSetStaged
	cs.PreviewRevision = project.Revision
	cs.CommitPolicy = "review"
	cs.ReviewToken = changeSetReviewToken(cs)
	cs.UpdatedAt = now()
	return cs, nil
}

// reviewChangeSetCommand serializes preview, author confirmation, rejection and undo against current storage.
func (engine *Engine) reviewChangeSetCommand(command, projectID string, args *arguments) (any, error) {
	var result any
	activeWriter := command == "apply_changeset" && engine.hasActiveCandidateWriter(projectID)
	err := engine.store.withWriteTransaction(func(conn *sql.Conn) error {
		var err error
		result, err = engine.reviewChangeSetInDB(conn, command, projectID, args, activeWriter)
		return err
	})
	return result, err
}

// hasActiveCandidateWriter checks in-process work before entering the database transaction.
func (engine *Engine) hasActiveCandidateWriter(projectID string) bool {
	engine.jobsMu.Lock()
	defer engine.jobsMu.Unlock()
	for _, job := range engine.jobs {
		if job.ProjectID == projectID && (job.Status == "running" || job.pendingFinal != nil) {
			return true
		}
	}
	return false
}

// reviewChangeSetInDB shares exact desktop checks with externally confirmed, receipt-backed transactions.
func (engine *Engine) reviewChangeSetInDB(conn *sql.Conn, command, projectID string, args *arguments, activeWriter bool) (any, error) {
	var result any
	project, err := readProject(conn, projectID)
	if err != nil {
		return nil, err
	}
	project.requestLanguage = args.optionalString("ui_language")
	id := args.text("changeset_id")
	if args.err != nil {
		return nil, args.err
	}
	stored := findChangeSet(&project, id)
	if stored == nil {
		return nil, errors.New("Changeset not found")
	}
	cs := *stored
	cs.Operations = append([]ChangeOperation{}, cs.Operations...)
	if command == "preview_changeset" {
		cs, err = previewChangeSet(project, cs, args.fields["operations"])
		if err != nil {
			return nil, err
		}
	} else if command == "reject_changeset" {
		if !contains([]string{"staged", "conflicted", "rejected"}, string(cs.Status)) {
			return nil, errors.New("Only pending changes can be rejected")
		}
		cs.Status, cs.ReviewToken, cs.UpdatedAt = ChangeSetRejected, "", now()
	} else {
		if _, edited := args.fields["operations"]; edited {
			return nil, errors.New("Preview edited operations before confirming")
		}
		if command == "apply_changeset" {
			token := args.optionalString("review_token")
			if token == "" || token != cs.ReviewToken {
				return nil, errors.New("Preview these changes and confirm the reviewed version first")
			}
			if cs.Status == ChangeSetApplied {
				result = changeSetReceipt(project, cs)
				return result, nil
			}
			if hasChapterDeletion(cs.Operations) {
				var running int
				if err := conn.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM runs WHERE project_id=? AND json_extract(document,'$.status')='running'", projectID).Scan(&running); err != nil {
					return nil, err
				}
				if activeWriter || running > 0 {
					return nil, errors.New("Stop the active writing task before deleting a chapter")
				}
			}
			if cs.Status != ChangeSetStaged || cs.ReviewToken != changeSetReviewToken(cs) {
				return nil, errors.New("The reviewed changes changed. Refresh the preview")
			}
			expected, ok := unsigned(args.fields["revision"])
			if !ok || (expected != project.Revision && expected != cs.PreviewRevision) {
				return nil, errors.New("The project changed. Refresh the preview before confirming")
			}
			if project.Revision != cs.PreviewRevision {
				if cs, err = refreshStalePreview(project, cs); err != nil {
					return nil, err
				}
			}
			if err := validateRevertConfirmation(&cs); err != nil {
				return nil, err
			}
			if err := syncChangeSetProposals(conn, &project, &cs, "validate"); err != nil {
				return nil, err
			}
			if err := applyChangeSetToProject(&project, &cs); err != nil {
				return nil, err
			}
			cs.AppliedRevision = project.Revision + 1
			cs.Archived = false
		} else {
			if cs.Status == ChangeSetUndone {
				result = changeSetReceipt(project, cs)
				return result, nil
			}
			if cs.Archived {
				return nil, errors.New("This is an imported history record. Prepare a new reviewed change to restore its values")
			}
			expected, ok := unsigned(args.fields["revision"])
			if !ok || expected != project.Revision {
				return nil, errors.New("The project changed. Refresh before undoing")
			}
			if project.Revision != cs.AppliedRevision {
				return nil, errors.New("The story has later edits. Review a new change before reverting")
			}
			if err := undoChangeSetOnProject(&project, &cs); err != nil {
				return nil, err
			}
		}
		upsertChangeSetInProject(&project, cs)
		if command == "apply_changeset" {
			if err := syncChangeSetProposals(conn, &project, &cs, "accepted"); err != nil {
				return nil, err
			}
		} else if err := syncChangeSetProposals(conn, &project, &cs, "undone"); err != nil {
			return nil, err
		}
		if err := saveProject(conn, &project, project.Revision); err != nil {
			return nil, err
		}
	}
	if command == "reject_changeset" {
		if err := syncChangeSetProposals(conn, &project, &cs, "rejected"); err != nil {
			return nil, err
		}
	} else if command == "preview_changeset" {
		if err := syncChangeSetProposals(conn, &project, &cs, "preview"); err != nil {
			return nil, err
		}
	}
	if err := writeChangeSetRecord(conn, projectID, cs); err != nil {
		return nil, err
	}
	upsertChangeSetInProject(&project, cs)
	if err := syncWikiCandidateState(conn, projectID, cs); err != nil {
		return nil, err
	}
	result = changeSetReceipt(project, cs)
	return result, nil
}

// refreshStalePreview re-previews a candidate whose preview predates the current revision. The author's
// confirmation still holds when every target has the same before value as in the reviewed preview, so
// saving another candidate or an unrelated record does not block it; a changed target needs a new review.
func refreshStalePreview(project Project, cs ChangeSet) (ChangeSet, error) {
	stale := errors.New("The project changed after this preview. Refresh the preview before confirming")
	// Chapter deletions renumber later prose and recovery candidates restore exact effects, so any later save needs a new review.
	if hasChapterDeletion(cs.Operations) || cs.RevertsChangeSetID != "" {
		return cs, stale
	}
	// Previewing rewrites operations in place, so compare against a snapshot taken before it runs.
	reviewed := append([]ChangeOperation{}, cs.Operations...)
	next := cs
	next.Operations = append([]ChangeOperation{}, cs.Operations...)
	refreshed, err := previewChangeSet(project, next, nil)
	if err != nil || len(refreshed.Operations) != len(reviewed) {
		return cs, stale
	}
	for i, op := range refreshed.Operations {
		old := reviewed[i]
		if op.ID != old.ID || op.TargetKind != old.TargetKind || op.OpType != old.OpType || jsonText(op.BeforeValue) != jsonText(old.BeforeValue) {
			return cs, stale
		}
	}
	return refreshed, nil
}

// changeSetReceipt returns persisted state and stable target identifiers for both GUI and CLI.
func changeSetReceipt(project Project, cs ChangeSet) map[string]any {
	return map[string]any{"project": project, "changeset": cs, "readiness": computeFlowReadiness(&project)}
}

// setJobChangeSet updates one public review record without changing private model continuation data.
func setJobChangeSet(job *Job, cs ChangeSet) {
	for i := range job.ChangeSets {
		if job.ChangeSets[i].ID == cs.ID {
			job.ChangeSets[i] = cs
			return
		}
	}
	job.ChangeSets = append(job.ChangeSets, cs)
}

// syncChangeSetProposals validates provenance and synchronizes selected proposals in the same transaction.
func syncChangeSetProposals(db documentDB, project *Project, cs *ChangeSet, decision string) error {
	if cs.JobID == "" {
		return nil
	}
	var raw string
	if err := db.QueryRowContext(context.Background(), "SELECT document FROM runs WHERE id=? AND project_id=?", cs.JobID, project.ID).Scan(&raw); err != nil {
		return err
	}
	job, err := decodeStoredJob(raw)
	if err != nil {
		return err
	}
	links, _ := legacyProposalLinks(&job)
	bindLegacyProposalIDs(cs, &job, links)
	for _, op := range cs.Operations {
		if decision == "validate" && cs.ProposalLinksVersion == 0 && op.ProposalID == "" && op.OpType == OpUpsert && contains([]string{"plan", "decision"}, string(op.TargetKind)) && contains([]string{"", "agent"}, cs.Source) {
			for _, proposal := range job.Proposals {
				if proposal.Kind == string(op.TargetKind) {
					return errors.New("Older proposal source links are ambiguous. Create a new reviewed change from the saved story")
				}
			}
		}
		if op.ProposalID == "" || decision == "preview" || decision == "undone" {
			continue
		}
		index := -1
		for i := range job.Proposals {
			if job.Proposals[i].ID == op.ProposalID {
				index = i
				break
			}
		}
		if index < 0 {
			return errors.New("The source proposal is missing from this task")
		}
		proposal := &job.Proposals[index]
		if decision == "validate" {
			if proposal.Status != "proposed" {
				return errors.New("This proposal was already reviewed. Refresh the pending changes")
			}
			continue
		}
		if decision == "rejected" && proposal.Status != "proposed" {
			continue
		}
		proposal.Status = decision
		if decision != "accepted" {
			continue
		}
		switch op.TargetKind {
		case TargetPlan:
			var plan StoryPlan
			if err := decodeJSON(op.AfterValue, &plan); err != nil {
				return err
			}
			updatePlanProposal(&job, index, plan)
		case TargetMemory:
			if index := memoryIndex(project, op.TargetID); index >= 0 {
				memory := project.Studio.Memories[index]
				proposal.Memory, proposal.Content = &memory, memory.Content
			}
		case TargetChapter:
			var patch chapterProsePatch
			if err := decodeJSON(op.AfterValue, &patch); err != nil {
				return err
			}
			proposal.Content = patch.Body
		case TargetDecision:
			var item StoryDecision
			if err := decodeJSON(op.AfterValue, &item); err != nil {
				return err
			}
			proposal.Content = item.Content
		}
	}
	if decision == "validate" {
		return nil
	}
	if decision == "preview" {
		cs.ReviewToken = changeSetReviewToken(*cs)
	}
	setJobChangeSet(&job, *cs)
	_, err = db.ExecContext(context.Background(), "UPDATE runs SET document=json_set(document,'$.proposals',json(?),'$.changesets',json(?)) WHERE id=? AND project_id=?", jsonText(job.Proposals), jsonText(job.ChangeSets), job.ID, project.ID)
	return err
}

type legacyProposalLink struct {
	changeSetID   string
	operation     ChangeOperation
	status        ChangeSetStatus
	proposalIndex int
}

// bindLegacyProposalIDs persists recovered links during review without changing candidate field values.
func bindLegacyProposalIDs(cs *ChangeSet, job *Job, links []legacyProposalLink) {
	for _, operations := range [][]ChangeOperation{cs.Operations, cs.Skipped} {
		for i := range operations {
			for _, link := range links {
				if link.changeSetID == cs.ID && link.operation.ID == operations[i].ID && operations[i].ProposalID == "" {
					operations[i].ProposalID = job.Proposals[link.proposalIndex].ID
				}
			}
		}
	}
}

// legacyProposalMatch ranks retained identity and labels before normalized payload equality.
func legacyProposalMatch(proposal StudioProposal, operation ChangeOperation) int {
	if proposal.Kind != string(operation.TargetKind) {
		return 0
	}
	if proposal.Kind == "plan" && proposal.Plan != nil {
		var saved StoryPlan
		if decodeJSON(operation.AfterValue, &saved) != nil {
			return 0
		}
		if proposal.Plan.ID != "" && (proposal.Plan.ID == operation.TargetID || proposal.Plan.ID == saved.ID) {
			return 3
		}
		if proposal.Title != "" && proposal.Title == operation.Title {
			return 2
		}
		original := *proposal.Plan
		for _, plan := range []*StoryPlan{&original, &saved} {
			plan.ID, plan.ParentID, plan.Status = "", "", ""
			plan.Characters = append([]string{}, plan.Characters...)
			plan.Clues = append([]string{}, plan.Clues...)
		}
		if jsonText(original) == jsonText(saved) {
			return 1
		}
	}
	if proposal.Kind == "decision" {
		if proposal.Title != "" && proposal.Title == operation.Title {
			return 2
		}
		var saved StoryDecision
		if decodeJSON(operation.AfterValue, &saved) == nil && saved.Title == proposal.Title && saved.Content == proposal.Content {
			return 1
		}
	}
	return 0
}

// legacyProposalLinks resolves only unique old plan/decision associations and identifies ambiguous legacy reviews.
func legacyProposalLinks(job *Job) ([]legacyProposalLink, map[string]bool) {
	candidates := []legacyProposalLink{}
	claimed := map[string]bool{}
	kinds := map[string]bool{}
	for _, cs := range job.ChangeSets {
		if cs.JobID != "" && cs.JobID != job.ID || cs.Source != "" && cs.Source != "agent" {
			continue
		}
		for group, operations := range [][]ChangeOperation{cs.Operations, cs.Skipped} {
			for _, op := range operations {
				if op.ProposalID != "" {
					claimed[op.ProposalID] = true
					continue
				}
				if cs.ProposalLinksVersion > 0 || op.OpType != OpUpsert || !contains([]string{"plan", "decision"}, string(op.TargetKind)) {
					continue
				}
				status := cs.Status
				if group == 1 {
					status = ChangeSetStaged
				}
				candidates = append(candidates, legacyProposalLink{changeSetID: cs.ID, operation: op, status: status, proposalIndex: -1})
				kinds[string(op.TargetKind)] = true
			}
		}
	}
	references := map[int]int{}
	for i := range candidates {
		best := 0
		matches := []int{}
		for index, proposal := range job.Proposals {
			score := legacyProposalMatch(proposal, candidates[i].operation)
			if score > best {
				best, matches = score, []int{index}
			} else if score > 0 && score == best {
				matches = append(matches, index)
			}
		}
		if len(matches) == 1 {
			candidates[i].proposalIndex = matches[0]
		}
		for _, index := range matches {
			references[index]++
		}
	}
	links := []legacyProposalLink{}
	for _, link := range candidates {
		if link.proposalIndex >= 0 && references[link.proposalIndex] == 1 {
			links = append(links, link)
			claimed[job.Proposals[link.proposalIndex].ID] = true
		}
	}
	blocked := map[string]bool{}
	for _, proposal := range job.Proposals {
		if proposal.Status == "proposed" && !claimed[proposal.ID] && kinds[proposal.Kind] {
			blocked[proposal.ID] = true
		}
	}
	return links, blocked
}

// updatePlanProposal adopts the persisted plan and updates pending child references using its original identity.
func updatePlanProposal(job *Job, index int, plan StoryPlan) {
	proposal := &job.Proposals[index]
	oldID := ""
	if proposal.Plan != nil {
		oldID = proposal.Plan.ID
	}
	proposal.Plan, proposal.Content = &plan, plan.Goal
	for i := range job.Proposals {
		if oldID != "" && job.Proposals[i].Plan != nil && job.Proposals[i].Plan.ParentID == oldID {
			child := *job.Proposals[i].Plan
			child.ParentID = plan.ID
			job.Proposals[i].Plan = &child
		}
	}
}

// normalizeLegacyProposalStates exposes recorded acceptance without changing signed previews or private continuation data.
func normalizeLegacyProposalStates(job *Job) {
	links, _ := legacyProposalLinks(job)
	for _, link := range links {
		proposal := &job.Proposals[link.proposalIndex]
		if proposal.Status != "proposed" {
			continue
		}
		switch link.status {
		case ChangeSetApplied, ChangeSetUndone:
			proposal.Status = "accepted"
			if proposal.Kind == "plan" {
				var plan StoryPlan
				if decodeJSON(link.operation.AfterValue, &plan) == nil {
					updatePlanProposal(job, link.proposalIndex, plan)
				}
			} else {
				var decision StoryDecision
				if decodeJSON(link.operation.AfterValue, &decision) == nil {
					proposal.Content = decision.Content
				}
			}
		case ChangeSetRejected:
			proposal.Status = "rejected"
		}
	}
}

// previewLegacyProposal adapts old proposal cards to the same frozen preview and explicit confirmation service.
func (engine *Engine) previewLegacyProposal(project Project, job Job, index int, args *arguments) (any, error) {
	proposal := job.Proposals[index]
	if raw, exists := args.fields["content"]; exists {
		var content string
		if decodeJSON(raw, &content) != nil || strings.TrimSpace(content) == "" {
			return nil, errors.New("Enter non-empty proposal content before reviewing")
		}
		proposal.Content = content
		if proposal.Plan != nil {
			value := *proposal.Plan
			value.Goal = content
			proposal.Plan = &value
		}
		if proposal.Memory != nil {
			value := *proposal.Memory
			value.Content = content
			proposal.Memory = &value
		}
	}
	if proposal.Kind == "memory" {
		copy := cloneProject(project)
		if err := acceptMemoryProposal(&copy, &proposal, job, args); err != nil {
			return nil, err
		}
	}
	if proposal.Kind == "chapter" {
		chapter := findChapter(&project, job.ChapterID)
		if chapter == nil || hashText(chapter.Body) != job.Prompt.SourceHash {
			return nil, errors.New("The chapter changed after generation. Review the current draft before applying this prose")
		}
		if mode := args.optionalString("mode"); mode != "" {
			proposal.Mode = mode
		}
		if !contains([]string{"", "append", "replace"}, proposal.Mode) {
			return nil, errors.New("Choose append or replace")
		}
	}
	cs := proposalsToChangeSet(&project, &job, []StudioProposal{proposal})
	if cs == nil {
		return nil, errors.New("The proposal cannot produce a valid preview")
	}
	links, _ := legacyProposalLinks(&job)
	for _, candidate := range project.Studio.ChangeSets {
		stored := candidate
		if stored.JobID != job.ID || !contains([]string{"staged", "conflicted"}, string(stored.Status)) {
			continue
		}
		stored.Operations = append([]ChangeOperation{}, stored.Operations...)
		stored.Skipped = append([]ChangeOperation{}, stored.Skipped...)
		bindLegacyProposalIDs(&stored, &job, links)
		matched := false
		for _, op := range stored.Operations {
			matched = matched || op.ProposalID == proposal.ID
		}
		if !matched {
			continue
		}
		cs.ID, cs.CreatedAt = stored.ID, stored.CreatedAt
		cs.ProposalLinksVersion = stored.ProposalLinksVersion
		for _, op := range append(append([]ChangeOperation{}, stored.Operations...), stored.Skipped...) {
			if op.ProposalID != proposal.ID {
				cs.Skipped = append(cs.Skipped, op)
			}
		}
		break
	}
	preview, err := previewChangeSet(project, *cs, nil)
	if err != nil {
		return nil, err
	}
	job.Proposals[index] = proposal
	setJobChangeSet(&job, preview)
	err = engine.store.withWriteTransaction(func(conn *sql.Conn) error {
		current, err := readProject(conn, project.ID)
		if err != nil {
			return err
		}
		if current.Revision != project.Revision {
			return errors.New("The project changed. Refresh before reviewing")
		}
		var latest string
		if err := conn.QueryRowContext(context.Background(), "SELECT document FROM runs WHERE id=? AND project_id=?", job.ID, project.ID).Scan(&latest); err != nil {
			return err
		}
		saved, err := decodeStoredJob(latest)
		if err != nil {
			return err
		}
		if index >= len(saved.Proposals) || saved.Proposals[index].ID != proposal.ID || saved.Proposals[index].Status != "proposed" {
			return errors.New("The proposal changed during review")
		}
		if findChangeSet(&current, preview.ID) == nil {
			if err := persistStagedChangeSetsInDB(conn, project.ID, []ChangeSet{preview}); err != nil {
				return err
			}
		} else if err := writeChangeSetRecord(conn, project.ID, preview); err != nil {
			return err
		}
		_, err = conn.ExecContext(context.Background(), "UPDATE runs SET document=json_set(document,'$.proposals',json(?),'$.changesets',json(?)) WHERE id=? AND project_id=?", jsonText(job.Proposals), jsonText(job.ChangeSets), job.ID, project.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	upsertChangeSetInProject(&project, preview)
	return map[string]any{"project": project, "job": job, "changeset": preview}, nil
}
