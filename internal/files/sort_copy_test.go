package files

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"testing"
	"time"
)

func TestSortDirectoryLinksBeforePagination(t *testing.T) {
	s := newService(t)
	dir, targetDir := t.TempDir(), t.TempDir()
	targetFile := filepath.Join(t.TempDir(), "target.txt")
	if err := os.WriteFile(targetFile, []byte("target"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a-directory", "z-directory"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"d.txt", "x.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range map[string]string{"m-directory-link": targetDir, "c-file-link": targetFile, "b-broken": filepath.Join(dir, "missing")} {
		if err := os.Symlink(target, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	folders := []string{"a-directory", "m-directory-link", "z-directory"}
	if runtime.GOOS == "windows" {
		if err := writeShortcut(filepath.Join(dir, "n-directory.lnk"), targetDir); err != nil {
			t.Fatal(err)
		}
		folders = []string{"a-directory", "m-directory-link", "n-directory.lnk", "z-directory"}
	}
	for _, field := range []string{"name", "mtime", "type", "size", "mode", "owner"} {
		for _, order := range []string{"asc", "desc"} {
			t.Run(field+"-"+order, func(t *testing.T) {
				var names []string
				cursor := ""
				filesStarted := false
				for {
					var page Listing
					params := url.Values{"path": {dir}, "sort": {field}, "order": {order}, "limit": {"2"}, "cursor": {cursor}}
					serviceRequest(t, s, "GET", "/files?"+params.Encode(), "owner", nil, nil, 200, &page).Body.Close()
					if page.Directories != len(folders) || page.Files != 4 {
						t.Fatal(page)
					}
					for _, e := range page.Entries {
						names = append(names, e.Name)
						if entryDirectory(e) && filesStarted {
							t.Fatal("directory appeared after a file", names)
						}
						if !entryDirectory(e) {
							filesStarted = true
						}
						if e.Name == "m-directory-link" && (e.Type != "symlink" || e.LinkTargetType != "directory") {
							t.Fatal("lost alias identity or target metadata", e)
						}
						if e.Name == "n-directory.lnk" && (e.Type != "file" || e.LinkKind != "shortcut" || e.LinkTargetType != "directory") {
							t.Fatal(e)
						}
						if e.Name == "b-broken" && (e.LinkError == "" || e.LinkTargetType != "") {
							t.Fatal("broken link disappeared or lost error", e)
						}
						if e.Size != nil || e.Modified != nil || e.Owner != nil {
							t.Fatal("sort metadata leaked", e)
						}
					}
					cursor = page.Next
					if cursor == "" {
						break
					}
				}
				if len(names) != len(folders)+4 {
					t.Fatal(names)
				}
				if field == "name" || field == "type" {
					wantFolders := slices.Clone(folders)
					wantFiles := []string{"b-broken", "c-file-link", "d.txt", "x.go"}
					if field == "type" {
						wantFiles = []string{"x.go", "d.txt", "b-broken", "c-file-link"}
					}
					if order == "desc" {
						slices.Reverse(wantFolders)
						slices.Reverse(wantFiles)
					}
					if want := append(wantFolders, wantFiles...); !slices.Equal(names, want) {
						t.Fatal(names, want)
					}
				}
			})
		}
	}
}

func TestSortCompleteDirectoryBeforePagination(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	names := []string{"Z.txt", "a.go", "b.txt", "c"}
	for i, name := range names {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, make([]byte, 4-i), 0600); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(int64(1700000000+i), 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"z-dir", "a-dir"} {
		path := filepath.Join(dir, name)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		stamp := time.Unix(1700000010, 0)
		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		sort, order string
		want        []string
	}{
		{"name", "asc", []string{"a-dir", "z-dir", "a.go", "b.txt", "c", "Z.txt"}},
		{"name", "desc", []string{"z-dir", "a-dir", "Z.txt", "c", "b.txt", "a.go"}},
		{"size", "asc", []string{"a-dir", "z-dir", "c", "b.txt", "a.go", "Z.txt"}},
		{"mtime", "asc", []string{"a-dir", "z-dir", "Z.txt", "a.go", "b.txt", "c"}},
		{"type", "asc", []string{"a-dir", "z-dir", "c", "a.go", "b.txt", "Z.txt"}},
	} {
		t.Run(test.sort+test.order, func(t *testing.T) {
			cursor := ""
			var got []string
			for {
				params := url.Values{"path": {dir}, "limit": {"2"}, "sort": {test.sort}, "order": {test.order}, "cursor": {cursor}}
				var page Listing
				serviceRequest(t, s, "GET", "/files?"+params.Encode(), "owner", nil, nil, 200, &page)
				if page.Files != 4 || page.Directories != 2 {
					t.Fatal(page)
				}
				for _, e := range page.Entries {
					got = append(got, e.Name)
					if e.Size != nil || e.Modified != nil || e.Owner != nil {
						t.Fatal("sorting leaked unrequested metadata", e)
					}
				}
				cursor = page.Next
				if cursor == "" {
					break
				}
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got %v want %v", got, test.want)
			}
		})
	}
	for _, field := range []string{"mode", "owner"} {
		var page Listing
		serviceRequest(t, s, "GET", "/files?"+url.Values{"path": {dir}, "sort": {field}, "fields": {"mode,owner,group"}}.Encode(), "owner", nil, nil, 200, &page)
		if len(page.Entries) != 6 {
			t.Fatal(page)
		}
	}
	serviceRequest(t, s, "GET", "/files?"+url.Values{"path": {dir}, "sort": {"unknown"}}.Encode(), "owner", nil, nil, 400, nil).Body.Close()
	serviceRequest(t, s, "GET", "/files?"+url.Values{"path": {dir}, "cursor": {"removed"}}.Encode(), "owner", nil, nil, 409, nil).Body.Close()
}
func TestCopyRecursiveConflictAndCancellation(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	if err := os.MkdirAll(filepath.Join(source, "nested", "empty"), 0750); err != nil {
		t.Fatal(err)
	}
	data := []byte("copied contents\n")
	if err := os.WriteFile(filepath.Join(source, "nested", "file.txt"), data, 0640); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(dir, "destination")
	body := map[string]string{"path": source, "destination": destination}
	serviceRequest(t, s, "POST", "/files/copy", "owner", body, nil, 201, nil).Body.Close()
	got, err := os.ReadFile(filepath.Join(destination, "nested", "file.txt"))
	if err != nil || string(got) != string(data) {
		t.Fatal(string(got), err)
	}
	if info, err := os.Stat(filepath.Join(destination, "nested", "empty")); err != nil || !info.IsDir() {
		t.Fatal(info, err)
	}
	serviceRequest(t, s, "POST", "/files/copy", "owner", body, nil, 409, nil).Body.Close()
	serviceRequest(t, s, "POST", "/files/copy", "owner", map[string]string{"path": source, "destination": filepath.Join(source, "nested", "self")}, nil, 400, nil).Body.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	target := filepath.Join(dir, "cancelled")
	if err := copyEntry(ctx, source, target); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, ".probex-copy-*"))
	if err != nil || len(leftovers) > 0 {
		t.Fatal(leftovers, err)
	}
	if err := os.WriteFile(filepath.Join(source, "nested", "file.txt"), []byte("changed"), 0640); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(filepath.Join(destination, "nested", "file.txt"))
	if err != nil || string(got) != string(data) {
		t.Fatal(string(got), err)
	}
}
func TestSortPagesOverTwoHundred(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	for i := 0; i < 245; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file-%03d", i)), make([]byte, 245-i), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var first, second Listing
	serviceRequest(t, s, "GET", "/files?"+url.Values{"path": {dir}, "sort": {"size"}, "fields": {"size"}}.Encode(), "owner", nil, nil, 200, &first)
	serviceRequest(t, s, "GET", "/files?"+url.Values{"path": {dir}, "sort": {"size"}, "fields": {"size"}, "cursor": {first.Next}}.Encode(), "owner", nil, nil, 200, &second)
	entries := append(first.Entries, second.Entries...)
	if len(entries) != 245 || entries[0].Name != "file-244" || entries[244].Name != "file-000" || !slices.IsSortedFunc(entries, func(a, b Entry) int { return int(*a.Size - *b.Size) }) {
		t.Fatal(len(entries), entries[0], entries[len(entries)-1])
	}
}
