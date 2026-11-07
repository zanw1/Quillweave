package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// listChangeSetsByStatus queries changeset parts by status using the partial expression index.
func (store *Store) listChangeSetsByStatus(projectID, status string) ([]ChangeSet, error) {
	rows, err := store.db.Query("SELECT document FROM project_parts WHERE project_id=? AND kind='changeset' AND json_extract(document,'$.status')=? ORDER BY position,id", projectID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ChangeSet{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		var cs ChangeSet
		if err := decodeJSON([]byte(raw), &cs); err != nil {
			return nil, err
		}
		result = append(result, cs)
	}
	return result, rows.Err()
}

// persistStagedChangeSetsInDB writes staged changeset parts without incrementing the project revision.
func persistStagedChangeSetsInDB(db documentDB, projectID string, changesets []ChangeSet) error {
	if len(changesets) == 0 {
		return nil
	}
	ctx := context.Background()
	var basePos int
	_ = db.QueryRowContext(ctx, "SELECT COALESCE(MAX(position)+1, 0) FROM project_parts WHERE project_id=? AND kind='changeset'", projectID).Scan(&basePos)
	for i, cs := range changesets {
		cs.ProjectID = projectID
		if err := validateChangeSet(&cs); err != nil {
			return err
		}
		result, err := db.ExecContext(ctx, `INSERT INTO project_parts(project_id,kind,id,position,document) VALUES(?,?,?,?,?) ON CONFLICT(project_id,kind,id) DO NOTHING`, projectID, "changeset", cs.ID, basePos+i, jsonText(cs))
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count == 0 {
			return errors.New("This changeset already exists. Reload its saved state")
		}
	}
	return nil
}

// upsertChangeSetInProject inserts or updates a ChangeSet inside project.Studio.ChangeSets.
func upsertChangeSetInProject(project *Project, cs ChangeSet) {
	for i := range project.Studio.ChangeSets {
		if project.Studio.ChangeSets[i].ID == cs.ID {
			project.Studio.ChangeSets[i] = cs
			return
		}
	}
	project.Studio.ChangeSets = append(project.Studio.ChangeSets, cs)
}

// findChangeSet locates a ChangeSet in project.Studio.ChangeSets by ID.
func findChangeSet(project *Project, id string) *ChangeSet {
	for i := range project.Studio.ChangeSets {
		if project.Studio.ChangeSets[i].ID == id {
			return &project.Studio.ChangeSets[i]
		}
	}
	return nil
}

// applyChangeSetToProject applies all operations in a ChangeSet to project and records before_value snapshots.
func applyChangeSetToProject(project *Project, cs *ChangeSet) error {
	if err := validateChangeSet(cs); err != nil {
		return err
	}
	if cs.Status == ChangeSetApplied {
		return errors.New("This changeset was already applied")
	}
	cs.BeforeStage = project.Studio.Stage
	cs.BeforeFactReferences = []FactReferenceSnapshot{}
	capturedFacts := map[string]bool{}
	idMap := map[string]string{}
	for i := range cs.Operations {
		op := &cs.Operations[i]
		switch op.TargetKind {
		case TargetLocation, TargetRoute:
			if err := applyWorldMapOperation(project, op); err != nil {
				return err
			}
		case TargetProjectMeta:
			var patch projectMetaPatch
			if err := decodeJSON(op.AfterValue, &patch); err != nil {
				return err
			}
			prevTitle, prevGenre, prevPremise, prevStyle, prevOutline := project.Title, project.Genre, project.Premise, project.Style, project.Outline
			prevWS, prevWords, prevPov := project.WritingStyle, project.DefaultChapterWords, project.NarrativePov
			op.BeforeValue = rawMessage(projectMetaPatch{
				Title:               &prevTitle,
				Genre:               &prevGenre,
				Premise:             &prevPremise,
				Style:               &prevStyle,
				Outline:             &prevOutline,
				WritingStyle:        &prevWS,
				DefaultChapterWords: &prevWords,
				NarrativePov:        &prevPov,
			})
			if patch.Title != nil {
				title, err := validateName(*patch.Title)
				if err != nil {
					return err
				}
				project.Title = title
			}
			if patch.Genre != nil {
				project.Genre = *patch.Genre
			}
			if patch.Premise != nil {
				project.Premise = *patch.Premise
			}
			if patch.Style != nil {
				project.Style = *patch.Style
			}
			if patch.Outline != nil {
				project.Outline = *patch.Outline
			}
			if patch.WritingStyle != nil {
				project.WritingStyle = *patch.WritingStyle
			}
			if patch.DefaultChapterWords != nil {
				if err := validateTarget(*patch.DefaultChapterWords); err != nil {
					return err
				}
				project.DefaultChapterWords = *patch.DefaultChapterWords
			}
			if patch.NarrativePov != nil {
				project.NarrativePov = *patch.NarrativePov
			}

		case TargetStudioGoal:
			var patch studioGoalPatch
			if err := decodeJSON(op.AfterValue, &patch); err != nil {
				return err
			}
			op.BeforeValue = rawMessage(studioGoalSnapshot(project))
			if patch.Mode != nil {
				if !contains([]string{"undecided", "serial", "traditional", "hybrid"}, *patch.Mode) {
					return errors.New("Invalid story form")
				}
				project.Studio.Mode = *patch.Mode
			}
			if patch.Stage != nil {
				if !contains([]string{"idea", "direction", "setting", "world", "planning", "writing", "review"}, *patch.Stage) {
					return errors.New("Invalid story stage")
				}
				project.Studio.Stage = *patch.Stage
			}
			if patch.TargetCharacters != nil {
				project.Studio.TargetCharacters = *patch.TargetCharacters
			}
			if patch.TargetChapters != nil {
				project.Studio.TargetChapters = *patch.TargetChapters
			}
			if patch.TargetVolumes != nil {
				project.Studio.TargetVolumes = *patch.TargetVolumes
			}
			if patch.Pacing != nil {
				project.Studio.Pacing = *patch.Pacing
			}
			if patch.NarrativeFocus != nil {
				project.Studio.NarrativeFocus = *patch.NarrativeFocus
			}
			if patch.Audience != nil {
				project.Studio.Audience = *patch.Audience
			}
			if patch.NamingStyle != nil {
				project.Studio.NamingStyle = *patch.NamingStyle
			}
			if patch.ForbiddenNames != nil {
				project.Studio.ForbiddenNames = append([]string{}, patch.ForbiddenNames...)
			}
			if patch.Preferences != nil {
				project.Studio.Preferences = append([]string{}, patch.Preferences...)
			}

		case TargetDecision:
			if op.OpType == OpDelete {
				kept := []StoryDecision{}
				for _, d := range project.Studio.Decisions {
					if d.ID == op.TargetID {
						op.BeforeValue = rawMessage(d)
						continue
					}
					kept = append(kept, d)
				}
				project.Studio.Decisions = kept
				continue
			}
			var decision StoryDecision
			if err := json.Unmarshal(op.AfterValue, &decision); err != nil {
				return err
			}
			if decision.ID == "" {
				decision.ID = newID()
			}
			if decision.Status == "" {
				decision.Status = "accepted"
			}
			if decision.CreatedAt == "" {
				decision.CreatedAt = now()
			}
			op.TargetID = decision.ID
			found := false
			for j := range project.Studio.Decisions {
				if project.Studio.Decisions[j].ID == decision.ID {
					op.BeforeValue = rawMessage(project.Studio.Decisions[j])
					project.Studio.Decisions[j] = decision
					found = true
					break
				}
			}
			if !found {
				op.BeforeValue = nil
				project.Studio.Decisions = append(project.Studio.Decisions, decision)
			}
			op.AfterValue = rawMessage(decision)

		case TargetEntity:
			if op.OpType == OpDelete {
				kept := []Entity{}
				for _, e := range project.Entities {
					if e.ID == op.TargetID {
						op.BeforeValue = rawMessage(e)
						continue
					}
					kept = append(kept, e)
				}
				project.Entities = kept
				continue
			}
			var rawEnt struct {
				ID          string   `json:"id"`
				Name        string   `json:"name"`
				Kind        string   `json:"kind"`
				Aliases     []string `json:"aliases"`
				Description string   `json:"description"`
			}
			if err := json.Unmarshal(op.AfterValue, &rawEnt); err != nil {
				return err
			}
			if rawEnt.Aliases == nil {
				rawEnt.Aliases = []string{}
			}
			if rawEnt.Kind == "" {
				rawEnt.Kind = "character"
			}
			entity := Entity{
				ID:          rawEnt.ID,
				Name:        rawEnt.Name,
				Kind:        rawEnt.Kind,
				Aliases:     rawEnt.Aliases,
				Description: rawEnt.Description,
			}
			for _, existing := range project.Entities {
				if (entity.ID != "" && existing.ID == entity.ID) || strings.EqualFold(existing.Name, entity.Name) {
					entity.ID = existing.ID
					op.BeforeValue = rawMessage(existing)
					if existing.Name != strings.TrimSpace(entity.Name) {
						captureRenamedFactReferences(project, cs, existing.Name, capturedFacts)
					}
					break
				}
			}
			if err := saveEntity(project, rawMessage(entity)); err != nil {
				return err
			}
			if resolved := project.ResolveEntity(entity.Name); resolved != nil {
				op.TargetID = resolved.ID
				op.AfterValue = rawMessage(*resolved)
			}

		case TargetMemory:
			if op.OpType == OpDelete {
				idx := memoryIndex(project, op.TargetID)
				if idx >= 0 {
					op.BeforeValue = rawMessage(project.Studio.Memories[idx])
					retired := project.Studio.Memories[idx]
					retired.Status = "rejected"
					if err := upsertMemory(project, retired); err != nil {
						return err
					}
				}
				continue
			}
			var memory StoryMemory
			if err := json.Unmarshal(op.AfterValue, &memory); err != nil {
				return err
			}
			memory.Status = "confirmed"
			if memory.Kind == "" {
				memory.Kind = "setting"
			}
			if idx := memoryIndex(project, memory.ID); idx >= 0 {
				op.BeforeValue = rawMessage(project.Studio.Memories[idx])
			} else if memory.Supersedes != "" {
				if idx := memoryIndex(project, memory.Supersedes); idx >= 0 {
					op.BeforeValue = rawMessage(project.Studio.Memories[idx])
				}
			}
			if err := upsertMemory(project, memory); err != nil {
				return err
			}
			saved := project.Studio.Memories[len(project.Studio.Memories)-1]
			if idx := memoryIndex(project, memory.ID); idx >= 0 && project.Studio.Memories[idx].Status == "confirmed" {
				saved = project.Studio.Memories[idx]
			}
			op.TargetID = saved.ID
			op.AfterValue = rawMessage(saved)

		case TargetPlan:
			if op.OpType == OpDelete {
				kept := []StoryPlan{}
				for _, p := range project.Studio.Plans {
					if p.ID == op.TargetID {
						op.BeforeValue = rawMessage(p)
						continue
					}
					kept = append(kept, p)
				}
				project.Studio.Plans = kept
				continue
			}
			var plan StoryPlan
			if err := json.Unmarshal(op.AfterValue, &plan); err != nil {
				return err
			}
			oldID := plan.ID
			if mapped, ok := idMap[plan.ParentID]; ok {
				plan.ParentID = mapped
			}
			op.BeforeValue = nil
			for _, existing := range project.Studio.Plans {
				if existing.ID == plan.ID {
					op.BeforeValue = rawMessage(existing)
					break
				}
			}
			if op.BeforeValue == nil && strings.HasPrefix(plan.ID, "pending-") {
				plan.ID = newID()
			}
			if plan.ID == "" {
				plan.ID = newID()
			}
			plan.Status = "confirmed"
			if err := upsertPlan(project, plan, true); err != nil {
				return err
			}
			if oldID != "" && oldID != plan.ID {
				idMap[oldID] = plan.ID
			}
			op.TargetID = plan.ID
			op.AfterValue = rawMessage(plan)

		case TargetChapter:
			var patch chapterProsePatch
			if err := decodeJSON(op.AfterValue, &patch); err != nil {
				return err
			}
			chapter := findChapter(project, op.TargetID)
			created := chapter == nil && op.OpType == OpUpsert
			if created {
				if patch.Title == nil || strings.TrimSpace(*patch.Title) == "" {
					return errors.New("A new chapter requires a title")
				}
				number := uint32(1)
				for _, item := range project.Chapters {
					number = max(number, item.Number+1)
				}
				if patch.Number != nil {
					number = *patch.Number
				}
				value := NewChapter(number, *patch.Title)
				if op.TargetID != "" {
					value.ID = op.TargetID
				}
				project.Chapters = append(project.Chapters, value)
				chapter = &project.Chapters[len(project.Chapters)-1]
				op.TargetID = chapter.ID
				patch.Number = &chapter.Number
			}
			if chapter == nil {
				return errors.New("Target chapter not found")
			}
			if patch.Number != nil && *patch.Number != chapter.Number {
				return errors.New("A prose change cannot renumber an existing chapter")
			}
			prevTitle, prevSyn := chapter.Title, chapter.Synopsis
			op.BeforeValue = rawMessage(chapterProsePatch{
				Title:    &prevTitle,
				Body:     chapter.Body,
				Synopsis: &prevSyn,
				Mode:     patch.Mode,
			})
			if created {
				op.BeforeValue = nil
			}
			nextTitle := chapter.Title
			if patch.Title != nil && strings.TrimSpace(*patch.Title) != "" {
				nextTitle = *patch.Title
			}
			nextSyn := chapter.Synopsis
			if patch.Synopsis != nil {
				nextSyn = *patch.Synopsis
			}
			nextBody := patch.Body
			if (op.OpType == OpAppendProse || patch.Mode == "append") && op.OpType != OpReplaceProse && patch.Mode != "replace" && chapter.Body != "" {
				nextBody = chapter.Body + "\n\n" + patch.Body
			}
			if len(nextBody) > 5_000_000 {
				return errors.New("The chapter exceeds 5 MB")
			}
			chapter.SetContent(nextTitle, nextBody, nextSyn, chapter.Status, chapter.TargetWords)
			op.AfterValue = rawMessage(patch)
			project.Studio.Stage = "writing"
		}
	}
	for i := range project.Studio.Plans {
		if mapped := idMap[project.Studio.Plans[i].ParentID]; mapped != "" {
			project.Studio.Plans[i].ParentID = mapped
		}
	}
	for i := range project.Studio.Locations {
		for j, id := range project.Studio.Locations[i].PlanIDs {
			if mapped := idMap[id]; mapped != "" {
				project.Studio.Locations[i].PlanIDs[j] = mapped
			}
		}
	}
	for i := range cs.Operations {
		op := &cs.Operations[i]
		if op.OpType != OpDelete && contains([]string{"plan", "location", "route"}, string(op.TargetKind)) {
			op.AfterValue = changeTargetValue(project, *op)
		}
	}
	if err := validateProject(project); err != nil {
		return err
	}
	cs.Status = ChangeSetApplied
	cs.UpdatedAt = now()
	return nil
}

// captureRenamedFactReferences records each affected fact once across a compound change.
func captureRenamedFactReferences(project *Project, cs *ChangeSet, name string, captured map[string]bool) {
	for _, fact := range project.Facts {
		if !captured[fact.ID] && (fact.Entity == name || fact.RelatedEntity == name) {
			cs.BeforeFactReferences = append(cs.BeforeFactReferences, FactReferenceSnapshot{FactID: fact.ID, Entity: fact.Entity, RelatedEntity: fact.RelatedEntity})
			captured[fact.ID] = true
		}
	}
}

// validateLegacyEntityUndo protects facts when an older rename omitted the identities needed for exact restoration.
func validateLegacyEntityUndo(project *Project, cs *ChangeSet) error {
	if cs.BeforeFactReferences != nil {
		return nil
	}
	for _, op := range cs.Operations {
		if op.TargetKind != TargetEntity || op.OpType != OpUpsert || len(op.BeforeValue) == 0 {
			continue
		}
		var before, after Entity
		if err := decodeJSON(op.BeforeValue, &before); err != nil {
			return err
		}
		if err := decodeJSON(op.AfterValue, &after); err != nil {
			return err
		}
		if before.Name == after.Name {
			continue
		}
		for _, fact := range project.Facts {
			if fact.Entity == after.Name || fact.RelatedEntity == after.Name {
				return errors.New("This older rename has no fact-reference snapshot. Review the current facts before reverting")
			}
		}
	}
	return nil
}

// undoChangeSetOnProject restores operations and the exact fact references captured before the change.
func undoChangeSetOnProject(project *Project, cs *ChangeSet) error {
	if cs.Status != ChangeSetApplied {
		return errors.New("Only applied changesets can be undone")
	}
	if err := validateLegacyEntityUndo(project, cs); err != nil {
		return err
	}
	for i := len(cs.Operations) - 1; i >= 0; i-- {
		op := cs.Operations[i]
		switch op.TargetKind {
		case TargetLocation, TargetRoute:
			if err := undoWorldMapOperation(project, op); err != nil {
				return err
			}
		case TargetProjectMeta:
			if len(op.BeforeValue) == 0 {
				continue
			}
			var prev projectMetaPatch
			if err := decodeJSON(op.BeforeValue, &prev); err != nil {
				return err
			}
			if prev.Title != nil {
				project.Title = *prev.Title
			}
			if prev.Genre != nil {
				project.Genre = *prev.Genre
			}
			if prev.Premise != nil {
				project.Premise = *prev.Premise
			}
			if prev.Style != nil {
				project.Style = *prev.Style
			}
			if prev.Outline != nil {
				project.Outline = *prev.Outline
			}
			if prev.WritingStyle != nil {
				project.WritingStyle = *prev.WritingStyle
			}
			if prev.DefaultChapterWords != nil {
				project.DefaultChapterWords = *prev.DefaultChapterWords
			}
			if prev.NarrativePov != nil {
				project.NarrativePov = *prev.NarrativePov
			}

		case TargetStudioGoal:
			if len(op.BeforeValue) == 0 {
				continue
			}
			var prev studioGoalPatch
			if err := decodeJSON(op.BeforeValue, &prev); err != nil {
				return err
			}
			if prev.Mode != nil {
				project.Studio.Mode = *prev.Mode
			}
			if prev.Stage != nil {
				project.Studio.Stage = *prev.Stage
			}
			if prev.TargetCharacters != nil {
				project.Studio.TargetCharacters = *prev.TargetCharacters
			}
			if prev.TargetChapters != nil {
				project.Studio.TargetChapters = *prev.TargetChapters
			}
			if prev.TargetVolumes != nil {
				project.Studio.TargetVolumes = *prev.TargetVolumes
			}
			if prev.Pacing != nil {
				project.Studio.Pacing = *prev.Pacing
			}
			if prev.NarrativeFocus != nil {
				project.Studio.NarrativeFocus = *prev.NarrativeFocus
			}
			if prev.Audience != nil {
				project.Studio.Audience = *prev.Audience
			}
			if prev.NamingStyle != nil {
				project.Studio.NamingStyle = *prev.NamingStyle
			}
			if prev.ForbiddenNames != nil {
				project.Studio.ForbiddenNames = append([]string{}, prev.ForbiddenNames...)
			}
			if prev.Preferences != nil {
				project.Studio.Preferences = append([]string{}, prev.Preferences...)
			}

		case TargetDecision:
			if len(op.BeforeValue) == 0 {
				kept := []StoryDecision{}
				for _, d := range project.Studio.Decisions {
					if d.ID != op.TargetID {
						kept = append(kept, d)
					}
				}
				project.Studio.Decisions = kept
			} else {
				var prev StoryDecision
				if err := decodeJSON(op.BeforeValue, &prev); err != nil {
					return err
				}
				replaced := false
				for j := range project.Studio.Decisions {
					if project.Studio.Decisions[j].ID == prev.ID {
						project.Studio.Decisions[j] = prev
						replaced = true
						break
					}
				}
				if !replaced {
					project.Studio.Decisions = append(project.Studio.Decisions, prev)
				}
			}

		case TargetEntity:
			if len(op.BeforeValue) == 0 {
				kept := []Entity{}
				for _, e := range project.Entities {
					if e.ID != op.TargetID {
						kept = append(kept, e)
					}
				}
				project.Entities = kept
			} else {
				var previous Entity
				if err := decodeJSON(op.BeforeValue, &previous); err != nil {
					return err
				}
				found := false
				for j := range project.Entities {
					if project.Entities[j].ID == previous.ID {
						project.Entities[j] = previous
						found = true
						break
					}
				}
				if !found {
					project.Entities = append(project.Entities, previous)
				}
			}

		case TargetMemory:
			if len(op.BeforeValue) == 0 {
				kept := []StoryMemory{}
				for _, m := range project.Studio.Memories {
					if m.ID != op.TargetID {
						kept = append(kept, m)
					}
				}
				project.Studio.Memories = kept
			} else {
				var prev StoryMemory
				if err := decodeJSON(op.BeforeValue, &prev); err != nil {
					return err
				}
				kept := []StoryMemory{}
				restored := false
				for _, m := range project.Studio.Memories {
					if m.ID == op.TargetID && m.ID != prev.ID {
						continue
					}
					if m.ID == prev.ID {
						kept = append(kept, prev)
						restored = true
						continue
					}
					kept = append(kept, m)
				}
				if !restored {
					kept = append(kept, prev)
				}
				project.Studio.Memories = kept
			}

		case TargetPlan:
			if len(op.BeforeValue) == 0 {
				kept := []StoryPlan{}
				for _, p := range project.Studio.Plans {
					if p.ID != op.TargetID {
						kept = append(kept, p)
					}
				}
				project.Studio.Plans = kept
			} else {
				var prev StoryPlan
				if err := decodeJSON(op.BeforeValue, &prev); err != nil {
					return err
				}
				replaced := false
				for j := range project.Studio.Plans {
					if project.Studio.Plans[j].ID == prev.ID {
						project.Studio.Plans[j] = prev
						replaced = true
						break
					}
				}
				if !replaced {
					project.Studio.Plans = append(project.Studio.Plans, prev)
				}
			}

		case TargetChapter:
			if len(op.BeforeValue) == 0 {
				kept := []Chapter{}
				for _, chapter := range project.Chapters {
					if chapter.ID != op.TargetID {
						kept = append(kept, chapter)
					}
				}
				project.Chapters = kept
				continue
			}
			var prev chapterProsePatch
			if err := decodeJSON(op.BeforeValue, &prev); err != nil {
				return err
			}
			chapter := findChapter(project, op.TargetID)
			if chapter == nil {
				return errors.New("Chapter to undo was not found")
			}
			title := chapter.Title
			if prev.Title != nil {
				title = *prev.Title
			}
			syn := chapter.Synopsis
			if prev.Synopsis != nil {
				syn = *prev.Synopsis
			}
			chapter.SetContent(title, prev.Body, syn, chapter.Status, chapter.TargetWords)
		}
	}
	previousReferences := map[string]FactReferenceSnapshot{}
	for _, reference := range cs.BeforeFactReferences {
		previousReferences[reference.FactID] = reference
	}
	for i := range project.Facts {
		if reference, found := previousReferences[project.Facts[i].ID]; found {
			project.Facts[i].Entity = reference.Entity
			project.Facts[i].RelatedEntity = reference.RelatedEntity
		}
	}
	if err := validateProject(project); err != nil {
		return err
	}
	if cs.BeforeStage != "" {
		project.Studio.Stage = cs.BeforeStage
	}
	cs.Status = ChangeSetUndone
	cs.UpdatedAt = now()
	return nil
}

// extractChangeSetFromTurn builds a deterministic ChangeSet from a conversation turn or text snippet.
func extractChangeSetFromTurn(project *Project, sessionID, turnID, text string) (ChangeSet, error) {
	cleaned := strings.TrimSpace(stripModelToolJSON(text))
	if cleaned == "" {
		return ChangeSet{}, errors.New("Select non-empty discussion text to extract settings")
	}
	title := localText(project, "Extracted story decision", "从讨论中提取的创作设定")
	for _, line := range strings.Split(cleaned, "\n") {
		trimmed := strings.TrimSpace(strings.TrimLeft(line, "#*- 1234567890."))
		if trimmed != "" {
			title = truncateSummary(trimmed, 48)
			break
		}
	}
	decision := StoryDecision{
		ID:        newID(),
		Title:     title,
		Content:   cleaned,
		Status:    "accepted",
		SessionID: sessionID,
		CreatedAt: now(),
	}
	ops := []ChangeOperation{
		{
			ID:         newID(),
			TargetKind: TargetDecision,
			TargetID:   decision.ID,
			OpType:     OpUpsert,
			Title:      title,
			Summary:    truncateSummary(cleaned, 140),
			AfterValue: rawMessage(decision),
		},
	}
	ops = append(ops, inferGoalOperations(project, title, cleaned)...)
	ts := now()
	return ChangeSet{
		ID:           newID(),
		ProjectID:    project.ID,
		SessionID:    sessionID,
		TurnID:       turnID,
		Source:       "extract",
		CommitPolicy: "review",
		Status:       ChangeSetStaged,
		BaseRevision: project.Revision,
		Summary:      title,
		Operations:   ops,
		CreatedAt:    ts,
		UpdatedAt:    ts,
	}, nil
}

// handleChangeSetCommand executes stage_changeset, apply_changeset, undo_changeset, extract_changeset, and list_changesets under engine.mu.
func (engine *Engine) handleChangeSetCommand(command string, projectID string, args *arguments) (any, error) {
	switch command {
	case "list_changesets":
		status := args.optionalString("status")
		if status != "" {
			return engine.store.listChangeSetsByStatus(projectID, status)
		}
		project, err := engine.store.Get(projectID)
		if err != nil {
			return nil, err
		}
		if project.Studio.ChangeSets == nil {
			return []ChangeSet{}, nil
		}
		return project.Studio.ChangeSets, nil

	case "stage_changeset":
		project, err := engine.store.Get(projectID)
		if err != nil {
			return nil, err
		}
		var cs ChangeSet
		if err := json.Unmarshal(args.fields["changeset"], &cs); err != nil {
			return nil, err
		}
		cs.ProjectID = project.ID
		cs.Status = ChangeSetStaged
		cs.ReviewToken = ""
		cs.PreviewRevision = 0
		cs.AppliedRevision = 0
		if cs.BaseRevision == 0 {
			cs.BaseRevision = project.Revision
		}
		if cs.CreatedAt == "" {
			cs.CreatedAt = now()
		}
		cs.UpdatedAt = now()
		if err := validateChangeSet(&cs); err != nil {
			return nil, err
		}
		if err := engine.store.withWriteTransaction(func(conn *sql.Conn) error {
			return persistStagedChangeSetsInDB(conn, project.ID, []ChangeSet{cs})
		}); err != nil {
			return nil, err
		}
		return cs, nil

	case "extract_changeset":
		project, err := engine.store.Get(projectID)
		if err != nil {
			return nil, err
		}
		project.requestLanguage = args.optionalString("ui_language")
		sessionID := args.optionalString("session_id")
		turnID := args.optionalString("turn_id")
		text := args.optionalString("text")
		if text == "" && sessionID != "" && turnID != "" {
			session, err := readConversation(engine.store.db, project.ID, sessionID)
			if err != nil {
				return nil, err
			}
			for _, turn := range session.Turns {
				if turn.ID == turnID {
					text = turn.Content
					break
				}
			}
		}
		cs, err := extractChangeSetFromTurn(&project, sessionID, turnID, text)
		if err != nil {
			return nil, err
		}
		if err := engine.store.withWriteTransaction(func(conn *sql.Conn) error {
			return persistStagedChangeSetsInDB(conn, project.ID, []ChangeSet{cs})
		}); err != nil {
			return nil, err
		}
		return cs, nil

	case "apply_changeset", "undo_changeset":
		return engine.reviewChangeSetCommand(command, projectID, args)
	}
	return nil, errors.New("Unsupported changeset command")
}
