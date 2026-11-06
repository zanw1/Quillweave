//go:build quillweave_whitebox

package core

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"quillweave/internal/platform"
	"runtime"
	"testing"
	"time"
)

// plantSearchIntent writes a terminal sidecar without going through the worker's publish path.
func plantSearchIntent(t *testing.T, engine *Engine, run searchRun, status string) string {
	t.Helper()
	planted := run
	planted.Status = status
	planted.FinishedAt = now()
	path, err := searchIntentPath(engine.owner.dir, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(jsonText(searchIntent{Run: planted, Owner: run.Owner, Hash: run.Hash})), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

// blockSearchRequest starts one search and waits until its provider request is in flight.
func blockSearchRequest(t *testing.T, engine *Engine) (searchRun, func()) {
	t.Helper()
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = readBodyForStudioTest(t, r)
		select {
		case <-started:
		default:
			close(started)
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		fmt.Fprint(w, `{"results":[{"url":"https://example.org/a","snippet":"kept"}]}`)
	}))
	t.Cleanup(server.Close)
	configureSearchFixture(t, engine, "deepseek", server.URL)
	run, err := engine.startSearchRun(searchStart{RequestID: newID(), Purpose: "test", Operation: "search", Query: "history"})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("Search request did not start")
	}
	return run, func() { close(release) }
}

// TestLocalRecoverySkipsExecutingSearch keeps a live provider call from being published by a polled sidecar.
func TestLocalRecoverySkipsExecutingSearch(t *testing.T) {
	engine := testEngine(t)
	run, release := blockSearchRequest(t, engine)
	defer release()
	plantSearchIntent(t, engine, run, "succeeded")
	if err := engine.recoverLocalSearchIntents(); err != nil {
		t.Fatal(err)
	}
	current, err := readSearchRun(engine.store.db, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminalSearch(current.Status) {
		t.Fatalf("recovery published an executing search: %s", current.Status)
	}
}

// unreadableOwner creates a peer runtime directory whose lock cannot be opened.
func unreadableOwner(t *testing.T, engine *Engine) (string, string) {
	t.Helper()
	id := "owner-" + newID()
	dir := filepath.Join(engine.owner.root, id)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	lock := filepath.Join(dir, "owner.lock")
	if err := os.WriteFile(lock, []byte("held"), 0000); err != nil {
		t.Fatal(err)
	}
	return id, lock
}

// TestReconcileSkipsUnreadableIdleOwner ignores a lock that is disappearing after its work has finished.
func TestReconcileSkipsUnreadableIdleOwner(t *testing.T) {
	engine := testEngine(t)
	_, _ = unreadableOwner(t, engine)
	if err := engine.reconcileJobsLocked(); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileKeepsUnreadableOwnerWithLiveSearch still reports a lock error while that owner has unfinished work.
func TestReconcileKeepsUnreadableOwnerWithLiveSearch(t *testing.T) {
	engine := testEngine(t)
	ownerID, lock := unreadableOwner(t, engine)
	_, err := engine.store.db.Exec("INSERT INTO search_runs(id,revision,project_id,request_scope,request_id,payload_hash,profile_id,purpose,operation,status,execution_owner,document,created_at) VALUES(?,0,NULL,'global',?,'hash','profile','test','search','running',?,'{}',?)", newID(), newID(), ownerID, now())
	if err != nil {
		t.Fatal(err)
	}
	if err := engine.reconcileJobsLocked(); err == nil {
		t.Fatal("unreadable owner with a running search was ignored")
	}
	// Drop the fixture before shutdown. Closing must not depend on this denied lock.
	if _, err := engine.store.db.Exec("DELETE FROM search_runs WHERE execution_owner=?", ownerID); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(lock, 0600); err != nil {
		t.Fatal(err)
	}
}

// TestOpenStoreKeepsBusyTimeoutAfterReconnect binds durability pragmas to the next physical connection.
func TestOpenStoreKeepsBusyTimeoutAfterReconnect(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "pragma.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	connection, err := store.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rawErr := connection.Raw(func(any) error { return driver.ErrBadConn })
	_ = connection.Close()
	if rawErr != nil && rawErr != driver.ErrBadConn {
		t.Fatal(rawErr)
	}
	var timeout int
	if err := store.db.QueryRow("PRAGMA busy_timeout").Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", timeout)
	}
}

// activeSearchTask returns the in-flight task for a run that has already started.
func activeSearchTask(t *testing.T, engine *Engine, id string) *searchTask {
	t.Helper()
	engine.searchRuntime.mu.Lock()
	task := engine.searchRuntime.tasks[id]
	engine.searchRuntime.mu.Unlock()
	if task == nil {
		t.Fatal("missing search task")
	}
	return task
}

// TestLocalRecoveryPublishesRetryingSearch publishes a synced sidecar once the worker is waiting on storage.
func TestLocalRecoveryPublishesRetryingSearch(t *testing.T) {
	engine := testEngine(t)
	run, release := blockSearchRequest(t, engine)
	defer release()
	plantSearchIntent(t, engine, run, "succeeded")
	activeSearchTask(t, engine, run.ID).phase.Store(searchPhaseRetrying)
	if err := engine.recoverLocalSearchIntents(); err != nil {
		t.Fatal(err)
	}
	current, err := readSearchRun(engine.store.db, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "succeeded" {
		t.Fatalf("retry recovery left status %s", current.Status)
	}
}

// TestLocalRecoveryRetriesUnreadableIntent leaves a live retry parked when the sidecar cannot be read yet.
func TestLocalRecoveryRetriesUnreadableIntent(t *testing.T) {
	engine := testEngine(t)
	run, release := blockSearchRequest(t, engine)
	defer release()
	path := plantSearchIntent(t, engine, run, "succeeded")
	// Windows Chmod(0000) only sets FILE_ATTRIBUTE_READONLY and still permits os.Open;
	// TryFileLock holds a mandatory LockFileEx byte-range lock on Windows so ReadFile returns ERROR_LOCK_VIOLATION.
	lock, acquired, err := platform.TryFileLock(path)
	if err != nil || !acquired {
		t.Fatalf("lock intent: %v", err)
	}
	defer func() {
		if err := lock.Close(); err != nil {
			t.Error(err)
		}
	}()
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0000); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(path, 0600)
	}
	activeSearchTask(t, engine, run.ID).phase.Store(searchPhaseRetrying)
	if err := engine.recoverLocalSearchIntents(); err != nil {
		t.Fatal(err)
	}
	current, err := readSearchRun(engine.store.db, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if terminalSearch(current.Status) {
		t.Fatalf("unreadable intent changed status to %s", current.Status)
	}
}
