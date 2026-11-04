package core

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"io"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Project preserves the existing JSON fields and persistence format.
type Project struct {
	ID                  string            `json:"id"`
	Title               string            `json:"title"`
	Genre               string            `json:"genre"`
	Premise             string            `json:"premise"`
	Style               string            `json:"style"`
	Outline             string            `json:"outline"`
	Revision            uint64            `json:"revision"`
	CreatedAt           string            `json:"created_at"`
	UpdatedAt           string            `json:"updated_at"`
	Chapters            []Chapter         `json:"chapters"`
	Entities            []Entity          `json:"entities"`
	Facts               []Fact            `json:"facts"`
	Sources             []Source          `json:"sources"`
	WikiPurpose         string            `json:"wiki_purpose"`
	Compilations        []WikiCompilation `json:"compilations"`
	WritingStyle        string            `json:"writing_style"`
	DefaultChapterWords uint32            `json:"default_chapter_words"`
	NarrativePov        string            `json:"narrative_pov"`
	ModelOverrides      map[string]string `json:"model_overrides"`
}

// MemorySection preserves the existing JSON fields and persistence format.
type MemorySection struct {
	Summary     string   `json:"summary"`
	EvidenceIDs []string `json:"evidence_ids"`
	Questions   []string `json:"questions"`
}

// WikiCompilation preserves the existing JSON fields and persistence format.
type WikiCompilation struct {
	ID              string          `json:"id"`
	SourceID        string          `json:"source_id"`
	SourceHash      string          `json:"source_hash"`
	RulesHash       string          `json:"rules_hash"`
	Fingerprint     string          `json:"fingerprint"`
	CompilerVersion string          `json:"compiler_version"`
	Model           string          `json:"model"`
	Chunks          uint64          `json:"chunks"`
	Sections        []MemorySection `json:"sections"`
	Status          string          `json:"status"`
	CreatedAt       string          `json:"created_at"`
}

// Chapter preserves the existing JSON fields and persistence format.
type Chapter struct {
	ID          string    `json:"id"`
	Number      uint32    `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	Synopsis    string    `json:"synopsis"`
	Status      string    `json:"status"`
	Versions    []Version `json:"versions"`
	TargetWords *uint32   `json:"target_words"`
}

// Version preserves the existing JSON fields and persistence format.
type Version struct {
	ID          string  `json:"id"`
	CreatedAt   string  `json:"created_at"`
	Title       string  `json:"title"`
	Body        string  `json:"body"`
	Synopsis    string  `json:"synopsis"`
	Status      string  `json:"status"`
	TargetWords *uint32 `json:"target_words"`
}

// Entity preserves the existing JSON fields and persistence format.
type Entity struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Aliases     []string `json:"aliases"`
	Description string   `json:"description"`
}

// Source preserves the existing JSON fields and persistence format.
type Source struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Category  string `json:"category"`
	URL       string `json:"url"`
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

// Fact preserves the existing JSON fields and persistence format.
type Fact struct {
	ID            string `json:"id"`
	Entity        string `json:"entity"`
	Kind          string `json:"kind"`
	Claim         string `json:"claim"`
	SourceID      string `json:"source_id"`
	SourceHash    string `json:"source_hash"`
	Quote         string `json:"quote"`
	Chapter       uint32 `json:"chapter"`
	StoryTime     string `json:"story_time"`
	Location      string `json:"location"`
	RelatedEntity string `json:"related_entity"`
	Epistemic     string `json:"epistemic"`
	Status        string `json:"status"`
	Model         string `json:"model"`
	CreatedAt     string `json:"created_at"`
}

// Candidate preserves the existing JSON fields and persistence format.
type Candidate struct {
	Entity        string `json:"entity"`
	Kind          string `json:"kind"`
	Claim         string `json:"claim"`
	Quote         string `json:"quote"`
	StoryTime     string `json:"story_time"`
	Location      string `json:"location"`
	RelatedEntity string `json:"related_entity"`
	Epistemic     string `json:"epistemic"`
}

// ProviderConfig preserves the existing JSON fields and persistence format.
type ProviderConfig struct {
	Protocol        string   `json:"protocol"`
	BaseURL         string   `json:"base_url"`
	Model           string   `json:"model"`
	Temperature     *float64 `json:"temperature"`
	MaxTokens       uint32   `json:"max_tokens"`
	TimeoutSeconds  uint64   `json:"timeout_seconds"`
	ContextChars    uint64   `json:"context_chars"`
	APIKey          string   `json:"api_key"`
	ProviderID      string   `json:"provider_id,omitempty"`
	ThinkingMode    string   `json:"thinking_mode,omitempty"`
	ReasoningEffort string   `json:"reasoning_effort,omitempty"`
	APIVersion      string   `json:"api_version,omitempty"`
}

// ModelProfile preserves the existing JSON fields and persistence format.
type ModelProfile struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Enabled bool           `json:"enabled"`
	Config  ProviderConfig `json:"config"`
}

// ModelSettings preserves the existing JSON fields and persistence format.
type ModelSettings struct {
	SchemaVersion  uint32            `json:"schema_version"`
	Revision       uint64            `json:"revision"`
	Profiles       []ModelProfile    `json:"profiles"`
	DefaultProfile string            `json:"default_profile"`
	Routes         map[string]string `json:"routes"`
}

// AIRequest preserves the existing JSON fields and persistence format.
type AIRequest struct {
	ProjectID   string    `json:"project_id"`
	ChapterID   string    `json:"chapter_id"`
	Action      string    `json:"action"`
	Instruction string    `json:"instruction"`
	SourceID    string    `json:"source_id"`
	History     []Message `json:"history"`
	Force       bool      `json:"force"`
}

// Message preserves the existing JSON fields and persistence format.
type Message struct {
	Role             string `json:"role"`
	Content          string `json:"content"`
	SourceJobID      string `json:"source_job_id,omitempty"`
	reasoningContent string
	encryptedContent string
}

// Prompt preserves the existing JSON fields and persistence format.
type Prompt struct {
	Messages   []Message     `json:"messages"`
	Context    []ContextItem `json:"context"`
	SourceHash string        `json:"source_hash"`
	SourceID   string        `json:"source_id"`
	Chars      int           `json:"chars"`
}

// LintFinding preserves the existing JSON fields and persistence format.
type LintFinding struct {
	Kind     string `json:"kind"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Entity   string `json:"entity"`
	SourceID string `json:"source_id"`
}

// ContextItem preserves the existing JSON fields and persistence format.
type ContextItem struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Score  int    `json:"score"`
	Reason string `json:"reason"`
}

// GraphNode preserves the existing JSON fields and persistence format.
type GraphNode struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Kind        string   `json:"kind"`
	EntityID    *string  `json:"entity_id"`
	SourceID    *string  `json:"source_id"`
	Description string   `json:"description"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// GraphEdge preserves the existing JSON fields and persistence format.
type GraphEdge struct {
	ID          string   `json:"id"`
	Source      string   `json:"source"`
	Target      string   `json:"target"`
	Kind        string   `json:"kind"`
	Status      string   `json:"status"`
	Label       string   `json:"label"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// KnowledgeGraph preserves the existing JSON fields and persistence format.
type KnowledgeGraph struct {
	Nodes               []GraphNode `json:"nodes"`
	Edges               []GraphEdge `json:"edges"`
	OmittedStale        int         `json:"omitted_stale"`
	UnresolvedRelations int         `json:"unresolved_relations"`
	ThroughChapter      uint32      `json:"through_chapter"`
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *Project) UnmarshalJSON(data []byte) error {
	type record Project
	next := record{WikiPurpose: DefaultWikiPurpose, Compilations: []WikiCompilation{}, WritingStyle: "custom", DefaultChapterWords: 3000, NarrativePov: "Third-person limited", ModelOverrides: map[string]string{}}
	if err := decodeRecord(data, &next, "id", "title", "genre", "premise", "style", "outline", "revision", "created_at", "updated_at", "chapters", "entities", "facts", "sources"); err != nil {
		return err
	}
	*value = Project(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *MemorySection) UnmarshalJSON(data []byte) error {
	type record MemorySection
	next := record{Questions: []string{}}
	if err := decodeRecord(data, &next, "summary", "evidence_ids"); err != nil {
		return err
	}
	*value = MemorySection(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *WikiCompilation) UnmarshalJSON(data []byte) error {
	type record WikiCompilation
	next := record{}
	if err := decodeRecord(data, &next, "id", "source_id", "source_hash", "rules_hash", "fingerprint", "compiler_version", "model", "chunks", "sections", "status", "created_at"); err != nil {
		return err
	}
	*value = WikiCompilation(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *Chapter) UnmarshalJSON(data []byte) error {
	type record Chapter
	next := record{}
	if err := decodeRecord(data, &next, "id", "number", "title", "body", "synopsis", "status", "versions"); err != nil {
		return err
	}
	*value = Chapter(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *Version) UnmarshalJSON(data []byte) error {
	type record Version
	next := record{}
	if err := decodeRecord(data, &next, "id", "created_at", "title", "body", "synopsis", "status"); err != nil {
		return err
	}
	*value = Version(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *Entity) UnmarshalJSON(data []byte) error {
	type record Entity
	next := record{}
	if err := decodeRecord(data, &next, "id", "name", "kind", "aliases", "description"); err != nil {
		return err
	}
	*value = Entity(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *Source) UnmarshalJSON(data []byte) error {
	type record Source
	next := record{}
	if err := decodeRecord(data, &next, "id", "title", "category", "url", "content", "created_at"); err != nil {
		return err
	}
	*value = Source(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *Fact) UnmarshalJSON(data []byte) error {
	type record Fact
	next := record{}
	if err := decodeRecord(data, &next, "id", "entity", "kind", "claim", "source_id", "source_hash", "quote", "chapter", "story_time", "location", "related_entity", "epistemic", "status", "model", "created_at"); err != nil {
		return err
	}
	*value = Fact(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *Candidate) UnmarshalJSON(data []byte) error {
	type record Candidate
	next := record{Epistemic: "inferred"}
	if err := decodeRecord(data, &next, "entity", "kind", "claim", "quote"); err != nil {
		return err
	}
	*value = Candidate(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *ProviderConfig) UnmarshalJSON(data []byte) error {
	type record ProviderConfig
	next := record{}
	if err := decodeRecord(data, &next, "protocol", "base_url", "model", "max_tokens", "timeout_seconds", "context_chars"); err != nil {
		return err
	}
	*value = ProviderConfig(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *ModelProfile) UnmarshalJSON(data []byte) error {
	type record ModelProfile
	next := record{}
	if err := decodeRecord(data, &next, "id", "name", "enabled", "config"); err != nil {
		return err
	}
	*value = ModelProfile(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *ModelSettings) UnmarshalJSON(data []byte) error {
	type record ModelSettings
	next := record{}
	if err := decodeRecord(data, &next, "schema_version", "revision", "profiles", "default_profile", "routes"); err != nil {
		return err
	}
	*value = ModelSettings(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *AIRequest) UnmarshalJSON(data []byte) error {
	type record AIRequest
	next := record{History: []Message{}}
	if err := decodeRecord(data, &next, "project_id", "chapter_id", "action", "instruction"); err != nil {
		return err
	}
	*value = AIRequest(next)
	return nil
}

// UnmarshalJSON preserves required fields, type constraints, and legacy defaults.
func (value *Message) UnmarshalJSON(data []byte) error {
	type record Message
	next := record{}
	if err := decodeRecord(data, &next, "role", "content"); err != nil {
		return err
	}
	*value = Message(next)
	return nil
}

const DefaultWikiPurpose = "Track character goals, choices and consequences, relationships, knowledge, time and place, and foreshadowing. Distinguish explicit source facts, model interpretations, and author plans."

// decodeRecord rejects missing fields and null values for non-nullable fields.
func decodeRecord(data []byte, destination any, required ...string) error {
	var fields map[string]json.RawMessage
	if err := decodeJSON(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return errors.New("Expected a JSON object")
	}
	for _, key := range required {
		if _, exists := fields[key]; !exists {
			return fmt.Errorf("Missing field: %s", key)
		}
	}
	typ := reflect.TypeOf(destination).Elem()
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		key := strings.Split(field.Tag.Get("json"), ",")[0]
		if field.PkgPath != "" || key == "-" {
			continue
		}
		raw, exists := fields[key]
		if exists && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) && field.Type.Kind() != reflect.Pointer {
			return fmt.Errorf("Field cannot be null: %s", key)
		}
		if exists && field.Type.Kind() == reflect.Slice {
			var values []json.RawMessage
			if err := decodeJSON(raw, &values); err != nil {
				return err
			}
			for _, value := range values {
				if bytes.Equal(bytes.TrimSpace(value), []byte("null")) && field.Type.Elem().Kind() != reflect.Pointer {
					return fmt.Errorf("Array element cannot be null: %s", key)
				}
			}
		}
		if exists && field.Type.Kind() == reflect.Map && field.Type.Elem().Kind() == reflect.String {
			var values map[string]string
			if err := decodeStringMap(raw, &values); err != nil {
				return err
			}
		}
	}
	return decodeJSON(data, destination)
}

// decodeJSON accepts one complete UTF-8 JSON value with no trailing content.
func decodeJSON(data []byte, destination any) error {
	if !utf8.Valid(data) {
		return errors.New("Invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("JSON contains trailing data")
	}
	return nil
}

// jsonBytes preserves Unicode and source HTML characters in JSON output.
func jsonBytes(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// jsonText encodes validated serializable domain data for prompts and records.
func jsonText(value any) string {
	data, err := jsonBytes(value)
	if err != nil {
		panic(fmt.Sprintf("Unable to encode domain value: %v", err))
	}
	return string(data)
}

// newID generates random UUID v4 identities independent of titles and filenames.
func newID() string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		panic(err)
	}
	data[6] = (data[6] & 0x0f) | 0x40
	data[8] = (data[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", data[:4], data[4:6], data[6:8], data[8:10], data[10:])
}

// now returns UTC RFC3339 timestamps compatible with saved documents.
func now() string { return time.Now().UTC().Format("2006-01-02T15:04:05.000000000+00:00") }

// hashText hashes the original UTF-8 content for stable evidence identity.
func hashText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// charCount counts Unicode code points instead of UTF-8 bytes.
func charCount(value string) int { return utf8.RuneCountInString(value) }

// wordCount counts non-whitespace Unicode characters consistently with the editor.
func wordCount(value string) int {
	count := 0
	for _, char := range value {
		if !unicode.IsSpace(char) {
			count++
		}
	}
	return count
}

// NewProject creates an offline project with explicit writing defaults.
func NewProject(title, genre, premise string) Project {
	return Project{ID: newID(), Title: strings.TrimSpace(title), Genre: genre, Premise: premise, CreatedAt: now(), UpdatedAt: now(), Chapters: []Chapter{}, Entities: []Entity{}, Facts: []Fact{}, Sources: []Source{}, WikiPurpose: DefaultWikiPurpose, Compilations: []WikiCompilation{}, WritingStyle: "custom", DefaultChapterWords: 3000, NarrativePov: "Third-person limited", ModelOverrides: map[string]string{}}
}

// NewChapter creates a draft chapter inheriting the project length target.
func NewChapter(number uint32, title string) Chapter {
	return Chapter{ID: newID(), Number: number, Title: title, Status: "draft", Versions: []Version{}}
}

// ResolveEntity resolves a display name or alias to a stable entity.
func (project *Project) ResolveEntity(name string) *Entity {
	for i := range project.Entities {
		entity := &project.Entities[i]
		if entity.Name == name || contains(entity.Aliases, name) {
			return entity
		}
	}
	return nil
}

// CanonicalName retains unknown names for knowledge review.
func (project *Project) CanonicalName(name string) string {
	if entity := project.ResolveEntity(name); entity != nil {
		return entity.Name
	}
	return name
}

// SourceText returns complete source text and chapter number, using zero for reference sources.
func (project *Project) SourceText(id string) (string, uint32, bool) {
	for _, chapter := range project.Chapters {
		if chapter.ID == id {
			return chapter.Body, chapter.Number, true
		}
	}
	for _, source := range project.Sources {
		if source.ID == id {
			return source.Content, 0, true
		}
	}
	return "", 0, false
}

// contains performs exact matching in small enum and alias lists.
func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

// equalTarget distinguishes inherited chapter length from an explicit override.
func equalTarget(a, b *uint32) bool { return a == nil && b == nil || a != nil && b != nil && *a == *b }

// SetContent retains a full version before changing content and deduplicates identical saves.
func (chapter *Chapter) SetContent(title, body, synopsis, status string, target *uint32) {
	if chapter.Title == title && chapter.Body == body && chapter.Synopsis == synopsis && chapter.Status == status && equalTarget(chapter.TargetWords, target) {
		return
	}
	chapter.Versions = append(chapter.Versions, Version{ID: newID(), CreatedAt: now(), Title: chapter.Title, Body: chapter.Body, Synopsis: chapter.Synopsis, Status: chapter.Status, TargetWords: chapter.TargetWords})
	chapter.Title = title
	chapter.Body = body
	chapter.Synopsis = synopsis
	chapter.Status = status
	chapter.TargetWords = target
}

// defaultProvider provides an unconfigured model with the existing session budgets.
func defaultProvider() ProviderConfig {
	temperature := 0.7
	return ProviderConfig{Protocol: "openai", BaseURL: "https://api.deepseek.com/v1", Temperature: &temperature, MaxTokens: 4096, TimeoutSeconds: 120, ContextChars: 24000}
}

// cloneProject copies project data for asynchronous work without shared mutable slices.
func cloneProject(project Project) Project {
	var result Project
	if err := decodeJSON([]byte(jsonText(project)), &result); err != nil {
		panic(err)
	}
	return result
}

// lowerText supports Unicode case expansion for retrieval and profile name comparisons.
func lowerText(value string) string { return cases.Lower(language.Und).String(value) }

// decodeStringMap requires string map values and rejects null inheritance values.
func decodeStringMap(data []byte, destination *map[string]string) error {
	var values map[string]json.RawMessage
	if err := decodeJSON(data, &values); err != nil {
		return err
	}
	if values == nil {
		return errors.New("Expected a string map")
	}
	result := map[string]string{}
	for key, raw := range values {
		var value string
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || decodeJSON(raw, &value) != nil {
			return errors.New("Map values must be strings")
		}
		result[key] = value
	}
	*destination = result
	return nil
}
