package core

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"quillweave/internal/platform"
	"strconv"
	"strings"
	"time"
)

// printCLIChangePreview shows the reviewed values and the exact confirmation command.
func printCLIChangePreview(stdout io.Writer, result any) error {
	var view struct {
		ChangeSet ChangeSet `json:"changeset"`
	}
	if err := decodeJSON(rawMessage(result), &view); err != nil {
		return err
	}
	cs := view.ChangeSet
	fmt.Fprintf(stdout, "Preview %s · project %s · revision %d\n", cs.ID, cs.ProjectID, cs.PreviewRevision)
	for _, op := range cs.Operations {
		fmt.Fprintf(stdout, "\n%s [%s]\nBefore: %s\nAfter: %s\n", op.Title, op.TargetKind, string(op.BeforeValue), string(op.PreviewValue))
	}
	fmt.Fprintf(stdout, "\nConfirm after review: quillweave change apply %s --project %s --confirm %s\n", cs.ID, cs.ProjectID, cs.ReviewToken)
	return nil
}

// ApplicationVersion is replaced with the release version when building the desktop and console companion together.
var ApplicationVersion = "0.6.1"

// IsCLICommand reports whether the first CLI argument invokes the headless CLI mode rather than the desktop GUI.
func IsCLICommand(arg string) bool {
	return contains([]string{"api", "writing", "connect", "projects", "status", "agent", "change", "goal", "session", "job", "read", "export", "import", "help", "--help", "-h", "version", "--version"}, strings.TrimSpace(arg))
}

type cliOptions struct {
	dbPath           string
	projectRef       string
	jsonOutput       bool
	readOnly         bool
	creator          bool
	client           string
	wait             bool
	backgroundWorker bool
	apply            bool
	confirm          string
	intent           string
	status           string
	format           string
	outputPath       string
	title            string
	genre            string
	premise          string
	mode             string
	stage            string
	chars            uint32
	chapters         uint32
	volumes          uint32
	sessionID        string
	chapterID        string
	requestID        string
	resumeID         string
	inputPath        string
	language         string
	offset           uint64
	positional       []string
}

// parseCLIFlags keeps value flags explicit, including the author's reviewed confirmation token.
func parseCLIFlags(args []string) (cliOptions, error) {
	opts := cliOptions{
		dbPath: os.Getenv("QUILLWEAVE_DB"),
		format: "manuscript",
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			opts.positional = append(opts.positional, args[i+1:]...)
			break
		}
		nextVal := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("Missing value for flag %s", arg)
			}
			i++
			return args[i], nil
		}
		switch arg {
		case "--offset":
			value, err := nextVal()
			if err != nil {
				return opts, err
			}
			offset, err := strconv.ParseUint(value, 10, 64)
			if err != nil || offset > 1_000_000 {
				return opts, errors.New("Invalid conversation offset")
			}
			opts.offset = offset
		case "--session", "--chapter", "--request-id", "--resume", "--input", "--language":
			value, err := nextVal()
			if err != nil {
				return opts, err
			}
			targets := map[string]*string{"--session": &opts.sessionID, "--chapter": &opts.chapterID, "--request-id": &opts.requestID, "--resume": &opts.resumeID, "--input": &opts.inputPath, "--language": &opts.language}
			*targets[arg] = value
		case "--db":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.dbPath = v
		case "-p", "--project":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.projectRef = v
		case "--json":
			opts.jsonOutput = true
		case "--read-only":
			opts.readOnly = true
		case "--creator":
			opts.creator = true
		case "--wait":
			opts.wait = true
		case "--background-worker":
			opts.backgroundWorker = true
		case "--client":
			value, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.client = value
		case "--apply":
			opts.apply = true
		case "--confirm":
			value, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.confirm = value
		case "--intent":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.intent = v
		case "--status":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.status = v
		case "--format", "-f":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.format = v
		case "-o", "--output":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.outputPath = v
		case "--title":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.title = v
		case "--genre":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.genre = v
		case "--premise":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.premise = v
		case "--mode":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.mode = v
		case "--stage":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			opts.stage = v
		case "--chars":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			n, parseErr := strconv.ParseUint(v, 10, 32)
			if parseErr != nil {
				return opts, errors.New("Invalid --chars integer")
			}
			opts.chars = uint32(n)
		case "--chapters":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			n, parseErr := strconv.ParseUint(v, 10, 32)
			if parseErr != nil {
				return opts, errors.New("Invalid --chapters integer")
			}
			opts.chapters = uint32(n)
		case "--volumes":
			v, err := nextVal()
			if err != nil {
				return opts, err
			}
			n, parseErr := strconv.ParseUint(v, 10, 32)
			if parseErr != nil {
				return opts, errors.New("Invalid --volumes integer")
			}
			opts.volumes = uint32(n)
		default:
			if strings.HasPrefix(arg, "--") {
				return opts, fmt.Errorf("Unknown flag %s. Put -- before an instruction beginning with --", arg)
			}
			opts.positional = append(opts.positional, arg)
		}
	}
	return opts, nil
}

// resolveCLIProject rejects ambiguous names before any project-scoped operation starts.
func resolveCLIProject(engine *Engine, ref string) (Project, error) {
	projects, err := engine.store.List()
	if err != nil {
		return Project{}, err
	}
	if len(projects) == 0 {
		return Project{}, errors.New("No projects found in database")
	}
	if ref == "" {
		if len(projects) > 1 {
			return Project{}, errors.New("Select one project with --project <id or unique title>")
		}
		return projects[0], nil
	}
	for _, p := range projects {
		if p.ID == ref {
			return p, nil
		}
	}
	matches := []Project{}
	for _, p := range projects {
		if strings.HasPrefix(p.ID, ref) || strings.Contains(lowerText(p.Title), lowerText(ref)) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return Project{}, errors.New("Project name is ambiguous. Select its full ID with --project")
	}
	return Project{}, fmt.Errorf("Project not found: %s", ref)
}

// writeCLIError exposes actionable failures and reserves exit code two for version conflicts.
func writeCLIError(stderr io.Writer, jsonMode bool, err error) int {
	code := 1
	msg := err.Error()
	if strings.Contains(lowerText(msg), "changed") || strings.Contains(lowerText(msg), "conflict") || strings.Contains(lowerText(msg), "revision") {
		code = 2
	}
	if jsonMode {
		fmt.Fprintln(stderr, jsonText(map[string]any{"error": msg, "code": code}))
	} else {
		fmt.Fprintln(stderr, "Error:", msg)
	}
	return code
}

// RunCLI executes headless Quillweave CLI commands against the shared SQLite store and Engine.
func RunCLI(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && contains([]string{"api", "writing", "connect"}, args[0]) {
		return RunCLIWithInput(args, os.Stdin, stdout, stderr)
	}
	if len(args) == 0 || contains([]string{"help", "--help", "-h"}, args[0]) {
		fmt.Fprintln(stdout, "Usage: quillweave <projects|status|read|agent|session|job|change|goal|export|import> [--project <id>] [--json]")
		fmt.Fprintln(stdout, "agent <instruction> [--session <id>] [--chapter <id>] [--resume <job-id>] [--request-id <id>]")
		fmt.Fprintln(stdout, "session <list|create|show|fork> [id] [turn-id]; job <list|show> [id]")
		fmt.Fprintln(stdout, "session list --offset <count> loads an older page of up to 100 conversations")
		fmt.Fprintln(stdout, "change stage --input <json-file>; change preview <id>; change apply <id> --confirm <review-token>")
		fmt.Fprintln(stdout, "api list|describe <operation>|call <operation> --input <file|-> --json; writing begin|compose|draft|review|status|refresh|finish|abandon --input <file|-> --json")
		fmt.Fprintln(stdout, "connect codex|claude|cursor|zcode [--project <id>]; quillweave mcp [--project <id>] [--read-only]; omit --project to choose the work on each call")
		return 0
	}
	if contains([]string{"version", "--version"}, args[0]) {
		fmt.Fprintf(stdout, "quillweave %s (api 1, database 6)\n", ApplicationVersion)
		return 0
	}
	opts, err := parseCLIFlags(args)
	if err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	if len(opts.positional) == 0 {
		return writeCLIError(stderr, opts.jsonOutput, errors.New("Missing CLI subcommand"))
	}
	dbPath := opts.dbPath
	if dbPath == "" {
		dir, dirErr := platform.DataDirectory()
		if dirErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, dirErr)
		}
		dbPath = filepath.Join(dir, "quillweave.sqlite3")
	}
	engine, err := NewEngine(dbPath)
	if err != nil {
		return writeCLIError(stderr, opts.jsonOutput, err)
	}
	defer engine.Close()

	cmd := opts.positional[0]
	switch cmd {
	case "projects":
		projects, err := engine.store.List()
		if err != nil {
			return writeCLIError(stderr, opts.jsonOutput, err)
		}
		if opts.jsonOutput {
			fmt.Fprintln(stdout, jsonText(projects))
		} else {
			for _, project := range projects {
				fmt.Fprintf(stdout, "%s\t%s\n", project.ID, project.Title)
			}
		}
		return 0
	case "session", "job", "read":
		return runCLIWorkspace(engine, cmd, opts, stdout, stderr)
	case "status":
		project, pErr := resolveCLIProject(engine, opts.projectRef)
		if pErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, pErr)
		}
		readiness := computeFlowReadiness(&project)
		staged, _ := engine.store.listChangeSetsByStatus(project.ID, string(ChangeSetStaged))
		if opts.jsonOutput {
			fmt.Fprintln(stdout, jsonText(map[string]any{
				"project_id":        project.ID,
				"title":             project.Title,
				"revision":          project.Revision,
				"mode":              project.Studio.Mode,
				"stage":             project.Studio.Stage,
				"readiness":         readiness,
				"staged_changesets": len(staged),
			}))
			return 0
		}
		fmt.Fprintf(stdout, "Project: %s (%s) | Revision: %d | Form: %s | Stage: %s\n", project.Title, project.ID, project.Revision, project.Studio.Mode, project.Studio.Stage)
		fmt.Fprintf(stdout, "Recommended Next Step: Step %d | Staged ChangeSets: %d\n", readiness.RecommendedStep, len(staged))
		for _, s := range readiness.Steps {
			fmt.Fprintf(stdout, "  [%d] %-8s %-12s (count=%d) %s\n", s.Index, s.Key, s.Status, s.Count, s.Hint)
		}
		return 0

	case "agent":
		if len(opts.positional) < 2 {
			return writeCLIError(stderr, opts.jsonOutput, errors.New("Usage: quillweave agent \"<instruction>\" [--session <id>] [--resume <job-id>]"))
		}
		project, pErr := resolveCLIProject(engine, opts.projectRef)
		if pErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, pErr)
		}
		instruction := strings.Join(opts.positional[1:], " ")
		policy := "review"
		if opts.apply {
			return writeCLIError(stderr, opts.jsonOutput, errors.New("Generate a preview first, then use change apply <id> --confirm <review-token>"))
		}
		if err := prepareCLISession(engine, project.ID, &opts); err != nil {
			return writeCLIError(stderr, opts.jsonOutput, err)
		}
		rawResp, dErr := engine.Dispatch("start_ai", rawMessage(map[string]any{
			"project_id":    project.ID,
			"chapter_id":    opts.chapterID,
			"session_id":    opts.sessionID,
			"request_id":    opts.requestID,
			"resume_job_id": opts.resumeID,
			"language":      opts.language,
			"action":        "agent",
			"instruction":   instruction,
			"intent_hint":   opts.intent,
			"commit_policy": policy,
		}))
		if dErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, dErr)
		}
		var job Job
		_ = decodeJSON([]byte(jsonText(rawResp)), &job)
		for job.Status == "running" {
			time.Sleep(100 * time.Millisecond)
			polled, pollErr := engine.Dispatch("get_job", rawMessage(map[string]any{"job_id": job.ID}))
			if pollErr != nil {
				return writeCLIError(stderr, opts.jsonOutput, pollErr)
			}
			_ = decodeJSON([]byte(jsonText(polled)), &job)
		}
		if job.Status != "done" {
			return writeCLIError(stderr, opts.jsonOutput, fmt.Errorf("Task %s (%s), conversation %s: %s", job.ID, job.Status, job.SessionID, job.Error))
		}
		updated, _ := engine.store.Get(project.ID)
		if opts.jsonOutput {
			fmt.Fprintln(stdout, jsonText(map[string]any{
				"job":        job,
				"project_id": updated.ID,
				"revision":   updated.Revision,
				"changesets": job.ChangeSets,
			}))
			return 0
		}
		fmt.Fprintf(stdout, "Conversation %s · task %s\n\n%s\n", job.SessionID, job.ID, job.Text)
		for _, cs := range job.ChangeSets {
			fmt.Fprintf(stdout, "\n[ChangeSet %s] status=%s operations=%d summary=%s\n", cs.ID, cs.Status, len(cs.Operations), cs.Summary)
		}
		return 0

	case "change":
		if len(opts.positional) < 2 {
			return writeCLIError(stderr, opts.jsonOutput, errors.New("Usage: quillweave change <stage|list|preview|apply|undo|reject> [changeset-id] [--input <json-file>] [--confirm <review-token>]"))
		}
		sub := opts.positional[1]
		project, pErr := resolveCLIProject(engine, opts.projectRef)
		if pErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, pErr)
		}
		switch sub {
		case "stage":
			return stageCLIChange(engine, project, opts, stdout, stderr)
		case "list":
			res, lErr := engine.Dispatch("list_changesets", rawMessage(map[string]any{
				"project_id": project.ID,
				"status":     opts.status,
			}))
			if lErr != nil {
				return writeCLIError(stderr, opts.jsonOutput, lErr)
			}
			if opts.jsonOutput {
				fmt.Fprintln(stdout, jsonText(res))
				return 0
			}
			var list []ChangeSet
			_ = decodeJSON([]byte(jsonText(res)), &list)
			for _, cs := range list {
				fmt.Fprintf(stdout, "%s\t%s\tops=%d\t%s\n", cs.ID, cs.Status, len(cs.Operations), cs.Summary)
			}
			return 0

		case "preview", "apply", "undo", "reject":
			csID := ""
			if len(opts.positional) >= 3 {
				csID = opts.positional[2]
			} else {
				return writeCLIError(stderr, opts.jsonOutput, fmt.Errorf("Usage: quillweave change %s <changeset-id>", sub))
			}
			dispatchCmd := sub + "_changeset"
			res, aErr := engine.Dispatch(dispatchCmd, rawMessage(map[string]any{
				"project_id":   project.ID,
				"revision":     project.Revision,
				"changeset_id": csID,
				"review_token": opts.confirm,
			}))
			if aErr != nil {
				return writeCLIError(stderr, opts.jsonOutput, aErr)
			}
			if opts.jsonOutput {
				fmt.Fprintln(stdout, jsonText(res))
				return 0
			}
			if sub == "preview" {
				if err := printCLIChangePreview(stdout, res); err != nil {
					return writeCLIError(stderr, opts.jsonOutput, err)
				}
			} else {
				fmt.Fprintf(stdout, "ChangeSet %s %s succeeded.\n", csID, sub)
			}
			return 0
		default:
			return writeCLIError(stderr, opts.jsonOutput, fmt.Errorf("Unknown change subcommand: %s", sub))
		}

	case "goal":
		if len(opts.positional) < 2 || opts.positional[1] != "set" {
			return writeCLIError(stderr, opts.jsonOutput, errors.New("Usage: quillweave goal set [--title ...] [--mode ...] [--chars ...]"))
		}
		project, pErr := resolveCLIProject(engine, opts.projectRef)
		if pErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, pErr)
		}
		ops := []ChangeOperation{}
		var meta projectMetaPatch
		hasMeta := false
		if opts.title != "" {
			t := opts.title
			meta.Title = &t
			hasMeta = true
		}
		if opts.genre != "" {
			g := opts.genre
			meta.Genre = &g
			hasMeta = true
		}
		if opts.premise != "" {
			p := opts.premise
			meta.Premise = &p
			hasMeta = true
		}
		if hasMeta {
			ops = append(ops, ChangeOperation{
				ID:         newID(),
				TargetKind: TargetProjectMeta,
				TargetID:   project.ID,
				OpType:     OpUpsert,
				Title:      "CLI project metadata update",
				Summary:    "Update project metadata via CLI",
				AfterValue: rawMessage(meta),
			})
		}
		var goal studioGoalPatch
		hasGoal := false
		if opts.mode != "" {
			m := opts.mode
			goal.Mode = &m
			hasGoal = true
		}
		if opts.stage != "" {
			s := opts.stage
			goal.Stage = &s
			hasGoal = true
		}
		if opts.chars > 0 {
			c := opts.chars
			goal.TargetCharacters = &c
			hasGoal = true
		}
		if opts.chapters > 0 {
			ch := opts.chapters
			goal.TargetChapters = &ch
			hasGoal = true
		}
		if opts.volumes > 0 {
			v := opts.volumes
			goal.TargetVolumes = &v
			hasGoal = true
		}
		if hasGoal {
			ops = append(ops, ChangeOperation{
				ID:         newID(),
				TargetKind: TargetStudioGoal,
				TargetID:   project.ID,
				OpType:     OpUpsert,
				Title:      "CLI studio goal update",
				Summary:    summarizeGoalPatch(&project, goal),
				AfterValue: rawMessage(goal),
			})
		}
		if len(ops) == 0 {
			return writeCLIError(stderr, opts.jsonOutput, errors.New("Specify at least one goal flag (--title, --genre, --premise, --mode, --stage, --chars, --chapters, --volumes)"))
		}
		stagedRaw, sErr := engine.Dispatch("stage_changeset", rawMessage(map[string]any{
			"project_id": project.ID,
			"changeset": ChangeSet{
				Source:       "cli",
				CommitPolicy: "review",
				Summary:      "CLI goal set",
				Operations:   ops,
			},
		}))
		if sErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, sErr)
		}
		var staged ChangeSet
		_ = decodeJSON([]byte(jsonText(stagedRaw)), &staged)
		preview, aErr := engine.Dispatch("preview_changeset", rawMessage(map[string]any{
			"project_id":   project.ID,
			"revision":     project.Revision,
			"changeset_id": staged.ID,
		}))
		if aErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, aErr)
		}
		if opts.jsonOutput {
			fmt.Fprintln(stdout, jsonText(preview))
			return 0
		}
		if err := printCLIChangePreview(stdout, preview); err != nil {
			return writeCLIError(stderr, opts.jsonOutput, err)
		}
		return 0

	case "export":
		project, pErr := resolveCLIProject(engine, opts.projectRef)
		if pErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, pErr)
		}
		res, eErr := engine.Dispatch("export", rawMessage(map[string]any{
			"project_id": project.ID,
			"format":     opts.format,
		}))
		if eErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, eErr)
		}
		var exported map[string]string
		_ = decodeJSON([]byte(jsonText(res)), &exported)
		if opts.outputPath != "" {
			if err := os.WriteFile(opts.outputPath, []byte(exported["content"]), 0600); err != nil {
				return writeCLIError(stderr, opts.jsonOutput, err)
			}
		}
		if opts.jsonOutput {
			fmt.Fprintln(stdout, jsonText(exported))
			return 0
		}
		if opts.outputPath != "" {
			fmt.Fprintf(stdout, "Exported %s to %s\n", exported["filename"], opts.outputPath)
		} else {
			fmt.Fprintln(stdout, exported["content"])
		}
		return 0

	case "import":
		if len(opts.positional) < 2 {
			return writeCLIError(stderr, opts.jsonOutput, errors.New("Usage: quillweave import <archive-file>"))
		}
		data, rErr := os.ReadFile(opts.positional[1])
		if rErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, rErr)
		}
		res, iErr := engine.Dispatch("import_archive", rawMessage(map[string]any{
			"content": string(data),
		}))
		if iErr != nil {
			return writeCLIError(stderr, opts.jsonOutput, iErr)
		}
		if opts.jsonOutput {
			fmt.Fprintln(stdout, jsonText(res))
			return 0
		}
		var imported Project
		_ = decodeJSON([]byte(jsonText(res)), &imported)
		fmt.Fprintf(stdout, "Imported project %s (%s)\n", imported.Title, imported.ID)
		return 0
	}
	return writeCLIError(stderr, opts.jsonOutput, fmt.Errorf("Unknown CLI command: %s", cmd))
}
