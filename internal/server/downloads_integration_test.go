package server_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type browserDownload struct {
	ID, Path, State string
	Size, Offset    int64
}

func newDownload(t *testing.T, f *fixture, node, owner, path string) browserDownload {
	t.Helper()
	var d browserDownload
	jsonFileRequest(t, f, "POST", "/api/nodes/"+node+"/downloads", owner, map[string]string{"path": path}, 201, &d)
	return d
}
func downloadState(t *testing.T, f *fixture, owner, id, state string) *http.Response {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		response := fileRequest(t, f, "HEAD", "/api/downloads/"+id, owner, nil, nil, 200)
		response.Body.Close()
		if response.Header.Get("Download-State") == state {
			return response
		}
		if time.Now().After(deadline) {
			t.Fatalf("download state=%s want=%s", response.Header.Get("Download-State"), state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestBrowserDownloadPauseSameResponseSHAAndOwnership(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	other := openFileOwner(t, f)
	data := make([]byte, 16<<20)
	for i := range data {
		data[i] = byte(i*31 + i/8191)
	}
	path := filepath.Join(t.TempDir(), "中文 download.bin")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	task := newDownload(t, f, node.ID, owner.id, path)
	if task.Size != int64(len(data)) || task.Path != path || task.State != "queued" {
		t.Fatal(task)
	}
	response := fileRequest(t, f, "HEAD", "/api/downloads/"+task.ID, other.id, nil, nil, 404)
	response.Body.Close()
	jsonFileRequest(t, f, "POST", "/api/downloads/"+task.ID+"/pause", owner.id, nil, 200, &task)
	response = fileRequest(t, f, "GET", "/api/downloads/"+task.ID, owner.id, nil, nil, 200)
	defer response.Body.Close()
	if response.ContentLength != int64(len(data)) || response.Header.Get("Content-Disposition") == "" ||
		response.Header.Get("Content-Type") != "application/octet-stream" || response.Header.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(response.Header.Get("Content-Security-Policy"), "sandbox") {
		t.Fatal(response.Header)
	}
	first := make(chan error, 1)
	head := make([]byte, 1)
	go func() { _, err := io.ReadFull(response.Body, head); first <- err }()
	select {
	case err := <-first:
		t.Fatal("paused download produced bytes", err)
	case <-time.After(100 * time.Millisecond):
	}
	status := downloadState(t, f, owner.id, task.ID, "paused")
	if status.Header.Get("Download-Offset") != "0" {
		t.Fatal(status.Header)
	}
	jsonFileRequest(t, f, "POST", "/api/downloads/"+task.ID+"/resume", owner.id, nil, 200, &task)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	hash.Write(head)
	n, err := io.Copy(hash, response.Body)
	if err != nil || n+1 != int64(len(data)) {
		t.Fatal(n, err)
	}
	expected := sha256.Sum256(data)
	if !bytes.Equal(hash.Sum(nil), expected[:]) {
		t.Fatal("download bytes changed while paused")
	}
	status = downloadState(t, f, owner.id, task.ID, "completed")
	if status.Header.Get("Download-Offset") != fmt.Sprint(len(data)) {
		t.Fatal(status.Header)
	}
	jsonFileRequest(t, f, "POST", "/api/downloads/"+task.ID+"/resume", owner.id, nil, 409, nil)
	response2 := fileRequest(t, f, "GET", "/api/downloads/"+task.ID, owner.id, nil, nil, 409)
	response2.Body.Close()
}
func TestBrowserDownloadDisconnectCancelAndNewAttempt(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	path := filepath.Join(t.TempDir(), "large")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(512 << 20); err != nil {
		t.Fatal(err)
	}
	file.Close()
	task := newDownload(t, f, node.ID, owner.id, path)
	response := fileRequest(t, f, "GET", "/api/downloads/"+task.ID, owner.id, nil, nil, 200)
	if _, err := io.ReadFull(response.Body, make([]byte, 1024)); err != nil {
		t.Fatal(err)
	}
	jsonFileRequest(t, f, "POST", "/api/downloads/"+task.ID+"/pause", owner.id, nil, 200, nil)
	before := downloadState(t, f, owner.id, task.ID, "paused").Header.Get("Download-Offset")
	time.Sleep(100 * time.Millisecond)
	after := downloadState(t, f, owner.id, task.ID, "paused").Header.Get("Download-Offset")
	a, _ := strconv.ParseInt(before, 10, 64)
	b, _ := strconv.ParseInt(after, 10, 64)
	if b-a > 32<<10 {
		t.Fatalf("paused output advanced %d bytes", b-a)
	}
	response.Body.Close()
	downloadState(t, f, owner.id, task.ID, "failed")
	jsonFileRequest(t, f, "POST", "/api/downloads/"+task.ID+"/resume", owner.id, nil, 409, nil)
	again := fileRequest(t, f, "GET", "/api/downloads/"+task.ID, owner.id, nil, nil, 409)
	again.Body.Close()
	replacement := newDownload(t, f, node.ID, owner.id, path)
	if replacement.ID == task.ID {
		t.Fatal("retry reused ended response")
	}
	jsonFileRequest(t, f, "POST", "/api/downloads/"+replacement.ID+"/pause", owner.id, nil, 200, nil)
	paused := fileRequest(t, f, "GET", "/api/downloads/"+replacement.ID, owner.id, nil, nil, 200)
	defer paused.Body.Close()
	jsonFileRequest(t, f, "DELETE", "/api/downloads/"+replacement.ID, owner.id, nil, 200, nil)
	if _, err := paused.Body.Read(make([]byte, 1)); err == nil {
		t.Fatal("cancel retained paused response")
	}
	missing := fileRequest(t, f, "HEAD", "/api/downloads/"+replacement.ID, owner.id, nil, nil, 404)
	missing.Body.Close()
}
func TestBrowserDownloadMetadataEmptyChangedAndOwnerEnd(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	dir := t.TempDir()
	jsonFileRequest(t, f, "POST", "/api/nodes/"+node.ID+"/downloads", owner.id, map[string]string{"path": dir}, 400, nil)
	path := filepath.Join(dir, "empty")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	task := newDownload(t, f, node.ID, owner.id, path)
	response := fileRequest(t, f, "GET", "/api/downloads/"+task.ID, owner.id, nil, nil, 200)
	b, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || len(b) != 0 {
		t.Fatal(b, err)
	}
	downloadState(t, f, owner.id, task.ID, "completed")
	changed := newDownload(t, f, node.ID, owner.id, path)
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	response = fileRequest(t, f, "GET", "/api/downloads/"+changed.ID, owner.id, nil, nil, 409)
	b, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Contains(b, []byte("file_changed")) {
		t.Fatal(string(b), err)
	}
	huge := filepath.Join(dir, "sparse")
	file, err := os.Create(huge)
	if err != nil {
		t.Fatal(err)
	}
	const size = int64(5<<30) + 17
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	file.Close()
	task = newDownload(t, f, node.ID, owner.id, huge)
	if task.Size != size {
		t.Fatal(task.Size)
	}
	jsonFileRequest(t, f, "POST", "/api/downloads/"+task.ID+"/pause", owner.id, nil, 200, nil)
	response = fileRequest(t, f, "GET", "/api/downloads/"+task.ID, owner.id, nil, nil, 200)
	defer response.Body.Close()
	if response.ContentLength != size {
		t.Fatal(response.ContentLength)
	}
	owner.conn.CloseNow()
	if _, err := response.Body.Read(make([]byte, 1)); err == nil {
		t.Fatal("ended page retained paused response")
	}
}
func TestBrowserDownloadNodeDisconnectWhilePaused(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, make([]byte, 1<<20), 0600); err != nil {
		t.Fatal(err)
	}
	task := newDownload(t, f, node.ID, owner.id, path)
	jsonFileRequest(t, f, "POST", "/api/downloads/"+task.ID+"/pause", owner.id, nil, 200, nil)
	response := fileRequest(t, f, "GET", "/api/downloads/"+task.ID, owner.id, nil, nil, 200)
	defer response.Body.Close()
	f.stopAgent()
	downloadState(t, f, owner.id, task.ID, "failed")
	if _, err := response.Body.Read(make([]byte, 1)); err == nil {
		t.Fatal("disconnected node retained response")
	}
	jsonFileRequest(t, f, "POST", "/api/downloads/"+task.ID+"/resume", owner.id, nil, 409, nil)
}

func TestBrowserDownloadLogoutAndDeletedNode(t *testing.T) {
	t.Run("cookie logout", func(t *testing.T) {
		f := newFixture(t, false)
		f.startAgent()
		node := f.node()
		login, err := http.Post(f.http.URL+"/api/login", "application/json", bytes.NewBufferString(fmt.Sprintf(`{"token":%q}`, f.admin)))
		if err != nil {
			t.Fatal(err)
		}
		login.Body.Close()
		cookie := login.Cookies()[0]
		owner := openFileOwnerHeaders(t, f, http.Header{"Cookie": []string{cookie.Name + "=" + cookie.Value}})
		request := func(method, path string, body io.Reader, want int) *http.Response {
			t.Helper()
			req, err := http.NewRequest(method, f.http.URL+path, body)
			if err != nil {
				t.Fatal(err)
			}
			req.AddCookie(cookie)
			req.Header.Set("Origin", f.http.URL)
			req.Header.Set("X-Probex-File-Session", owner.id)
			req.Header.Set("Content-Type", "application/json")
			response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != want {
				t.Fatal(response.StatusCode, want)
			}
			return response
		}
		path := filepath.Join(t.TempDir(), "source")
		if err := os.WriteFile(path, make([]byte, 1<<20), 0600); err != nil {
			t.Fatal(err)
		}
		response := request("POST", "/api/nodes/"+node.ID+"/downloads", bytes.NewBufferString(fmt.Sprintf(`{"path":%q}`, path)), 201)
		var task browserDownload
		if err := json.NewDecoder(response.Body).Decode(&task); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		response = request("POST", "/api/downloads/"+task.ID+"/pause", nil, 200)
		response.Body.Close()
		attachment := request("GET", "/api/downloads/"+task.ID, nil, 200)
		defer attachment.Body.Close()
		response = request("POST", "/api/logout", nil, 200)
		response.Body.Close()
		if _, err := attachment.Body.Read(make([]byte, 1)); err == nil {
			t.Fatal("logout retained download")
		}
		response = request("HEAD", "/api/downloads/"+task.ID, nil, 401)
		response.Body.Close()
	})
	t.Run("deleted node", func(t *testing.T) {
		f := newFixture(t, false)
		f.startAgent()
		node := f.node()
		owner := openFileOwner(t, f)
		path := filepath.Join(t.TempDir(), "source")
		if err := os.WriteFile(path, make([]byte, 1<<20), 0600); err != nil {
			t.Fatal(err)
		}
		task := newDownload(t, f, node.ID, owner.id, path)
		jsonFileRequest(t, f, "POST", "/api/downloads/"+task.ID+"/pause", owner.id, nil, 200, nil)
		response := fileRequest(t, f, "GET", "/api/downloads/"+task.ID, owner.id, nil, nil, 200)
		defer response.Body.Close()
		f.request("DELETE", "/api/nodes/"+node.ID, nil, 200, nil)
		select {
		case err := <-f.done:
			if err == nil {
				t.Fatal("deleted Agent must stop")
			}
			f.cancel()
			f.cancel = nil
		case <-time.After(5 * time.Second):
			t.Fatal("deleted Agent retained")
		}
		if _, err := response.Body.Read(make([]byte, 1)); err == nil {
			t.Fatal("deleted node retained download")
		}
		missing := fileRequest(t, f, "HEAD", "/api/downloads/"+task.ID, owner.id, nil, nil, 404)
		missing.Body.Close()
		if _, err := os.Stat(path); err != nil {
			t.Fatal("download cleanup deleted source", err)
		}
	})
}
