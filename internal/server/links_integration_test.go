package server_test

import (
	"net/url"
	"os"
	"path/filepath"
	"probex/internal/files"
	"testing"
)

func TestFileLinksAgentTextTargetsAndSharedSave(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node, owner := f.node(), openFileOwner(t, f)
	base := "/api/nodes/" + node.ID
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, soft, hard := filepath.Join(dir, "目标.txt"), filepath.Join(dir, "soft"), filepath.Join(dir, "hard.txt")
	if err := os.WriteFile(target, []byte("original\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		path string
		hard bool
	}{{soft, false}, {hard, true}} {
		jsonFileRequest(t, f, "POST", base+"/links", owner.id, map[string]any{"path": c.path, "target": target, "hard": c.hard}, 201, nil)
	}
	var entry files.Entry
	jsonFileRequest(t, f, "GET", base+"/resolve?path="+url.QueryEscape(soft), owner.id, nil, 200, &entry)
	if entry.Path != target || entry.Type != "file" {
		t.Fatal(entry)
	}
	var document textDocument
	jsonFileRequest(t, f, "GET", textURL(node.ID, soft), owner.id, nil, 200, &document)
	if document.Path != target || document.Text != "original\n" {
		t.Fatal(document)
	}
	original, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	jsonFileRequest(t, f, "PUT", textURL(node.ID, soft), owner.id, textSave("edited\n", "utf-8", document.Version, false, false), 200, &document)
	for _, path := range []string{target, hard, soft} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "edited\n" {
			t.Fatal(path, string(data), err)
		}
		info, err := os.Stat(path)
		if err != nil || !os.SameFile(original, info) {
			t.Fatal("shared identity lost", err)
		}
	}
	if _, err := os.Readlink(soft); err != nil {
		t.Fatal("save replaced symlink", err)
	}
	jsonFileRequest(t, f, "GET", textURL(node.ID, hard), owner.id, nil, 200, &document)
	if err := os.WriteFile(target, []byte("external edit\n"), 0600); err != nil {
		t.Fatal(err)
	}
	jsonFileRequest(t, f, "PUT", textURL(node.ID, hard), owner.id, textSave("stale", "utf-8", document.Version, false, false), 409, nil)
	if data, err := os.ReadFile(target); err != nil || string(data) != "external edit\n" {
		t.Fatal(string(data), err)
	}
	// A client can edit through the alias API while the returned path identifies
	// the real document. Removing the alias must leave both hard names intact.
	jsonFileRequest(t, f, "DELETE", base+"/file?path="+url.QueryEscape(soft), owner.id, nil, 200, nil)
	if _, err := os.Stat(target); err != nil {
		t.Fatal(err)
	}
	jsonFileRequest(t, f, "GET", base+"/resolve?path="+url.QueryEscape(dir), owner.id, nil, 200, &entry)
	if entry.Type != "directory" {
		t.Fatal(entry)
	}
}
