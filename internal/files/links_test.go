package files

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceLinksResolveAndEdit(t *testing.T) {
	s := newService(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "真实 file.txt")
	other := filepath.Join(dir, "other.txt")
	for _, path := range []string{target, other} {
		if err := os.WriteFile(path, []byte("original long content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "file-link")
	serviceRequest(t, s, "POST", "/links", "owner", map[string]any{"path": link, "target": filepath.Base(target)}, nil, 201, nil).Body.Close()
	var l Link
	serviceRequest(t, s, "GET", "/links?path="+url.QueryEscape(link), "owner", nil, nil, 200, &l).Body.Close()
	if l.Kind != "symlink" || l.Target != filepath.Base(target) || l.Hard {
		t.Fatal(l)
	}
	var resolved Entry
	serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(link), "owner", nil, nil, 200, &resolved).Body.Close()
	if resolved.Path != target || resolved.Type != "file" {
		t.Fatal(resolved)
	}
	serviceRequest(t, s, "POST", "/links", "owner", map[string]any{"path": link, "target": other}, nil, 409, nil).Body.Close()
	renamed := filepath.Join(dir, "renamed-link")
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": link, "destination": renamed, "target": other}, nil, 200, nil).Body.Close()
	if _, err := os.Lstat(link); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(renamed), "owner", nil, nil, 200, &resolved).Body.Close()
	if resolved.Path != other {
		t.Fatal(resolved)
	}
	// A conflicting rename or invalid target must leave the old link intact.
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": renamed, "destination": target, "target": target}, nil, 409, nil).Body.Close()
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": renamed, "target": ""}, nil, 400, nil).Body.Close()
	if got, err := os.Readlink(renamed); err != nil || got != other {
		t.Fatal(got, err)
	}
	for _, c := range []struct {
		name, target string
		status       int
	}{
		{"directory-link", dir, 200}, {"broken-link", filepath.Join(dir, "absent"), 404},
	} {
		path := filepath.Join(dir, c.name)
		serviceRequest(t, s, "POST", "/links", "owner", map[string]any{"path": path, "target": c.target}, nil, 201, nil).Body.Close()
		serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(path), "owner", nil, nil, c.status, &resolved).Body.Close()
		if c.status == 200 && (resolved.Type != "directory" || resolved.Path != dir) {
			t.Fatal(resolved)
		}
	}
	hard := filepath.Join(dir, "hard.txt")
	// Changing between file and directory symlink targets must retain the alias.
	directoryLink := filepath.Join(dir, "directory-link")
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": directoryLink, "target": target}, nil, 200, nil).Body.Close()
	serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(directoryLink), "owner", nil, nil, 200, &resolved).Body.Close()
	if resolved.Type != "file" {
		t.Fatal(resolved)
	}
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": directoryLink, "target": dir}, nil, 200, nil).Body.Close()
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": renamed, "target": target, "hard": true}, nil, 200, nil).Body.Close()
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": renamed, "target": other}, nil, 200, nil).Body.Close()
	serviceRequest(t, s, "POST", "/links", "owner", map[string]any{"path": hard, "target": target, "hard": true}, nil, 201, nil).Body.Close()
	serviceRequest(t, s, "GET", "/links?path="+url.QueryEscape(hard), "owner", nil, nil, 200, &l).Body.Close()
	if !l.Hard || l.Kind != "hard" || l.Target != "" {
		t.Fatal(l)
	}
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": hard, "target": "", "hard": true}, nil, 200, nil).Body.Close()
	original, _ := os.Stat(target)
	current, _ := os.Stat(hard)
	if !os.SameFile(original, current) {
		t.Fatal("hard link detached")
	}
	version, err := FileHash(hard)
	if err != nil {
		t.Fatal(err)
	}
	var u Upload
	serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Path: hard, Size: 3, Overwrite: true, IfMatch: version, PreserveLinks: true}, nil, 201, &u).Body.Close()
	serviceRequest(t, s, "PATCH", "/uploads/"+u.ID, "owner", []byte("new"), http.Header{"Upload-Offset": {"0"}}, 200, nil).Body.Close()
	serviceRequest(t, s, "POST", "/uploads/"+u.ID+"/commit", "owner", nil, nil, 200, nil).Body.Close()
	for _, path := range []string{target, hard} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "new" {
			t.Fatal(path, string(data), err)
		}
		info, err := os.Stat(path)
		if err != nil || !os.SameFile(original, info) {
			t.Fatal("file identity changed", err)
		}
	}
	hardRenamed := filepath.Join(dir, "hard-renamed.txt")
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": hard, "destination": hardRenamed, "hard": true}, nil, 200, nil).Body.Close()
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": hardRenamed, "target": other, "hard": true}, nil, 200, nil).Body.Close()
	current, _ = os.Stat(hardRenamed)
	otherInfo, _ := os.Stat(other)
	if !os.SameFile(current, otherInfo) || os.SameFile(original, current) {
		t.Fatal("hard link retarget failed")
	}
	// Switching a link type changes only the alias, never the former target.
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": hardRenamed, "target": filepath.Base(target)}, nil, 200, nil).Body.Close()
	if got, err := os.Readlink(hardRenamed); err != nil || got != filepath.Base(target) {
		t.Fatal(got, err)
	}
	serviceRequest(t, s, "POST", "/links", "owner", map[string]any{"path": filepath.Join(dir, "bad-hard"), "target": dir, "hard": true}, nil, 400, nil).Body.Close()
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": target, "target": other}, nil, 400, nil).Body.Close()
	serviceRequest(t, s, "DELETE", "/file?path="+url.QueryEscape(renamed), "owner", nil, nil, 200, nil).Body.Close()
	if _, err := os.Stat(other); err != nil {
		t.Fatal("deleting link deleted target", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if len(entry.Name()) > 13 && entry.Name()[:13] == ".probex-link-" {
			t.Fatal("temporary link retained", entry.Name())
		}
	}
}

func TestServiceLinkCycle(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	if err := os.Symlink(b, a); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(a, b); err != nil {
		t.Fatal(err)
	}
	response := serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(a), "owner", nil, nil, 500, nil)
	defer response.Body.Close()
	message, err := io.ReadAll(response.Body)
	if err != nil || len(message) == 0 {
		t.Fatal(string(message), err)
	}
}
