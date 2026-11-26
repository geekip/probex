package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	pathpkg "path"
	"probex/internal/files"
	"probex/internal/protocol"
	"probex/internal/tunnel"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type fileOwner struct {
	notices     chan fileEvent
	id, session string
	conn        *websocket.Conn
	ctx         context.Context
	cancel      context.CancelFunc
	nodes       map[string]bool
}
type fileTask struct {
	upload   files.Upload
	nodeID   string
	owner    *fileOwner
	ctx      context.Context
	cancel   context.CancelFunc
	stopPeer func() bool
	error    string
}
type fileEvent struct {
	Topic    string        `json:"topic,omitempty"`
	Download *download     `json:"download,omitempty"`
	Type     string        `json:"type"`
	ID       string        `json:"id,omitempty"`
	NodeID   string        `json:"node_id,omitempty"`
	Upload   *files.Upload `json:"upload,omitempty"`
	Error    string        `json:"error,omitempty"`
}
type agentBody struct {
	io.ReadCloser
	stream  *tunnel.Stream
	written <-chan error
}

func (b *agentBody) Close() error {
	b.stream.Close()
	err := b.ReadCloser.Close()
	writeErr := <-b.written
	if err != nil {
		return err
	}
	return writeErr
}
func (s *Server) agentFileRequest(ctx context.Context, nodeID, method, path, owner string, headers http.Header, body io.Reader, length int64) (*http.Response, error) {
	stream, err := s.openStream(ctx, nodeID, protocol.Message{Purpose: "file", Target: ""})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://agent/"+path, body)
	if err != nil {
		stream.Close()
		return nil, err
	}
	copyFileRequestHeaders(req.Header, headers)
	req.Header.Set("X-Probex-File-Owner", owner)
	req.Close = true
	req.ContentLength = length
	written := make(chan error, 1)
	go func() {
		err := req.Write(stream)
		if err != nil {
			stream.Close()
		}
		written <- err
	}()
	response, err := http.ReadResponse(bufio.NewReader(stream), req)
	if err != nil {
		stream.Close()
		<-written
		return nil, err
	}
	response.Body = &agentBody{ReadCloser: response.Body, stream: stream, written: written}
	return response, nil
}
func (s *Server) fileSession(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" && !sameOrigin(r) {
		files.Error(w, 403, "unauthorized", "origin rejected")
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		problem(w, 503, "server stopping")
		return
	}
	s.agents.Add(1)
	sessionID, _ := r.Context().Value(sessionKey{}).(string)
	expiry := s.sessions[sessionID]
	s.mu.Unlock()
	defer s.agents.Done()
	ctx, cancel := context.WithCancel(r.Context())
	if sessionID != "" {
		var deadline context.CancelFunc
		ctx, deadline = context.WithDeadline(ctx, expiry)
		defer deadline()
	}
	defer cancel()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(protocol.MaxMessage)
	owner := &fileOwner{id: protocol.RandomID(), session: sessionID, conn: conn, ctx: ctx, cancel: cancel, nodes: map[string]bool{}, notices: make(chan fileEvent, 64)}
	s.mu.Lock()
	if s.closed || ctx.Err() != nil || sessionID != "" && !s.sessions[sessionID].After(time.Now()) {
		s.mu.Unlock()
		return
	}
	s.filesMu.Lock()
	s.fileOwners[owner.id] = owner
	s.filesMu.Unlock()
	s.mu.Unlock()
	defer s.endFileOwner(owner)
	defer s.unsubscribeUpdate(owner, updateScope{})
	stop := context.AfterFunc(ctx, func() { conn.CloseNow() })
	defer stop()
	if err := s.fileEvent(owner, fileEvent{Type: "session", ID: owner.id}); err != nil {
		return
	}
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case event := <-owner.notices:
				if err := s.fileEvent(owner, event); err != nil {
					cancel()
					return
				}
			case <-ticker.C:
				ping, done := context.WithTimeout(ctx, 10*time.Second)
				err := conn.Ping(ping)
				done()
				if err != nil {
					conn.CloseNow()
					return
				}
			}
		}
	}()
	for {
		var m updateRequest
		if err := wsjson.Read(ctx, conn, &m); err != nil {
			return
		}
		switch {
		case m.Type == "ping":
		case m.Type == "subscribe" && s.validUpdateScope(m.updateScope):
			s.subscribeUpdate(owner, m.updateScope)
		case m.Type == "unsubscribe" && s.validUpdateScope(m.updateScope):
			s.unsubscribeUpdate(owner, m.updateScope)
		default:
			conn.Close(websocket.StatusPolicyViolation, "invalid file session message")
			return
		}
	}
}
func (s *Server) fileEvent(owner *fileOwner, event fileEvent) error {
	ctx, cancel := context.WithTimeout(owner.ctx, 5*time.Second)
	defer cancel()
	return wsjson.Write(ctx, owner.conn, event)
}
func (s *Server) endFileOwner(owner *fileOwner) {
	owner.cancel()
	s.filesMu.Lock()
	delete(s.fileOwners, owner.id)
	nodes := make([]string, 0, len(owner.nodes))
	for node := range owner.nodes {
		nodes = append(nodes, node)
	}
	for id, t := range s.fileTasks {
		if t.owner == owner {
			t.cancel()
			t.stopPeer()
			delete(s.fileTasks, id)
		}
	}
	for id, t := range s.downloads {
		if t.owner == owner {
			t.cancel()
			t.stopPeer()
			delete(s.downloads, id)
		}
	}
	s.filesMu.Unlock()
	for _, node := range nodes {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		response, err := s.agentFileRequest(ctx, node, "DELETE", "owners/"+owner.id, owner.id, nil, nil, 0)
		if err != nil {
			s.mu.Lock()
			online := s.peers[node] != nil
			s.mu.Unlock()
			if online {
				log.Printf("cancel file owner on node %s: %v", node, err)
			}
		}
		if err == nil {
			if response.StatusCode >= 300 {
				log.Printf("cancel file owner: node %s status %d", node, response.StatusCode)
			}
			response.Body.Close()
		}
		cancel()
	}
}
func (s *Server) stopFileSessions(session string) {
	s.filesMu.Lock()
	var list []*fileOwner
	for _, o := range s.fileOwners {
		if session == "" || o.session == session {
			list = append(list, o)
		}
	}
	s.filesMu.Unlock()
	for _, o := range list {
		o.cancel()
		o.conn.CloseNow()
	}
}
func (s *Server) removeNodeFileTasks(nodeID string) {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	for id, t := range s.fileTasks {
		if t.nodeID == nodeID {
			t.cancel()
			t.stopPeer()
			delete(s.fileTasks, id)
		}
	}
	for id, t := range s.downloads {
		if t.data.NodeID == nodeID {
			t.cancel()
			t.stopPeer()
			delete(s.downloads, id)
		}
	}
}
func (s *Server) requestFileOwner(w http.ResponseWriter, r *http.Request) *fileOwner {
	id := r.Header.Get("X-Probex-File-Session")
	if id == "" {
		id = r.URL.Query().Get("file_session")
	}
	s.filesMu.Lock()
	o := s.fileOwners[id]
	s.filesMu.Unlock()
	if o == nil || o.ctx.Err() != nil {
		files.Error(w, 410, "cancelled", "file session is missing or ended")
		return nil
	}
	session, _ := r.Context().Value(sessionKey{}).(string)
	if o.session != session {
		files.Error(w, 403, "unauthorized", "file session belongs to another login")
		return nil
	}
	return o
}
func copyFileRequestHeaders(dst, src http.Header) {
	copyResponseHeaders(dst, src, "Content-Type", "Range", "If-Range",
		"If-Match", "If-None-Match", "If-Modified-Since", "If-Unmodified-Since", "Upload-Offset")
}

func copyResponseHeaders(dst, src http.Header, allowed ...string) {
	for _, name := range allowed {
		if values := src.Values(name); len(values) != 0 {
			dst[http.CanonicalHeaderKey(name)] = values
		}
	}
}

func fileAttachment(w http.ResponseWriter, path string) {
	filename := pathpkg.Base(strings.ReplaceAll(path, "\\", "/"))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'")
}

func (s *Server) fileProxy(w http.ResponseWriter, r *http.Request, nodeID, path string) {
	preview := ""
	if path == "preview" {
		if r.Method != "GET" && r.Method != "HEAD" {
			files.Error(w, 405, "invalid_request", "method not allowed")
			return
		}
		preview = filePreviewType(r.URL.Query().Get("path"))
		if preview == "" {
			files.Error(w, 415, "unsupported", "file type has no media preview; download the file instead")
			return
		}
		path = "file"
	}
	ctx := r.Context()
	ownerID := ""
	if r.Header.Get("X-Probex-File-Session") != "" || r.URL.Query().Get("file_session") != "" || path == "file" && (r.Method == "GET" || r.Method == "HEAD") {
		owner := s.requestFileOwner(w, r)
		if owner == nil {
			return
		}
		ownerID = owner.id
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(owner.ctx, cancel)
		defer stop()
	}
	if ownerID != "" {
		stopIO := context.AfterFunc(ctx, func() {
			controller := http.NewResponseController(w)
			if err := controller.SetReadDeadline(time.Now()); err != nil {
				log.Printf("cancel file read: %v", err)
			}
			if err := controller.SetWriteDeadline(time.Now()); err != nil {
				log.Printf("cancel file response: %v", err)
			}
		})
		defer stopIO()
	}
	route := path
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	response, err := s.agentFileRequest(ctx, nodeID, r.Method, path, ownerID, r.Header, r.Body, r.ContentLength)
	if err != nil {
		files.Error(w, 502, "node_unavailable", err.Error())
		return
	}
	defer response.Body.Close()
	copyResponseHeaders(w.Header(), response.Header, "Content-Length")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if route == "file" && (r.Method == "GET" || r.Method == "HEAD") {
		copyResponseHeaders(w.Header(), response.Header, "Accept-Ranges", "Content-Range")
		if response.StatusCode < 300 {
			fileAttachment(w, r.URL.Query().Get("path"))
		}
	}
	if preview != "" && response.StatusCode < 300 {
		w.Header().Set("Content-Type", preview)
		w.Header().Set("Content-Disposition", "inline")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src data:; style-src 'unsafe-inline'; font-src data:")
	}
	w.WriteHeader(response.StatusCode)
	if _, err := io.Copy(w, response.Body); err != nil && ctx.Err() == nil {
		log.Printf("file proxy: %v", err)
	}
}
func (s *Server) createFileUpload(w http.ResponseWriter, r *http.Request, nodeID string) {
	owner := s.requestFileOwner(w, r)
	if owner == nil {
		return
	}
	var body files.UploadRequest
	if !decode(w, r, &body) {
		return
	}
	body.ID = protocol.RandomID()
	s.mu.Lock()
	p := s.peers[nodeID]
	s.mu.Unlock()
	if p == nil {
		files.Error(w, 409, "node_offline", "node offline")
		return
	}
	ctx, cancel := context.WithCancel(p.ctx)
	task := &fileTask{owner: owner, nodeID: nodeID, ctx: ctx, cancel: cancel, upload: files.Upload{ID: body.ID, Path: body.Path, Size: body.Size, State: "uploading"}, stopPeer: func() bool { return false }}
	s.filesMu.Lock()
	if owner.ctx.Err() != nil {
		s.filesMu.Unlock()
		cancel()
		files.Error(w, 410, "cancelled", "file owner ended")
		return
	}
	owner.nodes[nodeID] = true
	s.fileTasks[body.ID] = task
	stopPeer := context.AfterFunc(p.ctx, func() {
		cancel()
		s.filesMu.Lock()
		if task.upload.State == "uploading" {
			task.upload.State = "failed"
			task.error = "node disconnected"
		}
		snapshot := task.upload
		s.filesMu.Unlock()
		if err := s.fileEvent(owner, fileEvent{Type: "upload", NodeID: nodeID, Upload: &snapshot, Error: "node disconnected"}); err != nil {
			owner.conn.CloseNow()
		}
	})
	task.stopPeer = stopPeer
	s.filesMu.Unlock()
	data, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	requestCtx, stop := context.WithCancel(r.Context())
	defer stop()
	stopTask := context.AfterFunc(ctx, stop)
	defer stopTask()
	response, err := s.agentFileRequest(requestCtx, nodeID, "POST", "uploads", owner.id, http.Header{"Content-Type": []string{"application/json"}}, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		s.filesMu.Lock()
		delete(s.fileTasks, body.ID)
		s.filesMu.Unlock()
		cancel()
		stopPeer()
		files.Error(w, 502, "node_unavailable", err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 201 {
		s.filesMu.Lock()
		delete(s.fileTasks, body.ID)
		s.filesMu.Unlock()
		defer cancel()
		stopPeer()
		w.WriteHeader(response.StatusCode)
		io.Copy(w, response.Body)
		return
	}
	var result files.Upload
	if err := json.NewDecoder(io.LimitReader(response.Body, protocol.MaxMessage)).Decode(&result); err != nil {
		files.Error(w, 502, "invalid_response", err.Error())
		return
	}
	s.filesMu.Lock()
	task.upload = result
	s.filesMu.Unlock()
	files.JSON(w, 201, result)
	if err := s.fileEvent(owner, fileEvent{Type: "upload", NodeID: nodeID, Upload: &result}); err != nil {
		owner.conn.CloseNow()
	}
}
func (s *Server) fileUploadAPI(w http.ResponseWriter, r *http.Request, path string) {
	owner := s.requestFileOwner(w, r)
	if owner == nil {
		return
	}
	id, action, _ := strings.Cut(path, "/")
	if r.Method == "PATCH" && (r.ContentLength < 0 || r.ContentLength > files.UploadChunk) {
		files.Error(w, 400, "invalid_request", "upload chunks need a known length of at most 8MiB")
		return
	}
	if !(action == "" && (r.Method == "HEAD" || r.Method == "PATCH" || r.Method == "DELETE") || action == "commit" && r.Method == "POST") {
		files.Error(w, 405, "invalid_request", "method not allowed")
		return
	}
	s.filesMu.Lock()
	task := s.fileTasks[id]
	if task == nil || task.owner != owner {
		s.filesMu.Unlock()
		files.Error(w, 404, "not_found", "upload not found for this file session")
		return
	}
	state := task.upload.State
	snapshot := task.upload
	s.filesMu.Unlock()
	if state == "completed" && r.Method == "HEAD" {
		w.Header().Set("Upload-Offset", fmt.Sprint(snapshot.Offset))
		w.Header().Set("Upload-Length", fmt.Sprint(snapshot.Size))
		w.Header().Set("Upload-State", state)
		w.WriteHeader(200)
		return
	}
	if r.Method == "DELETE" {
		task.cancel()
	} else if task.ctx.Err() != nil {
		files.Error(w, 410, "cancelled", "upload was cancelled or node disconnected")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	if r.Method != "DELETE" {
		stop := context.AfterFunc(task.ctx, cancel)
		defer stop()
	}
	if r.Method == "PATCH" {
		stopRead := context.AfterFunc(ctx, func() {
			if err := http.NewResponseController(w).SetReadDeadline(time.Now()); err != nil {
				log.Printf("cancel upload read: %v", err)
			}
		})
		defer stopRead()
	}
	response, err := s.agentFileRequest(ctx, task.nodeID, r.Method, "uploads/"+path, owner.id, r.Header, r.Body, r.ContentLength)
	if err != nil {
		if r.Method != "DELETE" && task.ctx.Err() != nil {
			files.Error(w, 410, "cancelled", "upload was cancelled or node disconnected")
		} else {
			files.Error(w, 502, "node_unavailable", err.Error())
		}
		return
	}
	defer response.Body.Close()
	if r.Method == "DELETE" && response.StatusCode < 300 {
		s.filesMu.Lock()
		delete(s.fileTasks, id)
		s.filesMu.Unlock()
		task.stopPeer()
	}
	if response.StatusCode >= 300 || r.Method == "HEAD" || r.Method == "DELETE" {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		copyResponseHeaders(w.Header(), response.Header, "Content-Length")
		if r.Method == "HEAD" {
			copyResponseHeaders(w.Header(), response.Header, "Upload-Offset", "Upload-Length", "Upload-State")
		}
		w.WriteHeader(response.StatusCode)
		if r.Method != "HEAD" {
			io.Copy(w, response.Body)
		}
		return
	}
	if r.Method != "PATCH" && !(r.Method == "POST" && action == "commit") {
		files.Error(w, 405, "invalid_request", "method not allowed")
		return
	}
	var result files.Upload
	if err := json.NewDecoder(io.LimitReader(response.Body, protocol.MaxMessage)).Decode(&result); err != nil {
		files.Error(w, 502, "invalid_response", err.Error())
		return
	}
	s.filesMu.Lock()
	task.upload = result
	s.filesMu.Unlock()
	if result.State == "completed" {
		task.stopPeer()
	}
	files.JSON(w, 200, result)
	if err := s.fileEvent(owner, fileEvent{Type: "upload", NodeID: task.nodeID, Upload: &result}); err != nil {
		owner.conn.CloseNow()
	}
}
