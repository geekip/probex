package files

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestServiceInitialUserDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "用户 home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	variable := "HOME"
	if runtime.GOOS == "windows" {
		variable = "USERPROFILE"
	}
	t.Setenv(variable, home)
	s := newService(t)
	var list Listing
	serviceRequest(t, s, "GET", "/files?initial=true&hidden=true", "owner", nil, nil, 200, &list)
	if list.Path != home {
		t.Fatalf("initial directory=%q, want %q", list.Path, home)
	}
	explicit := t.TempDir()
	serviceRequest(t, s, "GET", "/files?initial=true&path="+url.QueryEscape(explicit), "owner", nil, nil, 200, &list)
	if list.Path != explicit {
		t.Fatalf("explicit directory=%q, want %q", list.Path, explicit)
	}
}

func serviceRequest(t *testing.T, s *Service, method, path, owner string, body any, headers http.Header, want int, result any) *http.Response {
	t.Helper()
	var reader io.Reader
	switch value := body.(type) {
	case []byte:
		reader = bytes.NewReader(value)
	case nil:
	default:
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, "http://"+s.Address+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	req.Header.Set("Authorization", "Bearer "+s.Key)
	req.Header.Set("X-Probex-File-Owner", owner)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		b, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("%s %s got %d want %d: %s", method, path, response.StatusCode, want, b)
	}
	if result != nil {
		defer response.Body.Close()
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			t.Fatal(err)
		}
	}
	return response
}
func newService(t *testing.T) *Service {
	t.Helper()
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	return s
}
func TestServiceAuthOwnerCancellationAndConflicts(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(dest, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	response, err := http.Get("http://" + s.Address + "/files")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("public file service accepted request")
	}
	serviceRequest(t, s, "DELETE", "/file?recursive=true", "owner", nil, nil, 400, nil).Body.Close()
	serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Path: dest, Size: 3}, nil, 409, nil).Body.Close()
	for _, relative := range []string{"../outside", "/absolute"} {
		serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Directory: dir, Relative: relative, Size: 3}, nil, 400, nil).Body.Close()
	}
	var u Upload
	serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Path: dest, Overwrite: true, Size: 3}, nil, 201, &u)
	path := "/uploads/" + u.ID
	serviceRequest(t, s, "HEAD", path, "other", nil, nil, 403, nil).Body.Close()
	serviceRequest(t, s, "PATCH", path, "owner", []byte("new"), http.Header{"Upload-Offset": []string{"1"}}, 409, nil).Body.Close()
	serviceRequest(t, s, "POST", path+"/commit", "owner", nil, nil, 409, nil).Body.Close()
	serviceRequest(t, s, "PATCH", path, "owner", []byte("new"), http.Header{"Upload-Offset": []string{"0"}}, 200, &u)
	serviceRequest(t, s, "DELETE", "/owners/owner", "owner", nil, nil, 200, nil).Body.Close()
	serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Path: dest, Overwrite: true, Size: 3}, nil, 410, nil).Body.Close()
	if b, err := os.ReadFile(dest); err != nil || string(b) != "original" {
		t.Fatal(string(b), err)
	}
	if temp, _ := filepath.Glob(filepath.Join(dir, ".probex-upload-*")); len(temp) != 0 {
		t.Fatal(temp)
	}
}
func TestService64BitOffsetsAndRange(t *testing.T) {
	s := newService(t)
	dest := filepath.Join(t.TempDir(), "sparse.bin")
	offset := int64(1<<33) + 123
	payload := []byte("high-offset-data")
	var u Upload
	serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Path: dest, Size: offset + int64(len(payload))}, nil, 201, &u)
	// Sparse local preparation avoids sending eight GiB while testing real 64-bit seeks.
	s.mu.Lock()
	active := s.uploads[u.ID]
	s.mu.Unlock()
	active.mu.Lock()
	if err := active.file.Truncate(offset); err != nil {
		t.Fatal(err)
	}
	if _, err := active.file.Seek(offset, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	active.Offset = offset
	active.mu.Unlock()
	path := "/uploads/" + u.ID
	serviceRequest(t, s, "PATCH", path, "owner", payload, http.Header{"Upload-Offset": []string{fmt.Sprint(offset)}}, 200, &u)
	response := serviceRequest(t, s, "HEAD", path, "owner", nil, nil, 200, nil)
	if response.Header.Get("Upload-Offset") != fmt.Sprint(offset+int64(len(payload))) {
		t.Fatal(response.Header)
	}
	response.Body.Close()
	serviceRequest(t, s, "POST", path+"/commit", "owner", nil, nil, 200, &u)
	response = serviceRequest(t, s, "GET", "/file?path="+url.QueryEscape(dest), "owner", nil, http.Header{"Range": []string{fmt.Sprintf("bytes=%d-", offset)}}, 206, nil)
	b, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Equal(b, payload) {
		t.Fatal(b, err)
	}
	if info, err := os.Stat(dest); err != nil || info.Size() != offset+int64(len(payload)) {
		t.Fatal(info, err)
	}
}
func TestServiceCommitVersionAndPermissions(t *testing.T) {
	s := newService(t)
	dest := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(dest, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	version, err := FileHash(dest)
	if err != nil {
		t.Fatal(err)
	}
	var u Upload
	serviceRequest(t, s, "POST", "/uploads", "owner", UploadRequest{Path: dest, Size: 3, Overwrite: true, IfMatch: version}, nil, 201, &u)
	path := "/uploads/" + u.ID
	serviceRequest(t, s, "PATCH", path, "owner", []byte("new"), http.Header{"Upload-Offset": []string{"0"}}, 200, &u)
	if err := os.WriteFile(dest, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	serviceRequest(t, s, "POST", path+"/commit", "owner", nil, nil, 409, nil).Body.Close()
	b, err := os.ReadFile(dest)
	if err != nil || sha256.Sum256(b) != sha256.Sum256([]byte("external")) {
		t.Fatal(string(b), err)
	}
	serviceRequest(t, s, "DELETE", path, "owner", nil, nil, 200, nil).Body.Close()
}

func TestServiceDirectoryOperationsPreserveConflicts(t *testing.T) {
	s := newService(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "new-directory")
	serviceRequest(t, s, "POST", "/directories", "owner", map[string]string{"path": dir}, nil, 201, nil).Body.Close()
	serviceRequest(t, s, "POST", "/directories", "owner", map[string]string{"path": dir}, nil, 409, nil).Body.Close()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	for _, p := range []string{first, second} {
		serviceRequest(t, s, "POST", "/files", "owner", map[string]string{"path": p}, nil, 201, nil).Body.Close()
	}
	serviceRequest(t, s, "POST", "/files/rename", "owner", map[string]string{"path": first, "destination": second}, nil, 409, nil).Body.Close()
	if _, err := os.Stat(first); err != nil {
		t.Fatal("rename conflict removed source", err)
	}
	serviceRequest(t, s, "DELETE", "/file?path="+url.QueryEscape(dir), "owner", nil, nil, 409, nil).Body.Close()
	if _, err := os.Stat(first); err != nil {
		t.Fatal("non-recursive delete removed child", err)
	}
	serviceRequest(t, s, "DELETE", "/file?recursive=true&path="+url.QueryEscape(dir), "owner", nil, nil, 200, nil).Body.Close()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("recursive delete did not remove directory", err)
	}
}
