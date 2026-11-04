package preview

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"quillweave/internal/core"
	"strings"
)

// Handler serves domain commands behind loopback Host, Origin, and client-marker checks.
func Handler(engine *core.Engine, assets fs.FS, port int) http.Handler {
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/command" {
			path := strings.TrimPrefix(request.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if _, err := fs.Stat(assets, path); err != nil {
				copy := request.Clone(request.Context())
				copy.URL.Path = "/"
				files.ServeHTTP(writer, copy)
			} else {
				files.ServeHTTP(writer, request)
			}
			return
		}
		if request.Method != http.MethodPost {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		allowedHosts := []string{fmt.Sprintf("127.0.0.1:%d", port), fmt.Sprintf("localhost:%d", port), "127.0.0.1:1420", "localhost:1420"}
		hostOK, originOK := false, false
		for _, host := range allowedHosts {
			hostOK = hostOK || request.Host == host
			originOK = originOK || request.Header.Get("Origin") == "http://"+host
		}
		if !hostOK || !originOK || request.Header.Get("X-Quillweave-Client") != "1" {
			respond(writer, http.StatusForbidden, map[string]any{"ok": false, "error": "Only the local Quillweave interface can access this endpoint"})
			return
		}
		media, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
		if media != "application/json" && !(strings.HasPrefix(media, "application/") && strings.HasSuffix(media, "+json")) {
			writer.WriteHeader(http.StatusUnsupportedMediaType)
			return
		}
		request.Body = http.MaxBytesReader(writer, request.Body, 45_000_000)
		var payload struct {
			Command *string         `json:"command"`
			Args    json.RawMessage `json:"args"`
		}
		decoder := json.NewDecoder(request.Body)
		if err := decoder.Decode(&payload); err != nil {
			code := http.StatusBadRequest
			var typeError *json.UnmarshalTypeError
			if errors.As(err, &typeError) {
				code = http.StatusUnprocessableEntity
			}
			if _, ok := err.(*http.MaxBytesError); ok {
				code = http.StatusRequestEntityTooLarge
			}
			respond(writer, code, map[string]any{"ok": false, "error": "The command request is not valid JSON"})
			return
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			respond(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": "The command request contains trailing data"})
			return
		}

		if payload.Command == nil {
			respond(writer, http.StatusUnprocessableEntity, map[string]any{"ok": false, "error": "Missing command name"})
			return
		}
		value, err := engine.Dispatch(*payload.Command, payload.Args)
		if err != nil {
			respond(writer, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		respond(writer, http.StatusOK, map[string]any{"ok": true, "result": value})
	})
}

// respond returns JSON while preserving source HTML characters.
func respond(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	encoder := json.NewEncoder(writer)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}
