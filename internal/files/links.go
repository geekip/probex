package files

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"probex/internal/protocol"
)

type Link struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	Hard   bool   `json:"hard"`
}

func inspectLink(path string) (Link, error) {
	l := Link{Path: path, Name: filepath.Base(path)}
	info, err := os.Lstat(path)
	if err != nil {
		return l, err
	}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		l.Kind = "symlink"
		l.Target, err = os.Readlink(path)
	case info.Mode().IsRegular():
		var shortcut bool
		shortcut, err = shortcutFile(path)
		if err != nil {
			return l, err
		}
		if shortcut {
			l.Kind = "shortcut"
			l.Target, err = shortcutTarget(path)
			return l, err
		}
		var count uint64
		count, err = hardLinks(path, info)
		if count > 1 {
			l.Kind, l.Hard = "hard", true
		}
	}
	return l, err
}

// Resolve follows filesystem links and Shell shortcuts without executing them.
func Resolve(path string) (Entry, error) {
	p, err := absolute(path)
	if err != nil {
		return Entry{}, err
	}
	seen := map[string]bool{}
	for {
		p, err = filepath.EvalSymlinks(p)
		if err != nil {
			return Entry{}, err
		}
		info, err := os.Stat(p)
		if err != nil {
			return Entry{}, err
		}
		shortcut := false
		if info.Mode().IsRegular() {
			shortcut, err = shortcutFile(p)
			if err != nil {
				return Entry{}, err
			}
		}
		if !shortcut {
			return entry(p, info, "size,mtime,mode,owner,group"), nil
		}
		if seen[p] {
			return Entry{}, fmt.Errorf("shortcut cycle at %s: %w", p, os.ErrInvalid)
		}
		seen[p] = true
		target, err := shortcutTarget(p)
		if err != nil {
			return Entry{}, err
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(p), target)
		}
		p = target
	}
}

func (s *Service) links(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		p, err := absolute(r.URL.Query().Get("path"))
		if err != nil {
			fail(w, err)
			return
		}
		l, err := inspectLink(p)
		if err != nil {
			fail(w, err)
			return
		}
		JSON(w, 200, l)
		return
	}
	if r.Method != "POST" && r.Method != "PUT" {
		Error(w, 405, "invalid_request", "method not allowed")
		return
	}
	var body struct {
		Path        string `json:"path"`
		Destination string `json:"destination"`
		Target      string `json:"target"`
		Hard        bool   `json:"hard"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := absolute(body.Path)
	if err != nil {
		fail(w, err)
		return
	}
	s.mutations.Lock()
	defer s.mutations.Unlock()
	destination := p
	var original Link
	if r.Method == "PUT" {
		original, err = inspectLink(p)
		if err != nil {
			fail(w, err)
			return
		}
		if original.Kind == "" {
			Error(w, 400, "invalid_request", "path is not a link")
			return
		}
		if body.Destination != "" {
			destination, err = absolute(body.Destination)
			if err != nil {
				fail(w, err)
				return
			}
		}
	}
	if r.Method == "POST" || destination != p {
		if _, err := os.Lstat(destination); err == nil {
			Error(w, 409, "exists", "destination exists")
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			fail(w, err)
			return
		}
	}
	target := body.Target
	// Hard links have no distinguished original pathname. Empty target keeps
	// the current shared file when only renaming an existing hard link.
	if target == "" && body.Hard && original.Hard {
		target = p
	}
	if target == "" {
		Error(w, 400, "invalid_request", "target path is required")
		return
	}
	temp := filepath.Join(filepath.Dir(destination), ".probex-link-"+protocol.RandomID())
	if !body.Hard && isShortcut(destination) {
		temp += ".lnk"
	}
	defer func() {
		if err := os.Remove(temp); err != nil && !errors.Is(err, os.ErrNotExist) {
			log.Printf("remove temporary link %s: %v", temp, err)
		}
	}()
	if err := createLink(temp, target, destination, body.Hard, original); err != nil {
		fail(w, err)
		return
	}
	if err := replaceLink(temp, destination, r.Method == "PUT" && destination == p); err != nil {
		fail(w, err)
		return
	}
	if r.Method == "PUT" && destination != p {
		if err := os.Remove(p); err != nil {
			fail(w, err)
			return
		}
	}
	status := 201
	if r.Method == "PUT" {
		status = 200
	}
	JSON(w, status, map[string]string{"path": destination})
}

func createLink(temp, target, destination string, hard bool, original Link) error {
	if hard {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(destination), target)
		}
		resolved, err := Resolve(target)
		if err != nil {
			return err
		}
		if resolved.Type != "file" {
			return fmt.Errorf("hard links require a regular file: %w", os.ErrInvalid)
		}
		return os.Link(resolved.Path, temp)
	}
	if isShortcut(destination) {
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(destination), target)
		}
		if original.Kind == "shortcut" {
			data, err := os.ReadFile(original.Path)
			if err != nil {
				return err
			}
			if err := os.WriteFile(temp, data, 0600); err != nil {
				return err
			}
		}
		return writeShortcut(temp, target)
	}
	return os.Symlink(target, temp)
}

// Renaming a replacement would detach a hard link from its sibling names.
func commitLinkedFile(temp, path string) (err error) {
	source, err := os.Open(temp)
	if err != nil {
		return err
	}
	defer func() {
		if source != nil {
			err = errors.Join(err, source.Close())
		}
	}()
	destination, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, destination.Close()) }()
	backup, err := os.CreateTemp(filepath.Dir(path), ".probex-backup-*")
	if err != nil {
		return err
	}
	keepBackup := false
	defer func() {
		err = errors.Join(err, backup.Close())
		if !keepBackup {
			err = errors.Join(err, os.Remove(backup.Name()))
		}
	}()
	if _, err := io.Copy(backup, destination); err != nil {
		return err
	}
	if err := backup.Sync(); err != nil {
		return err
	}
	if err := writeLinkedContents(destination, source); err != nil {
		_, restoreErr := backup.Seek(0, io.SeekStart)
		if restoreErr == nil {
			restoreErr = writeLinkedContents(destination, backup)
		}
		if restoreErr != nil {
			keepBackup = true
			return errors.Join(err, fmt.Errorf("restore original failed; original contents saved at %s: %w", backup.Name(), restoreErr))
		}
		return fmt.Errorf("save failed; original contents restored: %w", err)
	}
	closeErr := source.Close()
	source = nil
	if closeErr != nil {
		return closeErr
	}
	return os.Remove(temp)
}

func writeLinkedContents(destination, source *os.File) error {
	if _, err := destination.Seek(0, io.SeekStart); err != nil {
		return err
	}
	n, err := io.Copy(destination, source)
	if err != nil {
		return err
	}
	if err := destination.Truncate(n); err != nil {
		return err
	}
	return destination.Sync()
}
