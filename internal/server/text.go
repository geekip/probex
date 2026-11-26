package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"probex/internal/files"
	"probex/internal/protocol"
	"time"
)

const textWriteLimit = 2 << 20
const textReadLimit = 10 << 20

type textDocument struct {
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	Text         string `json:"text"`
	Encoding     string `json:"encoding"`
	BOM          bool   `json:"bom"`
	Version      string `json:"version"`
	Readonly     bool   `json:"readonly"`
	DownloadOnly bool   `json:"download_only"`
}
type fileAPIError struct {
	status        int
	code, message string
}

func (e *fileAPIError) Error() string { return e.message }
func writeFileFailure(w http.ResponseWriter, err error) {
	status, code := 500, "io_error"
	var remote *fileAPIError
	if errors.As(err, &remote) {
		status, code = remote.status, remote.code
	}
	files.Error(w, status, code, err.Error())
}
func agentFileStatus(response *http.Response) error {
	if response.StatusCode < 300 {
		return nil
	}
	var failure struct {
		Code    string `json:"code"`
		Message string `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, protocol.MaxMessage)).Decode(&failure); err != nil {
		return &fileAPIError{502, "invalid_response", fmt.Sprintf("invalid node error response: %v", err)}
	}
	return &fileAPIError{response.StatusCode, failure.Code, failure.Message}
}
func (s *Server) agentFileJSON(ctx context.Context, node, owner, method, path string, data []byte, result any) error {
	headers := http.Header{"Content-Type": []string{"application/json"}}
	if method == "PATCH" {
		headers.Set("Content-Type", "application/octet-stream")
		headers.Set("Upload-Offset", "0")
	}
	response, err := s.agentFileRequest(ctx, node, method, path, owner, headers, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return &fileAPIError{502, "node_unavailable", err.Error()}
	}
	defer response.Body.Close()
	if err := agentFileStatus(response); err != nil {
		return err
	}
	if result != nil {
		if err := json.NewDecoder(io.LimitReader(response.Body, protocol.MaxMessage)).Decode(result); err != nil {
			return &fileAPIError{502, "invalid_response", err.Error()}
		}
	}
	return nil
}
func textVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return "\"" + hex.EncodeToString(sum[:]) + "\""
}

func (s *Server) textAPI(w http.ResponseWriter, r *http.Request, node string) {
	if r.Method != "GET" && r.Method != "PUT" {
		files.Error(w, 405, "invalid_request", "method not allowed")
		return
	}
	owner := s.requestFileOwner(w, r)
	if owner == nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(owner.ctx, cancel)
	defer stop()
	s.mu.Lock()
	peer := s.peers[node]
	s.mu.Unlock()
	if peer == nil {
		files.Error(w, 409, "node_offline", "node offline")
		return
	}
	stopPeer := context.AfterFunc(peer.ctx, cancel)
	defer stopPeer()
	writeFailure := func(err error) {
		switch {
		case owner.ctx.Err() != nil:
			files.Error(w, 410, "cancelled", "file session ended")
		case peer.ctx.Err() != nil:
			files.Error(w, 409, "node_offline", "node offline")
		default:
			writeFileFailure(w, err)
		}
	}
	controller := http.NewResponseController(w)
	writeDocument := func(document textDocument) {
		if ctx.Err() != nil || owner.ctx.Err() != nil || peer.ctx.Err() != nil {
			writeFailure(context.Canceled)
			return
		}
		stopWrite := context.AfterFunc(ctx, func() {
			if err := controller.SetWriteDeadline(time.Now()); err != nil {
				log.Printf("cancel text response: %v", err)
			}
		})
		defer stopWrite()
		files.JSON(w, 200, document)
	}
	s.filesMu.Lock()
	if owner.ctx.Err() != nil {
		s.filesMu.Unlock()
		files.Error(w, 410, "cancelled", "file session ended")
		return
	}
	owner.nodes[node] = true
	s.filesMu.Unlock()
	var entry files.Entry
	if err := s.agentFileJSON(ctx, node, owner.id, "GET", "resolve?path="+url.QueryEscape(r.URL.Query().Get("path")), nil, &entry); err != nil {
		writeFailure(err)
		return
	}
	if entry.Type != "file" || entry.Size == nil {
		files.Error(w, 415, "unsupported", "text editing requires a regular file")
		return
	}
	if r.Method == "GET" {
		document, err := s.readText(ctx, node, owner.id, entry, r.URL.Query().Get("encoding"))
		if err != nil {
			writeFailure(err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if document.Version != "" {
			w.Header().Set("ETag", document.Version)
		}
		writeDocument(document)
		return
	}
	var body struct {
		Text      string `json:"text"`
		Encoding  string `json:"encoding"`
		BOM       bool   `json:"bom"`
		Version   string `json:"version"`
		Overwrite bool   `json:"overwrite"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 6*textWriteLimit+(64<<10)))
	decoder.DisallowUnknownFields()
	stopRead := context.AfterFunc(ctx, func() {
		if err := controller.SetReadDeadline(time.Now()); err != nil {
			log.Printf("cancel text request: %v", err)
		}
	})
	decodeErr := decoder.Decode(&body)
	stopRead()
	if decodeErr != nil {
		var maximum *http.MaxBytesError
		if ctx.Err() != nil {
			writeFailure(decodeErr)
		} else if errors.As(decodeErr, &maximum) {
			files.Error(w, 413, "text_too_large", decodeErr.Error())
		} else {
			files.Error(w, 400, "invalid_request", decodeErr.Error())
		}
		return
	}
	if !body.Overwrite && body.Version == "" {
		files.Error(w, 400, "version_required", "save requires the original file version")
		return
	}
	if *entry.Size > textWriteLimit {
		files.Error(w, 413, "read_only", "files larger than 2MiB are read-only")
		return
	}
	data, err := encodeText(body.Text, body.Encoding, body.BOM)
	if err != nil {
		files.Error(w, 415, "invalid_text", err.Error())
		return
	}
	if len(data) > textWriteLimit {
		files.Error(w, 413, "text_too_large", "encoded text exceeds 2MiB")
		return
	}
	version := body.Version
	if body.Overwrite {
		version = ""
	}
	if err := s.saveText(ctx, node, owner, entry.Path, data, version); err != nil {
		writeFailure(err)
		return
	}
	writeDocument(textDocument{Path: entry.Path, Size: int64(len(data)), Text: body.Text, Encoding: body.Encoding, BOM: body.BOM, Version: textVersion(data)})
}
func (s *Server) readText(ctx context.Context, node, owner string, entry files.Entry, encoding string) (textDocument, error) {
	document := textDocument{Path: entry.Path, Size: *entry.Size, Readonly: *entry.Size > textWriteLimit, DownloadOnly: *entry.Size > textReadLimit}
	if document.DownloadOnly {
		return document, nil
	}
	if encoding == "" {
		encoding = "auto"
	}
	response, err := s.agentFileRequest(ctx, node, "GET", "file?path="+url.QueryEscape(entry.Path), owner, nil, nil, 0)
	if err != nil {
		return document, &fileAPIError{502, "node_unavailable", err.Error()}
	}
	defer response.Body.Close()
	if err := agentFileStatus(response); err != nil {
		return document, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, textReadLimit+1))
	if err != nil {
		return document, &fileAPIError{502, "read_failed", err.Error()}
	}
	if len(data) > textReadLimit {
		return document, &fileAPIError{413, "text_too_large", "file grew beyond the 10MiB read limit"}
	}
	if int64(len(data)) != document.Size {
		return document, &fileAPIError{409, "file_changed", "file size changed while opening; load again"}
	}
	document.Text, document.Encoding, document.BOM, err = decodeText(data, encoding)
	if err != nil {
		return document, &fileAPIError{415, "invalid_text", err.Error()}
	}
	document.Version = textVersion(data)
	return document, nil
}
func (s *Server) saveText(ctx context.Context, node string, owner *fileOwner, path string, data []byte, version string) (failure error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	id := protocol.RandomID()
	request := files.UploadRequest{ID: id, Path: path, Size: int64(len(data)), Overwrite: true, IfMatch: version, PreserveLinks: true}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	// Creation is owner-bound so cancellation cannot lose a newly allocated temporary file.
	defer func() {
		if failure == nil || owner.ctx.Err() != nil {
			return
		}
		cleanup, cancel := context.WithTimeout(owner.ctx, 5*time.Second)
		defer cancel()
		err := s.agentFileJSON(cleanup, node, owner.id, "DELETE", "uploads/"+id, nil, nil)
		var remote *fileAPIError
		if errors.As(err, &remote) && remote.status == 404 && remote.code == "not_found" {
			return
		}
		if err != nil {
			failure = errors.Join(failure, fmt.Errorf("cancel temporary text upload: %w", err))
		}
	}()
	var upload files.Upload
	if err := s.agentFileJSON(owner.ctx, node, owner.id, "POST", "uploads", body, &upload); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) > 0 {
		if err := s.agentFileJSON(ctx, node, owner.id, "PATCH", "uploads/"+id, data, &upload); err != nil {
			return err
		}
	}
	if err := s.agentFileJSON(ctx, node, owner.id, "POST", "uploads/"+id+"/commit", nil, &upload); err != nil {
		return err
	}
	if upload.State != "completed" || upload.Offset != int64(len(data)) {
		return &fileAPIError{502, "invalid_response", "node did not confirm text commit"}
	}
	return nil
}
