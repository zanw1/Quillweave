package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// RunCLIWithInput exposes injectable stdin for structured commands and preserves existing CLI entrypoints.
func RunCLIWithInput(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || !contains([]string{"api", "writing", "connect"}, args[0]) {
		return RunCLI(args, stdout, stderr)
	}
	opts, err := parseCLIFlags(args)
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	if args[0] == "connect" {
		return printExternalConnection(opts, stdout, stderr)
	}
	if len(opts.positional) < 2 {
		return writeStructuredCLI(stdout, stderr, nil, externalFailure("INVALID_INPUT", "Use api list|describe|call, or writing begin|compose|draft|review|status|refresh|finish|abandon"), opts.jsonOutput)
	}
	if args[0] == "api" && opts.positional[1] == "list" {
		return writeStructuredCLI(stdout, stderr, CommandCatalog(), nil, opts.jsonOutput)
	}
	if args[0] == "api" && opts.positional[1] == "stream" {
		return runCLIStream(stdin, stdout, stderr, opts)
	}
	if args[0] == "api" && opts.positional[1] == "describe" && len(opts.positional) == 3 {
		value, err := DescribeCommand(opts.positional[2])
		return writeStructuredCLI(stdout, stderr, value, err, opts.jsonOutput)
	}
	operation := ""
	if args[0] == "api" && opts.positional[1] == "call" && len(opts.positional) == 3 {
		operation = opts.positional[2]
	}
	if args[0] == "writing" {
		operation = map[string]string{"begin": "writing_begin", "compose": "writing_submit_composition", "draft": "writing_submit_draft", "review": "writing_submit_review", "status": "writing_status", "refresh": "writing_refresh", "finish": "writing_finish", "abandon": "writing_abandon"}[opts.positional[1]]
	}
	if operation == "" {
		return writeStructuredCLI(stdout, stderr, nil, externalFailure("INVALID_INPUT", "Choose a documented API operation or writing subcommand"), opts.jsonOutput)
	}
	raw, err := readStructuredCLIInput(opts.inputPath, stdin)
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	var fields map[string]json.RawMessage
	if err := externalDecode(raw, &fields); err != nil || fields == nil {
		if err == nil {
			err = externalFailure("INVALID_INPUT", "The command input must be a JSON object")
		}
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	if opts.requestID != "" {
		fields["request_id"] = rawMessage(opts.requestID)
	}
	if opts.sessionID != "" {
		fields["writing_session_id"] = rawMessage(opts.sessionID)
	}
	if opts.chapterID != "" {
		fields["chapter_id"] = rawMessage(opts.chapterID)
	}
	longTask := operation == "start_ai" || operation == "start_search_run"
	if longTask && (opts.creator || opts.readOnly) {
		return writeStructuredCLI(stdout, stderr, nil, externalFailure("CAPABILITY_UNAVAILABLE", "Model tasks belong to the author CLI"), opts.jsonOutput)
	}
	if longTask && !opts.wait && !opts.backgroundWorker {
		return startDetachedCLIJob(operation, rawMessage(fields), opts, stdout, stderr)
	}
	path, err := externalDatabasePath(opts.dbPath)
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	options := ExternalOptions{ReadOnly: opts.readOnly, Author: args[0] == "api" && !opts.creator && !opts.readOnly, Client: opts.client, Transport: "cli"}
	service, err := OpenExternalService(path, options)
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	defer service.Close()
	if opts.projectRef != "" {
		cards, lookupErr := service.engine.store.listProjectCards()
		matches := []string{}
		for _, card := range cards {
			if card.ID == opts.projectRef {
				matches = []string{card.ID}
				break
			}
			if strings.HasPrefix(card.ID, opts.projectRef) || strings.Contains(lowerText(card.Title), lowerText(opts.projectRef)) {
				matches = append(matches, card.ID)
			}
		}
		if lookupErr != nil {
			return writeStructuredCLI(stdout, stderr, nil, lookupErr, opts.jsonOutput)
		}
		if len(matches) != 1 {
			return writeStructuredCLI(stdout, stderr, nil, externalFailure("PROJECT_SCOPE", "Select one project by its full ID or unique title"), opts.jsonOutput)
		}
		service.options.ProjectID = matches[0]
	}
	value, err := service.Execute(context.Background(), operation, rawMessage(fields))
	if err == nil && longTask {
		if opts.backgroundWorker {
			code := writeStructuredCLI(stdout, stderr, value, nil, true)
			_, _ = waitCLIJob(service, operation, value)
			return code
		}
		value, err = waitCLIJob(service, operation, value)
	}
	return writeStructuredCLI(stdout, stderr, value, err, opts.jsonOutput)
}

// readStructuredCLIInput consumes a bounded JSON object from stdin or a caller-selected file.
func readStructuredCLIInput(path string, stdin io.Reader) (json.RawMessage, error) {
	if path == "" {
		return json.RawMessage("{}"), nil
	}
	reader := stdin
	if path != "-" {
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		reader = file
	}
	raw, err := io.ReadAll(io.LimitReader(reader, 8_000_001))
	if err != nil {
		return nil, err
	}
	if len(raw) > 8_000_000 {
		return nil, externalFailure("INVALID_INPUT", "The structured input exceeds 8 MB")
	}
	return raw, nil
}

// writeStructuredCLI keeps one machine-readable envelope on stdout and actionable exit codes.
func writeStructuredCLI(stdout, stderr io.Writer, value any, err error, jsonMode bool) int {
	envelope := ExternalEnvelope(value, err)
	if jsonMode || err == nil {
		fmt.Fprintln(stdout, jsonText(envelope))
	} else {
		fmt.Fprintln(stderr, envelope.Error)
	}
	if envelope.OK {
		return 0
	}
	if contains([]string{"REVISION_CONFLICT", "REQUEST_CONFLICT", "SOURCES_CHANGED", "REVERT_CONFLICT"}, envelope.ErrorCode) {
		return 2
	}
	return 1
}

// printExternalConnection emits client configuration without changing the client's persistent settings.
// Without --project the connection is unbound and the agent picks the work through list_projects on each call.
func printExternalConnection(opts cliOptions, stdout, stderr io.Writer) int {
	if len(opts.positional) != 2 || !contains([]string{"codex", "claude", "cursor", "zcode"}, opts.positional[1]) {
		return writeStructuredCLI(stdout, stderr, nil, externalFailure("INVALID_INPUT", "Use connect codex|claude|cursor|zcode [--project <project-id>]"), opts.jsonOutput)
	}
	executable, err := os.Executable()
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, opts.jsonOutput)
	}
	companion := filepath.Join(filepath.Dir(executable), "quillweave-cli")
	if strings.EqualFold(filepath.Ext(executable), ".exe") {
		companion += ".exe"
	}
	if info, err := os.Stat(companion); err == nil && !info.IsDir() {
		executable = companion
	}
	arguments := []string{"mcp", "--client", opts.positional[1]}
	if opts.projectRef != "" {
		arguments = []string{"mcp", "--project", opts.projectRef, "--client", opts.positional[1]}
	}
	if opts.dbPath != "" {
		arguments = append(arguments, "--db", opts.dbPath)
	}
	if opts.readOnly {
		arguments = append(arguments, "--read-only")
	}
	if opts.positional[1] == "codex" {
		quoted := []string{}
		for _, argument := range arguments {
			quoted = append(quoted, strconv.Quote(argument))
		}
		content := fmt.Sprintf("[mcp_servers.quillweave]\ncommand = %s\nargs = [%s]\n", strconv.Quote(executable), strings.Join(quoted, ", "))
		if opts.jsonOutput {
			return writeStructuredCLI(stdout, stderr, map[string]any{"client": "codex", "format": "toml", "content": content}, nil, true)
		}
		fmt.Fprint(stdout, content)
		return 0
	}
	entry := map[string]any{"type": "stdio", "command": executable, "args": arguments}
	servers := map[string]any{"quillweave": entry}
	configuration := map[string]any{"mcpServers": servers}
	if opts.positional[1] == "zcode" {
		// ZCode's pooled-connection health check still uses ping, which requires the legacy protocol era.
		entry["protocolVersion"] = "legacy"
		configuration = map[string]any{"mcp": map[string]any{"servers": servers}}
	}
	if opts.jsonOutput {
		return writeStructuredCLI(stdout, stderr, map[string]any{"client": opts.positional[1], "format": "json", "configuration": configuration}, nil, true)
	}
	raw, _ := json.MarshalIndent(configuration, "", "  ")
	fmt.Fprintln(stdout, string(raw))
	return 0
}
