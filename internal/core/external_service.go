package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"quillweave/internal/platform"
)

// ExternalOptions selects one project and the capabilities of an external connection.
type ExternalOptions struct {
	ProjectID string
	ReadOnly  bool
	Author    bool
	Client    string
	Transport string
}

// ExternalService shares domain operations without loading model credentials during startup.
type ExternalService struct {
	engine  *Engine
	options ExternalOptions
}

// ExternalError carries stable machine-readable failures across CLI and MCP.
type ExternalError struct {
	Code    string
	Message string
	Details map[string]any
}

// Error returns the author-facing diagnostic without changing its structured code.
func (fault *ExternalError) Error() string { return fault.Message }

// externalFailure constructs a domain error with optional recovery details.
func externalFailure(code, message string, details ...map[string]any) error {
	fault := &ExternalError{Code: code, Message: message}
	if len(details) > 0 {
		fault.Details = details[0]
	}
	return fault
}

// ExternalEnvelope gives both transports the existing result envelope and stable errors.
func ExternalEnvelope(value any, err error) CommandEnvelope {
	if err == nil {
		return CommandEnvelope{OK: true, Result: value}
	}
	var fault *ExternalError
	if errors.As(err, &fault) {
		return CommandEnvelope{Result: value, Error: fault.Message, ErrorCode: fault.Code, Details: fault.Details}
	}
	var sqliteError interface{ Code() int }
	if errors.As(err, &sqliteError) && sqliteError.Code()&255 == 5 {
		return CommandEnvelope{Error: "The database is busy; retry this request", ErrorCode: "BUSY_RETRY"}
	}
	if errors.Is(err, errWritingSourcesChanged) {
		return CommandEnvelope{Error: err.Error(), ErrorCode: "SOURCES_CHANGED"}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return CommandEnvelope{Error: "Record not found", ErrorCode: "NOT_FOUND"}
	}
	envelope := Envelope(value, err)
	if envelope.ErrorCode == "" {
		envelope.ErrorCode = "OPERATION_FAILED"
	}
	return envelope
}

// CommandEnvelopeSchema is the common public response contract for CLI discovery and MCP tools.
func CommandEnvelopeSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}, "result": map[string]any{}, "error": map[string]any{"type": "string"}, "error_code": map[string]any{"type": "string"}, "details": map[string]any{"type": "object", "additionalProperties": true}}, "required": []string{"ok"}, "additionalProperties": false}
}

// OpenExternalService opens the shared workspace with explicit read-only or creator capabilities.
func OpenExternalService(path string, options ExternalOptions) (*ExternalService, error) {
	var store *Store
	var err error
	if options.ReadOnly {
		store, err = openExternalReadStore(path)
	} else {
		store, err = OpenStore(path)
	}
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	engine := &Engine{store: store, ctx: ctx, cancel: cancel, jobs: map[string]*Job{}, searchKeys: map[string]searchSecret{}}
	if !options.ReadOnly {
		engine.owner, err = openRuntimeOwner(store.path)
		if err != nil {
			cancel()
			store.Close()
			return nil, err
		}
	}
	if options.Client == "" {
		options.Client = "external"
	}
	return &ExternalService{engine: engine, options: options}, nil
}

// openExternalReadStore uses SQLite read-only mode without migrations, runtime locks or credential access.
func openExternalReadStore(path string) (*Store, error) {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return nil, err
	}
	location := &url.URL{Scheme: "file", Path: canonical}
	database, err := sql.Open("sqlite", location.String()+"?mode=ro&_busy_timeout=5000&_query_only=1")
	if err != nil {
		return nil, err
	}
	database.SetMaxOpenConns(1)
	var version int
	if err = database.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		database.Close()
		return nil, err
	}
	if version < 4 || version > 6 {
		database.Close()
		return nil, externalFailure("SCHEMA_VERSION", "Open this database with a compatible Quillweave desktop version first")
	}
	return &Store{db: database, path: canonical}, nil
}

// Close releases this connection while retaining durable external writing sessions.
func (service *ExternalService) Close() error {
	if service.options.ReadOnly {
		service.engine.cancel()
		return service.engine.store.Close()
	}
	return service.engine.Close()
}

// projectID binds every operation to the connection's project when one was selected.
func (service *ExternalService) projectID(id string) (string, error) {
	if service.options.ProjectID != "" {
		if id != "" && id != service.options.ProjectID {
			return "", externalFailure("PROJECT_SCOPE", "Choose a record from the connected project")
		}
		id = service.options.ProjectID
	}
	if id == "" {
		return "", externalFailure("INVALID_INPUT", "A project_id is required. Call list_projects and pass the id of the work the author chose")
	}
	return id, nil
}

// BoundProjectID reports the project fixed at startup. An empty value means every call names its own project.
func (service *ExternalService) BoundProjectID() string { return service.options.ProjectID }

// writable rejects writes before they can reach a transaction.
func (service *ExternalService) writable() error {
	if service.options.ReadOnly {
		return externalFailure("READ_ONLY_MODE", "This connection provides read-only tools")
	}
	return nil
}

// externalDecode rejects malformed, trailing and oversized structured inputs.
func externalDecode(raw json.RawMessage, destination any) error {
	if len(raw) > 8_000_000 {
		return externalFailure("INVALID_INPUT", "The structured request exceeds 8 MB")
	}
	if err := decodeToolRecord(string(raw), destination); err != nil {
		return externalFailure("INVALID_INPUT", err.Error())
	}
	return nil
}

// externalDatabasePath resolves the same default workspace used by the desktop application.
func externalDatabasePath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	if path = os.Getenv("QUILLWEAVE_DB"); path != "" {
		return path, nil
	}
	directory, err := platform.DataDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "quillweave.sqlite3"), nil
}

// validateExternalContext checks cancellation at every externally initiated operation boundary.
func validateExternalContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return externalFailure("CANCELLED", fmt.Sprint(err))
	}
	return nil
}
