//go:build quillweave_whitebox

package core

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// modelServer creates a local fixture server with connection cleanup on test exit.
func modelServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(func() { server.CloseClientConnections(); server.Close() })
	return server
}

// streamPayload wraps fixture prose in an SSE response.
func streamPayload(text string) string {
	return "data: " + jsonText(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": text}}}}) + "\n\ndata: [DONE]\n\n"
}

// staticModel provides cancellable latency and closes blocked handlers during cleanup.
func staticModel(t *testing.T, payload string, delay time.Duration) *httptest.Server {
	return modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if delay > 0 {
			_, _ = io.Copy(io.Discard, request.Body)
			select {
			case <-time.After(delay):
			case <-request.Context().Done():
				return
			}
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, payload)
	})
}

// sequenceModel returns stage responses in order and records real request counts.
func sequenceModel(t *testing.T, responses []string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	count := &atomic.Int64{}
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		index := int(count.Add(1)) - 1
		text := "{}"
		if index < len(responses) {
			text = responses[index]
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(writer, streamPayload(text))
	})
	return server, count
}

// finishJob waits for a task terminal state with a finite test deadline.
func finishJob(t *testing.T, engine *Engine, job Job) Job {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		current := invoke[Job](t, engine, "get_job", map[string]any{"job_id": job.ID})
		if current.Status != "running" {
			return current
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("Task did not finish before the test deadline")
	return Job{}
}
