package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// studioCommand routes studio capabilities without holding application locks during external requests.
func (engine *Engine) studioCommand(command string, args *arguments, raw json.RawMessage) (any, error) {
	engine.mu.Lock()
	closed := engine.closed
	engine.mu.Unlock()
	if closed {
		return nil, errors.New("The application is closing")
	}
	switch command {
	case "writing_skills":
		return writingSkills(), nil
	case "get_integrations":
		engine.mu.Lock()
		defer engine.mu.Unlock()
		if err := engine.refreshIntegrationsLocked(); err != nil {
			return nil, err
		}
		return integrationView(engine.integrations), nil
	case "save_integrations":
		settings, keys, err := decodeSettings(raw)
		if err != nil {
			return nil, err
		}
		return engine.saveIntegrations(settings, keys)
	case "search_web", "test_search":
		return engine.legacySearchCommand(command, args)
	case "retain_external", "recall_external", "reflect_external":
		return nil, errors.New("External memory integration has been retired. Use local story records")
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil, errors.New("The application is closing")
	}
	if err := engine.reconcileJobsLocked(); err != nil {
		return nil, err
	}
	projectID := args.text("project_id")
	if args.err != nil {
		return nil, args.err
	}
	switch command {
	case "list_conversations":
		offset := uint64(0)
		if raw, exists := args.fields["offset"]; exists {
			value, ok := unsigned(raw)
			if !ok || value > 1_000_000 {
				return nil, errors.New("Invalid conversation offset")
			}
			offset = value
		}
		return engine.store.Conversations(projectID, args.optionalString("chapter_id"), int(offset))
	case "create_conversation":
		return engine.store.createConversation(projectID, args.optionalString("chapter_id"), args.optionalString("title"))
	case "get_conversation":
		return readConversation(engine.store.db, projectID, args.text("session_id"))
	case "fork_conversation":
		return engine.store.forkConversation(projectID, args.text("session_id"), args.text("turn_id"))
	case "update_conversation":
		revision, ok := unsigned(args.fields["revision"])
		if !ok {
			return nil, errors.New("Missing conversation revision")
		}
		var title, draft *string
		if data, ok := args.fields["title"]; ok {
			var v string
			if err := decodeJSON(data, &v); err != nil {
				return nil, err
			}
			title = &v
		}
		if data, ok := args.fields["draft"]; ok {
			var v string
			if err := decodeJSON(data, &v); err != nil {
				return nil, err
			}
			draft = &v
		}
		return engine.store.updateConversation(projectID, args.text("session_id"), revision, title, draft)
	case "studio":
		project, err := engine.store.Get(projectID)
		if err != nil {
			return nil, err
		}
		project.requestLanguage = args.optionalString("ui_language")
		return map[string]any{"project": project, "warnings": studioWarnings(&project), "findings": studioFindings(&project, args.optionalString("chapter_id")), "readiness": computeFlowReadiness(&project)}, nil
	case "get_flow_readiness":
		project, err := engine.store.Get(projectID)
		if err != nil {
			return nil, err
		}
		project.requestLanguage = args.optionalString("ui_language")
		return computeFlowReadiness(&project), nil
	case "preview_changeset", "reject_changeset":
		return engine.reviewChangeSetCommand(command, projectID, args)
	case "stage_changeset", "apply_changeset", "undo_changeset", "extract_changeset", "list_changesets":
		return engine.handleChangeSetCommand(command, projectID, args)
	case "list_experiments", "record_experiment", "evaluate_policy":
		return engine.experimentCommand(command, args)
	case "review_proposal":
		return engine.reviewProposal(args)
	}
	return engine.mutateLocked(args, func(project *Project) error {
		switch command {
		case "save_studio":
			var state StudioState
			if err := decodeJSON(args.fields["studio"], &state); err != nil {
				return err
			}
			if err := validateStudio(&state); err != nil {
				return err
			}
			state.Plans = project.Studio.Plans
			state.Memories = project.Studio.Memories
			state.Decisions = project.Studio.Decisions
			state.ChangeSets = project.Studio.ChangeSets
			state.Locations = project.Studio.Locations
			state.Routes = project.Studio.Routes
			project.Studio = state
			if data, exists := args.fields["story"]; exists {
				var story struct {
					Title               string `json:"title"`
					Genre               string `json:"genre"`
					Premise             string `json:"premise"`
					Style               string `json:"style"`
					Outline             string `json:"outline"`
					WritingStyle        string `json:"writing_style"`
					DefaultChapterWords uint32 `json:"default_chapter_words"`
					NarrativePov        string `json:"narrative_pov"`
				}
				if err := decodeJSON(data, &story); err != nil {
					return err
				}
				title, err := validateName(story.Title)
				if err != nil {
					return err
				}
				if err := validateTarget(story.DefaultChapterWords); err != nil {
					return err
				}
				valid := false
				for _, preset := range writingPresets() {
					valid = valid || preset.ID == story.WritingStyle
				}
				if !valid || !contains([]string{"First person", "Third-person limited", "Omniscient"}, narrativePovLabel(story.NarrativePov)) {
					return errors.New("Invalid writing settings")
				}
				project.Title = title
				project.Genre = story.Genre
				project.Premise = story.Premise
				project.Style = story.Style
				project.Outline = story.Outline
				project.WritingStyle = story.WritingStyle
				project.DefaultChapterWords = story.DefaultChapterWords
				project.NarrativePov = story.NarrativePov
			}
		case "save_plan":
			var plan StoryPlan
			if err := decodeJSON(args.fields["plan"], &plan); err != nil {
				return err
			}
			if err := upsertPlan(project, plan); err != nil {
				return err
			}
		case "delete_plan":
			id := args.text("plan_id")
			for _, p := range project.Studio.Plans {
				if p.ParentID == id {
					return errors.New("Remove child plans before deleting their parent")
				}
			}
			plans := []StoryPlan{}
			for _, p := range project.Studio.Plans {
				if p.ID != id {
					plans = append(plans, p)
				}
			}
			project.Studio.Plans = plans
		case "create_planned_chapters":
			count := 0
			for _, plan := range project.Studio.Plans {
				if plan.Level != "chapter" || plan.Status != "confirmed" || plan.StartChapter == 0 {
					continue
				}
				exists := false
				for _, c := range project.Chapters {
					exists = exists || c.Number == plan.StartChapter
				}
				if exists {
					continue
				}
				chapter := NewChapter(plan.StartChapter, plan.Title)
				chapter.Synopsis = plan.Goal + "\n" + plan.Conflict + "\n" + plan.Consequence
				if plan.TargetCharacters > 0 {
					target := plan.TargetCharacters
					chapter.TargetWords = &target
				}
				project.Chapters = append(project.Chapters, chapter)
				count++
			}
			if count == 0 {
				return errors.New("No new confirmed chapter plans are available")
			}
			sortChapters(project)
			project.Studio.Stage = "writing"
		case "save_memory":
			var memory StoryMemory
			if err := decodeJSON(args.fields["memory"], &memory); err != nil {
				return err
			}
			if err := upsertMemory(project, memory); err != nil {
				return err
			}
		case "save_decision":
			var decision StoryDecision
			if err := decodeJSON(args.fields["decision"], &decision); err != nil {
				return err
			}
			if decision.ID == "" {
				decision.ID = newID()
				decision.CreatedAt = now()
			}
			found := false
			for i := range project.Studio.Decisions {
				if project.Studio.Decisions[i].ID == decision.ID {
					project.Studio.Decisions[i] = decision
					found = true
				}
			}
			if !found {
				project.Studio.Decisions = append(project.Studio.Decisions, decision)
			}
		default:
			return errors.New("Unsupported studio command")
		}
		return validateStudio(&project.Studio)
	})
}

// upsertPlan validates a single edit or defers validation to the enclosing atomic changeset.
func upsertPlan(project *Project, plan StoryPlan, deferValidation ...bool) error {
	if plan.ID == "" {
		plan.ID = newID()
	}
	if plan.Status == "" {
		plan.Status = "confirmed"
	}
	if plan.Status == "confirmed" && contains([]string{"idea", "direction", "setting", "world"}, project.Studio.Stage) {
		project.Studio.Stage = "planning"
	}
	if plan.Level == "chapter" && plan.StartChapter > 0 && plan.EndChapter == 0 {
		plan.EndChapter = plan.StartChapter
	}
	position := -1
	for i := range project.Studio.Plans {
		if project.Studio.Plans[i].ID == plan.ID {
			position = i
			break
		}
	}
	if position >= 0 {
		project.Studio.Plans[position] = plan
	} else {
		project.Studio.Plans = append(project.Studio.Plans, plan)
	}
	if len(deferValidation) > 0 && deferValidation[0] {
		return nil
	}
	return validateStudio(&project.Studio)
}

// reviewProposal converts legacy acceptance into a preview and records explicit author rejection.
func (engine *Engine) reviewProposal(args *arguments) (any, error) {
	project, err := engine.store.Get(args.text("project_id"))
	if err != nil {
		return nil, err
	}
	revision, ok := unsigned(args.fields["revision"])
	if !ok || revision != project.Revision {
		return nil, errors.New("The project changed. Refresh before reviewing this proposal")
	}
	job, err := engine.loadStoredJob(args.text("job_id"))
	if err != nil {
		return nil, err
	}
	if job.ProjectID != project.ID {
		return nil, errors.New("Task belongs to another project")
	}
	if job.Status != "done" {
		return nil, errors.New("Only completed proposals can be reviewed")
	}
	decision := args.text("decision")
	if !contains([]string{"accepted", "rejected"}, decision) {
		return nil, errors.New("Choose accept or reject")
	}
	_, ambiguous := legacyProposalLinks(&job)
	found := false
	for i := range job.Proposals {
		proposal := &job.Proposals[i]
		if proposal.ID != args.optionalString("proposal_id") {
			continue
		}
		if proposal.Status != "proposed" {
			return nil, errors.New("This proposal was already reviewed")
		}
		if ambiguous[proposal.ID] {
			return nil, errors.New("Older proposal source links are ambiguous. Create a new reviewed change from the saved story")
		}
		if decision == "accepted" {
			return engine.previewLegacyProposal(project, job, i, args)
		}
		found = true
		proposal.Status = "rejected"
		project.Studio.Decisions = append(project.Studio.Decisions, StoryDecision{ID: newID(), Title: proposal.Title, Content: proposal.Content, Status: "rejected", SessionID: job.SessionID, CreatedAt: now()})
	}
	if !found {
		return nil, errors.New("Proposal not found")
	}
	if err := validateStudio(&project.Studio); err != nil {
		return nil, err
	}
	if err := engine.store.withWriteTransaction(func(conn *sql.Conn) error {
		if err := saveProject(conn, &project, revision); err != nil {
			return err
		}
		_, err := conn.ExecContext(context.Background(), "UPDATE runs SET document=json_set(document,'$.proposals',json(?)) WHERE id=? AND project_id=?", jsonText(job.Proposals), job.ID, project.ID)
		return err
	}); err != nil {
		return nil, err
	}
	return map[string]any{"project": project, "job": job}, nil
}
