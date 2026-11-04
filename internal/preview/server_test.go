package preview

import (
	"bytes"
	"net/http/httptest"
	"path/filepath"
	"quillweave/internal/core"
	"testing"
	"testing/fstest"
)

// TestCommandBoundary checks the loopback command and JSON transport boundaries.
func TestCommandBoundary(t *testing.T) {
	engine, err := core.NewEngine(filepath.Join(t.TempDir(), "preview.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	handler := Handler(engine, fstest.MapFS{"index.html": {Data: []byte("<html>Quillweave</html>")}}, 1421)
	cases := []struct {
		name, method, host, origin, marker, contentType, body string
		status                                                int
	}{
		{"Valid command", "POST", "127.0.0.1:1421", "http://127.0.0.1:1421", "1", "application/json", `{"command":"list_projects","args":{}}`, 200},
		{"Cross-site origin", "POST", "127.0.0.1:1421", "https://untrusted.example", "1", "application/json", `{"command":"list_projects"}`, 403},
		{"Host rebinding", "POST", "untrusted.example:1421", "http://127.0.0.1:1421", "1", "application/json", `{"command":"list_projects"}`, 403},
		{"Missing client marker", "POST", "127.0.0.1:1421", "http://127.0.0.1:1421", "", "application/json", `{"command":"list_projects"}`, 403},
		{"Incorrect method", "GET", "127.0.0.1:1421", "http://127.0.0.1:1421", "1", "application/json", "", 405},
		{"Incorrect media type", "POST", "127.0.0.1:1421", "http://127.0.0.1:1421", "1", "text/plain", `{"command":"list_projects"}`, 415},
		{"Invalid JSON", "POST", "127.0.0.1:1421", "http://127.0.0.1:1421", "1", "application/json", "{", 400},
		{"Trailing JSON", "POST", "127.0.0.1:1421", "http://127.0.0.1:1421", "1", "application/json", `{"command":"create_example"} {}`, 400},
		{"Missing command", "POST", "127.0.0.1:1421", "http://127.0.0.1:1421", "1", "application/json", `{}`, 422},
		{"Unknown command", "POST", "127.0.0.1:1421", "http://127.0.0.1:1421", "1", "application/json", `{"command":"unknown"}`, 400},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, "http://127.0.0.1:1421/api/command", bytes.NewBufferString(test.body))
			request.Host = test.host
			request.Header.Set("Origin", test.origin)
			request.Header.Set("X-Quillweave-Client", test.marker)
			request.Header.Set("Content-Type", test.contentType)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("HTTP %d, expected %d", response.Code, test.status)
			}
		})
	}
	value, err := engine.Dispatch("list_projects", nil)
	if err != nil {
		t.Fatal(err)
	}
	if projects, ok := value.([]map[string]any); !ok || len(projects) != 0 {
		t.Fatal("A rejected request changed the database")
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1:1421/unknown-route", nil))
	if response.Code != 200 || !bytes.Contains(response.Body.Bytes(), []byte("Quillweave")) {
		t.Fatal("Frontend fallback route failed")
	}
}
