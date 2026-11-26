package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"probex/internal/agent"
	"probex/internal/files"
	"strings"
	"sync"
	"testing"
	"time"
)

type textDocument struct {
	Path, Text, Encoding, Version string
	Size                          int64
	BOM, Readonly                 bool
	DownloadOnly                  bool `json:"download_only"`
}

func textURL(node, path string) string {
	return "/api/nodes/" + node + "/text?path=" + url.QueryEscape(path)
}
func textSave(text, encoding, version string, bom, overwrite bool) map[string]any {
	return map[string]any{"text": text, "encoding": encoding, "version": version, "bom": bom, "overwrite": overwrite}
}

func watchRequestEnd(t *testing.T, f *fixture, suffix string) func() {
	t.Helper()
	done := make(chan struct{})
	var once sync.Once
	handler := f.app.Handler()
	f.http.Close()
	f.http = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handler.ServeHTTP(w, r)
		if strings.HasSuffix(r.URL.Path, suffix) {
			once.Do(func() { close(done) })
		}
	}))
	return func() {
		t.Helper()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("handler %s remained active after cancellation", suffix)
		}
	}
}

func TestTextAgentEncodingsBOMNewlinesAndAtomicSave(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	for _, c := range []struct {
		name, encoding, text string
		data                 []byte
		bom                  bool
	}{
		{"UTF8", "utf-8", "中文\r\nline\nlast\rend\t", []byte("中文\r\nline\nlast\rend\t"), false},
		{"BOM", "utf-8", "中文\r\n", append([]byte{0xef, 0xbb, 0xbf}, []byte("中文\r\n")...), true},
		{"GBK", "gbk", "中文€\r\n", []byte{0xd6, 0xd0, 0xce, 0xc4, 0x80, 13, 10}, false},
		{"empty", "utf-8", "", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "中文.txt")
			if err := os.WriteFile(path, c.data, 0640); err != nil {
				t.Fatal(err)
			}
			var doc textDocument
			jsonFileRequest(t, f, "GET", textURL(node.ID, path), owner.id, nil, 200, &doc)
			if doc.Text != c.text || doc.Encoding != c.encoding || doc.BOM != c.bom || doc.Size != int64(len(c.data)) || doc.Readonly || doc.DownloadOnly {
				t.Fatal(doc)
			}
			expected, err := files.FileHash(path)
			if err != nil || doc.Version != expected {
				t.Fatal(doc.Version, expected, err)
			}
			var saved textDocument
			jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave(doc.Text, doc.Encoding, doc.Version, doc.BOM, false), 200, &saved)
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, c.data) {
				t.Fatal("changed bytes", data, err)
			}
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0640 {
				t.Fatal(info, err)
			}
			if saved.Version != expected || saved.Size != doc.Size {
				t.Fatal(saved)
			}
			noUploadTemps(t, filepath.Dir(path))
		})
	}
}
func TestTextAgentRawByteSizeBoundaries(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	const edit = 2 << 20
	const read = 10 << 20
	for _, size := range []int64{edit, edit + 1, read, read + 1, 5<<30 + 17} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "size.txt")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			if size <= read {
				_, err = file.Write(bytes.Repeat([]byte("a"), int(size)))
			} else {
				err = file.Truncate(size)
			}
			if err != nil {
				t.Fatal(err)
			}
			file.Close()
			var doc textDocument
			jsonFileRequest(t, f, "GET", textURL(node.ID, path), owner.id, nil, 200, &doc)
			if doc.Size != size || doc.Readonly != (size > edit) || doc.DownloadOnly != (size > read) {
				t.Fatal(doc.Size, doc.Readonly, doc.DownloadOnly)
			}
			if size > read {
				if doc.Text != "" || doc.Version != "" {
					t.Fatal("oversized file content read")
				}
			} else if len(doc.Text) != int(size) {
				t.Fatal(len(doc.Text), size)
			}
			status := 413
			if size == edit {
				status = 200
			}
			jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave("changed", "utf-8", doc.Version, false, true), status, nil)
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if status == 413 && info.Size() != size {
				t.Fatal("read-only source changed", info.Size())
			}
			noUploadTemps(t, filepath.Dir(path))
		})
	}
	// Limits apply to the encoded result, including the BOM, not Unicode character count.
	path := filepath.Join(t.TempDir(), "encoded.txt")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave(strings.Repeat("中", edit/2), "gbk", "", false, true), 200, nil)
	info, err := os.Stat(path)
	if err != nil || info.Size() != edit {
		t.Fatal(info, err)
	}
	jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave(strings.Repeat("中", edit/2), "utf-8", "", false, true), 413, nil)
	jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave(strings.Repeat("a", edit), "utf-8", "", true, true), 413, nil)
	jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave(strings.Repeat("a", edit-3), "utf-8", "", true, true), 200, nil)
	data, err := os.ReadFile(path)
	if err != nil || len(data) != edit || !bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}) {
		t.Fatal(len(data), err)
	}
}
func TestTextAgentVersionConflictExplicitOverwriteAndRejections(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	path := filepath.Join(t.TempDir(), "edit.txt")
	if err := os.WriteFile(path, []byte("opened"), 0600); err != nil {
		t.Fatal(err)
	}
	var doc textDocument
	jsonFileRequest(t, f, "GET", textURL(node.ID, path), owner.id, nil, 200, &doc)
	if err := os.WriteFile(path, []byte("external change"), 0600); err != nil {
		t.Fatal(err)
	}
	var conflict struct{ Code string }
	jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave("buffer", "utf-8", doc.Version, false, false), 409, &conflict)
	if conflict.Code != "version_conflict" {
		t.Fatal(conflict)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "external change" {
		t.Fatal(string(data), err)
	}
	noUploadTemps(t, filepath.Dir(path))
	jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave("buffer", "utf-8", "", false, false), 400, nil)
	jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave("buffer", "utf-8", doc.Version, false, true), 200, &doc)
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "buffer" {
		t.Fatal(string(data), err)
	}
	jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave("😀", "gbk", doc.Version, false, false), 415, nil)
	data, err = os.ReadFile(path)
	if err != nil || string(data) != "buffer" {
		t.Fatal(string(data), err)
	}
	jsonFileRequest(t, f, "GET", textURL(node.ID, filepath.Dir(path)), owner.id, nil, 415, nil)
	jsonFileRequest(t, f, "GET", textURL(node.ID, path)+"&encoding=latin1", owner.id, nil, 415, nil)
	if err := os.WriteFile(path, []byte{0x81}, 0600); err != nil {
		t.Fatal(err)
	}
	jsonFileRequest(t, f, "GET", textURL(node.ID, path), owner.id, nil, 415, nil)
	if err := os.WriteFile(path, []byte("binary\x00data"), 0600); err != nil {
		t.Fatal(err)
	}
	jsonFileRequest(t, f, "GET", textURL(node.ID, path), owner.id, nil, 415, nil)
	noUploadTemps(t, filepath.Dir(path))
}
func TestTextAgentOwnerAndHTTPContract(t *testing.T) {
	f := newFixture(t, false)
	ownerEnded := watchRequestEnd(t, f, "/api/file-sessions")
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	path := filepath.Join(t.TempDir(), "text")
	if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	jsonFileRequest(t, f, "GET", textURL(node.ID, path), "", nil, 410, nil)
	response := fileRequest(t, f, "GET", textURL(node.ID, path), owner.id, nil, nil, 200)
	if response.Header.Get("ETag") == "" || response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal(response.Header)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	jsonFileRequest(t, f, "POST", textURL(node.ID, path), owner.id, nil, 405, nil)
	owner.conn.CloseNow()
	ownerEnded()
	jsonFileRequest(t, f, "GET", textURL(node.ID, path), owner.id, nil, 410, nil)
}

func TestTextAgentCookieIsolationAndRequestLimit(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	path := filepath.Join(t.TempDir(), "text")
	if err := os.WriteFile(path, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"token": f.admin})
	if err != nil {
		t.Fatal(err)
	}
	login, err := http.Post(f.http.URL+"/api/login", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()
	req, err := http.NewRequest("GET", f.http.URL+textURL(node.ID, path), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(login.Cookies()[0])
	req.Header.Set("X-Probex-File-Session", owner.id)
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal(response.Status)
	}
	jsonFileRequest(t, f, "PUT", textURL(node.ID, path), owner.id, textSave(strings.Repeat("a", 12<<20+65537), "utf-8", "", false, true), 413, nil)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "private" {
		t.Fatal(string(data), err)
	}
	noUploadTemps(t, filepath.Dir(path))
}
func TestTextAgentOwnerEndInterruptsStalledBody(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	owner := openFileOwner(t, f)
	path := filepath.Join(t.TempDir(), "text")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(f.http.URL, "http://"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	fmt.Fprintf(conn, "PUT %s HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\nX-Probex-File-Session: %s\r\nContent-Length: 1048576\r\n\r\n", textURL(node.ID, path), f.admin, owner.id)
	if _, err := io.WriteString(conn, `{"text":"`+strings.Repeat("a", 65536)); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, conn); done <- err }()
	select {
	case err := <-done:
		t.Fatal("incomplete request already ended", err)
	case <-time.After(100 * time.Millisecond):
	}
	owner.conn.CloseNow()
	select {
	case err := <-done:
		if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
			t.Fatal("stalled body reached read deadline", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("owner end left text request body blocked")
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "old" {
		t.Fatal(string(data), err)
	}
	noUploadTemps(t, filepath.Dir(path))
}
func TestTextAgentOwnerAndNodeEndInterruptBufferedResponse(t *testing.T) {
	for _, mode := range []string{"owner", "node"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, false)
			responseEnded := watchRequestEnd(t, f, "/text")
			f.startAgent()
			node := f.node()
			owner := openFileOwner(t, f)
			path := filepath.Join(t.TempDir(), "large.txt")
			const size = 10 << 20
			if err := os.WriteFile(path, bytes.Repeat([]byte("a"), size), 0600); err != nil {
				t.Fatal(err)
			}
			conn, err := net.DialTimeout("tcp", strings.TrimPrefix(f.http.URL, "http://"), time.Second)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err := conn.(*net.TCPConn).SetReadBuffer(64 << 10); err != nil {
				t.Fatal(err)
			}
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: test\r\nAuthorization: Bearer %s\r\nX-Probex-File-Session: %s\r\n\r\n", textURL(node.ID, path), f.admin, owner.id)
			response, err := http.ReadResponse(bufio.NewReader(conn), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal(response.Status)
			}
			if mode == "owner" {
				owner.conn.CloseNow()
			} else {
				f.stopAgent()
			}
			responseEnded()
			started := time.Now()
			received, err := io.ReadAll(response.Body)
			// Raw JSON also includes metadata; payload size alone cannot tell
			// whether cancellation interrupted the serialized response.
			if err == nil || json.Valid(received) {
				t.Fatal("buffered text response completed after cancellation", len(received), err)
			}
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				t.Fatal("buffered response stayed open", err)
			}
			if time.Since(started) > 3*time.Second {
				t.Fatal("slow text cancellation", time.Since(started))
			}
		})
	}
}

// Limit the real Agent's incoming data so cancellation occurs during a PATCH,
// rather than after a loopback save has already committed.
type textSlowConnection struct{ net.Conn }

func (c textSlowConnection) Read(buffer []byte) (int, error) {
	n, err := c.Conn.Read(buffer)
	if n >= 1024 {
		time.Sleep(2 * time.Millisecond)
	}
	return n, err
}
func TestTextAgentCancelDuringSaveKeepsOriginalAndRemovesTemporary(t *testing.T) {
	for _, mode := range []string{"request", "owner", "node"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t, false)
			target, err := url.Parse(f.http.URL)
			if err != nil {
				t.Fatal(err)
			}
			proxy := httputil.NewSingleHostReverseProxy(target)
			proxy.Transport = &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				conn, err := (&net.Dialer{}).DialContext(ctx, network, address)
				if err != nil {
					return nil, err
				}
				return textSlowConnection{conn}, nil
			}}
			endpoint := httptest.NewServer(proxy)
			t.Cleanup(endpoint.Close)
			var created struct{ Key string }
			f.request("POST", "/api/nodes", map[string]string{"name": "slow-editor"}, 201, &created)
			agentContext, stop := context.WithCancel(context.Background())
			f.cancel = stop
			f.done = make(chan error, 1)
			go func() {
				f.done <- agent.Run(agentContext, agent.Config{Server: endpoint.URL, JoinKey: created.Key, State: f.state})
			}()
			node := f.node()
			owner := openFileOwner(t, f)
			path := filepath.Join(t.TempDir(), "original")
			if err := os.WriteFile(path, []byte("old content"), 0600); err != nil {
				t.Fatal(err)
			}
			var opened textDocument
			jsonFileRequest(t, f, "GET", textURL(node.ID, path), owner.id, nil, 200, &opened)
			data, err := json.Marshal(textSave(strings.Repeat("x", 2<<20), "utf-8", opened.Version, false, false))
			if err != nil {
				t.Fatal(err)
			}
			requestContext, cancel := context.WithCancel(context.Background())
			defer cancel()
			request, err := http.NewRequestWithContext(requestContext, "PUT", f.http.URL+textURL(node.ID, path), bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+f.admin)
			request.Header.Set("X-Probex-File-Session", owner.id)
			done := make(chan struct{})
			go func() {
				defer close(done)
				response, err := http.DefaultClient.Do(request)
				if err == nil {
					io.Copy(io.Discard, response.Body)
					response.Body.Close()
				}
			}()
			deadline := time.Now().Add(4 * time.Second)
			for {
				matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".probex-upload-*"))
				if err != nil {
					t.Fatal(err)
				}
				active := false
				for _, name := range matches {
					info, err := os.Stat(name)
					if err != nil {
						t.Fatal(err)
					}
					if info.Size() > 0 && info.Size() < 2<<20 {
						active = true
					}
				}
				if active {
					break
				}
				select {
				case <-done:
					t.Fatal("save ended before its PATCH was observed")
				default:
				}
				if time.Now().After(deadline) {
					t.Fatal("save did not reach Agent")
				}
				time.Sleep(time.Millisecond)
			}
			switch mode {
			case "request":
				cancel()
			case "owner":
				owner.conn.CloseNow()
			case "node":
				f.stopAgent()
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("text save did not terminate")
			}
			noUploadTemps(t, filepath.Dir(path))
			original, err := os.ReadFile(path)
			if err != nil || string(original) != "old content" {
				t.Fatal("cancel changed original", len(original), err)
			}
		})
	}
}
