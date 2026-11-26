package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// copyEntry stages a complete entry beside its destination before publishing it.
// Symlinks are copied as links; their targets are never traversed.
func copyEntry(ctx context.Context, from, to string) (err error) {
	info, err := os.Lstat(from)
	if err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(to))
	if err != nil {
		return err
	}
	if info.IsDir() {
		source, err := filepath.EvalSymlinks(from)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, parent)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("cannot copy a directory into itself: %w", os.ErrInvalid)
		}
	}
	staging, err := os.MkdirTemp(parent, ".probex-copy-*")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	destination := filepath.Join(staging, "entry")
	err = filepath.WalkDir(from, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		switch {
		case d.IsDir():
			return os.Mkdir(target, info.Mode().Perm()|0700)
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			input, err := os.Open(path)
			if err != nil {
				return err
			}
			defer input.Close()
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, &copyReader{ctx, input})
			return errors.Join(copyErr, output.Close())
		default:
			return fmt.Errorf("cannot copy special file %s: %w", path, os.ErrInvalid)
		}
	})
	if err != nil {
		return err
	}
	// Restore directory permissions after their children have been created.
	err = filepath.WalkDir(from, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		return os.Chmod(filepath.Join(destination, relative), info.Mode().Perm())
	})
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Lstat(to); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(destination, to)
}

type copyReader struct {
	ctx   context.Context
	input io.Reader
}

func (r *copyReader) Read(b []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.input.Read(b)
}
func (s *Service) copy(w http.ResponseWriter, r *http.Request) {
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
		fail(w, os.ErrExist)
		return
	} else if !errors.Is(err, os.ErrNotExist) {
		fail(w, err)
		return
	}
	if err := copyEntry(r.Context(), from, to); err != nil {
		fail(w, err)
		return
	}
	JSON(w, 201, map[string]string{"path": to})
}
