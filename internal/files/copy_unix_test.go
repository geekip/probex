//go:build !windows

package files

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestRecursiveCopyPreservesLinksAndRejectsSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source")
	destination := filepath.Join(dir, "destination")
	if err := os.Mkdir(source, 0750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "file"), []byte("contents"), 0640); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"link": "file", "broken": "missing", "cycle": "."} {
		if err := os.Symlink(target, filepath.Join(source, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := copyEntry(context.Background(), source, destination); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"link": "file", "broken": "missing", "cycle": "."} {
		if got, err := os.Readlink(filepath.Join(destination, name)); err != nil || got != target {
			t.Fatal(name, got, err)
		}
	}
	if info, err := os.Stat(filepath.Join(destination, "file")); err != nil || info.Mode().Perm() != 0640 {
		t.Fatal(info, err)
	}
	if err := syscall.Mkfifo(filepath.Join(source, "pipe"), 0600); err != nil {
		t.Fatal(err)
	}
	failed := filepath.Join(dir, "failed")
	if err := copyEntry(context.Background(), source, failed); err == nil {
		t.Fatal("copied special file")
	}
	if _, err := os.Stat(failed); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if leftovers, err := filepath.Glob(filepath.Join(dir, ".probex-copy-*")); err != nil || len(leftovers) > 0 {
		t.Fatal(leftovers, err)
	}
}
