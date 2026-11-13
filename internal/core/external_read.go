package core

import (
	"context"
	"encoding/base64"
	"strings"
)

// ExternalReadRequest bounds project reads and identifies one record or page.
type ExternalReadRequest struct {
	ProjectID      string `json:"project_id,omitempty"`
	ID             string `json:"id,omitempty"`
	ChapterID      string `json:"chapter_id,omitempty"`
	Kind           string `json:"kind,omitempty"`
	Query          string `json:"query,omitempty"`
	ThroughChapter uint32 `json:"through_chapter,omitempty"`
	Cursor         string `json:"cursor,omitempty"`
	Limit          int    `json:"limit,omitempty"`
	Offset         int    `json:"offset,omitempty"`
	MaxChars       int    `json:"max_chars,omitempty"`
}

// ExternalPage makes every truncated collection resumable against a known snapshot.
type ExternalPage struct {
	Items      []any  `json:"items"`
	Revision   uint64 `json:"revision"`
	NextCursor string `json:"next_cursor,omitempty"`
	Total      int    `json:"total"`
}

type externalCursor struct {
	Offset   int    `json:"offset"`
	Snapshot string `json:"snapshot"`
}

// externalPage returns a bounded collection and rejects cursors from changed content.
func externalPage(items []any, request ExternalReadRequest, revision uint64) (ExternalPage, error) {
	fingerprint := hashText(jsonText(items))
	offset := request.Offset
	if request.Cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(request.Cursor)
		var cursor externalCursor
		if err != nil || decodeJSON(raw, &cursor) != nil {
			return ExternalPage{}, externalFailure("INVALID_INPUT", "Invalid page cursor")
		}
		if cursor.Snapshot != fingerprint {
			return ExternalPage{}, externalFailure("SOURCES_CHANGED", "The collection changed; restart pagination")
		}
		offset = cursor.Offset
	}
	if offset < 0 || offset > len(items) {
		return ExternalPage{}, externalFailure("INVALID_INPUT", "The page offset is outside this collection")
	}
	limit := request.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 100 {
		return ExternalPage{}, externalFailure("INVALID_INPUT", "Choose a page size between 1 and 100")
	}
	budget, err := externalPageBudget(request.MaxChars)
	if err != nil {
		return ExternalPage{}, err
	}
	end := min(len(items), offset+limit)
	used := 0
	for index := offset; index < end; index++ {
		size := charCount(jsonText(items[index]))
		if used+size > budget {
			if index == offset {
				return ExternalPage{}, externalFailure("OUTPUT_BUDGET", "This record exceeds the page budget; raise max_chars (up to 60000) or use a focused record window")
			}
			end = index
			break
		}
		used += size
	}
	page := ExternalPage{Items: items[offset:end], Revision: revision, Total: len(items)}
	if end < len(items) {
		page.NextCursor = base64.RawURLEncoding.EncodeToString(rawMessage(externalCursor{Offset: end, Snapshot: fingerprint}))
	}
	return page, nil
}

// externalPageBudget returns the per-page character budget. The default keeps one
// page of Chinese JSON inside the 25k-token tool-output limit of MCP clients such as
// Claude Code; callers may raise it with max_chars up to the hard ceiling.
func externalPageBudget(maxChars int) (int, error) {
	if maxChars == 0 {
		return 20000, nil
	}
	if maxChars < 1 || maxChars > 60000 {
		return 0, externalFailure("INVALID_INPUT", "max_chars must be between 1 and 60000")
	}
	return maxChars, nil
}

// outlineMatches applies the optional read_outline filters: id, kind (plan level),
// chapter number derived from chapter_id, and a title substring query.
func outlineMatches(plan StoryPlan, request ExternalReadRequest, chapterNumber uint32) bool {
	if request.ID != "" && plan.ID != request.ID {
		return false
	}
	if request.Kind != "" && plan.Level != request.Kind {
		return false
	}
	if chapterNumber > 0 && (plan.Level != "chapter" || chapterNumber < plan.StartChapter || chapterNumber > plan.EndChapter) {
		return false
	}
	return request.Query == "" || strings.Contains(plan.Title, request.Query)
}

// outlineChapterNumber resolves chapter_id to its chapter number; 0 means no chapter filter.
func outlineChapterNumber(project *Project, chapterID string) (uint32, error) {
	if chapterID == "" {
		return 0, nil
	}
	for _, chapter := range project.Chapters {
		if chapter.ID == chapterID {
			return chapter.Number, nil
		}
	}
	return 0, externalFailure("NOT_FOUND", "Chapter not found")
}

// Read executes only pure domain reads, including side-effect-free candidate previews.
func (service *ExternalService) Read(ctx context.Context, operation string, request ExternalReadRequest) (any, error) {
	if err := validateExternalContext(ctx); err != nil {
		return nil, err
	}
	if operation == "list_projects" {
		cards, err := service.engine.store.listProjectCards()
		if err != nil {
			return nil, err
		}
		items := []any{}
		for _, card := range cards {
			if service.options.ProjectID == "" || card.ID == service.options.ProjectID {
				items = append(items, card)
			}
		}
		return externalPage(items, request, 0)
	}
	id, err := service.projectID(request.ProjectID)
	if err != nil {
		return nil, err
	}
	if operation == "list_chapters" || operation == "read_chapter" {
		return service.readChapterRecords(id, operation, request)
	}
	if operation == "get_record" && contains([]string{"fact", "compilation"}, request.Kind) {
		return service.readWikiRecord(id, request)
	}
	project, err := service.engine.store.Get(id)
	if err != nil {
		return nil, err
	}
	items := []any{}
	switch operation {
	case "project_overview":
		words, pending := 0, 0
		for _, chapter := range project.Chapters {
			words += wordCount(chapter.Body)
		}
		for _, candidate := range project.Studio.ChangeSets {
			if candidate.Status == ChangeSetStaged || candidate.Status == ChangeSetConflicted {
				pending++
			}
		}
		return map[string]any{"id": id, "title": project.Title, "premise": project.Premise, "revision": project.Revision, "chapter_count": len(project.Chapters), "word_count": words, "pending_candidates": pending}, nil
	case "read_outline":
		chapterNumber, err := outlineChapterNumber(&project, request.ChapterID)
		if err != nil {
			return nil, err
		}
		for _, plan := range project.Studio.Plans {
			if outlineMatches(plan, request, chapterNumber) {
				items = append(items, plan)
			}
		}
	case "story_memory":
		through := request.ThroughChapter
		if through == 0 {
			through = ^uint32(0)
		}
		limit := max(96, len(project.Studio.Memories)+len(project.Facts)+len(project.Studio.Locations)+len(project.Studio.Routes)+len(project.Sources)+len(project.Entities)+len(project.Studio.Plans)+len(project.Studio.Decisions)+len(project.Chapters))
		for _, item := range mergeKnowledgeContext(&project, request.Query, through, nil, limit) {
			if item.Kind == "candidate_source" {
				continue
			}
			items = append(items, item)
		}
	case "list_candidates":
		for _, candidate := range project.Studio.ChangeSets {
			items = append(items, map[string]any{"id": candidate.ID, "status": candidate.Status, "source": candidate.Source, "summary": candidate.Summary, "updated_at": candidate.UpdatedAt, "provenance": candidate.Provenance})
		}
	case "get_candidate":
		candidate := findChangeSet(&project, request.ID)
		if candidate == nil {
			return nil, externalFailure("NOT_FOUND", "Candidate not found")
		}
		copy := *candidate
		copy.Operations = append([]ChangeOperation{}, candidate.Operations...)
		if copy.Status == ChangeSetStaged || copy.Status == ChangeSetConflicted {
			preview, previewErr := previewChangeSet(project, copy, nil)
			err = previewErr
			if err == nil {
				copy = preview
			}
		}
		copy.ReviewToken = ""
		view := map[string]any{}
		if decodeErr := decodeJSON(rawMessage(copy), &view); decodeErr != nil {
			return nil, decodeErr
		}
		if err != nil {
			view["preview_error"] = ExternalEnvelope(nil, err)
		}
		return view, nil
	case "review_candidate":
		return reviewCandidate(&project, request)
	case "search_story", "get_record":
		tool := "search_story_facts"
		if operation == "get_record" {
			tool = map[string]string{"entity": "get_entity_detail", "source": "get_source_detail", "decision": "get_decision_detail", "map": "get_world_map"}[request.Kind]
			if tool == "" {
				return nil, externalFailure("INVALID_INPUT", "Choose entity, source, decision or map")
			}
		}
		text, err := service.engine.executeScopedAgentTool(&project, Job{ProjectID: id, ctx: ctx}, agentStepEnvelope{Tool: tool, EntityID: request.ID, SourceID: request.ID, Query: request.Query, Through: request.ThroughChapter, Offset: request.Offset, Limit: request.MaxChars})
		if err != nil {
			return nil, err
		}
		var value any
		err = decodeJSON([]byte(text), &value)
		return value, err
	default:
		return nil, externalFailure("NOT_FOUND", "Unknown read operation")
	}
	return externalPage(items, request, project.Revision)
}

// readChapterRecords reads individual normalized chapters without hydrating all project histories.
func (service *ExternalService) readChapterRecords(projectID, operation string, request ExternalReadRequest) (any, error) {
	var result any
	err := service.engine.store.withReadSnapshot(func(db documentDB) error {
		var revision uint64
		if err := db.QueryRowContext(background(), "SELECT revision FROM projects WHERE id=?", projectID).Scan(&revision); err != nil {
			return err
		}
		if operation == "read_chapter" {
			id := request.ID
			if request.ChapterID != "" {
				id = request.ChapterID
			}
			var raw string
			if err := db.QueryRowContext(background(), "SELECT document FROM project_parts WHERE project_id=? AND kind='chapter' AND id=?", projectID, id).Scan(&raw); err != nil {
				return err
			}
			var chapter Chapter
			if err := decodeJSON([]byte(raw), &chapter); err != nil {
				return err
			}
			text := []rune(chapter.Body)
			offset, limit := request.Offset, request.MaxChars
			if limit == 0 {
				limit = 30000
			}
			if offset < 0 || offset > len(text) || limit < 1 || limit > 60000 {
				return externalFailure("INVALID_INPUT", "Invalid chapter window; max_chars must be between 1 and 60000")
			}
			end := min(len(text), offset+limit)
			result = map[string]any{"id": id, "number": chapter.Number, "title": chapter.Title, "body": string(text[offset:end]), "body_hash": hashText(chapter.Body), "revision": revision, "offset": offset, "next_offset": end, "total_chars": len(text), "truncated": end < len(text)}
			return nil
		}
		rows, err := db.QueryContext(background(), "SELECT document FROM project_parts WHERE project_id=? AND kind='chapter' ORDER BY position,id", projectID)
		if err != nil {
			return err
		}
		defer rows.Close()
		items := []any{}
		for rows.Next() {
			var raw string
			var chapter Chapter
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			if err := decodeJSON([]byte(raw), &chapter); err != nil {
				return err
			}
			if request.Query != "" && !strings.Contains(chapter.Title, request.Query) {
				continue
			}
			items = append(items, map[string]any{"id": chapter.ID, "number": chapter.Number, "title": chapter.Title, "chars": charCount(chapter.Body), "versions": len(chapter.Versions)})
		}
		if err := rows.Err(); err != nil {
			return err
		}
		result, err = externalPage(items, request, revision)
		return err
	})
	return result, err
}
