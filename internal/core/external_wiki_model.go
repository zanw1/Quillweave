package core

// WikiBeginInput selects one chapter or world source for external compilation or fact re-review.
type WikiBeginInput struct {
	ProjectID   string `json:"project_id,omitempty"`
	RequestID   string `json:"request_id"`
	SourceID    string `json:"source_id"`
	Mode        string `json:"mode,omitempty"`
	Instruction string `json:"instruction,omitempty"`
	Language    string `json:"language,omitempty"`
}

// WikiSessionRequest identifies a project-scoped, durable Wiki session.
type WikiSessionRequest struct {
	ProjectID     string `json:"project_id,omitempty"`
	WikiSessionID string `json:"wiki_session_id"`
}

// WikiMutation identifies one retryable update to the exact session revision.
type WikiMutation struct {
	WikiSessionRequest
	RequestID       string `json:"request_id"`
	SessionRevision uint64 `json:"session_revision"`
}

// WikiExtractionInput checkpoints one completely processed source block, including an empty facts array.
type WikiExtractionInput struct {
	WikiMutation
	ChunkID    string      `json:"chunk_id"`
	SourceHash string      `json:"source_hash"`
	Facts      []Candidate `json:"facts"`
}

// WikiFactReview binds one semantic decision to the unchanged fact content.
type WikiFactReview struct {
	FactID   string `json:"fact_id"`
	FactHash string `json:"fact_hash"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
}

// WikiReviewInput submits a bounded batch of reviews against the frozen evidence version.
type WikiReviewInput struct {
	WikiMutation
	EvidenceVersion string           `json:"evidence_version"`
	Reviews         []WikiFactReview `json:"reviews"`
}

// WikiCompilationInput submits source-supported summaries using the current reviewed fact set.
type WikiCompilationInput struct {
	WikiMutation
	EvidenceVersion string          `json:"evidence_version"`
	Sections        []MemorySection `json:"sections"`
}

// WikiMaterialRequest reads resumable source, stage, fact, review, summary or change windows.
type WikiMaterialRequest struct {
	WikiSessionRequest
	Kind     string `json:"kind,omitempty"`
	ChunkID  string `json:"chunk_id,omitempty"`
	Offset   int    `json:"offset,omitempty"`
	MaxChars int    `json:"max_chars,omitempty"`
}

// WikiRecordsRequest filters normalized Wiki records or durable sessions without loading chapter histories.
type WikiRecordsRequest struct {
	ProjectID string `json:"project_id,omitempty"`
	SourceID  string `json:"source_id,omitempty"`
	Kind      string `json:"kind,omitempty"`
	Status    string `json:"status,omitempty"`
	Query     string `json:"query,omitempty"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
	MaxChars  int    `json:"max_chars,omitempty"`
}

// WikiChunk retains complete source coverage and the last extraction's per-record diagnostics.
type WikiChunk struct {
	ID         string      `json:"id"`
	Offset     int         `json:"offset"`
	End        int         `json:"end"`
	Submitted  bool        `json:"submitted"`
	Candidates []Candidate `json:"candidates"`
	Errors     []string    `json:"errors"`
}

// ExternalWikiSession stores creative checkpoints independently of live facts and compiled memories.
type ExternalWikiSession struct {
	ID              string           `json:"id"`
	ProjectID       string           `json:"project_id"`
	SourceID        string           `json:"source_id"`
	Revision        uint64           `json:"revision"`
	Phase           string           `json:"phase"`
	Mode            string           `json:"mode"`
	Instruction     string           `json:"instruction"`
	Language        string           `json:"language"`
	SourceText      string           `json:"source_text"`
	SourceHash      string           `json:"source_hash"`
	Chapter         uint32           `json:"chapter"`
	RulesHash       string           `json:"rules_hash"`
	EntityHash      string           `json:"entity_hash"`
	FactsHash       string           `json:"facts_hash"`
	EvidenceVersion string           `json:"evidence_version"`
	BaseFacts       []Fact           `json:"base_facts"`
	KnownEntities   []Entity         `json:"known_entities"`
	Entities        []Entity         `json:"entities"`
	Chunks          []WikiChunk      `json:"chunks"`
	Facts           []Fact           `json:"facts"`
	Reviews         []WikiFactReview `json:"reviews"`
	Sections        []MemorySection  `json:"sections"`
	CandidateID     string           `json:"candidate_id,omitempty"`
	Client          string           `json:"client"`
	Transport       string           `json:"transport"`
	CreatedAt       string           `json:"created_at"`
	UpdatedAt       string           `json:"updated_at"`
}

// WikiPatch is a server-generated single-source compound candidate with stable record identities.
type WikiPatch struct {
	SourceID    string           `json:"source_id"`
	SourceHash  string           `json:"source_hash"`
	Chapter     uint32           `json:"chapter"`
	RulesHash   string           `json:"rules_hash"`
	EntityHash  string           `json:"entity_hash"`
	FactsHash   string           `json:"facts_hash"`
	Entities    []Entity         `json:"entities"`
	Facts       []Fact           `json:"facts"`
	Compilation *WikiCompilation `json:"compilation,omitempty"`
	Reviews     []WikiFactReview `json:"reviews"`
}

// wikiFactHash identifies semantic fact content independently of timestamps and author review state.
func wikiFactHash(fact Fact) string {
	return hashText(jsonText([]any{fact.ID, fact.Entity, fact.Kind, fact.Claim, fact.SourceID, fact.SourceHash, fact.Quote, fact.Chapter, fact.StoryTime, fact.Location, fact.RelatedEntity, fact.Epistemic}))
}

// configureWikiSchema adds shared enum and batch constraints to the typed, root-object contracts.
func configureWikiSchema(name string, schema map[string]any) {
	properties := schema["properties"].(map[string]any)
	if name == "wiki_begin" {
		properties["mode"].(map[string]any)["enum"] = []string{"compile", "review"}
	}
	if name == "wiki_material" {
		properties["kind"].(map[string]any)["enum"] = []string{"stage", "source", "chunks", "facts", "evidence", "review", "compilation", "changes"}
	}
	if name == "list_wiki_records" {
		properties["kind"].(map[string]any)["enum"] = []string{"fact", "compilation", "session"}
	}
	if name == "wiki_submit_extraction" {
		facts := properties["facts"].(map[string]any)
		facts["maxItems"] = 100
		facts["items"].(map[string]any)["required"] = []string{"entity", "kind", "claim", "quote", "epistemic"}
	}
	if name == "wiki_submit_review" {
		reviews := properties["reviews"].(map[string]any)
		reviews["minItems"], reviews["maxItems"] = 1, 100
		item := reviews["items"].(map[string]any)
		item["required"] = []string{"fact_id", "fact_hash", "decision", "reason"}
		item["properties"].(map[string]any)["decision"].(map[string]any)["enum"] = []string{"accept", "reject", "needs_revision"}
	}
	if name == "resolve_candidate_conflicts" {
		choices := properties["resolutions"].(map[string]any)
		choices["minItems"] = 1
		item := choices["items"].(map[string]any)
		item["required"] = []string{"kind", "id", "resolution"}
		item["properties"].(map[string]any)["resolution"].(map[string]any)["enum"] = []string{"keep", "restore"}
	}
}
