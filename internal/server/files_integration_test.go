package server_test

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"probex/internal/agent"
	"probex/internal/files"
	"probex/internal/protocol"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type ownerConnection struct {
	id   string
	conn *websocket.Conn
}

func openFileOwner(t *testing.T, f *fixture) ownerConnection {
	return openFileOwnerHeaders(t, f, http.Header{"Authorization": []string{"Bearer " + f.admin}})
}
func openFileOwnerHeaders(t *testing.T, f *fixture, headers http.Header) ownerConnection {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, _, err := websocket.Dial(ctx, strings.Replace(f.http.URL, "http", "ws", 1)+"/api/file-sessions", &websocket.DialOptions{HTTPHeader: headers})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	var event struct {
		ID   string `json:"id"`
		Type string `json:"type"`
	}
	if err := wsjson.Read(ctx, c, &event); err != nil || event.Type != "session" {
		cancel()
		c.CloseNow()
		t.Fatal(event, err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			var event any
			if wsjson.Read(ctx, c, &event) != nil {
				return
			}
		}
	}()
	t.Cleanup(func() { cancel(); c.CloseNow(); <-done })
	return ownerConnection{event.ID, c}
}
func fileRequest(t *testing.T, f *fixture, method, path, owner string, body io.Reader, headers http.Header, want int) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, f.http.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = http.Header{}
	}
	req.Header.Set("Authorization", "Bearer "+f.admin)
	if owner != "" {
		req.Header.Set("X-Probex-File-Session", owner)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != want {
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		t.Fatalf("%s %s status=%d want=%d body=%s", method, path, response.StatusCode, want, data)
	}
	return response
}
func jsonFileRequest(t *testing.T, f *fixture, method, path, owner string, body any, want int, result any) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	response := fileRequest(t, f, method, path, owner, reader, http.Header{"Content-Type": []string{"application/json"}}, want)
	defer response.Body.Close()
	if result != nil {
		if err := json.NewDecoder(response.Body).Decode(result); err != nil {
			t.Fatal(err)
		}
	}
}
func noUploadTemps(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		matches, err := filepath.Glob(filepath.Join(dir, ".probex-upload-*"))
		if err != nil {
			t.Fatal(err)
		}
		if len(matches) == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("temporary uploads retained", matches)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestFilesAgentBrowseCRUDRangeAndUploadSHA(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	base := "/api/nodes/" + node.ID
	dir := t.TempDir()
	for i := 0; i < 220; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("entry-%03d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, ".hidden"), nil, 0600)
	os.Mkdir(filepath.Join(dir, "folder"), 0700)
	os.WriteFile(filepath.Join(dir, "z-last"), []byte("tail"), 0600)
	var list files.Listing
	jsonFileRequest(t, f, "GET", base+"/files?path="+url.QueryEscape(dir)+"&limit=10&fields=size,mtime,mode,owner,group", "", nil, 200, &list)
	if len(list.Entries) != 10 || list.Files != 221 || list.Directories != 1 || list.Next == "" || list.Entries[0].UID == nil || list.Entries[0].Size == nil {
		t.Fatal(list)
	}
	cursor := list.Next
	jsonFileRequest(t, f, "GET", base+"/files?path="+url.QueryEscape(dir)+"&cursor="+url.QueryEscape(cursor)+"&limit=10", "", nil, 200, &list)
	if len(list.Entries) != 10 || list.Entries[0].Name <= cursor || list.Files != 221 {
		t.Fatal(list)
	}
	jsonFileRequest(t, f, "GET", base+"/files?path="+url.QueryEscape(dir)+"&query=z-last", "", nil, 200, &list)
	if len(list.Entries) != 1 || list.Files != 1 || list.Entries[0].Name != "z-last" {
		t.Fatal("search ignored unloaded names", list)
	}
	jsonFileRequest(t, f, "GET", base+"/files?path="+url.QueryEscape(dir)+"&hidden=true", "", nil, 200, &list)
	if list.Files != 222 {
		t.Fatal(list)
	}
	newPath := filepath.Join(dir, "new.txt")
	jsonFileRequest(t, f, "POST", base+"/files", "", map[string]string{"path": newPath}, 201, nil)
	jsonFileRequest(t, f, "POST", base+"/files", "", map[string]string{"path": newPath}, 409, nil)
	jsonFileRequest(t, f, "POST", base+"/files/rename", "", map[string]string{"path": newPath, "destination": filepath.Join(dir, "renamed.txt")}, 200, nil)
	newPath = filepath.Join(dir, "renamed.txt")
	payload := bytes.Repeat([]byte("abc\x00\xff"), 2<<20)
	var upload files.Upload
	jsonFileRequest(t, f, "POST", base+"/uploads", owner.id, files.UploadRequest{Path: newPath, Size: int64(len(payload)), Overwrite: false}, 409, nil)
	jsonFileRequest(t, f, "POST", base+"/uploads", owner.id, files.UploadRequest{Path: newPath, Size: int64(len(payload)), Overwrite: true}, 201, &upload)
	endpoint := "/api/uploads/" + upload.ID
	// Early device rejection must still reach the caller during a large request body.
	rejected := fileRequest(t, f, "PATCH", endpoint, owner.id, bytes.NewReader(payload[:files.UploadChunk]), http.Header{"Upload-Offset": []string{"1"}}, 409)
	rejected.Body.Close()
	// A second page cannot access this upload, even under the same administrator.
	other := openFileOwner(t, f)
	response := fileRequest(t, f, "HEAD", endpoint, other.id, nil, nil, 404)
	response.Body.Close()
	offset := 0
	for offset < len(payload) {
		size := min(files.UploadChunk, len(payload)-offset)
		response := fileRequest(t, f, "PATCH", endpoint, owner.id, bytes.NewReader(payload[offset:offset+size]), http.Header{"Upload-Offset": []string{fmt.Sprint(offset)}}, 200)
		if err := json.NewDecoder(response.Body).Decode(&upload); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		offset += size
		if upload.Offset != int64(offset) {
			t.Fatal(upload)
		}
	}
	response = fileRequest(t, f, "HEAD", endpoint, owner.id, nil, nil, 200)
	if response.Header.Get("Upload-Offset") != fmt.Sprint(len(payload)) {
		t.Fatal(response.Header)
	}
	response.Body.Close()
	jsonFileRequest(t, f, "POST", endpoint+"/commit", owner.id, nil, 200, &upload)
	if upload.State != "completed" {
		t.Fatal(upload)
	}
	response = fileRequest(t, f, "GET", base+"/file?path="+url.QueryEscape(newPath), owner.id, nil, nil, 200)
	hash := sha256.New()
	if _, err := io.Copy(hash, response.Body); err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	want := sha256.Sum256(payload)
	if !bytes.Equal(hash.Sum(nil), want[:]) {
		t.Fatal("upload/download SHA mismatch")
	}
	response = fileRequest(t, f, "GET", base+"/file?path="+url.QueryEscape(newPath), owner.id, nil, http.Header{"Range": []string{"bytes=5-15"}}, 206)
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || !bytes.Equal(data, payload[5:16]) {
		t.Fatal(data, err)
	}
	noUploadTemps(t, dir)
	jsonFileRequest(t, f, "DELETE", base+"/file?path="+url.QueryEscape(newPath), "", nil, 200, nil)
	jsonFileRequest(t, f, "GET", base+"/stat?path="+url.QueryEscape(newPath), "", nil, 404, nil)
}

func TestFilesAgentInitialUserDirectory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "用户 home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	var list files.Listing
	jsonFileRequest(t, f, "GET", "/api/nodes/"+node.ID+"/files?initial=true&hidden=true", owner.id, nil, 200, &list)
	if list.Path != home {
		t.Fatalf("Agent initial directory=%q, want %q", list.Path, home)
	}
}
func TestFilesOwnerCloseAndNodeDeleteCancelTemps(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	dir := t.TempDir()
	dest := filepath.Join(dir, "old")
	if err := os.WriteFile(dest, []byte("old-content"), 0600); err != nil {
		t.Fatal(err)
	}
	base := "/api/nodes/" + node.ID
	var upload files.Upload
	jsonFileRequest(t, f, "POST", base+"/uploads", owner.id, files.UploadRequest{Path: dest, Size: 100, Overwrite: true}, 201, &upload)
	response := fileRequest(t, f, "PATCH", "/api/uploads/"+upload.ID, owner.id, strings.NewReader("new"), http.Header{"Upload-Offset": []string{"0"}}, 200)
	response.Body.Close()
	owner.conn.CloseNow()
	noUploadTemps(t, dir)
	if b, err := os.ReadFile(dest); err != nil || string(b) != "old-content" {
		t.Fatal(string(b), err)
	}
	jsonFileRequest(t, f, "POST", base+"/uploads", owner.id, files.UploadRequest{Path: dest, Size: 3, Overwrite: true}, 410, nil)
	next := openFileOwner(t, f)
	jsonFileRequest(t, f, "POST", base+"/uploads", next.id, files.UploadRequest{Path: dest, Size: 9, Overwrite: true}, 201, &upload)
	f.request("DELETE", base, nil, 200, nil)
	select {
	case err := <-f.done:
		if err == nil || !strings.Contains(err.Error(), "revoked") {
			t.Fatal(err)
		}
		f.cancel()
		f.cancel = nil
	case <-time.After(3 * time.Second):
		t.Fatal("Agent did not exit")
	}
	noUploadTemps(t, dir)
	if b, err := os.ReadFile(dest); err != nil || string(b) != "old-content" {
		t.Fatal(string(b), err)
	}
}

func TestFilesStalledUploadCancellation(t *testing.T) {
	for _, mode := range []string{"owner", "upload"} {
		t.Run(mode, func(t *testing.T) { stalledUploadCancellation(t, mode) })
	}
}
func stalledUploadCancellation(t *testing.T, mode string) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	dir := t.TempDir()
	var upload files.Upload
	jsonFileRequest(t, f, "POST", "/api/nodes/"+node.ID+"/uploads", owner.id, files.UploadRequest{Path: filepath.Join(dir, "stalled"), Size: 1 << 20}, 201, &upload)
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	req, err := http.NewRequest("PATCH", f.http.URL+"/api/uploads/"+upload.ID, reader)
	if err != nil {
		t.Fatal(err)
	}
	req.ContentLength = 1 << 20
	req.Header.Set("Authorization", "Bearer "+f.admin)
	req.Header.Set("X-Probex-File-Session", owner.id)
	req.Header.Set("Upload-Offset", "0")
	result := make(chan error, 1)
	go func() {
		resp, err := (&http.Client{Timeout: 8 * time.Second}).Do(req)
		if resp != nil {
			resp.Body.Close()
		}
		result <- err
	}()
	if _, err := writer.Write(bytes.Repeat([]byte("x"), 65536)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		matches, _ := filepath.Glob(filepath.Join(dir, ".probex-upload-*"))
		active := false
		for _, p := range matches {
			if info, err := os.Stat(p); err == nil && info.Size() > 0 {
				active = true
			}
		}
		if active {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("PATCH did not reach Agent")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if mode == "owner" {
		owner.conn.CloseNow()
	} else {
		jsonFileRequest(t, f, "DELETE", "/api/uploads/"+upload.ID, owner.id, nil, 200, nil)
	}
	select {
	case <-result:
	case <-time.After(3 * time.Second):
		t.Fatal("owner close did not interrupt stalled request body")
	}
	noUploadTemps(t, dir)
}
func TestFilesCookieOwnerIsolationAndLogout(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	dir := t.TempDir()
	login := func() *http.Cookie {
		t.Helper()
		b, _ := json.Marshal(map[string]string{"token": f.admin})
		resp, err := http.Post(f.http.URL+"/api/login", "application/json", bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.Cookies()[0]
	}
	first, second := login(), login()
	owner := openFileOwnerHeaders(t, f, http.Header{"Cookie": []string{first.String()}, "Origin": []string{f.http.URL}})
	cookieRequest := func(method, path string, cookie *http.Cookie, body io.Reader, want int) *http.Response {
		t.Helper()
		req, err := http.NewRequest(method, f.http.URL+path, body)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(cookie)
		req.Header.Set("Origin", f.http.URL)
		req.Header.Set("X-Probex-File-Session", owner.id)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != want {
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Fatal(resp.Status, string(data))
		}
		return resp
	}
	b, _ := json.Marshal(files.UploadRequest{Path: filepath.Join(dir, "pending"), Size: 100})
	resp := cookieRequest("POST", "/api/nodes/"+node.ID+"/uploads", first, bytes.NewReader(b), 201)
	var upload files.Upload
	if err := json.NewDecoder(resp.Body).Decode(&upload); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	readOnly, err := http.NewRequest("HEAD", f.http.URL+"/api/uploads/"+upload.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	readOnly.AddCookie(first)
	readOnly.Header.Set("X-Probex-File-Session", owner.id)
	head, err := http.DefaultClient.Do(readOnly)
	if err != nil {
		t.Fatal(err)
	}
	head.Body.Close()
	if head.StatusCode != 200 {
		t.Fatal("same-origin HEAD without Origin rejected", head.Status)
	}
	cookieRequest("HEAD", "/api/uploads/"+upload.ID, second, nil, 403).Body.Close()
	cookieRequest("POST", "/api/logout", first, nil, 200).Body.Close()
	noUploadTemps(t, dir)
	cookieRequest("HEAD", "/api/uploads/"+upload.ID, first, nil, 401).Body.Close()
}

func TestFilesOwnerCloseInterruptsBlockedDownload(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	path := filepath.Join(t.TempDir(), "sparse-download")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	const size = 128 << 20
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	file.Close()
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(f.http.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "GET /api/nodes/%s/file?path=%s HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\nX-Probex-File-Session: %s\r\n\r\n", node.ID, url.QueryEscape(path), f.admin, owner.id)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.Status)
	}
	// Leave the large response unread until the owner's cancellation reaches the server.
	owner.conn.CloseNow()
	deadline := time.Now().Add(3 * time.Second)
	n, err := io.Copy(io.Discard, response.Body)
	if err == nil || n >= size {
		t.Fatal("download completed after cancellation", n, err)
	}
	if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("download stayed open until read deadline", n, err)
	}
	if time.Now().After(deadline) {
		t.Fatal("slow download cancellation", err)
	}
}

func TestFilesTasksStayBoundToTheirNode(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	first := f.node()
	owner := openFileOwner(t, f)
	var joined struct {
		Key string `json:"key"`
	}
	f.request("POST", "/api/nodes", map[string]string{"name": "file-second"}, 201, &joined)
	join, err := protocol.DecodeEnrollment(joined.Key)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	state := filepath.Join(t.TempDir(), "second.json")
	go func() { done <- agent.Run(ctx, agent.Config{Server: f.http.URL, JoinKey: joined.Key, State: state}) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		var nodes []protocol.Node
		f.request("GET", "/api/nodes", nil, 200, &nodes)
		online := false
		for _, n := range nodes {
			if n.ID == join.NodeID && n.Online {
				online = true
			}
		}
		if online {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("second node offline")
		}
		time.Sleep(10 * time.Millisecond)
	}
	firstDir, secondDir := t.TempDir(), t.TempDir()
	var a, b files.Upload
	jsonFileRequest(t, f, "POST", "/api/nodes/"+first.ID+"/uploads", owner.id, files.UploadRequest{Path: filepath.Join(firstDir, "first"), Size: 3}, 201, &a)
	jsonFileRequest(t, f, "POST", "/api/nodes/"+join.NodeID+"/uploads", owner.id, files.UploadRequest{Path: filepath.Join(secondDir, "second"), Size: 3}, 201, &b)
	// Caller-supplied node hints cannot redirect an existing upload's stored node binding.
	response := fileRequest(t, f, "PATCH", "/api/uploads/"+a.ID+"?node_id="+join.NodeID, owner.id, strings.NewReader("aaa"), http.Header{"Upload-Offset": []string{"0"}, "X-Probex-ID": []string{join.NodeID}}, 200)
	response.Body.Close()
	response = fileRequest(t, f, "PATCH", "/api/uploads/"+b.ID, owner.id, strings.NewReader("bbb"), http.Header{"Upload-Offset": []string{"0"}}, 200)
	response.Body.Close()
	f.stopAgent()
	f.waitOffline(first.ID)
	noUploadTemps(t, firstDir)
	response = fileRequest(t, f, "HEAD", "/api/uploads/"+a.ID, owner.id, nil, nil, 410)
	response.Body.Close()
	jsonFileRequest(t, f, "POST", "/api/uploads/"+b.ID+"/commit", owner.id, nil, 200, &b)
	if data, err := os.ReadFile(b.Path); err != nil || string(data) != "bbb" {
		t.Fatal(string(data), err)
	}
	noUploadTemps(t, secondDir)
}

func TestFilesUploadCreateConflictResponseBody(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	path := filepath.Join(t.TempDir(), "existing")
	if err := os.WriteFile(path, []byte("old target"), 0600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		var result struct {
			Error string `json:"error"`
			Code  string `json:"code"`
		}
		jsonFileRequest(t, f, "POST", "/api/nodes/"+node.ID+"/uploads", owner.id, files.UploadRequest{Path: path, Size: 3}, 409, &result)
		if result.Code != "exists" || result.Error == "" {
			t.Fatalf("response %d: %+v", i, result)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old target" {
		t.Fatal(string(data), err)
	}
	noUploadTemps(t, filepath.Dir(path))
}
