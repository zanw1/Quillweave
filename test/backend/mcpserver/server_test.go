//go:build quillweave_whitebox

package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"os/exec"
	"path/filepath"
	"quillweave/internal/core"
	"slices"
	"strings"
	"testing"
	"unicode"
)

// TestMCPWikiFlow exercises Wiki extraction, review, compilation and confirmation over the actual protocol.
func TestMCPWikiFlow(t *testing.T) {
	_, session, path, projectID := mcpFixture(t, false)
	engine, err := core.NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	// dispatch reads real author results while the tested creator operations stay on MCP.
	dispatch := func(name string, input any) core.Project {
		raw, _ := json.Marshal(input)
		value, err := engine.Dispatch(name, raw)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ = json.Marshal(value)
		var project core.Project
		if err := json.Unmarshal(raw, &project); err != nil {
			t.Fatal(err)
		}
		return project
	}
	project := dispatch("get_project", map[string]any{"project_id": projectID})
	project = dispatch("new_chapter", map[string]any{"project_id": projectID, "revision": project.Revision, "title": "The ledger"})
	chapter := project.Chapters[0]
	project = dispatch("save_chapter", map[string]any{"project_id": projectID, "revision": project.Revision, "chapter_id": chapter.ID, "title": chapter.Title, "body": "Lin Wu locked the ledger inside a drawer.", "synopsis": "", "status": "draft", "target_words": nil})
	view := callMCP(t, session, "wiki_begin", map[string]any{"request_id": "mcp-wiki-begin", "source_id": chapter.ID})
	chunk := view["chunks"].([]any)[0].(map[string]any)
	// mutate carries forward the exact identity and revision returned by the previous protocol call.
	mutate := func(name, request string, fields map[string]any) {
		fields["request_id"], fields["wiki_session_id"], fields["session_revision"] = request, view["wiki_session_id"], view["session_revision"]
		view = callMCP(t, session, name, fields)
	}
	mutate("wiki_submit_extraction", "mcp-wiki-extract", map[string]any{"chunk_id": chunk["id"], "source_hash": view["source_hash"], "facts": []map[string]any{{"entity": "Lin Wu", "entity_kind": "character", "kind": "event", "claim": "Lin Wu secured the ledger.", "quote": "Lin Wu locked the ledger inside a drawer.", "epistemic": "explicit"}}})
	material := callMCP(t, session, "wiki_material", map[string]any{"wiki_session_id": view["wiki_session_id"], "kind": "facts"})
	var facts []struct {
		Fact core.Fact `json:"fact"`
		Hash string    `json:"fact_hash"`
	}
	if err := json.Unmarshal([]byte(material["text"].(string)), &facts); err != nil || len(facts) != 1 {
		t.Fatal("Invalid protocol evidence material", err)
	}
	mutate("wiki_submit_review", "mcp-wiki-review", map[string]any{"evidence_version": view["evidence_version"], "reviews": []map[string]any{{"fact_id": facts[0].Fact.ID, "fact_hash": facts[0].Hash, "decision": "accept", "reason": "The exact quote supports this action."}}})
	mutate("wiki_submit_compilation", "mcp-wiki-compile", map[string]any{"evidence_version": view["evidence_version"], "sections": []map[string]any{{"summary": "Lin Wu secured the ledger inside a drawer.", "evidence_ids": []string{facts[0].Fact.ID}, "questions": []string{}}}})
	mutate("wiki_finish", "mcp-wiki-finish", map[string]any{})
	review := callMCP(t, session, "review_candidate", map[string]any{"id": view["candidate_id"]})
	confirmed := callMCP(t, session, "confirm_candidate", map[string]any{"request_id": "mcp-wiki-confirm", "candidate_id": view["candidate_id"], "confirm_token": review["confirm_token"]})
	if confirmed["status"] != "applied" {
		t.Fatal("Protocol Wiki candidate was not applied")
	}
	project = dispatch("get_project", map[string]any{"project_id": projectID})
	if len(project.Facts) != 1 || len(project.Compilations) != 1 {
		t.Fatal("Protocol Wiki flow lost source-backed records")
	}
}

// TestMCPLegacyKeepAlive preserves ZCode's ping-based connection reuse under its legacy protocol setting.
func TestMCPLegacyKeepAlive(t *testing.T) {
	service, _, _, _ := mcpFixture(t, false)
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := NewServer(service, false).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "zcode", Version: "compatibility-test"}, nil)
	session, err := client.Connect(ctx, clientTransport, &mcp.ClientSessionOptions{ProtocolVersion: "2025-11-25"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	if version := session.InitializeResult().ProtocolVersion; version != "2025-11-25" {
		t.Fatalf("Expected handshake-era protocol, got %s", version)
	}
	for range 2 {
		if err := session.Ping(ctx, nil); err != nil {
			t.Fatalf("Connection-pool health check failed: %v", err)
		}
		if page := callMCP(t, session, "list_projects", map[string]any{}); page["total"] != float64(1) {
			t.Fatal("Tool call failed after the health check")
		}
	}
}

// TestMCPDiscoveryMetadata checks English tool metadata and scope hints in every connection mode.
func TestMCPDiscoveryMetadata(t *testing.T) {
	_, _, path, projectID := mcpFixture(t, false)
	for _, bound := range []bool{false, true} {
		for _, readOnly := range []bool{false, true} {
			name := "unbound"
			if bound {
				name = "bound"
			}
			if readOnly {
				name += "-read-only"
			}
			t.Run(name, func(t *testing.T) {
				options := core.ExternalOptions{Client: "discovery-test", ReadOnly: readOnly}
				if bound {
					options.ProjectID = projectID
				}
				service, err := core.OpenExternalService(path, options)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { service.Close() })
				session := connectMCP(t, service, readOnly)
				listed, err := session.ListTools(context.Background(), nil)
				if err != nil {
					t.Fatal(err)
				}
				expected := 35
				if readOnly {
					expected = 16
				}
				if len(listed.Tools) != expected {
					t.Fatalf("Discovered %d tools, want %d", len(listed.Tools), expected)
				}
				for _, tool := range listed.Tools {
					raw, err := json.Marshal(tool)
					if err != nil {
						t.Fatal(err)
					}
					if tool.Description == "" || strings.ContainsFunc(string(raw), func(r rune) bool { return unicode.Is(unicode.Han, r) }) {
						t.Fatalf("Tool metadata must use English: %s", tool.Name)
					}
					if readOnly && !tool.Annotations.ReadOnlyHint {
						t.Fatalf("Read-only discovery exposed %s", tool.Name)
					}
				}
			})
		}
	}
}

// mcpFixture creates disposable application data and a real protocol session.
func mcpFixture(t *testing.T, readOnly bool) (*core.ExternalService, *mcp.ClientSession, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.sqlite3")
	engine, err := core.NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	raw, _ := json.Marshal(map[string]any{"title": "MCP fiction", "genre": "Fantasy", "premise": "A keeper investigates a ledger"})
	created, err := engine.Dispatch("create_project", raw)
	if err != nil {
		t.Fatal(err)
	}
	var project core.Project
	raw, _ = json.Marshal(created)
	if err := json.Unmarshal(raw, &project); err != nil {
		t.Fatal(err)
	}
	service, err := core.OpenExternalService(path, core.ExternalOptions{ProjectID: project.ID, ReadOnly: readOnly, Client: "protocol-test", Transport: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	return service, connectMCP(t, service, readOnly), path, project.ID
}

// connectMCP serves one external service over in-memory transports and returns the client side.
func connectMCP(t *testing.T, service *core.ExternalService, readOnly bool) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := NewServer(service, readOnly).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test-host", Version: "1"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

// TestMCPStdioRestart verifies the packaged entrypoint and durable sessions across real server processes.
func TestMCPStdioRestart(t *testing.T) {
	_, _, path, projectID := mcpFixture(t, false)
	binary := filepath.Join(t.TempDir(), "quillweave")
	build := exec.Command("go", "build", "-o", binary, "./cmd/quillweave-cli")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("Build console companion: %v %s", err, output)
	}
	// connect starts a real console subprocess and waits for its protocol discovery.
	connect := func(scope ...string) *mcp.ClientSession {
		command := exec.Command(binary, append([]string{"mcp", "--db", path, "--client", "restart-test"}, scope...)...)
		client := mcp.NewClient(&mcp.Implementation{Name: "restart-host", Version: "1"}, nil)
		session, err := client.Connect(context.Background(), &mcp.CommandTransport{Command: command}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })
		return session
	}
	session := connect("--project", projectID)
	view := callMCP(t, session, "writing_begin", map[string]any{"request_id": "stdio-begin", "title": "A pipe-tested chapter", "instruction": "Write a quiet scene", "mode": "create"})
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	reconnected := connect("--project", projectID)
	status := callMCP(t, reconnected, "writing_status", map[string]any{"writing_session_id": view["writing_session_id"]})
	if status["phase"] != "awaiting_composition" || status["session_revision"] != view["session_revision"] {
		t.Fatal("Process restart changed the durable session")
	}
	// Starting without --project must serve the catalog instead of exiting, and resume the same session by naming its work.
	unbound := connect()
	if listed := callMCP(t, unbound, "list_projects", map[string]any{}); listed["total"] != float64(1) {
		t.Fatalf("Unbound process listed %v works", listed["total"])
	}
	status = callMCP(t, unbound, "writing_status", map[string]any{"project_id": projectID, "writing_session_id": view["writing_session_id"]})
	if status["phase"] != "awaiting_composition" {
		t.Fatal("Unbound process could not resume the session in its named work")
	}
}

// TestMCPUnboundProjectChoice verifies that a connection without --project makes the agent name a listed work on
// every call, and keeps each writing session inside the work that started it.
func TestMCPUnboundProjectChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.sqlite3")
	engine, err := core.NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	ids := []string{}
	for _, title := range []string{"守墓", "听潮"} {
		raw, _ := json.Marshal(map[string]any{"title": title, "genre": "Fantasy", "premise": title + "的故事"})
		created, err := engine.Dispatch("create_project", raw)
		if err != nil {
			t.Fatal(err)
		}
		var project struct {
			ID string `json:"id"`
		}
		raw, _ = json.Marshal(created)
		if err := json.Unmarshal(raw, &project); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, project.ID)
	}
	service, err := core.OpenExternalService(path, core.ExternalOptions{Client: "protocol-test", Transport: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { service.Close() })
	session := connectMCP(t, service, false)
	if !strings.Contains(session.InitializeResult().Instructions, "qw_list_projects") {
		t.Fatal("Unbound instructions do not tell the agent to list works first")
	}
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		raw, _ := json.Marshal(tool.InputSchema)
		var schema struct {
			Required []string `json:"required"`
		}
		if err := json.Unmarshal(raw, &schema); err != nil {
			t.Fatal(err)
		}
		if needs := tool.Name != "qw_list_projects"; needs != slices.Contains(schema.Required, "project_id") {
			t.Fatalf("%s: project_id required=%v", tool.Name, !needs)
		}
	}
	if page := callMCP(t, session, "list_projects", map[string]any{}); page["total"] != float64(2) {
		t.Fatalf("list_projects returned %v works", page["total"])
	}
	for index, title := range []string{"守墓", "听潮"} {
		if overview := callMCP(t, session, "project_overview", map[string]any{"project_id": ids[index]}); overview["title"] != title {
			t.Fatalf("project_overview opened %v instead of %s", overview["title"], title)
		}
	}
	missing, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "qw_project_overview", Arguments: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !missing.IsError {
		t.Fatal("An unbound call without project_id was accepted")
	}
	view := callMCP(t, session, "writing_begin", map[string]any{"project_id": ids[0], "request_id": "begin", "title": "账册", "instruction": "写守墓人整理账册的日常", "mode": "create"})
	crossed, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "qw_writing_status", Arguments: map[string]any{"project_id": ids[1], "writing_session_id": view["writing_session_id"]}})
	if err != nil {
		t.Fatal(err)
	}
	if !crossed.IsError {
		t.Fatal("A writing session leaked into another work")
	}
	var stdout, stderr bytes.Buffer
	if code := core.RunCLIWithInput([]string{"connect", "claude", "--json"}, strings.NewReader(""), &stdout, &stderr); code != 0 || strings.Contains(stdout.String(), "--project") {
		t.Fatalf("connect without --project: %d %s %s", code, stdout.String(), stderr.String())
	}
}

// callMCP decodes the actual MCP result envelope used by all supported clients.
func callMCP(t *testing.T, session *mcp.ClientSession, name string, input any) map[string]any {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "qw_" + name, Arguments: input})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
		Error  string         `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if result.IsError || !envelope.OK {
		t.Fatalf("%s: %s", name, envelope.Error)
	}
	return envelope.Result
}

// TestMCPExternalWriting executes every creative stage over the real SDK schema and transport boundary.
func TestMCPExternalWriting(t *testing.T) {
	_, session, _, _ := mcpFixture(t, false)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Tools) < 20 {
		t.Fatal("Creator tools were omitted")
	}
	hints := map[string]*mcp.ToolAnnotations{}
	for _, tool := range listed.Tools {
		hints[tool.Name] = tool.Annotations
	}
	review, confirm, finish := hints["qw_review_candidate"], hints["qw_confirm_candidate"], hints["qw_writing_finish"]
	if review == nil || !review.ReadOnlyHint || confirm == nil || confirm.ReadOnlyHint || !*confirm.DestructiveHint || *finish.DestructiveHint {
		t.Fatal("Candidate review and confirmation lost their read-only and destructive annotations")
	}
	if !strings.Contains(session.InitializeResult().Instructions, "only after the author explicitly agrees") {
		t.Fatal("Instructions do not keep confirmation an explicit author decision")
	}
	view := callMCP(t, session, "writing_begin", map[string]any{"request_id": "begin", "title": "账册", "instruction": "写守墓人整理账册的日常", "mode": "create"})
	if view["compact"] != true || view["brief"] != nil || view["draft"] != nil || view["evidence"] == nil || view["output_schema"] == nil {
		t.Fatal("MCP begin did not return the compact session reply")
	}
	guard, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "qw_writing_submit_draft", Arguments: map[string]any{"request_id": "skip-composition", "writing_session_id": view["writing_session_id"], "session_revision": view["session_revision"], "draft": "Skipped composition"}})
	if err != nil {
		t.Fatal(err)
	}
	rawGuard, _ := json.Marshal(guard.StructuredContent)
	var rejection core.CommandEnvelope
	if err := json.Unmarshal(rawGuard, &rejection); err != nil {
		t.Fatal(err)
	}
	if !guard.IsError || rejection.ErrorCode != "PHASE_ORDER" {
		t.Fatal("MCP lost the shared business error envelope")
	}
	// mutation binds each new call to the exact preceding session revision.
	mutation := func(request string) map[string]any {
		return map[string]any{"request_id": request, "writing_session_id": view["writing_session_id"], "session_revision": view["session_revision"]}
	}
	input := mutation("composition")
	input["composition"] = core.WritingComposition{Intent: "整理账册", Approach: "由日常动作展现关系", SceneCards: []core.WritingScene{{POV: "林无", Goal: "整理账册", Choice: "包好账册", Action: "将账册包入布中", Outcome: "账册得到保护", Characters: []core.WritingSceneCharacter{{Name: "林无", Want: "保管账册", Response: "亲手包好账册"}}}}}
	view = callMCP(t, session, "writing_submit_composition", input)
	input = mutation("draft")
	input["draft"] = "林无把账册包进粗布，放回木匣。"
	view = callMCP(t, session, "writing_submit_draft", input)
	if reply, _ := json.Marshal(view); view["draft"] != nil || view["draft_chars"] == nil || len(reply) > 12000 {
		t.Fatal("MCP draft reply echoed the prose back into agent context")
	}
	input = mutation("review")
	input["draft_hash"], input["evidence_version"] = view["draft_hash"], view["evidence_version"]
	input["review"] = map[string]any{"summary": "动作清楚，保留日常场景。", "issues": []any{}, "needs_revision": false, "memories": []any{}}
	view = callMCP(t, session, "writing_submit_review", input)
	view = callMCP(t, session, "writing_finish", mutation("finish"))
	if view["candidate_id"] == "" {
		t.Fatal("MCP writing did not produce a candidate")
	}
	// The compact reply must not hide the retained draft from a reconnecting agent.
	status := callMCP(t, session, "writing_status", map[string]any{"writing_session_id": view["writing_session_id"]})
	if status["draft"] != "林无把账册包进粗布，放回木匣。" {
		t.Fatal("writing_status stopped returning the retained draft")
	}
	candidate := callMCP(t, session, "get_candidate", map[string]any{"id": view["candidate_id"]})
	raw, _ := json.Marshal(candidate)
	if strings.Contains(string(raw), "review_token") {
		t.Fatal("Creator preview exposed a confirmation token")
	}
	reviewed := callMCP(t, session, "review_candidate", map[string]any{"id": view["candidate_id"]})
	confirmed := callMCP(t, session, "confirm_candidate", map[string]any{"request_id": "confirm", "candidate_id": view["candidate_id"], "confirm_token": reviewed["confirm_token"]})
	raw, _ = json.Marshal([]any{reviewed, confirmed})
	if !strings.Contains(reviewed["text"].(string), "林无把账册包进粗布") || confirmed["status"] != "applied" || strings.Contains(string(raw), "review_token") {
		t.Fatalf("MCP confirmation did not apply the reviewed candidate: %s", raw)
	}
}

// TestMCPReadOnlyAndCLIParity verifies tool registration and identical business projections across adapters.
func TestMCPReadOnlyAndCLIParity(t *testing.T) {
	_, session, path, projectID := mcpFixture(t, true)
	listed, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range listed.Tools {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatalf("Read-only connection exposed %s", tool.Name)
		}
	}
	value := callMCP(t, session, "project_overview", map[string]any{})
	var stdout, stderr bytes.Buffer
	code := core.RunCLIWithInput([]string{"api", "call", "project_overview", "--creator", "--db", path, "--project", projectID, "--json"}, strings.NewReader(""), &stdout, &stderr)
	if code != 0 {
		t.Fatal(stderr.String(), stdout.String())
	}
	var envelope struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	left, _ := json.Marshal(value)
	right, _ := json.Marshal(envelope.Result)
	if !bytes.Equal(left, right) {
		t.Fatal("CLI and MCP projections differ")
	}
}
