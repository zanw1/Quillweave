//go:build quillweave_whitebox

package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRecoveryChild is an isolated real-engine process terminated by its parent recovery test.
func TestRecoveryChild(t *testing.T) {
	path := os.Getenv("QUILLWEAVE_RECOVERY_DB")
	if path == "" {
		t.Skip("Recovery subprocess only")
	}
	engine, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	p := testProject()
	insertProject(t, engine, p)
	config := nativeConfig("openai")
	config.BaseURL = os.Getenv("QUILLWEAVE_RECOVERY_MODEL")
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	if os.Getenv("QUILLWEAVE_RECOVERY_MODE") == "terminal" {
		if _, err := engine.store.db.Exec("CREATE TRIGGER recovery_failure BEFORE UPDATE ON runs WHEN json_extract(new.document,'$.status')!='running' BEGIN SELECT RAISE(ABORT,'recovery_failure'); END"); err != nil {
			t.Fatal(err)
		}
	}
	job := invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "chapter_id": "", "session_id": session.ID, "action": "ask", "instruction": "Before crash", "request_id": "before-crash"})
	if os.Getenv("QUILLWEAVE_RECOVERY_MODE") == "terminal" {
		job = finishJob(t, engine, job)
		if job.Status != "error" {
			t.Fatal("The storage fault did not reach the terminal path")
		}
	}
	ids := map[string]string{"project_id": p.ID, "session_id": session.ID, "job_id": job.ID}
	if err := os.WriteFile(os.Getenv("QUILLWEAVE_RECOVERY_READY"), []byte(jsonText(ids)), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Minute)
	t.Fatal("The recovery parent did not terminate the child")
}

// TestRepairedProcessRecovery restores both interrupted streams and completed results whose SQL commit failed.
func TestRepairedProcessRecovery(t *testing.T) {
	for _, mode := range []string{"stream", "terminal"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("QUILLWEAVE_API_KEY", "")
			var complete atomic.Bool
			complete.Store(mode == "terminal")
			server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if complete.Load() {
					fmt.Fprint(w, streamPayload("Completed result"))
					return
				}
				fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Checkpoint before crash\"}}]}\n\n")
				w.(http.Flusher).Flush()
				<-r.Context().Done()
			})
			folder := t.TempDir()
			path := filepath.Join(folder, "recovery.sqlite3")
			ready := filepath.Join(folder, "ready.json")
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			child := exec.Command(executable, "-test.run=^TestRecoveryChild$", "-test.timeout=50s")
			child.Env = append(os.Environ(), "QUILLWEAVE_RECOVERY_DB="+path, "QUILLWEAVE_RECOVERY_MODEL="+server.URL, "QUILLWEAVE_RECOVERY_READY="+ready, "QUILLWEAVE_RECOVERY_MODE="+mode)
			var output bytes.Buffer
			child.Stdout, child.Stderr = &output, &output
			if err := child.Start(); err != nil {
				t.Fatal(err)
			}
			waited := false
			defer func() {
				if !waited {
					child.Process.Kill()
					child.Wait()
				}
			}()
			ids := map[string]string{}
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				data, err := os.ReadFile(ready)
				if err == nil && json.Unmarshal(data, &ids) == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if ids["job_id"] == "" {
				t.Fatal("Child not ready: " + output.String())
			}
			observed, err := OpenStore(path)
			if err != nil {
				t.Fatal(err)
			}
			text := ""
			for time.Now().Before(deadline) {
				_ = observed.db.QueryRow("SELECT COALESCE(json_extract(document,'$.text'),'') FROM runs WHERE id=?", ids["job_id"]).Scan(&text)
				if text != "" {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			observed.Close()
			if text == "" {
				t.Fatal("No durable output was observed before the crash")
			}
			if err := child.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = child.Wait()
			waited = true
			if mode == "terminal" {
				store, err := OpenStore(path)
				if err != nil {
					t.Fatal(err)
				}
				_, err = store.db.Exec("DROP TRIGGER recovery_failure")
				store.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			engine, err := NewEngine(path)
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			job := invoke[Job](t, engine, "get_job", map[string]any{"job_id": ids["job_id"]})
			expected := "interrupted"
			if mode == "terminal" {
				expected = "done"
			}
			session := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": ids["project_id"], "session_id": ids["session_id"]})
			if job.Status != expected || len(session.Turns) != 2 || session.Turns[0].Status != expected || session.Turns[1].Status != expected {
				t.Fatalf("Recovered task/turn states disagree: %s %+v", job.Status, session.Turns)
			}
			if mode == "terminal" && job.Text != "Completed result" {
				t.Fatal("The synced terminal intent lost completed prose")
			}
			complete.Store(true)
			next := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": ids["project_id"], "session_id": session.ID, "chapter_id": "", "action": "ask", "instruction": "After restart", "request_id": "after-restart"}))
			if next.Status != "done" || next.SessionID != session.ID {
				t.Fatal("The original recovered conversation is still blocked")
			}
		})
	}
}

// TestRepairedPendingFinishRetriesAfterStorageRecovery avoids regenerating a result after a transient commit failure.
func TestRepairedPendingFinishRetriesAfterStorageRecovery(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	server := staticModel(t, streamPayload("Completed result"), 0)
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, engine, config)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	if _, err := engine.store.db.Exec("CREATE TRIGGER pending_failure BEFORE UPDATE ON runs WHEN json_extract(new.document,'$.status')!='running' BEGIN SELECT RAISE(ABORT,'pending_failure'); END"); err != nil {
		t.Fatal(err)
	}
	job := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "session_id": session.ID, "chapter_id": "", "action": "ask", "instruction": "First", "request_id": "first"}))
	if job.Status != "error" {
		t.Fatal("The terminal failure did not occur")
	}
	if _, err := engine.store.db.Exec("DROP TRIGGER pending_failure"); err != nil {
		t.Fatal(err)
	}
	next := finishJob(t, engine, invoke[Job](t, engine, "start_ai", map[string]any{"project_id": p.ID, "session_id": session.ID, "chapter_id": "", "action": "ask", "instruction": "Second", "request_id": "second"}))
	previous := invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
	if next.Status != "done" || previous.Status != "done" || previous.Text != "Completed result" {
		t.Fatal("The pending terminal transaction was not reconciled")
	}
	history := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	if len(history.Turns) != 4 {
		t.Fatal("Retry duplicated or lost conversation turns")
	}
}

// TestRepairedLivePeerOwnership remains live across another engine's recovery and deletion attempts.
func TestRepairedLivePeerOwnership(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peer.sqlite3")
	first, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	p := testProject()
	insertProject(t, first, p)
	var done atomic.Bool
	server := modelServer(t, func(w http.ResponseWriter, r *http.Request) {
		if done.Load() {
			fmt.Fprint(w, streamPayload("Next result"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	config := nativeConfig("openai")
	config.BaseURL = server.URL
	configureModel(t, first, config)
	session := invoke[Conversation](t, first, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	job := invoke[Job](t, first, "start_ai", map[string]any{"project_id": p.ID, "session_id": session.ID, "chapter_id": "", "action": "ask", "instruction": "Wait"})
	peer, err := NewEngine(path)
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	live := invoke[Job](t, peer, "get_job", map[string]any{"job_id": job.ID})
	if live.Status != "running" {
		t.Fatal("A live peer was falsely recovered")
	}
	reject(t, peer, "delete_project", map[string]any{"project_id": p.ID, "revision": p.Revision})
	other := invoke[Conversation](t, peer, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	_, err = peer.Dispatch("start_ai", rawMessage(map[string]any{"project_id": p.ID, "session_id": other.ID, "chapter_id": "", "action": "ask", "instruction": "Concurrent"}))
	if err == nil || !strings.Contains(err.Error(), "running task") {
		t.Fatal("Two engines acquired the same project task slot")
	}
	invoke[map[string]any](t, first, "cancel_ai", map[string]any{"job_id": job.ID})
	if finishJob(t, first, job).Status != "cancelled" {
		t.Fatal("Owner cancellation failed")
	}
	done.Store(true)
	next := finishJob(t, peer, invoke[Job](t, peer, "start_ai", map[string]any{"project_id": p.ID, "session_id": other.ID, "chapter_id": "", "action": "ask", "instruction": "Next"}))
	if next.Status != "done" {
		t.Fatal(next.Error)
	}
}

// TestRepairedCachedCompletionRecovery reconciles the persisted legacy done-job/running-turn crash window.
func TestRepairedCachedCompletionRecovery(t *testing.T) {
	engine := testEngine(t)
	p := testProject()
	insertProject(t, engine, p)
	session := invoke[Conversation](t, engine, "create_conversation", map[string]any{"project_id": p.ID, "chapter_id": ""})
	job := Job{ID: newID(), ProjectID: p.ID, SessionID: session.ID, Status: "done", Action: "wiki", Text: "Reused compilation", CreatedAt: now(), Prompt: Prompt{Messages: []Message{}, Context: []ContextItem{}}, ownerID: engine.owner.id, BaseRevision: p.Revision}
	if _, err := engine.store.beginDurableJob(AIRequest{ProjectID: p.ID, SessionID: session.ID, RequestID: "cached", Instruction: "Reuse"}, &job); err != nil {
		t.Fatal(err)
	}
	var initial string
	engine.store.db.QueryRow("SELECT json_extract(document,'$.status') FROM runs WHERE id=?", job.ID).Scan(&initial)
	if initial != "running" {
		t.Fatal("Cached completion published a terminal task before its turn")
	}
	if err := engine.store.SaveRun(job.ID, p.ID, storedJob(&job)); err != nil {
		t.Fatal(err)
	}
	view := invoke[Conversation](t, engine, "get_conversation", map[string]any{"project_id": p.ID, "session_id": session.ID})
	if len(view.Turns) != 2 || view.Turns[0].Status != "done" || view.Turns[1].Content != job.Text {
		t.Fatal("A legacy cached completion left its conversation blocked")
	}
}
