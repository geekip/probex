package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"probex/internal/files"
	"probex/internal/protocol"
	"strings"
	"time"
)

type download struct {
	ID     string `json:"id"`
	NodeID string `json:"node_id"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	State  string `json:"state"`
	Error  string `json:"error,omitempty"`
}
type downloadTask struct {
	data     download
	owner    *fileOwner
	ctx      context.Context
	cancel   context.CancelFunc
	stopPeer func() bool
	claimed  bool
	resume   chan struct{}
}

func downloadError(w http.ResponseWriter, response *http.Response) {
	status := response.StatusCode
	if status < 400 {
		status = http.StatusBadGateway
	}
	var problem struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, protocol.MaxMessage)).Decode(&problem); err != nil || problem.Code == "" || problem.Error == "" {
		problem.Code, problem.Error = "download_failed", fmt.Sprintf("node file response %d", response.StatusCode)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	files.Error(w, status, problem.Code, problem.Error)
}

func (s *Server) downloadEvent(task *downloadTask) {
	s.filesMu.Lock()
	exists := s.downloads[task.data.ID] == task
	snapshot := task.data
	s.filesMu.Unlock()
	if exists {
		if err := s.fileEvent(task.owner, fileEvent{Type: "download", NodeID: snapshot.NodeID, Download: &snapshot}); err != nil {
			task.owner.conn.CloseNow()
		}
	}
}
func (s *Server) failDownload(task *downloadTask, err error) {
	s.filesMu.Lock()
	if s.downloads[task.data.ID] != task || task.data.State == "completed" || task.data.State == "failed" {
		s.filesMu.Unlock()
		return
	}
	task.data.State, task.data.Error = "failed", err.Error()
	s.filesMu.Unlock()
	task.stopPeer()
	s.downloadEvent(task)
}
func (s *Server) createDownload(w http.ResponseWriter, r *http.Request, nodeID string) {
	owner := s.requestFileOwner(w, r)
	if owner == nil {
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &body) {
		return
	}
	s.mu.Lock()
	p := s.peers[nodeID]
	s.mu.Unlock()
	if p == nil {
		files.Error(w, 409, "node_offline", "node offline")
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stopOwner := context.AfterFunc(owner.ctx, cancel)
	defer stopOwner()
	response, err := s.agentFileRequest(ctx, nodeID, "GET", "stat?path="+url.QueryEscape(body.Path), owner.id, nil, nil, 0)
	if err != nil {
		files.Error(w, 502, "node_unavailable", err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		downloadError(w, response)
		return
	}
	var entry files.Entry
	if err := json.NewDecoder(io.LimitReader(response.Body, protocol.MaxMessage)).Decode(&entry); err != nil {
		files.Error(w, 502, "invalid_response", err.Error())
		return
	}
	if entry.Type != "file" || entry.Size == nil {
		files.Error(w, 400, "unsupported", "browser downloads require a regular file; use probexd cp -r for a directory")
		return
	}
	taskCtx, taskCancel := context.WithCancel(owner.ctx)
	task := &downloadTask{data: download{ID: protocol.RandomID(), NodeID: nodeID, Path: entry.Path, Size: *entry.Size, State: "queued"}, owner: owner, ctx: taskCtx, cancel: taskCancel}
	s.filesMu.Lock()
	if owner.ctx.Err() != nil {
		s.filesMu.Unlock()
		taskCancel()
		files.Error(w, 410, "cancelled", "file session ended")
		return
	}
	owner.nodes[nodeID] = true
	s.downloads[task.data.ID] = task
	task.stopPeer = context.AfterFunc(p.ctx, func() {
		s.filesMu.Lock()
		if s.downloads[task.data.ID] == task && task.data.State != "completed" {
			task.data.State, task.data.Error = "failed", "node disconnected"
		}
		s.filesMu.Unlock()
		task.cancel()
		s.downloadEvent(task)
	})
	snapshot := task.data
	s.filesMu.Unlock()
	files.JSON(w, 201, snapshot)
	s.downloadEvent(task)
}
func (s *Server) downloadAPI(w http.ResponseWriter, r *http.Request, path string) {
	owner := s.requestFileOwner(w, r)
	if owner == nil {
		return
	}
	id, action, _ := strings.Cut(path, "/")
	s.filesMu.Lock()
	task := s.downloads[id]
	if task == nil || task.owner != owner {
		s.filesMu.Unlock()
		files.Error(w, 404, "not_found", "download not found for this file session")
		return
	}
	if action == "" && r.Method == "HEAD" {
		snapshot := task.data
		s.filesMu.Unlock()
		w.Header().Set("Download-Offset", fmt.Sprint(snapshot.Offset))
		w.Header().Set("Download-Length", fmt.Sprint(snapshot.Size))
		w.Header().Set("Download-State", snapshot.State)
		w.WriteHeader(200)
		return
	}
	if action == "" && r.Method == "DELETE" {
		delete(s.downloads, id)
		s.filesMu.Unlock()
		task.cancel()
		task.stopPeer()
		files.JSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if task.data.State == "failed" || task.data.State == "completed" || task.ctx.Err() != nil {
		s.filesMu.Unlock()
		files.Error(w, 409, "transfer_ended", "download ended; create a new download to start again")
		return
	}
	if r.Method == "POST" && (action == "pause" || action == "resume") {
		if action == "pause" && task.resume == nil {
			task.resume = make(chan struct{})
			task.data.State = "paused"
		}
		if action == "resume" && task.resume != nil {
			close(task.resume)
			task.resume = nil
			if task.claimed {
				task.data.State = "downloading"
			} else {
				task.data.State = "queued"
			}
		}
		snapshot := task.data
		s.filesMu.Unlock()
		files.JSON(w, 200, snapshot)
		s.downloadEvent(task)
		return
	}
	if r.Method != "GET" || action != "" {
		s.filesMu.Unlock()
		files.Error(w, 405, "invalid_request", "method not allowed")
		return
	}
	if task.claimed || r.Header.Get("Range") != "" {
		s.filesMu.Unlock()
		files.Error(w, 409, "transfer_ended", "download response cannot be reopened or resumed; create a new download")
		return
	}
	task.claimed = true
	if task.resume == nil {
		task.data.State = "downloading"
	}
	s.filesMu.Unlock()
	s.serveDownload(w, r, task)
}
func (s *Server) waitDownload(ctx context.Context, task *downloadTask) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.filesMu.Lock()
		resume := task.resume
		s.filesMu.Unlock()
		if resume == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-resume:
		}
	}
}
func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request, task *downloadTask) {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(task.ctx, cancel)
	defer stop()
	controller := http.NewResponseController(w)
	stopIO := context.AfterFunc(ctx, func() {
		if err := controller.SetWriteDeadline(time.Now()); err != nil {
			log.Printf("cancel download response: %v", err)
		}
	})
	defer stopIO()
	s.filesMu.Lock()
	snapshot := task.data
	s.filesMu.Unlock()
	response, err := s.agentFileRequest(ctx, snapshot.NodeID, "GET", "file?path="+url.QueryEscape(snapshot.Path), task.owner.id, nil, nil, 0)
	if err != nil {
		s.failDownload(task, err)
		files.Error(w, 502, "node_unavailable", err.Error())
		return
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		s.failDownload(task, fmt.Errorf("node file response %d", response.StatusCode))
		downloadError(w, response)
		return
	}
	if response.ContentLength != snapshot.Size {
		err = errors.New("file length changed before download; create a new download")
		s.failDownload(task, err)
		files.Error(w, 409, "file_changed", err.Error())
		return
	}
	fileAttachment(w, snapshot.Path)
	w.Header().Set("Content-Length", fmt.Sprint(snapshot.Size))
	w.WriteHeader(200)
	if err := controller.Flush(); err != nil {
		s.failDownload(task, err)
		return
	}
	s.downloadEvent(task)
	lastEvent := time.Now()
	data := make([]byte, 32<<10)
	for {
		if err = s.waitDownload(ctx, task); err != nil {
			break
		}
		n, readErr := response.Body.Read(data)
		if n > 0 {
			if err = s.waitDownload(ctx, task); err != nil {
				break
			}
			var written int
			written, err = w.Write(data[:n])
			s.filesMu.Lock()
			task.data.Offset += int64(written)
			finished := task.data.Offset == task.data.Size
			s.filesMu.Unlock()
			if err == nil {
				err = controller.Flush()
			}
			if err != nil {
				break
			}
			if finished {
				break
			}
			if time.Since(lastEvent) >= 200*time.Millisecond {
				s.downloadEvent(task)
				lastEvent = time.Now()
			}
		}
		if readErr != nil {
			if readErr != io.EOF {
				err = readErr
			}
			break
		}
	}
	if err != nil {
		s.failDownload(task, err)
		return
	}
	s.filesMu.Lock()
	if task.data.Offset != task.data.Size {
		s.filesMu.Unlock()
		s.failDownload(task, io.ErrUnexpectedEOF)
		return
	}
	if s.downloads[task.data.ID] == task && task.ctx.Err() == nil {
		task.data.State = "completed"
	}
	s.filesMu.Unlock()
	task.stopPeer()
	s.downloadEvent(task)
}
