package core

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

// startDetachedCLIJob keeps the executing owner alive in a console worker after returning its task ID.
func startDetachedCLIJob(operation string, raw json.RawMessage, opts cliOptions, stdout, stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	arguments := []string{"api", "call", operation, "--background-worker", "--input", "-", "--json"}
	if opts.dbPath != "" {
		arguments = append(arguments, "--db", opts.dbPath)
	}
	if opts.projectRef != "" {
		arguments = append(arguments, "--project", opts.projectRef)
	}
	if opts.client != "" {
		arguments = append(arguments, "--client", opts.client)
	}
	command := exec.Command(executable, arguments...)
	command.Stdin = strings.NewReader(string(raw))
	quiet, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	defer quiet.Close()
	command.Stderr = quiet
	pipe, err := command.StdoutPipe()
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	defer pipe.Close()
	if err := command.Start(); err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	var envelope CommandEnvelope
	err = json.NewDecoder(io.LimitReader(pipe, 8_000_001)).Decode(&envelope)
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	go func() { _ = command.Wait() }()
	if envelope.OK {
		return writeStructuredCLI(stdout, stderr, envelope.Result, nil, opts.jsonOutput)
	}
	return writeStructuredCLI(stdout, stderr, nil, externalFailure(envelope.ErrorCode, envelope.Error, envelope.Details), opts.jsonOutput)
}

// waitCLIJob retains one process owner until its durable model or research task reaches a terminal state.
func waitCLIJob(service *ExternalService, operation string, initial any) (any, error) {
	var identity struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := decodeJSON(rawMessage(initial), &identity); err != nil {
		return nil, err
	}
	value := initial
	command, key := "get_job", "job_id"
	if operation == "start_search_run" {
		command, key = "get_search_run", "run_id"
	}
	for identity.Status == "running" || identity.Status == "queued" {
		time.Sleep(100 * time.Millisecond)
		var err error
		value, err = service.engine.Dispatch(command, rawMessage(map[string]any{key: identity.ID, "project_id": service.options.ProjectID}))
		if err != nil {
			return nil, err
		}
		if err := decodeJSON(rawMessage(value), &identity); err != nil {
			return nil, err
		}
	}
	if !contains([]string{"done", "succeeded", "empty"}, identity.Status) {
		code := "TASK_FAILED"
		if identity.Status == "cancelled" {
			code = "CANCELLED"
		}
		return value, externalFailure(code, "The task ended with status "+identity.Status, map[string]any{"task_id": identity.ID, "status": identity.Status})
	}
	return value, nil
}
