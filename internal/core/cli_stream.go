package core

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// runCLIStream retains process-scoped archive transfers while serving newline-delimited API calls.
func runCLIStream(input io.Reader, stdout, stderr io.Writer, options cliOptions) int {
	path, err := externalDatabasePath(options.dbPath)
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, true)
	}
	service, err := OpenExternalService(path, ExternalOptions{ProjectID: options.projectRef, ReadOnly: options.readOnly, Author: !options.creator && !options.readOnly, Client: options.client, Transport: "cli"})
	if err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, true)
	}
	defer service.Close()
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 8_000_001)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		var request struct {
			Operation string          `json:"operation"`
			Input     json.RawMessage `json:"input"`
		}
		if err := externalDecode(scanner.Bytes(), &request); err != nil {
			fmt.Fprintln(stdout, jsonText(ExternalEnvelope(nil, err)))
			continue
		}
		value, err := service.Execute(context.Background(), request.Operation, request.Input)
		fmt.Fprintln(stdout, jsonText(ExternalEnvelope(value, err)))
	}
	if err := scanner.Err(); err != nil {
		return writeStructuredCLI(stdout, stderr, nil, err, true)
	}
	return 0
}
