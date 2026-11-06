//go:build quillweave_whitebox

package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestLegacyImportPreservesOriginals checks legacy text, versions, and archives while retaining original file bytes.
func TestLegacyImportPreservesOriginals(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"chapters", ".versions", "\u89d2\u8272\u5e93/\u4e3b\u8981\u89d2\u8272"} {
		if err := os.MkdirAll(filepath.Join(root, directory), 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{"chapters/chapter_10.txt": "\u7b2c10\u7ae0 \u56de\u5f52\n\u539f\u59cb\u6b63\u6587", "chapters/chapter_2.txt": "\u7b2c\u4e8c\u7ae0\u5185\u5bb9", "Novel_architecture.txt": "\u65e7\u8bbe\u5b9a", "foreshadowing.json": `{"foreshadowings":[]}`, ".versions/chapter_10_v1.json": `{"metadata":{"created_at":"2025-01-01"},"content":"\u5386\u53f2\u6b63\u6587"}`, "\u89d2\u8272\u5e93/\u4e3b\u8981\u89d2\u8272/\u6797\u96fe.txt": "\u89d2\u8272\u8d44\u6599"}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	project, err := importLegacy(root)
	if err != nil {
		t.Fatal(err)
	}
	assert(t, len(project.Chapters) == 2 && project.Chapters[0].Number == 2 && project.Chapters[1].Number == 10 && project.Chapters[1].Versions[0].Body == "\u5386\u53f2\u6b63\u6587", "\u65e7\u7ae0\u8282\u6216\u7248\u672c\u672a\u4fdd\u7559")
	assert(t, len(project.Facts) == 0 && len(project.Sources) == 3, "\u65e7\u673a\u5668\u7ed3\u8bba\u88ab\u9519\u8bef\u63d0\u5347\u4e3a\u4e8b\u5b9e")
	for name, text := range files {
		after, err := os.ReadFile(filepath.Join(root, name))
		assert(t, err == nil && string(after) == text, "\u5bfc\u5165\u6539\u53d8\u4e86\u539f\u6587\u4ef6")
	}
}

// TestBackupRoundtripAndInvalidArchive restores a separate project and rejects malformed imports atomically.
func TestBackupRoundtripAndInvalidArchive(t *testing.T) {
	engine := testEngine(t)
	project := ExampleProject()
	insertProject(t, engine, project)
	exported := invoke[map[string]string](t, engine, "export", map[string]any{"project_id": project.ID, "format": "archive"})
	imported := invoke[Project](t, engine, "import_archive", map[string]any{"content": exported["content"]})
	assert(t, imported.ID != project.ID && reflect.DeepEqual(imported.Chapters, project.Chapters) && reflect.DeepEqual(imported.Facts, project.Facts), "Backup round-trip changed content")
	reject(t, engine, "import_archive", map[string]any{"content": "{}"})
	projects, err := engine.store.List()
	assert(t, err == nil && len(projects) == 2, "An invalid backup created a project")
}

// TestOldProjectSchemaLoadsWithStoryDefaults checks defaults for historical projects missing newer fields.
func TestOldProjectSchemaLoadsWithStoryDefaults(t *testing.T) {
	var value map[string]json.RawMessage
	_ = decodeJSON(rawMessage(testProject()), &value)
	delete(value, "wiki_purpose")
	delete(value, "compilations")
	var project Project
	err := decodeJSON(rawMessage(value), &project)
	assert(t, err == nil && project.WikiPurpose != "" && len(project.Compilations) == 0 && len(project.Chapters) == 1, "Legacy format compatibility failed")
}
