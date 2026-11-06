package core

import (
	"context"
	websearch "quillweave/internal/search"
	"sync"
)

type SearchSourceProvenance = websearch.Provenance
type SearchSelection = websearch.Selection

type searchSecret struct {
	Value, Identity string
	Generation      uint64
}
type searchTask struct {
	cancel   context.CancelFunc
	done     chan struct{}
	profiles map[string]bool
}
type searchRuntime struct {
	mu    sync.Mutex
	tasks map[string]*searchTask
}

type searchPlan struct {
	Profile            websearch.Profile          `json:"profile"`
	Fallbacks          []websearch.Profile        `json:"fallbacks"`
	Policy             string                     `json:"fallback_policy"`
	Source             string                     `json:"source"`
	SettingsRevision   uint64                     `json:"settings_revision"`
	ModelRevision      uint64                     `json:"model_revision"`
	ModelAllowance     int                        `json:"model_allowance,omitempty"`
	ModelProfileID     string                     `json:"model_profile_id,omitempty"`
	Model              websearch.Model            `json:"model"`
	TaskModelProfileID string                     `json:"task_model_profile_id,omitempty"`
	Models             map[string]websearch.Model `json:"models"`
}
type searchRun struct {
	ID                   string             `json:"id"`
	Revision             uint64             `json:"revision"`
	ProjectID            string             `json:"project_id,omitempty"`
	RequestID            string             `json:"request_id"`
	Purpose              string             `json:"purpose"`
	Operation            string             `json:"operation"`
	Status               string             `json:"status"`
	Query                string             `json:"query"`
	URL                  string             `json:"url,omitempty"`
	Language             string             `json:"language"`
	Plan                 searchPlan         `json:"effective_profile"`
	Response             websearch.Response `json:"response"`
	Attempts             []searchAttempt    `json:"attempts"`
	ReservedSearches     int                `json:"reserved_searches"`
	ReservedFetches      int                `json:"reserved_fetches"`
	NativeModelCalls     int                `json:"native_model_calls"`
	Error                *websearch.Fault   `json:"error,omitempty"`
	CreatedAt            string             `json:"created_at"`
	FinishedAt           string             `json:"finished_at,omitempty"`
	Owner                string             `json:"-"`
	Hash                 string             `json:"-"`
	ParentJobID          string             `json:"parent_job_id,omitempty"`
	CredentialGeneration uint64             `json:"credential_generation,omitempty"`
}
type searchAttempt struct {
	ProfileID string           `json:"profile_id"`
	Provider  string           `json:"provider"`
	Status    string           `json:"status"`
	Error     *websearch.Fault `json:"error,omitempty"`
}
type searchStart struct {
	RequestID       string              `json:"request_id"`
	ProjectID       string              `json:"project_id"`
	SessionID       string              `json:"session_id"`
	ModelProfileID  string              `json:"model_profile_id"`
	Selection       websearch.Selection `json:"selection"`
	Purpose         string              `json:"purpose"`
	Operation       string              `json:"operation"`
	Query           string              `json:"query"`
	URL             string              `json:"url"`
	UILanguage      string              `json:"ui_language"`
	ParentJobID     string              `json:"-"`
	SearchAllowance int                 `json:"-"`
	ModelAllowance  int                 `json:"-"`
}

// background supplies short database operations without holding an outgoing request's cancellation token.
func background() context.Context { return context.Background() }

// terminalSearch protects completed runs against late supplier responses.
func terminalSearch(status string) bool { return status != "queued" && status != "running" }
