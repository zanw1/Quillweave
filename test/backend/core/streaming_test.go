//go:build quillweave_whitebox

package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// TestProviderURLsPreservePaths retains custom version paths and rejects unsafe endpoint credentials or schemes.
func TestProviderURLsPreservePaths(t *testing.T) {
	config := defaultProvider()
	config.Model = "test"
	config.BaseURL = "https://example.org/custom/v4/"
	endpoint, err := validateConfig(config)
	assert(t, err == nil && endpoint == "https://example.org/custom/v4/chat/completions", "The path was rewritten")
	config.Protocol = "ollama"
	config.BaseURL = "http://localhost:11434/api"
	endpoint, err = validateConfig(config)
	assert(t, err == nil && endpoint == "http://localhost:11434/api/chat", "Incorrect Ollama endpoint")
	for _, address := range []string{"https://secret@example.org", "http://untrusted.example.org", "https://example.org/?key=x", "https://example.org/#token"} {
		config.BaseURL = address
		_, err := validateConfig(config)
		assert(t, err != nil, "An unsafe endpoint was accepted")
	}
}

// TestStreamProtocolsAndTruncation checks Unicode protocol parsing, completion markers, truncation, and service errors.
func TestStreamProtocolsAndTruncation(t *testing.T) {
	text, _, err := parseLine("data: {\"choices\":[{\"delta\":{\"content\":\"\u4e2d\u6587\"}}]}\r", "openai")
	assert(t, err == nil && text == "\u4e2d\u6587", "SSE Unicode text parsing failed")
	_, done, err := parseLine("data: [DONE]", "openai")
	assert(t, err == nil && done, "Completion marker was not recognized")
	text, done, err = parseLine(`{"message":{"content":"Text"},"done":true}`, "ollama")
	assert(t, err == nil && done && text == "Text", "NDJSON parsing failed")
	_, _, err = parseLine(`data: {"choices":[{"finish_reason":"length"}]}`, "openai")
	assert(t, err != nil, "Truncation was treated as successful completion")
	_, _, err = parseLine(`data: {"error":"private service detail"}`, "openai")
	assert(t, err != nil && !strings.Contains(err.Error(), "private"), "Private provider response content was exposed")
}

// TestStreamingByteBoundariesAndRedirects checks Unicode byte boundaries and credential-safe redirect rejection.
func TestStreamingByteBoundariesAndRedirects(t *testing.T) {
	server := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/event-stream")
		for _, value := range []byte(streamPayload("\u4e2d\u6587🙂\u8de8\u5757")) {
			_, _ = writer.Write([]byte{value})
			writer.(http.Flusher).Flush()
		}
	})
	config := defaultProvider()
	config.Model = "mock"
	config.BaseURL = server.URL
	output, err := streamModel(context.Background(), config, []Message{{Role: "user", Content: "Test"}}, func(string) {})
	assert(t, err == nil && output == "\u4e2d\u6587🙂\u8de8\u5757", "UTF-8 network boundary handling failed")
	var leaked atomic.Bool
	target := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		leaked.Store(true)
		fmt.Fprint(writer, streamPayload("Unexpected request"))
	})
	redirect := modelServer(t, func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL, http.StatusFound)
	})
	config.BaseURL = redirect.URL
	config.APIKey = "synthetic-key"
	_, err = streamModel(context.Background(), config, []Message{}, func(string) {})
	assert(t, err != nil && strings.Contains(err.Error(), "302") && !leaked.Load(), "A redirect exposed the model request")
}
