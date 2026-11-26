package server_test

import (
	"bytes"
	"os"
	"path/filepath"
	"probex/internal/server"
	"strings"
	"testing"
)

func copyCLI(t *testing.T, f *fixture, args ...string) error {
	t.Helper()
	flags := []string{"cp", "--server", f.http.URL, "--admin-token", f.admin}
	return server.Main(append(flags, args...))
}
func expectContents(t *testing.T, path string, expected []byte) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(b, expected) {
		t.Fatalf("%s length=%d want=%d error=%v", path, len(b), len(expected), err)
	}
}
func TestCopyFilesDirectoriesMultiSourceAndOverwrite(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	node := f.node()
	local, remote, download := t.TempDir(), t.TempDir(), t.TempDir()
	a, b := filepath.Join(local, "中文 a.txt"), filepath.Join(local, "b:colon.txt")
	if err := os.WriteFile(a, []byte("unicode-data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("colon-data"), 0600); err != nil {
		t.Fatal(err)
	}
	// Single upload and download can rename, resolving node by name or ID.
	remoteA := filepath.Join(remote, "renamed.txt")
	if err := copyCLI(t, f, a, node.Name+":"+remoteA); err != nil {
		t.Fatal(err)
	}
	expectContents(t, remoteA, []byte("unicode-data"))
	localA := filepath.Join(download, "renamed-again.txt")
	if err := copyCLI(t, f, node.ID+":"+remoteA, localA); err != nil {
		t.Fatal(err)
	}
	expectContents(t, localA, []byte("unicode-data"))
	if err := copyCLI(t, f, a, b, node.ID+":"+remote+"/"); err != nil {
		t.Fatal(err)
	}
	expectContents(t, filepath.Join(remote, filepath.Base(b)), []byte("colon-data"))
	multi := filepath.Join(download, "multi")
	if err := os.Mkdir(multi, 0700); err != nil {
		t.Fatal(err)
	}
	if err := copyCLI(t, f, node.Name+":"+filepath.Join(remote, filepath.Base(a)), node.ID+":"+filepath.Join(remote, filepath.Base(b)), multi); err != nil {
		t.Fatal(err)
	}
	expectContents(t, filepath.Join(multi, filepath.Base(a)), []byte("unicode-data"))
	expectContents(t, filepath.Join(multi, filepath.Base(b)), []byte("colon-data"))
	if err := copyCLI(t, f, a, b, node.ID+":"+filepath.Join(remote, "missing")); err == nil {
		t.Fatal("multi-source target need not be a directory")
	}
	if _, err := os.Stat(filepath.Join(remote, "missing")); !os.IsNotExist(err) {
		t.Fatal("invalid target mutated", err)
	}
	if err := copyCLI(t, f, a, node.ID+":"+remoteA); err == nil || !strings.Contains(err.Error(), "non-interactive") {
		t.Fatal("overwrite did not reject non-interactive input", err)
	}
	if err := os.WriteFile(localA, []byte("old-kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyCLI(t, f, node.ID+":"+remoteA, localA); err == nil {
		t.Fatal("download overwrite accepted")
	}
	expectContents(t, localA, []byte("old-kept"))
	if err := copyCLI(t, f, "-f", node.ID+":"+remoteA, localA); err != nil {
		t.Fatal(err)
	}
	expectContents(t, localA, []byte("unicode-data"))
	project := filepath.Join(local, "project")
	if err := os.MkdirAll(filepath.Join(project, "nested", "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "nested", "payload"), bytes.Repeat([]byte("content"), 1<<16), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".hidden"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyCLI(t, f, project, node.ID+":"+remote); err == nil {
		t.Fatal("directory copied without -r")
	}
	if err := copyCLI(t, f, "-r", project, node.Name+":"+remote+"/"); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(remote, "project", "nested", "empty")); err != nil || !info.IsDir() {
		t.Fatal(info, err)
	}
	result := filepath.Join(download, "tree")
	if err := copyCLI(t, f, "-r", node.ID+":"+filepath.Join(remote, "project"), result); err != nil {
		t.Fatal(err)
	}
	expectContents(t, filepath.Join(result, "nested", "payload"), bytes.Repeat([]byte("content"), 1<<16))
	expectContents(t, filepath.Join(result, ".hidden"), nil)
	if info, err := os.Stat(filepath.Join(result, "nested", "empty")); err != nil || !info.IsDir() {
		t.Fatal(info, err)
	}
	existing := filepath.Join(download, "existing-directory")
	if err := os.Mkdir(existing, 0700); err != nil {
		t.Fatal(err)
	}
	if err := copyCLI(t, f, "-r", node.Name+":"+filepath.Join(remote, "project"), existing+"/"); err != nil {
		t.Fatal(err)
	}
	expectContents(t, filepath.Join(existing, "project", "nested", "payload"), bytes.Repeat([]byte("content"), 1<<16))
	if info, err := os.Stat(filepath.Join(existing, "project", "nested", "empty")); err != nil || !info.IsDir() {
		t.Fatal(info, err)
	}
	noUploadTemps(t, remote)
	if temps, _ := filepath.Glob(filepath.Join(download, ".probex-cp-*")); len(temps) != 0 {
		t.Fatal(temps)
	}
}
func TestCopyUnsupportedLinksReportedAndOtherFilesContinue(t *testing.T) {
	f := newFixture(t, false)
	f.startAgent()
	n := f.node()
	source, target := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "regular"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("regular", filepath.Join(source, "linked")); err != nil {
		t.Fatal(err)
	}
	err := copyCLI(t, f, "-r", source, n.ID+":"+target)
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatal("unsupported link silently skipped", err)
	}
	expectContents(t, filepath.Join(target, filepath.Base(source), "regular"), []byte("kept"))
	if _, err := os.Lstat(filepath.Join(target, filepath.Base(source), "linked")); !os.IsNotExist(err) {
		t.Fatal("symlink followed", err)
	}
}
