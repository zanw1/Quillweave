//go:build quillweave_whitebox

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestExternalMaterialWindows preserves exact large drafts while bounding transport responses.
func TestExternalMaterialWindows(t *testing.T) {
	_, service, project := externalFixture(t)
	view := externalInvoke[externalWritingView](t, service, "writing_begin", WritingBeginInput{RequestID: "large-begin", ChapterID: project.Chapters[0].ID, Instruction: "Continue", ContextChars: 200000})
	var composition WritingComposition
	_ = decodeJSON([]byte(writingCompositionFixture()), &composition)
	view = externalInvoke[externalWritingView](t, service, "writing_submit_composition", WritingCompositionInput{WritingMutation: externalMutation(view, "large-compose"), Composition: composition})
	draft := " \n" + strings.Repeat("账册已经包好。", 12000) + "\n "
	result, err := service.SubmitDraft(context.Background(), WritingDraftInput{WritingMutation: externalMutation(view, "large-draft"), Draft: draft})
	if err != nil {
		t.Fatal(err)
	}
	assert(t, charCount(jsonText(result)) <= 60000, "Large writing response exceeded its transport budget")
	_ = decodeJSON(rawMessage(result), &view)
	var reconstructed strings.Builder
	offset := 0
	for {
		packet := externalInvoke[struct {
			Text      string `json:"text"`
			Next      int    `json:"next_offset"`
			Truncated bool   `json:"truncated"`
			Hash      string `json:"text_hash"`
		}](t, service, "writing_material", WritingMaterialRequest{WritingSessionID: view.ID, Kind: "draft", Offset: offset, MaxChars: 30000})
		assert(t, packet.Hash == hashText(draft), "A material page changed its source hash")
		reconstructed.WriteString(packet.Text)
		if !packet.Truncated {
			break
		}
		offset = packet.Next
	}
	assert(t, reconstructed.String() == draft, "Material windows lost whitespace or Unicode characters")
}

// TestExternalMemoryPagination exposes all applicable memories beyond the old overview limit.
func TestExternalMemoryPagination(t *testing.T) {
	engine, service, project := externalFixture(t)
	for index := 0; index < 180; index++ {
		project.Studio.Memories = append(project.Studio.Memories, StoryMemory{ID: fmt.Sprint("memory-", index), Kind: "fact", Title: fmt.Sprint("Memory ", index), Content: fmt.Sprint("Independent source-supported state ", index), Status: "confirmed", SourceID: project.Chapters[0].ID, SourceHash: hashText(project.Chapters[0].Body), Chapter: 1, Quote: "Lin Wu", Epistemic: "explicit"})
	}
	if err := engine.store.Save(&project, project.Revision); err != nil {
		t.Fatal(err)
	}
	request := ExternalReadRequest{Limit: 50, ThroughChapter: 1}
	total := 0
	for {
		page := externalInvoke[ExternalPage](t, service, "story_memory", request)
		total += len(page.Items)
		if page.NextCursor == "" {
			break
		}
		request.Cursor = page.NextCursor
	}
	assert(t, total >= 180, "Memory pagination silently retained the overview's old 96-record limit")
}

// TestExternalCLIStreamRetainsTransfers validates stateful native operations across multiple input frames.
func TestExternalCLIStreamRetainsTransfers(t *testing.T) {
	engine, _, project := externalFixture(t)
	input, writer := io.Pipe()
	reader, output := io.Pipe()
	defer input.Close()
	defer writer.Close()
	defer reader.Close()
	defer output.Close()
	done := make(chan int, 1)
	go func() {
		done <- RunCLIWithInput([]string{"api", "stream", "--db", engine.store.path, "--project", project.ID}, input, output, io.Discard)
	}()
	encoder, decoder := json.NewEncoder(writer), json.NewDecoder(reader)
	if err := encoder.Encode(map[string]any{"operation": "begin_archive_export", "input": map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	var first struct {
		OK     bool `json:"ok"`
		Result struct {
			ID string `json:"id"`
		} `json:"result"`
	}
	if err := decoder.Decode(&first); err != nil {
		t.Fatal(err)
	}
	assert(t, first.OK && first.Result.ID != "", "The stream failed to reserve an archive transfer")
	if err := encoder.Encode(map[string]any{"operation": "read_archive_chunk", "input": map[string]any{"transfer_id": first.Result.ID, "offset": 0}}); err != nil {
		t.Fatal(err)
	}
	var second CommandEnvelope
	if err := decoder.Decode(&second); err != nil {
		t.Fatal(err)
	}
	assert(t, second.OK, "The next stream frame lost its live archive transfer")
	writer.Close()
	assert(t, <-done == 0, "The stream did not close cleanly")
}
