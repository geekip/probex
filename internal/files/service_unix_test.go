//go:build !windows

package files

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceNoSpaceAndSpecialFiles(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "retained")
	if err := os.WriteFile(dest, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	var u Upload
	serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Path: dest, Size: 3, Overwrite: true}, nil, 201, &u)
	s.mu.Lock()
	active := s.uploads[u.ID]
	s.mu.Unlock()
	full, err := os.OpenFile("/dev/full", os.O_WRONLY, 0)
	if err != nil {
		t.Skip("no /dev/full available")
	}
	active.mu.Lock()
	active.file.Close()
	active.file = full
	active.mu.Unlock()
	serviceRequest(t, s, "PATCH", "/uploads/"+u.ID, "owner", []byte("new"), http.Header{"Upload-Offset": []string{"0"}}, 507, nil).Body.Close()
	serviceRequest(t, s, "DELETE", "/uploads/"+u.ID, "owner", nil, nil, 200, nil).Body.Close()
	if data, err := os.ReadFile(dest); err != nil || string(data) != "old" {
		t.Fatal(string(data), err)
	}
	serviceRequest(t, s, "GET", "/file?path=/dev/null", "owner", nil, nil, 415, nil).Body.Close()
}

func TestServiceDeleteSymlinkPreservesTarget(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	if err := os.Mkdir(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "kept"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	serviceRequest(t, s, "DELETE", "/file?recursive=true&path="+url.QueryEscape(link), "owner", nil, nil, 200, nil).Body.Close()
	if b, err := os.ReadFile(filepath.Join(target, "kept")); err != nil || string(b) != "kept" {
		t.Fatal(string(b), err)
	}
}

func TestTextCommitPreservesOwnerGroupAndMode(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to assign the fixture to a different account")
	}
	s := newService(t)
	path := filepath.Join(t.TempDir(), "config.txt")
	if err := os.WriteFile(path, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	version, err := FileHash(path)
	if err != nil {
		t.Fatal(err)
	}
	request := UploadRequest{Path: path, ID: "edit", Size: 5, Overwrite: true, IfMatch: version, PreserveLinks: true}
	serviceRequest(t, s, "POST", "/uploads", "owner", request, nil, 201, nil).Body.Close()
	serviceRequest(t, s, "PATCH", "/uploads/edit", "owner", []byte("after"), http.Header{"Upload-Offset": {"0"}}, 200, nil).Body.Close()
	serviceRequest(t, s, "POST", "/uploads/edit/commit", "owner", nil, nil, 200, nil).Body.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	entry := entry(path, info, "owner,group")
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "after" || info.Mode().Perm() != 0600 || *entry.UID != 65534 || *entry.GID != 65534 {
		t.Fatalf("file content or access metadata changed incorrectly: %q, %+v, %v", data, info, err)
	}
}
