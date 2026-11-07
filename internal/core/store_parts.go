package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"
)

type projectPart struct {
	kind, id, document string
	position           int
}

// splitProject separates large independently editable records from the metadata document.
func splitProject(project *Project) (Project, []projectPart) {
	metadata := *project
	parts := []projectPart{}
	add := func(kind, id string, position int, value any) {
		parts = append(parts, projectPart{kind: kind, id: id, position: position, document: jsonText(value)})
	}
	for i, v := range project.Chapters {
		add("chapter", v.ID, i, v)
	}
	for i, v := range project.Entities {
		add("entity", v.ID, i, v)
	}
	for i, v := range project.Facts {
		add("fact", v.ID, i, v)
	}
	for i, v := range project.Sources {
		add("source", v.ID, i, v)
	}
	for i, v := range project.Compilations {
		add("compilation", v.ID, i, v)
	}
	for i, v := range project.Studio.Plans {
		add("plan", v.ID, i, v)
	}
	for i, v := range project.Studio.Memories {
		add("memory", v.ID, i, v)
	}
	for i, v := range project.Studio.Decisions {
		add("decision", v.ID, i, v)
	}
	for i, v := range project.Studio.ChangeSets {
		add("changeset", v.ID, i, v)
	}
	for i, v := range project.Studio.Locations {
		add("location", v.ID, i, v)
	}
	for i, v := range project.Studio.Routes {
		add("route", v.ID, i, v)
	}
	metadata.Chapters = []Chapter{}
	metadata.Entities = []Entity{}
	metadata.Facts = []Fact{}
	metadata.Sources = []Source{}
	metadata.Compilations = []WikiCompilation{}
	metadata.Studio.Plans = []StoryPlan{}
	metadata.Studio.Memories = []StoryMemory{}
	metadata.Studio.Decisions = []StoryDecision{}
	metadata.Studio.ChangeSets = nil
	metadata.Studio.Locations = nil
	metadata.Studio.Routes = nil
	metadata.Conversations = nil
	metadata.ArchiveJobs = nil
	metadata.Experiments = nil
	return metadata, parts
}

// hydrateProject assembles the compatible public project shape from normalized records.
func hydrateProject(db documentDB, project *Project) error {
	rows, err := db.QueryContext(context.Background(), "SELECT kind,document FROM project_parts WHERE project_id=? ORDER BY position,id", project.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, raw string
		if err := rows.Scan(&kind, &raw); err != nil {
			return err
		}
		switch kind {
		case "location":
			var value WorldLocation
			if err := decodeJSON([]byte(raw), &value); err != nil {
				return err
			}
			project.Studio.Locations = append(project.Studio.Locations, value)
		case "route":
			var value WorldRoute
			if err := decodeJSON([]byte(raw), &value); err != nil {
				return err
			}
			project.Studio.Routes = append(project.Studio.Routes, value)
		case "chapter":
			var v Chapter
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return err
			}
			project.Chapters = append(project.Chapters, v)
		case "entity":
			var v Entity
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return err
			}
			project.Entities = append(project.Entities, v)
		case "fact":
			var v Fact
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return err
			}
			project.Facts = append(project.Facts, v)
		case "source":
			var v Source
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return err
			}
			project.Sources = append(project.Sources, v)
		case "compilation":
			var v WikiCompilation
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return err
			}
			project.Compilations = append(project.Compilations, v)
		case "plan":
			var v StoryPlan
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return err
			}
			project.Studio.Plans = append(project.Studio.Plans, v)
		case "memory":
			var v StoryMemory
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return err
			}
			project.Studio.Memories = append(project.Studio.Memories, v)
		case "decision":
			var v StoryDecision
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return err
			}
			project.Studio.Decisions = append(project.Studio.Decisions, v)
		case "changeset":
			var v ChangeSet
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return err
			}
			project.Studio.ChangeSets = append(project.Studio.ChangeSets, v)
		}
	}
	return rows.Err()
}

// saveParts updates changed rows only, retaining chapter versions and rebuilding only affected indexes.
func saveParts(db documentDB, projectID string, parts []projectPart) error {
	ctx := context.Background()
	rows, err := db.QueryContext(ctx, "SELECT kind,id FROM project_parts WHERE project_id=?", projectID)
	if err != nil {
		return err
	}
	old := map[string]projectPart{}
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			rows.Close()
			return err
		}
		old[kind+":"+id] = projectPart{kind: kind, id: id}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, part := range parts {
		_, existed := old[part.kind+":"+part.id]
		delete(old, part.kind+":"+part.id)
		if part.kind == "changeset" {
			if _, err := db.ExecContext(ctx, "INSERT INTO project_parts(project_id,kind,id,position,document) VALUES(?,?,?,?,?) ON CONFLICT(project_id,kind,id) DO NOTHING", projectID, part.kind, part.id, part.position, part.document); err != nil {
				return err
			}
			continue
		}
		result, err := db.ExecContext(ctx, `INSERT INTO project_parts(project_id,kind,id,position,document) VALUES(?,?,?,?,?) ON CONFLICT(project_id,kind,id) DO UPDATE SET position=excluded.position,document=excluded.document WHERE position!=excluded.position OR document!=excluded.document`, projectID, part.kind, part.id, part.position, part.document)
		if err != nil {
			return err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if changed > 0 {
			if err := indexPart(db, projectID, part, existed); err != nil {
				return err
			}
		}
	}
	for _, part := range old {
		if part.kind == "changeset" {
			continue
		}
		if _, err := db.ExecContext(ctx, "DELETE FROM project_parts WHERE project_id=? AND kind=? AND id=?", projectID, part.kind, part.id); err != nil {
			return err
		}
		if _, err := db.ExecContext(ctx, "DELETE FROM story_fts WHERE project_id=? AND part_id=?", projectID, part.kind+":"+part.id); err != nil {
			return err
		}
	}
	return nil
}

// indexPart replaces existing source passages and inserts new parts without scanning unrelated index entries.
func indexPart(db documentDB, projectID string, part projectPart, existed bool) error {
	if part.kind != "chapter" && part.kind != "source" {
		return nil
	}
	ctx := context.Background()
	key := part.kind + ":" + part.id
	if existed {
		if _, err := db.ExecContext(ctx, "DELETE FROM story_fts WHERE project_id=? AND part_id=?", projectID, key); err != nil {
			return err
		}
	}
	var text, title, prefix, kind string
	var chapter uint32
	switch part.kind {
	case "chapter":
		var v Chapter
		if err := json.Unmarshal([]byte(part.document), &v); err != nil {
			return err
		}
		text = v.Body
		title = fmt.Sprintf("Chapter %d — %s", v.Number, v.Title)
		chapter = v.Number
		prefix = "C:"
		kind = "chapter"
	case "source":
		var v Source
		if err := json.Unmarshal([]byte(part.document), &v); err != nil {
			return err
		}
		if v.Category == "legacy" {
			return nil
		}
		text = v.Content
		title = v.Title
		prefix = "S:"
		kind = v.Category
	default:
		return nil
	}
	for i, chunk := range chunks(text, 900, 100) {
		if _, err := db.ExecContext(ctx, "INSERT INTO story_fts(project_id,part_id,item_id,title,kind,chapter,content) VALUES(?,?,?,?,?,?,?)", projectID, key, fmt.Sprintf("%s%s:%d", prefix, part.id, i+1), title, kind, chapter, chunk); err != nil {
			return err
		}
	}
	return nil
}

// migrateStudioStore atomically upgrades legacy documents without changing project revisions.
func (store *Store) migrateStudioStore() error {
	_, err := store.db.Exec(`PRAGMA foreign_keys=ON;
 CREATE TABLE IF NOT EXISTS project_parts(project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,kind TEXT NOT NULL,id TEXT NOT NULL,position INTEGER NOT NULL,document TEXT NOT NULL,PRIMARY KEY(project_id,kind,id));
 CREATE INDEX IF NOT EXISTS project_parts_order ON project_parts(project_id,kind,position);
 CREATE VIRTUAL TABLE IF NOT EXISTS story_fts USING fts5(project_id UNINDEXED,part_id UNINDEXED,item_id UNINDEXED,title,kind UNINDEXED,chapter UNINDEXED,content,tokenize='trigram');
 CREATE TABLE IF NOT EXISTS conversations(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,chapter_id TEXT NOT NULL,document TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS conversations_project ON conversations(project_id,chapter_id);
 CREATE TABLE IF NOT EXISTS conversation_turns(id TEXT PRIMARY KEY,session_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,ordinal INTEGER NOT NULL,document TEXT NOT NULL,UNIQUE(session_id,ordinal));
 CREATE TABLE IF NOT EXISTS integrations(id INTEGER PRIMARY KEY CHECK(id=1),document TEXT NOT NULL);
 CREATE TABLE IF NOT EXISTS experiments(id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,document TEXT NOT NULL);
 CREATE INDEX IF NOT EXISTS runs_status_owner ON runs(json_extract(document,'$.status'),COALESCE(json_extract(document,'$.execution_owner'),''),project_id);
 CREATE INDEX IF NOT EXISTS conversation_turns_status_job ON conversation_turns(json_extract(document,'$.status'),json_extract(document,'$.job_id'));`)
	if err != nil {
		return err
	}
	return store.withWriteTransaction(func(conn *sql.Conn) error {
		var version int
		if err := conn.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
			return err
		}
		if version >= 2 {
			return nil
		}
		rows, err := conn.QueryContext(context.Background(), "SELECT document FROM projects")
		if err != nil {
			return err
		}
		projects := []Project{}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				rows.Close()
				return err
			}
			var p Project
			if err := decodeJSON([]byte(raw), &p); err != nil {
				rows.Close()
				return err
			}
			projects = append(projects, p)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		for _, p := range projects {
			metadata, parts := splitProject(&p)
			if err := saveParts(conn, p.ID, parts); err != nil {
				return err
			}
			if _, err := conn.ExecContext(context.Background(), "UPDATE projects SET document=? WHERE id=?", jsonText(metadata), p.ID); err != nil {
				return err
			}
			if err := migrateLegacyConversations(conn, p.ID); err != nil {
				return err
			}
		}
		_, err = conn.ExecContext(context.Background(), "PRAGMA user_version=2")
		return err
	})
}

// SearchPassages performs bounded local full-text retrieval with a short-query fallback.
func (store *Store) SearchPassages(projectID, query string, through uint32, sessionIDs ...string) ([]ContextItem, error) {
	needles := []string{}
	for term := range terms(query) {
		if charCount(term) >= 3 {
			needles = append(needles, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
		}
	}
	runes := []rune(query)
	seen := map[string]bool{}
	for _, term := range needles {
		seen[term] = true
	}
	for i := 0; i+2 < len(runes); i++ {
		if unicode.Is(unicode.Han, runes[i]) && unicode.Is(unicode.Han, runes[i+1]) && unicode.Is(unicode.Han, runes[i+2]) {
			term := `"` + string(runes[i:i+3]) + `"`
			if !seen[term] {
				needles = append(needles, term)
				seen[term] = true
			}
		}
	}
	sort.Strings(needles)
	if len(needles) > 24 {
		needles = needles[:24]
	}
	sessionID := ""
	if len(sessionIDs) > 0 {
		sessionID = sessionIDs[0]
	}
	statement := "SELECT item_id,title,kind,content FROM story_fts WHERE project_id=? AND CAST(chapter AS INTEGER)<=? AND (kind!='discussion' OR part_id LIKE ?) AND story_fts MATCH ? ORDER BY bm25(story_fts) LIMIT 24"
	needle := strings.Join(needles, " OR ")
	if needle == "" {
		statement = `SELECT item_id,title,kind,content FROM story_fts WHERE project_id=? AND CAST(chapter AS INTEGER)<=? AND (kind!='discussion' OR part_id LIKE ?) AND (content LIKE ? ESCAPE '\' OR title LIKE ? ESCAPE '\') LIMIT 24`
		needle = "%" + strings.NewReplacer("\\", "\\\\", "%", "\\%", "_", "\\_").Replace(query) + "%"
	}
	args := []any{projectID, through, "session:" + sessionID + ":%", needle}
	if len(needles) == 0 {
		args = append(args, needle)
	}
	rows, err := store.db.Query(statement, args...)
	if err != nil {
		return nil, err
	}
	items := []ContextItem{}
	for rows.Next() {
		var item ContextItem
		if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &item.Text); err != nil {
			rows.Close()
			return nil, err
		}
		item.Score = 8
		item.Reason = "Local full-text index"
		if item.Kind == "discussion" {
			item.Reason = "Earlier discussion excerpt from this branch; not an accepted story fact"
		}
		items = append(items, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(items) > 0 || len(needles) == 0 {
		return items, err
	}
	return store.searchChinesePairs(projectID, query, through, sessionID)
}

// searchChinesePairs recovers two-character names embedded in longer questions when trigram search misses.
func (store *Store) searchChinesePairs(projectID, query string, through uint32, sessionID string) ([]ContextItem, error) {
	pairs := []string{}
	for term := range terms(query) {
		runes := []rune(term)
		if len(runes) == 2 && unicode.Is(unicode.Han, runes[0]) && unicode.Is(unicode.Han, runes[1]) {
			pairs = append(pairs, term)
		}
	}
	sort.Strings(pairs)
	if len(pairs) > 24 {
		pairs = pairs[:24]
	}
	items := []ContextItem{}
	if len(pairs) == 0 {
		return items, nil
	}
	conditions := []string{}
	args := []any{projectID, through, "session:" + sessionID + ":%"}
	for _, pair := range pairs {
		conditions = append(conditions, "(content LIKE ? OR title LIKE ?)")
		args = append(args, "%"+pair+"%", "%"+pair+"%")
	}
	rows, err := store.db.Query("SELECT item_id,title,kind,content FROM story_fts WHERE project_id=? AND CAST(chapter AS INTEGER)<=? AND (kind!='discussion' OR part_id LIKE ?) AND ("+strings.Join(conditions, " OR ")+") LIMIT 24", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item ContextItem
		if err := rows.Scan(&item.ID, &item.Title, &item.Kind, &item.Text); err != nil {
			return nil, err
		}
		item.Score = 4
		item.Reason = "Chinese pair fallback after no full-text matches"
		if item.Kind == "discussion" {
			item.Reason += "; discussion is not an accepted story fact"
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
