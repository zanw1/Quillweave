package core

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// prepareCLISession selects a durable desktop-compatible conversation and validates resumable task scope.
func prepareCLISession(engine *Engine, projectID string, opts *cliOptions) error {
	if opts.resumeID != "" {
		job, err := engine.loadJob(opts.resumeID, projectID)
		if err != nil {
			return err
		}
		if opts.sessionID == "" {
			opts.sessionID = job.SessionID
		}
		if opts.chapterID == "" {
			opts.chapterID = job.ChapterID
		}
	}
	if opts.sessionID != "" {
		_, err := readConversation(engine.store.db, projectID, opts.sessionID)
		return err
	}
	result, err := engine.Dispatch("create_conversation", rawMessage(map[string]any{"project_id": projectID, "chapter_id": "", "title": opts.title}))
	if err != nil {
		return err
	}
	var session Conversation
	if err := decodeJSON(rawMessage(result), &session); err != nil {
		return err
	}
	opts.sessionID = session.ID
	return nil
}

// runCLIWorkspace exposes the same conversation and task commands as the desktop application.
func runCLIWorkspace(engine *Engine, command string, opts cliOptions, stdout, stderr io.Writer) int {
	project, err := resolveCLIProject(engine, opts.projectRef)
	if err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	operation := "list"
	if len(opts.positional) > 1 {
		operation = opts.positional[1]
	}
	id := ""
	if len(opts.positional) > 2 {
		id = opts.positional[2]
	}
	args := map[string]any{"project_id": project.ID, "chapter_id": opts.chapterID, "session_id": id, "job_id": id, "title": opts.title, "offset": opts.offset}
	var result any
	switch command {
	case "read":
		result = project
		if len(opts.positional) > 1 {
			tools := map[string]string{"entity": "get_entity_detail", "chapter": "get_chapter_window", "plan": "get_plan_node", "map": "get_world_map", "source": "get_source_detail"}
			tool := tools[operation]
			if tool == "" {
				err = errors.New("Usage: quillweave read [entity|chapter|plan|map|source] [id]")
			} else {
				var text string
				text, err = engine.executeScopedAgentTool(&project, Job{ProjectID: project.ID, ChapterID: opts.chapterID, SessionID: opts.sessionID}, agentStepEnvelope{Tool: tool, EntityID: id, ChapterID: id, PlanID: id, SourceID: id})
				if err == nil {
					err = decodeJSON([]byte(text), &result)
				}
			}
		}
	case "session":
		dispatch := map[string]string{"list": "list_conversations", "create": "create_conversation", "show": "get_conversation", "fork": "fork_conversation"}[operation]
		if dispatch == "" || (operation == "show" || operation == "fork") && id == "" {
			err = errors.New("Usage: quillweave session <list|create|show|fork> [id] [turn-id]")
		} else {
			if operation == "fork" && len(opts.positional) > 3 {
				args["turn_id"] = opts.positional[3]
			}
			result, err = engine.Dispatch(dispatch, rawMessage(args))
		}
	case "job":
		dispatch := map[string]string{"list": "get_runs", "show": "get_job"}[operation]
		if dispatch == "" || operation != "list" && id == "" {
			err = errors.New("Usage: quillweave job <list|show> [id]")
		} else {
			if operation != "list" {
				_, err = engine.loadJob(id, project.ID)
			}
			if err == nil {
				result, err = engine.Dispatch(dispatch, rawMessage(args))
			}
		}
	}
	if err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	fmt.Fprintln(stdout, jsonText(result))
	return 0
}

// stageCLIChange accepts bounded structured candidates and returns a reviewable desktop-visible preview.
func stageCLIChange(engine *Engine, project Project, opts cliOptions, stdout, stderr io.Writer) int {
	if opts.inputPath == "" {
		return writeCLIError(stderr, opts.jsonOutput, errors.New("Supply --input with a JSON file containing summary and operations"))
	}
	file, err := os.Open(opts.inputPath)
	if err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 2_000_001))
	if err != nil || len(data) > 2_000_000 {
		return writeCLIError(stderr, opts.jsonOutput, errors.New("Unable to read a candidate JSON file within 2 MB"))
	}
	var candidate ChangeSet
	if err := decodeJSON(data, &candidate); err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	candidate = ChangeSet{ID: newID(), ProjectID: project.ID, BaseRevision: project.Revision, SessionID: opts.sessionID, CommitPolicy: "review", Source: "cli", Summary: candidate.Summary, Operations: candidate.Operations}
	if opts.sessionID != "" {
		if _, err := readConversation(engine.store.db, project.ID, opts.sessionID); err != nil {
			return writeCLIError(stderr, opts.jsonOutput, err)
		}
	}
	result, err := engine.Dispatch("stage_changeset", rawMessage(map[string]any{"project_id": project.ID, "changeset": candidate}))
	if err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	var staged ChangeSet
	if err := decodeJSON(rawMessage(result), &staged); err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	preview, err := engine.Dispatch("preview_changeset", rawMessage(map[string]any{"project_id": project.ID, "changeset_id": staged.ID}))
	if err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	if opts.jsonOutput {
		fmt.Fprintln(stdout, jsonText(preview))
		return 0
	}
	if err := printCLIChangePreview(stdout, preview); err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	return 0
}
