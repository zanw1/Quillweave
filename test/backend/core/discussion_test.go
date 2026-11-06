//go:build quillweave_whitebox

package core

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// TestPlanningConversationUsesOutlineHistory checks actual requests through discussion, outline, and follow-up tasks.
func TestPlanningConversationUsesOutlineHistory(t *testing.T) {
	for _, shared := range []bool{true, false} {
		t.Run(fmt.Sprintf("shared-profile=%t", shared), func(t *testing.T) {
			engine := testEngine(t)
			project := invoke[Project](t, engine, "create_project", map[string]any{"title": "A lighthouse mystery", "genre": "Mystery", "premise": "A keeper protects her sister."})
			var mutex sync.Mutex
			requests := []map[string]any{}
			replies := []string{"The sister should hold the missing clue.", "1. The letter arrives.\n2. The sisters follow the tide.", "Give the sister a choice that changes the ending."}
			server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
					t.Error(err)
					writer.WriteHeader(400)
					return
				}
				mutex.Lock()
				index := len(requests)
				requests = append(requests, body)
				mutex.Unlock()
				writer.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"PRIVATE_PLAN_%d\"}}]}\n\n%s", index, streamPayload(replies[index]))
			})
			config := providerPresets()[7].Config
			config.BaseURL = server.URL
			settings := invoke[settingsResponse](t, engine, "get_model_settings", nil).Settings
			settings.Profiles = []ModelProfile{{ID: "writer", Name: "Writer", Enabled: true, Config: config}, {ID: "outliner", Name: "Outliner", Enabled: true, Config: config}}
			settings.DefaultProfile = "writer"
			if !shared {
				settings.Routes["outline"] = "outliner"
			}
			invoke[settingsResponse](t, engine, "save_model_settings", map[string]any{"settings": settings})
			args := map[string]any{"project_id": project.ID, "chapter_id": "", "action": "ask", "instruction": "Discuss a mystery in which the keeper protects her sister."}
			first := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, first.Status == "done", first.Error)
			args["action"], args["instruction"] = "outline", "Generate a chapter outline from our discussion."
			args["history"] = []Message{{Role: "user", Content: "Discuss a mystery in which the keeper protects her sister."}, {Role: "assistant", Content: first.Text, SourceJobID: first.ID}}
			outline := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, outline.Status == "done", outline.Error)
			args["action"], args["instruction"] = "ask", "How can we improve this outline?"
			args["history"] = []Message{{Role: "user", Content: "Generate a chapter outline from our discussion."}, {Role: "assistant", Content: outline.Text, SourceJobID: outline.ID}}
			followup := finishJob(t, engine, invoke[Job](t, engine, "start_ai", args))
			assert(t, followup.Status == "done", followup.Error)
			mutex.Lock()
			defer mutex.Unlock()
			assert(t, len(requests) == 3 && strings.Contains(jsonText(requests[1]), first.Text) && strings.Contains(jsonText(requests[2]), "The letter arrives"), "Planning discussion or outline was missing from the request")
			assert(t, strings.Contains(jsonText(requests[1]), "PRIVATE_PLAN_0") == shared && strings.Contains(jsonText(requests[2]), "PRIVATE_PLAN_1") == shared, "Private planning history crossed profile boundaries or was lost")
			runs := invoke[[]json.RawMessage](t, engine, "get_runs", map[string]any{"project_id": project.ID})
			assert(t, !strings.Contains(jsonText(runs), "PRIVATE_PLAN_"), "Public planning history exposed private context")
		})
	}
}

// TestEnglishPromptsAndLegacyViewpoints checks English defaults and forward-compatible saved viewpoints.
func TestEnglishPromptsAndLegacyViewpoints(t *testing.T) {
	han := regexp.MustCompile(`[\p{Han}]`)
	project := testProject()
	for _, style := range writingPresets() {
		project.WritingStyle = style.ID
		for _, action := range []string{"ask", "outline", "architecture", "continue", "rewrite", "review"} {
			prompt, err := prepareAI(&project, AIRequest{ProjectID: project.ID, ChapterID: project.Chapters[0].ID, Action: action, Instruction: "Develop the story."}, 24000)
			assert(t, err == nil && !han.MatchString(jsonText(prompt.Messages)), "Application-owned planning prompts must be English")
		}
	}
	assert(t, !han.MatchString(extractionSystem+memorySystem+DefaultWikiPurpose), "Wiki instructions must be English")
	engine := testEngine(t)
	legacy := "\u7b2c\u4e09\u4eba\u79f0\u6709\u9650\u89c6\u89d2"
	saved := invoke[Project](t, engine, "create_project", map[string]any{"title": "Legacy viewpoint", "genre": "Mystery", "premise": "An old project.", "narrative_pov": legacy})
	assert(t, saved.NarrativePov == legacy, "A saved legacy viewpoint was rewritten")
	prompt := invoke[Prompt](t, engine, "prepare_ai", map[string]any{"project_id": saved.ID, "chapter_id": "", "action": "outline", "instruction": "Create an outline."})
	assert(t, strings.Contains(jsonText(prompt.Messages), "Third-person limited") && !han.MatchString(jsonText(prompt.Messages)), "A legacy viewpoint did not receive an English prompt label")
}

// TestUndecidedStoryDiscussionDefersWritingDefaults keeps brainstorming open while later settings remain editable.
func TestUndecidedStoryDiscussionDefersWritingDefaults(t *testing.T) {
	engine := testEngine(t)
	project := invoke[Project](t, engine, "create_project", map[string]any{"title": "Untitled story", "genre": "", "premise": ""})
	assert(t, project.Genre == "" && project.Premise == "" && len(project.Chapters) == 0, "Creation invented a story before discussion")
	prompt, err := prepareAI(&project, AIRequest{ProjectID: project.ID, Action: "ask", Instruction: "I want to write, but I do not have an idea yet."}, 24000)
	assert(t, err == nil, "An undecided project could not start a discussion")
	text := jsonText(prompt.Messages)
	assert(t, strings.Contains(text, "one or two easy questions") && !strings.Contains(text, "Target chapter length is about") && !strings.Contains(text, "Narrative viewpoint:"), "Application defaults constrained brainstorming")
	updated := invoke[Project](t, engine, "update_project", map[string]any{
		"project_id": project.ID, "revision": project.Revision, "title": "The lighthouse letters", "genre": "Mystery", "premise": "A keeper finds a letter from her future self.", "style": "Quiet suspense", "outline": "The letter arrives before the storm.",
		"writing_style": "mystery", "default_chapter_words": 4200, "narrative_pov": "First person",
	})
	assert(t, updated.ID == project.ID && updated.Genre == "Mystery" && updated.DefaultChapterWords == 4200 && updated.NarrativePov == "First person", "Story details could not be decided after creation")
	outline, err := prepareAI(&updated, AIRequest{ProjectID: updated.ID, Action: "outline", Instruction: "Outline the story we discussed."}, 24000)
	assert(t, err == nil && strings.Contains(jsonText(outline.Messages), "4200") && strings.Contains(jsonText(outline.Messages), "Narrative viewpoint: First person"), "Later writing settings were absent from a concrete planning task")
}

// TestUndecidedStoryDiscussionStreamsBeforeSetup verifies a real local model request with no premise or genre.
func TestUndecidedStoryDiscussionStreamsBeforeSetup(t *testing.T) {
	engine := testEngine(t)
	project := invoke[Project](t, engine, "create_project", map[string]any{"title": "Untitled story", "genre": "", "premise": ""})
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
			writer.WriteHeader(400)
			return
		}
		text := jsonText(body)
		assert(t, strings.Contains(text, "undecided story") && !strings.Contains(text, "Target chapter length is about"), "The outgoing request required premature writing decisions")
		fmt.Fprint(writer, streamPayload("What kinds of stories or feelings have stayed with you recently?"))
	})
	config := defaultProvider()
	config.BaseURL, config.Model = server.URL, "brainstorm-fixture"
	configureModel(t, engine, config)
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": project.ID, "chapter_id": "", "action": "ask", "instruction": "I have no idea yet."}))
	assert(t, job.Status == "done" && strings.Contains(job.Text, "feelings"), "Discussion did not complete for an undecided project")
	saved := invoke[Project](t, engine, "get_project", map[string]any{"project_id": project.ID})
	assert(t, saved.Genre == "" && saved.Premise == "" && saved.Outline == "", "Brainstorming silently committed model proposals as story facts")
}
