package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"time"
)

type Engine struct {
	mu      sync.Mutex
	store   *Store
	models  ModelSettings
	closed  bool
	jobsMu  sync.Mutex
	jobs    map[string]*Job
	ctx     context.Context
	cancel  context.CancelFunc
	workers sync.WaitGroup
}
type arguments struct {
	fields map[string]json.RawMessage
	err    error
}

// NewEngine shares the domain service between desktop and loopback HTTP with process-local keys.
func NewEngine(path string) (*Engine, error) {
	store, err := OpenStore(path)
	if err != nil {
		return nil, err
	}
	models, err := store.ModelSettings()
	if err != nil {
		store.Close()
		return nil, err
	}
	models = models.WithoutSecrets()
	for i := range models.Profiles {
		if models.Profiles[i].ID == models.DefaultProfile {
			models.Profiles[i].Config.APIKey = os.Getenv("QUILLWEAVE_API_KEY")
			break
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Engine{store: store, models: models, jobs: map[string]*Job{}, ctx: ctx, cancel: cancel}, nil
}

// Close cancels requests, waits for workers, and then closes the database.
func (engine *Engine) Close() error {
	engine.mu.Lock()
	if engine.closed {
		engine.mu.Unlock()
		return nil
	}
	engine.closed = true
	engine.cancel()
	engine.mu.Unlock()
	engine.workers.Wait()
	return engine.store.Close()
}

// refreshModelsLocked refreshes persistent metadata under engine.mu and merges matching process-local keys.
func (engine *Engine) refreshModelsLocked() error {
	latest, err := engine.store.ModelSettings()
	if err != nil {
		return err
	}
	latest = latest.WithoutSecrets()
	for i := range latest.Profiles {
		latest.Profiles[i].Config.APIKey = engine.models.keyFor(latest.Profiles[i])
	}
	engine.models = latest
	return nil
}

// text retains missing-string errors throughout subsequent argument validation.
func (args *arguments) text(key string) string {
	var value string
	raw, exists := args.fields[key]
	if !exists || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || decodeJSON(raw, &value) != nil {
		if args.err == nil {
			args.err = fmt.Errorf("Missing argument: %s", key)
		}
		return ""
	}
	return value
}

// optionalString reads an optional identifier and treats incompatible types as unmatched.
func (args *arguments) optionalString(key string) string {
	var value string
	_ = decodeJSON(args.fields[key], &value)
	return value
}

// unsigned accepts unsigned JSON integers without truncating floating-point values.
func unsigned(raw json.RawMessage) (uint64, bool) {
	var value uint64
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || decodeJSON(raw, &value) != nil {
		return 0, false
	}
	return value, true
}

// flag uses the original false default for non-boolean input.
func (args *arguments) flag(key string) bool {
	var value bool
	_ = decodeJSON(args.fields[key], &value)
	return value
}

// validateName trims titles and names and checks their length.
func validateName(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" || charCount(value) > 120 {
		return "", errors.New("Names must contain 1–120 characters")
	}
	return value, nil
}

// optionalTarget distinguishes an omitted value, explicit null, and a chapter override.
func optionalTarget(args *arguments, key string, previous *uint32) (*uint32, error) {
	raw, exists := args.fields[key]
	if !exists {
		return previous, nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	number, ok := unsigned(raw)
	if !ok || number > math.MaxUint32 {
		return nil, errors.New("Target length must be a positive integer")
	}
	target := uint32(number)
	if err := validateTarget(target); err != nil {
		return nil, err
	}
	return &target, nil
}

// applyWritingSettings updates only the writing parameters explicitly supplied by a request.
func applyWritingSettings(project *Project, args *arguments) error {
	if raw, exists := args.fields["writing_style"]; exists {
		var style string
		if string(raw) == "null" || decodeJSON(raw, &style) != nil {
			return errors.New("Select a valid writing style")
		}
		valid := false
		for _, preset := range writingPresets() {
			valid = valid || preset.ID == style
		}
		if !valid {
			return errors.New("Unknown writing style")
		}
		project.WritingStyle = style
	}
	if _, exists := args.fields["default_chapter_words"]; exists {
		target, err := optionalTarget(args, "default_chapter_words", nil)
		if err != nil {
			return err
		}
		if target == nil {
			return errors.New("Default chapter length is required")
		}
		project.DefaultChapterWords = *target
	}
	if raw, exists := args.fields["narrative_pov"]; exists {
		var pov string
		if string(raw) == "null" || decodeJSON(raw, &pov) != nil {
			return errors.New("Invalid narrative viewpoint format")
		}
		if !contains([]string{"First person", "Third-person limited", "Omniscient"}, narrativePovLabel(pov)) {
			return errors.New("Select a valid narrative viewpoint")
		}
		project.NarrativePov = pov
	}
	return nil
}

// mutateLocked saves text, versions, and derived data through one revision-protected document update.
func (engine *Engine) mutateLocked(args *arguments, change func(*Project) error) (any, error) {
	project, err := engine.store.Get(args.text("project_id"))
	if args.err != nil {
		return nil, args.err
	}
	if err != nil {
		return nil, err
	}
	expected, ok := unsigned(args.fields["revision"])
	if !ok {
		return nil, errors.New("Missing project revision")
	}
	if project.Revision != expected {
		return nil, errors.New("The project changed. Your edits are retained. Refresh and try again.")
	}
	if err := change(&project); err != nil {
		return nil, err
	}
	if args.err != nil {
		return nil, args.err
	}
	if err := engine.store.Save(&project, expected); err != nil {
		return nil, err
	}
	return project, nil
}

// settingsView returns credential-presence flags with the redacted model configuration.
func settingsView(models ModelSettings) map[string]any {
	status := map[string]bool{}
	for _, profile := range models.Profiles {
		status[profile.ID] = profile.Config.APIKey != ""
	}
	return map[string]any{"settings": models.WithoutSecrets(), "key_status": status}
}

// Dispatch serves the shared business commands for desktop and HTTP transports.
func (engine *Engine) Dispatch(command string, raw json.RawMessage) (result any, err error) {
	if len(raw) == 0 {
		raw = json.RawMessage("null")
	}
	var decoded any
	if err = decodeJSON(raw, &decoded); err != nil {
		return nil, err
	}
	args := &arguments{fields: map[string]json.RawMessage{}}
	if _, ok := decoded.(map[string]any); ok {
		_ = decodeJSON(raw, &args.fields)
	}
	defer func() {
		if args.err != nil {
			result = nil
			err = args.err
		}
	}()
	switch command {
	case "start_ai":
		var request AIRequest
		if decodeJSON(raw, &request) != nil {
			return nil, errors.New("The AI request is incomplete")
		}
		return engine.start(request)
	case "test_model":
		id := args.optionalString("profile_id")
		return engine.testModel(id)
	case "list_provider_models":
		return engine.discoverModels(raw)
	case "get_job":
		id := args.text("job_id")
		engine.jobsMu.Lock()
		defer engine.jobsMu.Unlock()
		job := engine.jobs[id]
		if job == nil {
			return nil, errors.New("The task is unavailable or the app restarted. Check task history")
		}
		return job.snapshot(), nil
	case "cancel_ai":
		id := args.text("job_id")
		engine.jobsMu.Lock()
		job := engine.jobs[id]
		accepted := job != nil && job.Status == "running" && !job.committing
		if accepted {
			job.cancel()
		}
		engine.jobsMu.Unlock()
		return map[string]bool{"cancelled": accepted}, nil
	}
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if engine.closed {
		return nil, errors.New("The application is closing")
	}
	switch command {
	case "list_projects":
		projects, err := engine.store.List()
		if err != nil {
			return nil, err
		}
		result := []map[string]any{}
		for _, project := range projects {
			words := 0
			for _, chapter := range project.Chapters {
				words += wordCount(chapter.Body)
			}
			result = append(result, map[string]any{"id": project.ID, "title": project.Title, "genre": project.Genre, "premise": project.Premise, "updated_at": project.UpdatedAt, "chapter_count": len(project.Chapters), "word_count": words})
		}
		return result, nil
	case "get_project":
		return engine.store.Get(args.text("project_id"))
	case "delete_project":
		id := args.text("project_id")
		expected, ok := unsigned(args.fields["revision"])
		if args.err != nil {
			return nil, args.err
		}
		if !ok {
			return nil, errors.New("Missing project revision")
		}
		engine.jobsMu.Lock()
		defer engine.jobsMu.Unlock()
		for _, job := range engine.jobs {
			if job.ProjectID == id && job.Status == "running" {
				return nil, errors.New("This project has a running AI task. Stop it and wait for it to finish before deleting")
			}
		}
		if err := engine.store.DeleteProject(id, expected); err != nil {
			return nil, err
		}
		for jobID, job := range engine.jobs {
			if job.ProjectID == id {
				if job.cancel != nil {
					job.cancel()
				}
				delete(engine.jobs, jobID)
			}
		}
		return map[string]bool{"deleted": true}, nil
	case "knowledge_graph":
		project, err := engine.store.Get(args.text("project_id"))
		if err != nil {
			return nil, err
		}
		through := uint32(math.MaxUint32)
		if raw, exists := args.fields["through_chapter"]; exists {
			number, ok := unsigned(raw)
			if !ok || number > math.MaxUint32 {
				return nil, errors.New("Chapter number must be a nonnegative integer")
			}
			through = uint32(number)
		}
		return buildGraph(&project, through, args.flag("include_pending")), nil
	case "wiki_health":
		project, err := engine.store.Get(args.text("project_id"))
		if err != nil {
			return nil, err
		}
		records := []map[string]any{}
		for _, record := range latestCompilations(&project) {
			records = append(records, map[string]any{"record": record, "current": compilationCurrent(&project, record), "source_title": sourceTitle(&project, record.SourceID)})
		}
		return map[string]any{"findings": lintWiki(&project), "compilations": records, "compiler_version": CompilerVersion}, nil
	case "create_project":
		title, err := validateName(args.text("title"))
		if err != nil {
			return nil, err
		}
		project := NewProject(title, args.text("genre"), args.text("premise"))
		if err := applyWritingSettings(&project, args); err != nil {
			return nil, err
		}
		if args.err != nil {
			return nil, args.err
		}
		if err := engine.store.Insert(&project); err != nil {
			return nil, err
		}
		return project, nil
	case "create_example":
		project := ExampleProject()
		if err := engine.store.Insert(&project); err != nil {
			return nil, err
		}
		return project, nil
	case "update_project":
		return engine.mutateLocked(args, func(project *Project) error {
			title, err := validateName(args.text("title"))
			if err != nil {
				return err
			}
			project.Title = title
			project.Genre = args.text("genre")
			project.Premise = args.text("premise")
			project.Style = args.text("style")
			project.Outline = args.text("outline")
			return applyWritingSettings(project, args)
		})
	case "new_chapter":
		return engine.mutateLocked(args, func(project *Project) error {
			number := uint32(0)
			for _, chapter := range project.Chapters {
				number = max(number, chapter.Number)
			}
			if number == math.MaxUint32 {
				return errors.New("Chapter count is out of range")
			}
			title, err := validateName(args.text("title"))
			if err != nil {
				return err
			}
			chapter := NewChapter(number+1, title)
			chapter.TargetWords, err = optionalTarget(args, "target_words", nil)
			if err != nil {
				return err
			}
			project.Chapters = append(project.Chapters, chapter)
			return nil
		})
	case "save_chapter":
		return engine.mutateLocked(args, func(project *Project) error {
			chapter := findChapter(project, args.optionalString("chapter_id"))
			if chapter == nil {
				return errors.New("Chapter not found")
			}
			status := args.text("status")
			if !contains([]string{"draft", "final"}, status) {
				return errors.New("Invalid chapter status")
			}
			title, err := validateName(args.text("title"))
			if err != nil {
				return err
			}
			body, synopsis := args.text("body"), args.text("synopsis")
			target, err := optionalTarget(args, "target_words", chapter.TargetWords)
			if err != nil {
				return err
			}
			chapter.SetContent(title, body, synopsis, status, target)
			return nil
		})
	case "restore_version":
		return engine.mutateLocked(args, func(project *Project) error {
			chapter := findChapter(project, args.optionalString("chapter_id"))
			if chapter == nil {
				return errors.New("Chapter not found")
			}
			for _, version := range chapter.Versions {
				if version.ID == args.optionalString("version_id") {
					chapter.SetContent(version.Title, version.Body, version.Synopsis, version.Status, version.TargetWords)
					return nil
				}
			}
			return errors.New("Version not found")
		})
	case "save_entity":
		return engine.mutateLocked(args, func(project *Project) error { return saveEntity(project, args.fields["entity"]) })
	case "add_source":
		return engine.mutateLocked(args, func(project *Project) error {
			content := args.text("content")
			if strings.TrimSpace(content) == "" || len(content) > 5_000_000 {
				return errors.New("Source content is required and must be smaller than 5 MB")
			}
			category := args.text("category")
			if !contains([]string{"research", "craft", "world"}, category) {
				return errors.New("Select reference, writing method, or worldbuilding as the category")
			}
			title, err := validateName(args.text("title"))
			if err != nil {
				return err
			}
			project.Sources = append(project.Sources, Source{ID: newID(), Title: title, Category: category, URL: args.text("url"), Content: content, CreatedAt: now()})
			return nil
		})
	case "review_fact":
		return engine.mutateLocked(args, func(project *Project) error {
			index := -1
			for i, fact := range project.Facts {
				if fact.ID == args.optionalString("fact_id") {
					index = i
					break
				}
			}
			if index < 0 {
				return errors.New("Analysis entry not found")
			}
			status := args.text("status")
			if !contains([]string{"confirmed", "rejected", "pending"}, status) {
				return errors.New("Invalid review status")
			}
			if status == "confirmed" && !factCurrent(project, &project.Facts[index]) {
				return errors.New("The source text changed. Analyze it again before confirming")
			}
			project.Facts[index].Status = status
			return nil
		})
	case "review_memory":
		return engine.mutateLocked(args, func(project *Project) error {
			index := -1
			for i, record := range project.Compilations {
				if record.ID == args.optionalString("memory_id") {
					index = i
					break
				}
			}
			if index < 0 {
				return errors.New("Story memory not found")
			}
			status := args.text("status")
			if !contains([]string{"pending", "confirmed", "rejected"}, status) {
				return errors.New("Invalid review status")
			}
			if status == "confirmed" {
				record := &project.Compilations[index]
				if !compilationCurrent(project, record) {
					return errors.New("The memory source or tracking focus changed. Compile it again")
				}
				for _, section := range record.Sections {
					for _, id := range section.EvidenceIDs {
						found := false
						for _, fact := range project.Facts {
							found = found || fact.ID == id && fact.Status == "confirmed"
						}
						if !found {
							return errors.New("Review and confirm the evidence cited by this memory first")
						}
					}
				}
			}
			project.Compilations[index].Status = status
			return nil
		})
	case "save_wiki_purpose":
		return engine.mutateLocked(args, func(project *Project) error {
			purpose := strings.TrimSpace(args.text("purpose"))
			if purpose == "" || charCount(purpose) > 4000 {
				return errors.New("Tracking focus must contain 1–4,000 characters")
			}
			project.WikiPurpose = purpose
			return nil
		})
	case "search":
		project, err := engine.store.Get(args.text("project_id"))
		if err != nil {
			return nil, err
		}
		through, ok := unsigned(args.fields["through_chapter"])
		if !ok {
			through = math.MaxUint32
		}
		return retrieve(&project, args.text("query"), uint32(min(through, math.MaxUint32)), 20), nil
	case "import_legacy":
		path := args.text("path")
		if args.err != nil {
			return nil, args.err
		}
		project, err := importLegacy(path)
		if err != nil {
			return nil, err
		}
		if err := engine.store.Insert(&project); err != nil {
			return nil, err
		}
		return project, nil
	case "import_archive":
		content := args.text("content")
		if args.err != nil {
			return nil, args.err
		}
		if len(content) > 40_000_000 {
			return nil, errors.New("The backup exceeds the 40 MB import limit")
		}
		var value any
		if decodeJSON([]byte(content), &value) != nil {
			return nil, errors.New("The backup is not valid JSON")
		}
		archive, _ := value.(map[string]any)
		if archive["format"] != "quillweave-1" {
			return nil, errors.New("Unsupported backup format")
		}
		var project Project
		if decodeJSON(rawMessage(archive["project"]), &project) != nil {
			return nil, errors.New("The backup is incomplete")
		}
		if err := validateProject(&project); err != nil {
			return nil, err
		}
		project.ID = newID()
		project.Revision = 0
		project.UpdatedAt = now()
		if err := engine.store.Insert(&project); err != nil {
			return nil, err
		}
		return project, nil
	case "export":
		project, err := engine.store.Get(args.text("project_id"))
		if err != nil {
			return nil, err
		}
		format := args.text("format")
		filename, content := "", ""
		switch format {
		case "archive":
			filename = project.Title + ".quillweave.json"
			var buffer bytes.Buffer
			encoder := json.NewEncoder(&buffer)
			encoder.SetIndent("", "  ")
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(map[string]any{"format": "quillweave-1", "project": project}); err != nil {
				return nil, err
			}
			content = strings.TrimSuffix(buffer.String(), "\n")
		case "manuscript":
			filename = project.Title + ".md"
			chapters := []string{}
			for _, chapter := range project.Chapters {
				chapters = append(chapters, fmt.Sprintf("## Chapter %d — %s\n\n%s", chapter.Number, chapter.Title, chapter.Body))
			}
			content = "# " + project.Title + "\n\n" + strings.Join(chapters, "\n\n")
		case "wiki":
			filename = project.Title + "-wiki.md"
			content = renderWiki(&project)
		default:
			return nil, errors.New("Unknown export format")
		}
		return map[string]string{"filename": filename, "content": content}, nil
	case "get_model_settings":
		if err := engine.refreshModelsLocked(); err != nil {
			return nil, err
		}
		return settingsView(engine.models), nil
	case "save_model_settings":
		var proposed ModelSettings
		if decodeJSON(args.fields["settings"], &proposed) != nil {
			return nil, errors.New("Model settings are incomplete")
		}
		updates := map[string]SecretUpdate{}
		if raw, exists := args.fields["key_updates"]; exists {
			if string(raw) == "null" || decodeJSON(raw, &updates) != nil {
				return nil, errors.New("Invalid API key operation format")
			}
		}
		if err := engine.refreshModelsLocked(); err != nil {
			return nil, err
		}
		next, err := updateModels(engine.models, proposed, updates)
		if err != nil {
			return nil, err
		}
		if err := engine.store.SaveModelSettings(next, engine.models.Revision); err != nil {
			return nil, err
		}
		engine.models = next
		return settingsView(engine.models), nil
	case "set_project_models":
		var overrides map[string]string
		raw := args.fields["overrides"]
		if decodeStringMap(raw, &overrides) != nil {
			return nil, errors.New("Invalid project model selection format")
		}
		id := args.text("project_id")
		if args.err != nil {
			return nil, args.err
		}
		expected, ok := unsigned(args.fields["revision"])
		if !ok {
			return nil, errors.New("Missing project revision")
		}
		project, err := engine.store.SetProjectModels(id, expected, overrides)
		if err != nil {
			return nil, err
		}
		return project, nil
	case "writing_presets":
		return writingPresets(), nil
	case "provider_presets":
		return providerCatalog(), nil
	case "prepare_ai":
		var request AIRequest
		if decodeJSON(raw, &request) != nil {
			return nil, errors.New("The AI request is incomplete")
		}
		project, err := engine.store.Get(request.ProjectID)
		if err != nil {
			return nil, err
		}
		if err := engine.refreshModelsLocked(); err != nil {
			return nil, err
		}
		budget := 24000
		if model, err := engine.models.Select(&project, request.Action); err == nil {
			budget = int(model.Config.ContextChars)
			request, err = engine.restoreProviderHistory(request, model.Config, model.ProfileID)
			if err != nil {
				return nil, err
			}
		}
		return prepareAI(&project, request, budget)
	case "get_runs":
		return engine.store.Runs(args.text("project_id"))
	default:
		return nil, fmt.Errorf("Unknown command: %s", command)
	}
}

// findChapter finds a chapter by its stable identifier.
func findChapter(project *Project, id string) *Chapter {
	for i := range project.Chapters {
		if project.Chapters[i].ID == id {
			return &project.Chapters[i]
		}
	}
	return nil
}

// saveEntity enforces alias uniqueness and updates both ends of existing evidence after a rename.
func saveEntity(project *Project, raw json.RawMessage) error {
	var entity Entity
	if decodeJSON(raw, &entity) != nil {
		return errors.New("Character or entry information is incomplete")
	}
	name, err := validateName(entity.Name)
	if err != nil {
		return err
	}
	entity.Name = name
	if !contains(EntityKinds, entity.Kind) {
		return errors.New("Invalid entry type")
	}
	identifiers := map[string]bool{entity.Name: true}
	for i, alias := range entity.Aliases {
		name, err := validateName(alias)
		if err != nil {
			return err
		}
		entity.Aliases[i] = name
		if identifiers[name] {
			return errors.New("Names and aliases must be unique")
		}
		identifiers[name] = true
	}
	for _, existing := range project.Entities {
		if existing.ID == entity.ID {
			continue
		}
		if identifiers[existing.Name] {
			return errors.New("Another entry already uses this name or alias")
		}
		for _, alias := range existing.Aliases {
			if identifiers[alias] {
				return errors.New("Another entry already uses this name or alias")
			}
		}
	}
	for i, existing := range project.Entities {
		if existing.ID == entity.ID {
			for j := range project.Facts {
				if project.Facts[j].Entity == existing.Name {
					project.Facts[j].Entity = entity.Name
				}
				if project.Facts[j].RelatedEntity == existing.Name {
					project.Facts[j].RelatedEntity = entity.Name
				}
			}
			project.Entities[i] = entity
			return nil
		}
	}
	entity.ID = newID()
	project.Entities = append(project.Entities, entity)
	return nil
}

// validateProject validates chapter, source, and entity identity before importing a complete project.
func validateProject(project *Project) error {
	if _, err := validateName(project.Title); err != nil {
		return err
	}
	if err := validateTarget(project.DefaultChapterWords); err != nil {
		return err
	}
	ids := map[string]bool{}
	numbers := map[uint32]bool{}
	for _, chapter := range project.Chapters {
		if chapter.TargetWords != nil {
			if err := validateTarget(*chapter.TargetWords); err != nil {
				return err
			}
		}
		for _, version := range chapter.Versions {
			if version.TargetWords != nil {
				if err := validateTarget(*version.TargetWords); err != nil {
					return err
				}
			}
		}
		if chapter.Number == 0 || numbers[chapter.Number] || ids[chapter.ID] || !contains([]string{"draft", "final"}, chapter.Status) {
			return errors.New("The backup contains duplicate chapters or invalid statuses")
		}
		numbers[chapter.Number] = true
		ids[chapter.ID] = true
	}
	for _, source := range project.Sources {
		if ids[source.ID] {
			return errors.New("The backup contains duplicate source IDs")
		}
		ids[source.ID] = true
	}
	entityIDs, names := map[string]bool{}, map[string]bool{}
	for _, entity := range project.Entities {
		if _, err := validateName(entity.Name); err != nil {
			return err
		}
		if entity.ID == "" || entityIDs[entity.ID] || !contains(EntityKinds, entity.Kind) {
			return errors.New("The backup contains an invalid entity ID or type")
		}
		entityIDs[entity.ID] = true
		for _, alias := range append([]string{entity.Name}, entity.Aliases...) {
			name := strings.TrimSpace(alias)
			if name == "" || names[name] {
				return errors.New("The backup contains ambiguous character names or aliases")
			}
			names[name] = true
		}
	}
	for _, fact := range project.Facts {
		if !ids[fact.SourceID] || !contains([]string{"pending", "confirmed", "rejected"}, fact.Status) || !contains(FactKinds, fact.Kind) {
			return errors.New("The backup contains invalid evidence references or statuses")
		}
	}
	return nil
}

// testModel sends a short streaming request and returns connection status.
func (engine *Engine) testModel(id string) (any, error) {
	engine.mu.Lock()
	if engine.closed {
		engine.mu.Unlock()
		return nil, errors.New("The application is closing")
	}
	if err := engine.refreshModelsLocked(); err != nil {
		engine.mu.Unlock()
		return nil, err
	}
	var config ProviderConfig
	found := false
	for _, profile := range engine.models.Profiles {
		if profile.ID == id {
			config = profile.Config
			found = true
			break
		}
	}
	engine.mu.Unlock()
	if !found {
		return nil, errors.New("Model profile not found")
	}
	if config.ProviderID != "" || !contains([]string{"openai", "ollama"}, config.Protocol) {
		config.MaxTokens = min(config.MaxTokens, 4096)
	} else {
		config.MaxTokens = 256
	}
	config.TimeoutSeconds = min(config.TimeoutSeconds, 30)
	started := time.Now()
	_, err := streamModel(engine.ctx, config, []Message{{Role: "user", Content: "Connection test. Reply with OK."}}, func(string) {})
	if err != nil {
		return nil, err
	}
	return map[string]any{"success": true, "message": "Connection and streaming response succeeded", "elapsed_ms": time.Since(started).Milliseconds()}, nil
}
