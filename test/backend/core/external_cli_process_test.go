//go:build quillweave_whitebox

package core

import (
	"bytes"
	"context"
	"debug/pe"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestExternalConnectionConfigurations checks every host's native config in plain and envelope output modes.
func TestExternalConnectionConfigurations(t *testing.T) {
	database := filepath.Join(t.TempDir(), "Data 资料", "workspace.sqlite3")
	cases := []struct {
		client   string
		project  string
		readOnly bool
	}{
		{"codex", "", false}, {"codex", "work-id", false},
		{"claude", "", false}, {"claude", "work-id", false},
		{"cursor", "", false}, {"cursor", "work-id", false},
		{"zcode", "", false}, {"zcode", "work-id", false},
		{"cursor", "", true}, {"zcode", "work-id", true},
	}
	for _, scenario := range cases {
		for _, structured := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/project=%s/readonly=%t/json=%t", scenario.client, scenario.project, scenario.readOnly, structured), func(t *testing.T) {
				arguments := []string{"connect", scenario.client, "--db", database}
				expected := []string{"mcp", "--client", scenario.client, "--db", database}
				if scenario.project != "" {
					arguments = append(arguments, "--project", scenario.project)
					expected = []string{"mcp", "--project", scenario.project, "--client", scenario.client, "--db", database}
				}
				if scenario.readOnly {
					arguments = append(arguments, "--read-only")
					expected = append(expected, "--read-only")
				}
				if structured {
					arguments = append(arguments, "--json")
				}
				var stdout, stderr bytes.Buffer
				if code := RunCLIWithInput(arguments, strings.NewReader(""), &stdout, &stderr); code != 0 {
					t.Fatalf("connect failed: %d %s %s", code, stdout.String(), stderr.String())
				}
				raw := stdout.Bytes()
				if structured {
					var envelope struct {
						OK     bool `json:"ok"`
						Result struct {
							Client        string          `json:"client"`
							Format        string          `json:"format"`
							Content       string          `json:"content"`
							Configuration json.RawMessage `json:"configuration"`
						} `json:"result"`
					}
					if err := json.Unmarshal(raw, &envelope); err != nil || !envelope.OK || envelope.Result.Client != scenario.client {
						t.Fatalf("Invalid connection envelope: %s", raw)
					}
					raw = envelope.Result.Configuration
					if scenario.client == "codex" {
						assert(t, envelope.Result.Format == "toml", "Codex config lost its TOML format")
						raw = []byte(envelope.Result.Content)
					} else {
						assert(t, envelope.Result.Format == "json", "Host config lost its JSON format")
					}
				}
				if scenario.client == "codex" {
					quoted := make([]string, len(expected))
					for i, argument := range expected {
						quoted[i] = strconv.Quote(argument)
					}
					assert(t, strings.HasPrefix(string(raw), "[mcp_servers.quillweave]\ncommand = "), "Missing Codex server table")
					assert(t, strings.Contains(string(raw), "args = ["+strings.Join(quoted, ", ")+"]"), "Codex arguments changed")
					return
				}
				var configuration struct {
					Servers map[string]json.RawMessage `json:"mcpServers"`
					MCP     struct {
						Servers map[string]json.RawMessage `json:"servers"`
					} `json:"mcp"`
				}
				if err := json.Unmarshal(raw, &configuration); err != nil {
					t.Fatal(err)
				}
				servers := configuration.Servers
				if scenario.client == "zcode" {
					assert(t, len(servers) == 0, "ZCode native config used the wrong root")
					servers = configuration.MCP.Servers
				}
				var entry struct {
					Type            string   `json:"type"`
					Command         string   `json:"command"`
					Args            []string `json:"args"`
					ProtocolVersion string   `json:"protocolVersion"`
				}
				if err := json.Unmarshal(servers["quillweave"], &entry); err != nil {
					t.Fatal(err)
				}
				assert(t, entry.Type == "stdio" && filepath.IsAbs(entry.Command), "Host needs an explicit stdio type and absolute command")
				assert(t, slices.Equal(entry.Args, expected), "Host arguments lost scope, provenance or database settings")
				if scenario.client == "zcode" {
					assert(t, entry.ProtocolVersion == "legacy", "ZCode health checks need legacy ping support")
				}
			})
		}
	}
	_, err := os.Stat(database)
	assert(t, os.IsNotExist(err), "Generating config created a database")
	var stdout, stderr bytes.Buffer
	code := RunCLIWithInput([]string{"connect", "unknown-host", "--json"}, strings.NewReader(""), &stdout, &stderr)
	var rejected CommandEnvelope
	assert(t, code == 1 && json.Unmarshal(stdout.Bytes(), &rejected) == nil && rejected.ErrorCode == "INVALID_INPUT", "Unknown host was accepted")
}

// TestExternalCLIAsyncOwner verifies that returning a model task ID retains a live independent worker.
func TestExternalCLIAsyncOwner(t *testing.T) {
	engine, _, project := externalFixture(t)
	release := make(chan struct{})
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-request.Context().Done():
			return
		case <-release:
		}
		fmt.Fprint(writer, streamPayload("The ledger is safely wrapped."))
	})
	t.Cleanup(func() { close(release) })
	config := defaultProvider()
	config.BaseURL, config.Model = server.URL, "async-fixture"
	configureModel(t, engine, config)
	binary := filepath.Join(t.TempDir(), "quillweave")
	build := exec.Command("go", "build", "-o", binary, "./cmd/quillweave-cli")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Build console: %v %s", err, output)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary, "api", "call", "start_ai", "--project", project.ID, "--db", engine.store.path, "--input", "-", "--json")
	command.Stdin = strings.NewReader(jsonText(map[string]any{"chapter_id": project.Chapters[0].ID, "action": "ask", "instruction": "Discuss the ledger"}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Start async task: %v %s", err, output)
	}
	var envelope struct {
		OK     bool `json:"ok"`
		Result Job  `json:"result"`
	}
	if err := decodeJSON(output, &envelope); err != nil {
		t.Fatal(err, string(output))
	}
	assert(t, envelope.OK && envelope.Result.Status == "running", "The CLI did not return its durable running task")
	job := invoke[Job](t, engine, "get_job", map[string]any{"job_id": envelope.Result.ID})
	assert(t, job.Status == "running", "The CLI exit interrupted its worker")
	invoke[any](t, engine, "cancel_ai", map[string]any{"job_id": job.ID})
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		current := invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
		if current.Status == "cancelled" {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("Detached task did not respond to cross-process cancellation")
}

// TestExternalWindowsConsoleBuild checks the actual Windows executable uses the console subsystem.
func TestExternalWindowsConsoleBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("Windows cross-build")
	}
	binary := filepath.Join(t.TempDir(), "quillweave.exe")
	command := exec.Command("go", "build", "-o", binary, "./cmd/quillweave-cli")
	command.Dir = filepath.Join("..", "..")
	command.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Windows console build: %v %s", err, output)
	}
	file, err := pe.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	header, ok := file.OptionalHeader.(*pe.OptionalHeader64)
	assert(t, ok && header.Subsystem == pe.IMAGE_SUBSYSTEM_WINDOWS_CUI, "Windows companion was linked as a GUI executable")
}
