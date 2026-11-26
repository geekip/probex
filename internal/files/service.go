// Package files implements the Agent's authenticated loopback filesystem service.
package files

import (
	"cmp"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"probex/internal/protocol"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const UploadChunk = 8 << 20

type Entry struct {
	Name           string     `json:"name"`
	Path           string     `json:"path"`
	Type           string     `json:"type"`
	Size           *int64     `json:"size,omitempty"`
	Modified       *time.Time `json:"mtime,omitempty"`
	Mode           *string    `json:"mode,omitempty"`
	UID            *uint32    `json:"uid,omitempty"`
	GID            *uint32    `json:"gid,omitempty"`
	Owner          *string    `json:"owner,omitempty"`
	Group          *string    `json:"group,omitempty"`
	LinkKind       string     `json:"link_kind,omitempty"`
	LinkTargetType string     `json:"link_target_type,omitempty"`
	LinkError      string     `json:"link_error,omitempty"`
}
type Listing struct {
	Path        string  `json:"path"`
	Entries     []Entry `json:"entries"`
	Next        string  `json:"next_cursor"`
	Files       int     `json:"file_count"`
	Directories int     `json:"directory_count"`
}
type UploadRequest struct {
	ID            string `json:"id,omitempty"`
	Path          string `json:"path"`
	Directory     string `json:"directory,omitempty"`
	Relative      string `json:"relative,omitempty"`
	Size          int64  `json:"size"`
	Overwrite     bool   `json:"overwrite"`
	IfMatch       string `json:"if_match,omitempty"`
	PreserveLinks bool   `json:"preserve_links,omitempty"`
}
type Upload struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	State  string `json:"state"`
}
type Service struct {
	closed       bool
	deadOwners   map[string]bool
	Address, Key string
	server       *http.Server
	listener     net.Listener
	done         chan error
	mu           sync.Mutex
	mutations    sync.Mutex
	uploads      map[string]*upload
}

func New() (*Service, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s := &Service{Address: l.Addr().String(), Key: protocol.RandomID(), listener: l, done: make(chan error, 1), uploads: map[string]*upload{}, deadOwners: map[string]bool{}}
	s.server = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() { s.done <- s.server.Serve(l) }()
	return s, nil
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	err := s.server.Close()
	serveErr := <-s.done
	s.mu.Lock()
	list := s.uploads
	s.uploads = map[string]*upload{}
	s.mu.Unlock()
	for _, u := range list {
		u.mu.Lock()
		if err := u.cancel(); err != nil {
			log.Printf("cancel upload: %v", err)
		}
		u.mu.Unlock()
	}
	if err != nil {
		return err
	}
	if !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}
func JSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		log.Printf("file response: %v", err)
	}
}
func Error(w http.ResponseWriter, status int, code, message string) {
	JSON(w, status, map[string]string{"code": code, "error": message})
}
func fail(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, os.ErrInvalid):
		Error(w, 400, "invalid_request", err.Error())
	case errors.Is(err, os.ErrNotExist):
		Error(w, 404, "not_found", err.Error())
	case errors.Is(err, os.ErrPermission):
		Error(w, 403, "permission_denied", err.Error())
	case errors.Is(err, os.ErrExist):
		Error(w, 409, "exists", err.Error())
	case spaceError(err):
		Error(w, 507, "no_space", err.Error())
	default:
		Error(w, 500, "io_error", err.Error())
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		Error(w, 400, "invalid_request", err.Error())
		return false
	}
	return true
}
func absolute(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("path is required: %w", os.ErrInvalid)
	}
	return filepath.Abs(path)
}
func kind(info fs.FileInfo) string {
	if info.Mode()&os.ModeSymlink != 0 {
		return "symlink"
	}
	if info.IsDir() {
		return "directory"
	}
	if info.Mode().IsRegular() {
		return "file"
	}
	return "special"
}
func entry(path string, info fs.FileInfo, fields string) Entry {
	e := Entry{Name: info.Name(), Path: path, Type: kind(info)}
	if e.Type == "symlink" {
		e.LinkKind = "symlink"
	} else if e.Type == "file" && isShortcut(path) {
		e.LinkKind = "shortcut"
	}
	wanted := "," + fields + ","
	has := func(name string) bool { return strings.Contains(wanted, ","+name+",") }
	if has("size") {
		size := info.Size()
		e.Size = &size
	}
	if has("mtime") {
		stamp := info.ModTime()
		e.Modified = &stamp
	}
	metadata(&e, info, has)
	return e
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.Key)) != 1 {
		Error(w, 403, "unauthorized", "invalid file service credentials")
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimPrefix(r.URL.Path, "/")
	if strings.HasPrefix(path, "owners/") && r.Method == "DELETE" {
		s.cancelOwner(w, r, strings.TrimPrefix(path, "owners/"))
		return
	}
	if path == "uploads" && r.Method == "POST" {
		s.createUpload(w, r)
		return
	}
	if strings.HasPrefix(path, "uploads/") {
		s.uploadAPI(w, r, strings.TrimPrefix(path, "uploads/"))
		return
	}
	if path == "files" && r.Method == "GET" {
		s.list(w, r)
		return
	}
	if path == "stat" && r.Method == "GET" {
		p, err := absolute(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, err)
			return
		}
		info, err := os.Lstat(p)
		if err != nil {
			fail(w, err)
			return
		}
		JSON(w, 200, entry(p, info, "size,mtime,mode,owner,group"))
		return
	}
	if path == "links" {
		s.links(w, r)
		return
	}
	if path == "resolve" && r.Method == "GET" {
		e, err := Resolve(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, err)
			return
		}
		JSON(w, 200, e)
		return
	}
	if path == "file" && (r.Method == "GET" || r.Method == "HEAD") {
		s.read(w, r)
		return
	}
	s.mutations.Lock()
	defer s.mutations.Unlock()
	if (path == "files" || path == "directories") && r.Method == "POST" {
		var body struct {
			Path string `json:"path"`
		}
		if !decode(w, r, &body) {
			return
		}
		p, err := absolute(body.Path)
		if err != nil {
			fail(w, err)
			return
		}
		if path == "directories" {
			err = os.Mkdir(p, 0755)
		} else {
			var f *os.File
			f, err = os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
			if err == nil {
				err = f.Close()
			}
		}
		if err != nil {
			fail(w, err)
			return
		}
		JSON(w, 201, map[string]string{"path": p})
		return
	}
	if path == "files/copy" && r.Method == "POST" {
		s.copy(w, r)
		return
	}
	if path == "files/rename" && r.Method == "POST" {
		var body struct {
			Path        string `json:"path"`
			Destination string `json:"destination"`
		}
		if !decode(w, r, &body) {
			return
		}
		from, err := absolute(body.Path)
		if err != nil {
			fail(w, err)
			return
		}
		to, err := absolute(body.Destination)
		if err != nil {
			fail(w, err)
			return
		}
		if _, err := os.Lstat(to); err == nil {
			Error(w, 409, "exists", "destination exists")
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			fail(w, err)
			return
		}
		if err := os.Rename(from, to); err != nil {
			fail(w, err)
			return
		}
		JSON(w, 200, map[string]string{"path": to})
		return
	}
	if path == "file" && r.Method == "DELETE" {
		p, err := absolute(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, err)
			return
		}
		info, err := os.Lstat(p)
		if err != nil {
			fail(w, err)
			return
		}
		if info.IsDir() && r.URL.Query().Get("recursive") == "true" {
			err = os.RemoveAll(p)
		} else {
			err = os.Remove(p)
		}
		if err != nil {
			fail(w, err)
			return
		}
		JSON(w, 200, map[string]bool{"ok": true})
		return
	}
	Error(w, 405, "invalid_request", "method not allowed")
}
func (s *Service) list(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	raw := query.Get("path")
	if raw == "" && query.Get("initial") == "true" {
		var err error
		raw, err = os.UserHomeDir()
		if err != nil {
			fail(w, err)
			return
		}
	}
	if raw == "" {
		if roots, ok, err := roots(); ok {
			if err != nil {
				fail(w, err)
				return
			}
			JSON(w, 200, Listing{Entries: roots, Directories: len(roots)})
			return
		}
	}
	if raw == "" {
		raw = string(filepath.Separator)
	}
	p, err := absolute(raw)
	if err != nil {
		fail(w, err)
		return
	}
	limit := 200
	if value := query.Get("limit"); value != "" {
		limit, err = strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 500 {
			Error(w, 400, "invalid_request", "limit must be 1..500")
			return
		}
	}
	entries, err := os.ReadDir(p)
	if err != nil {
		fail(w, err)
		return
	}
	order := query.Get("sort")
	if order == "" {
		order = "name"
	}
	if !slices.Contains([]string{"name", "mtime", "type", "size", "mode", "owner"}, order) || query.Get("order") != "" && query.Get("order") != "asc" && query.Get("order") != "desc" {
		Error(w, 400, "invalid_request", "invalid sort field or order")
		return
	}
	result := Listing{Path: p, Entries: []Entry{}}
	filter := strings.ToLower(query.Get("query"))
	type item struct {
		entry Entry
		info  fs.FileInfo
		name  string
	}
	items := []item{}
	var sortFields string
	if order == "owner" {
		sortFields = "owner,group"
	}
	showHidden := query.Get("hidden") == "true"
	descending := query.Get("order") == "desc"
	for _, d := range entries {
		if !showHidden {
			hide, err := hidden(d)
			if err != nil {
				fail(w, err)
				return
			}
			if hide {
				continue
			}
		}
		name := strings.ToLower(d.Name())
		if !strings.Contains(name, filter) {
			continue
		}
		full := filepath.Join(p, d.Name())
		info, err := d.Info()
		if err != nil {
			fail(w, err)
			return
		}
		e := entry(full, info, sortFields)
		if e.LinkKind == "symlink" || e.LinkKind == "shortcut" {
			target, err := Resolve(full)
			if err != nil {
				e.LinkError = err.Error()
			} else if target.Type == "file" || target.Type == "directory" {
				e.LinkTargetType = target.Type
			}
		}
		if entryDirectory(e) {
			result.Directories++
		} else {
			result.Files++
		}
		items = append(items, item{e, info, name})
	}
	slices.SortFunc(items, func(a, b item) int {
		if entryDirectory(a.entry) != entryDirectory(b.entry) {
			if entryDirectory(a.entry) {
				return -1
			}
			return 1
		}
		c := 0
		switch order {
		case "mtime":
			c = a.info.ModTime().Compare(b.info.ModTime())
		case "size":
			c = cmp.Compare(a.info.Size(), b.info.Size())
		case "mode":
			c = cmp.Compare(a.info.Mode().String(), b.info.Mode().String())
		case "type":
			c = cmp.Compare(entryType(a.entry), entryType(b.entry))
		case "owner":
			c = cmp.Compare(entryOwner(a.entry), entryOwner(b.entry))
		}
		if c == 0 {
			c = cmp.Compare(a.name, b.name)
		}
		if c == 0 {
			c = cmp.Compare(a.entry.Name, b.entry.Name)
		}
		if descending {
			return -c
		}
		return c
	})
	start := 0
	if cursor := query.Get("cursor"); cursor != "" {
		index := slices.IndexFunc(items, func(i item) bool { return i.entry.Name == cursor })
		if index < 0 {
			Error(w, 409, "invalid_cursor", "directory changed; refresh the listing")
			return
		}
		start = index + 1
	}
	end := min(start+limit, len(items))
	fields := query.Get("fields")
	for _, i := range items[start:end] {
		// Sorting can require metadata that the caller did not request.
		e := entry(i.entry.Path, i.info, fields)
		e.LinkTargetType, e.LinkError = i.entry.LinkTargetType, i.entry.LinkError
		result.Entries = append(result.Entries, e)
	}
	if end < len(items) {
		result.Next = items[end-1].entry.Name
	}

	JSON(w, 200, result)
}
func (s *Service) read(w http.ResponseWriter, r *http.Request) {
	p, err := absolute(r.URL.Query().Get("path"))
	if err != nil {
		fail(w, err)
		return
	}
	info, err := os.Stat(p)
	if err != nil {
		fail(w, err)
		return
	}
	if !info.Mode().IsRegular() {
		Error(w, 415, "unsupported", "only regular files can be read")
		return
	}
	f, err := os.Open(p)
	if err != nil {
		fail(w, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": info.Name()}))
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func entryDirectory(e Entry) bool { return e.Type == "directory" || e.LinkTargetType == "directory" }
func entryType(e Entry) string {
	if entryDirectory(e) {
		return "directory"
	}
	if e.Type != "file" {
		return e.Type
	}
	return strings.ToLower(filepath.Ext(e.Name))
}
func entryOwner(e Entry) string {
	owner, group := "", ""
	if e.Owner != nil {
		owner = *e.Owner
	} else if e.UID != nil {
		owner = strconv.FormatUint(uint64(*e.UID), 10)
	}
	if e.Group != nil {
		group = *e.Group
	} else if e.GID != nil {
		group = strconv.FormatUint(uint64(*e.GID), 10)
	}
	return owner + "/" + group
}
