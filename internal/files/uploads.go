package files

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"probex/internal/protocol"
	"strconv"
	"strings"
	"sync"
)

type upload struct {
	owner string
	mu    sync.Mutex
	Upload
	file          *os.File
	temporary     string
	overwrite     bool
	ifMatch       string
	preserveLinks bool
}

func (u *upload) cancel() error {
	if u.State == "completed" || u.State == "cancelled" {
		return nil
	}
	var closeErr error
	if u.file != nil {
		closeErr = u.file.Close()
		u.file = nil
	}
	removeErr := os.Remove(u.temporary)
	err := errors.Join(closeErr, removeErr)
	if err == nil {
		u.State = "cancelled"
	} else {
		u.State = "failed"
	}
	return err
}

func uploadPath(body UploadRequest) (string, error) {
	if body.Relative != "" {
		if body.Path != "" || body.Directory == "" || !filepath.IsLocal(body.Relative) {
			return "", errors.New("relative upload path must stay inside its directory")
		}
		return absolute(filepath.Join(body.Directory, body.Relative))
	}
	if body.Path == "" {
		return "", errors.New("upload path required")
	}
	return absolute(body.Path)
}
func (s *Service) createUpload(w http.ResponseWriter, r *http.Request) {
	var body UploadRequest
	if !decode(w, r, &body) {
		return
	}
	p, err := uploadPath(body)
	if err != nil || body.Size < 0 {
		Error(w, 400, "invalid_request", "invalid upload path or size")
		return
	}
	info, err := os.Lstat(p)
	if err == nil {
		if !info.Mode().IsRegular() {
			Error(w, 415, "unsupported", "upload target must be a regular file")
			return
		}
		if !body.Overwrite {
			Error(w, 409, "exists", "destination exists")
			return
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		fail(w, err)
		return
	}
	f, err := os.CreateTemp(filepath.Dir(p), ".probex-upload-*")
	if err != nil {
		fail(w, err)
		return
	}
	id := body.ID
	if id == "" {
		id = protocol.RandomID()
	}
	u := &upload{owner: r.Header.Get("X-Probex-File-Owner"), Upload: Upload{ID: id, Path: p, Size: body.Size, State: "uploading"}, file: f, temporary: f.Name(), overwrite: body.Overwrite, ifMatch: body.IfMatch, preserveLinks: body.PreserveLinks}
	s.mu.Lock()
	if s.closed || s.deadOwners[u.owner] || s.uploads[u.ID] != nil {
		s.mu.Unlock()
		if err := u.cancel(); err != nil {
			fail(w, err)
			return
		}
		Error(w, 410, "cancelled", "file owner ended or upload already exists")
		return
	}
	s.uploads[u.ID] = u
	s.mu.Unlock()
	JSON(w, 201, u.Upload)
}
func (s *Service) uploadAPI(w http.ResponseWriter, r *http.Request, path string) {
	id, action, _ := strings.Cut(path, "/")
	s.mu.Lock()
	u := s.uploads[id]
	s.mu.Unlock()
	if u == nil {
		Error(w, 404, "not_found", "upload not found")
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if r.Header.Get("X-Probex-File-Owner") != u.owner {
		Error(w, 403, "unauthorized", "upload owner mismatch")
		return
	}
	if r.Method == "HEAD" && action == "" {
		w.Header().Set("Upload-Offset", strconv.FormatInt(u.Offset, 10))
		w.Header().Set("Upload-Length", strconv.FormatInt(u.Size, 10))
		w.Header().Set("Upload-State", u.State)
		w.WriteHeader(200)
		return
	}
	if r.Method == "DELETE" && action == "" {
		if err := u.cancel(); err != nil {
			fail(w, err)
			return
		}
		s.mu.Lock()
		delete(s.uploads, id)
		s.mu.Unlock()
		JSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if u.State != "uploading" {
		Error(w, 409, "invalid_state", "upload is "+u.State)
		return
	}
	if r.Method == "PATCH" && action == "" {
		offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
		if err != nil || offset != u.Offset {
			Error(w, 409, "offset_conflict", "upload offset does not match")
			return
		}
		if r.ContentLength < 0 || r.ContentLength > UploadChunk || r.ContentLength > u.Size-u.Offset {
			Error(w, 400, "invalid_request", "chunk length is unknown or exceeds remaining size/8MiB")
			return
		}
		n, err := io.CopyN(u.file, r.Body, r.ContentLength)
		u.Offset += n
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Upload-Offset", strconv.FormatInt(u.Offset, 10))
		JSON(w, 200, u.Upload)
		return
	}
	if r.Method == "POST" && action == "commit" {
		if u.Offset != u.Size {
			Error(w, 409, "size_mismatch", "upload is incomplete")
			return
		}
		s.mutations.Lock()
		defer s.mutations.Unlock()
		info, err := os.Lstat(u.Path)
		if err == nil && (!u.overwrite || !info.Mode().IsRegular()) {
			Error(w, 409, "exists", "destination already exists or is not a regular file")
			return
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			fail(w, err)
			return
		}
		if u.ifMatch != "" {
			version, err := FileHash(u.Path)
			if err != nil {
				fail(w, err)
				return
			}
			if version != u.ifMatch {
				Error(w, 409, "version_conflict", "file changed since it was opened")
				return
			}
		}
		var count uint64
		if u.preserveLinks && info != nil {
			count, err = hardLinks(u.Path, info)
			if err != nil {
				fail(w, err)
				return
			}
		}
		if info != nil && count <= 1 {
			if err := preserveOwner(u.file, info); err != nil {
				fail(w, err)
				return
			}
			if err := u.file.Chmod(info.Mode().Perm()); err != nil {
				fail(w, err)
				return
			}
			if err := preserveAccessACL(u.file, u.Path); err != nil {
				fail(w, err)
				return
			}
		}
		if err := u.file.Sync(); err != nil {
			fail(w, err)
			return
		}
		closeErr := u.file.Close()
		u.file = nil
		if closeErr != nil {
			u.State = "failed"
			fail(w, closeErr)
			return
		}
		if count > 1 {
			err = commitLinkedFile(u.temporary, u.Path)
		} else {
			err = os.Rename(u.temporary, u.Path)
		}
		if err != nil {
			u.State = "failed"
			fail(w, err)
			return
		}
		u.State = "completed"
		JSON(w, 200, u.Upload)
		return
	}
	Error(w, 405, "invalid_request", "method not allowed")
}
func FileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return "\"" + hex.EncodeToString(h.Sum(nil)) + "\"", nil
}

func (s *Service) cancelOwner(w http.ResponseWriter, r *http.Request, id string) {
	if id == "" {
		Error(w, 400, "invalid_request", "owner required")
		return
	}
	s.mu.Lock()
	s.deadOwners[id] = true
	var list []*upload
	for _, u := range s.uploads {
		if u.owner == id {
			list = append(list, u)
		}
	}
	s.mu.Unlock()
	var failures []error
	for _, u := range list {
		u.mu.Lock()
		err := u.cancel()
		u.mu.Unlock()
		if err != nil {
			failures = append(failures, err)
		} else {
			s.mu.Lock()
			delete(s.uploads, u.ID)
			s.mu.Unlock()
		}
	}
	if err := errors.Join(failures...); err != nil {
		fail(w, err)
		return
	}
	JSON(w, 200, map[string]bool{"ok": true})
}
