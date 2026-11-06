package backend_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	// Keep ordinary Go test caching dependent on the actual production packages.
	_ "quillweave/internal/core"
	_ "quillweave/internal/platform"
	_ "quillweave/internal/preview"
	_ "quillweave/internal/search"
)

type layout struct {
	Modules map[string]string `json:"go_modules"`
}

type event struct {
	Action  string
	Package string
	Test    string
	Output  string
}

type moduleResult struct {
	Module      string   `json:"module"`
	Package     string   `json:"package"`
	Files       int      `json:"files"`
	ExpectedTop int      `json:"expected_top_level"`
	PassedTop   int      `json:"passed_top_level"`
	PassedNodes int      `json:"passed_all_test_nodes"`
	Skipped     []string `json:"skipped"`
	Executed    []string `json:"executed_names"`
	Race        bool     `json:"race"`
}

// TestBackendModules preserves a real default go test ./... entry for centralized white-box sources.
func TestBackendModules(t *testing.T) {
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(filepath.Join(directory, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "test", "layout.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config layout
	if err := json.Unmarshal(data, &config); err != nil || len(config.Modules) == 0 {
		t.Fatalf("Invalid test layout: %v", err)
	}
	names := make([]string, 0, len(config.Modules))
	for name := range config.Modules {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			runModule(t, root, name, config.Modules[name])
		})
	}
}

// moduleOverlay validates and reads every backing file so test-source changes invalidate Go's cache.
func moduleOverlay(root, module, target, filter string) (map[string]string, map[string]bool, error) {
	directory := filepath.Join(root, "test", "backend", module)
	files, err := filepath.Glob(filepath.Join(directory, "*_test.go"))
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, fmt.Errorf("module %s has no test sources", module)
	}
	var selected *regexp.Regexp
	if filter != "" {
		selected, err = regexp.Compile(strings.SplitN(filter, "/", 2)[0])
		if err != nil {
			return nil, nil, err
		}
	}
	replacements, expected := map[string]string{}, map[string]bool{}
	for _, source := range files {
		data, err := os.ReadFile(source)
		if err != nil {
			return nil, nil, err
		}
		if !bytes.HasPrefix(data, []byte("//go:build quillweave_whitebox\n")) {
			return nil, nil, fmt.Errorf("missing white-box source tag: %s", source)
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), source, data, 0)
		if err != nil {
			return nil, nil, err
		}
		for _, declaration := range parsed.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok || !strings.HasPrefix(function.Name.Name, "Test") || function.Name.Name == "TestMain" {
				continue
			}
			if selected == nil || selected.MatchString(function.Name.Name) {
				expected[function.Name.Name] = true
			}
		}
		replacements[filepath.Join(root, filepath.FromSlash(target), filepath.Base(source))] = source
	}
	return replacements, expected, nil
}

// runModule uses Go's overlay rather than exporting private APIs or copying tests into production directories.
func runModule(t *testing.T, root, name, target string) {
	t.Helper()
	filter := os.Getenv("QUILLWEAVE_GO_TEST_RUN")
	replacements, expected, err := moduleOverlay(root, name, target, filter)
	if err != nil {
		t.Fatal(err)
	}
	result := moduleResult{Module: name, Package: "quillweave/" + target, Files: len(replacements), ExpectedTop: len(expected), Race: raceEnabled, Skipped: []string{}, Executed: []string{}}
	if len(expected) == 0 && filter != "" {
		encoded, _ := json.Marshal(result)
		t.Logf("BACKEND_RESULT %s", encoded)
		t.Skip("No test names match the explicitly requested filter in this module")
	}
	if len(expected) == 0 {
		t.Fatal("A module without discoverable test functions cannot pass")
	}
	data, err := json.Marshal(map[string]any{"Replace": replacements})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"test", "-json", "-count=1", "-timeout=10m", "-overlay=" + path, "-tags=quillweave_whitebox"}
	if raceEnabled {
		args = append(args, "-race")
	}
	if testing.Short() {
		args = append(args, "-short")
	}
	if filter != "" {
		args = append(args, "-run="+filter)
	}
	args = append(args, "./"+target)
	ctx := context.Background()
	if deadline, ok := t.Deadline(); ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline.Add(-5*time.Second))
		defer cancel()
	}
	command := exec.CommandContext(ctx, "go", args...)
	command.Dir = root
	output, runErr := command.CombinedOutput()
	terminal := map[string]bool{}
	for _, line := range bytes.Split(output, []byte("\n")) {
		var item event
		if json.Unmarshal(line, &item) != nil || item.Test == "" {
			continue
		}
		if item.Action == "run" {
			result.Executed = append(result.Executed, item.Package+"::"+item.Test)
		}
		if !strings.Contains(item.Test, "/") && (item.Action == "pass" || item.Action == "skip" || item.Action == "fail") {
			terminal[item.Test] = true
		}
		if item.Action == "skip" {
			result.Skipped = append(result.Skipped, item.Package+"::"+item.Test)
		}
		if item.Action == "pass" {
			result.PassedNodes++
			if !strings.Contains(item.Test, "/") {
				result.PassedTop++
			}
		}
	}
	encoded, _ := json.Marshal(result)
	t.Logf("BACKEND_RESULT %s", encoded)
	if runErr != nil {
		t.Fatalf("%s failed: %v\n%s", name, runErr, output)
	}
	if result.PassedTop == 0 || len(terminal) != len(expected) {
		t.Fatalf("Empty or incomplete module: executed %d/%d top-level tests, %d passed\n%s", len(terminal), len(expected), result.PassedTop, output)
	}
	for name := range expected {
		if !terminal[name] {
			t.Errorf("The declared test did not execute: %s", name)
		}
	}
}
