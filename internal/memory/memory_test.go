package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStoreWriteReadSearchAndPersistToDisk(t *testing.T) {
	root := t.TempDir()
	store := NewStore(root)
	userID := int64(99)

	if err := store.WriteFile(userID, "preferences.md", "喜欢咖啡\n常用语言 Go"); err != nil {
		t.Fatalf("write file failed: %v", err)
	}
	if err := store.SaveSessionTranscript(userID, "active", "assistant", "记录内容"); err != nil {
		t.Fatalf("save transcript failed: %v", err)
	}

	content, err := store.ReadFile(userID, "preferences.md")
	if err != nil {
		t.Fatalf("read file failed: %v", err)
	}
	if !strings.Contains(content, "喜欢咖啡") {
		t.Fatalf("unexpected file content: %q", content)
	}

	results, err := store.Search(userID, "咖啡 Go")
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(results) == 0 || results[0].File != "preferences.md" {
		t.Fatalf("unexpected search results: %#v", results)
	}

	files, err := store.ListFiles(userID)
	if err != nil {
		t.Fatalf("list files failed: %v", err)
	}
	if len(files) != 1 || files[0] != "preferences.md" {
		t.Fatalf("unexpected memory files: %#v", files)
	}

	if _, err := os.Stat(filepath.Join(root, "99", "preferences.md")); err != nil {
		t.Fatalf("expected markdown file on disk: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "99", "sessions", "active.jsonl")); err != nil {
		t.Fatalf("expected transcript file on disk: %v", err)
	}
}
