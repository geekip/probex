package files

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestServiceWindowsSystemDriveListing(t *testing.T) {
	s := newService(t)
	root := os.Getenv("SystemDrive") + `\`
	for _, hidden := range []string{"false", "true"} {
		var list Listing
		serviceRequest(t, s, "GET", "/files?path="+url.QueryEscape(root)+"&hidden="+hidden+"&fields=size,mtime", "owner", nil, nil, 200, &list)
		if !strings.EqualFold(list.Path, root) || len(list.Entries) == 0 {
			t.Fatalf("system drive listing: %+v", list)
		}
	}
}

func TestServiceWindowsCachedHiddenAttributes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hidden.txt")
	if err := os.WriteFile(path, []byte("cached"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatal(entries, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	isHidden, err := hidden(entries[0])
	if err != nil || !isHidden {
		t.Fatalf("enumeration attributes were not used: hidden=%v err=%v", isHidden, err)
	}
}

func TestServiceWindowsDrivesPathsAndMetadata(t *testing.T) {
	s := newService(t)
	var list Listing
	serviceRequest(t, s, "GET", "/files", "owner", nil, nil, 200, &list)
	if len(list.Entries) == 0 {
		t.Fatal("no drives")
	}
	for _, e := range list.Entries {
		if !filepath.IsAbs(e.Path) || e.Type != "directory" {
			t.Fatal(e)
		}
	}
	dir := filepath.Join(t.TempDir(), "中文 folder")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	hidden := filepath.Join(dir, "hidden.txt")
	if err := os.WriteFile(hidden, []byte("hidden"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := windows.UTF16PtrFromString(hidden)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetFileAttributes(p, windows.FILE_ATTRIBUTE_HIDDEN); err != nil {
		t.Fatal(err)
	}
	serviceRequest(t, s, "GET", "/files?path="+url.QueryEscape(dir)+"&fields=mode,owner,group,size", "owner", nil, nil, 200, &list)
	if list.Files != 0 || list.Directories != 1 {
		t.Fatal(list)
	}
	serviceRequest(t, s, "GET", "/files?path="+url.QueryEscape(dir)+"&fields=mode,owner,group,size&hidden=true", "owner", nil, nil, 200, &list)
	if list.Files != 1 {
		t.Fatal(list)
	}
	for _, e := range list.Entries {
		if e.UID != nil || e.Owner != nil || e.Mode != nil {
			t.Fatal("invented POSIX metadata", e)
		}
	}
	var u Upload
	serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Directory: dir, Relative: "sub\\file.txt", Size: 3}, nil, 201, &u)
	endpoint := "/uploads/" + u.ID
	serviceRequest(t, s, "PATCH", endpoint, "owner", []byte("win"), http.Header{"Upload-Offset": []string{"0"}}, 200, &u)
	serviceRequest(t, s, "POST", endpoint+"/commit", "owner", nil, nil, 200, &u)
	response := serviceRequest(t, s, "GET", "/file?path="+url.QueryEscape(u.Path), "owner", nil, nil, 200, nil)
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(data) != "win" || !strings.HasPrefix(u.Path, filepath.VolumeName(dir)) {
		t.Fatal(u, string(data), err)
	}
	for _, relative := range []string{"..\\outside", "C:\\outside", "C:relative"} {
		serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Directory: dir, Relative: relative, Size: 1}, nil, 400, nil).Body.Close()
	}
}
