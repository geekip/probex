package files

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSavePreservesNamedUserACL(t *testing.T) {
	s := newService(t)
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	const attribute = "system.posix_acl_access"
	acl := []byte{
		2, 0, 0, 0, // Version 2.
		1, 0, 6, 0, 255, 255, 255, 255, // Owner.
		2, 0, 4, 0, 17, 39, 0, 0, // Named user 10001.
		4, 0, 0, 0, 255, 255, 255, 255, // Owning group.
		16, 0, 4, 0, 255, 255, 255, 255, // ACL mask.
		32, 0, 0, 0, 255, 255, 255, 255, // Other.
	}
	if err := unix.Setxattr(path, attribute, acl, 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) {
			t.Skip("temporary filesystem does not support POSIX ACLs")
		}
		t.Fatal(err)
	}
	req := UploadRequest{Path: path, ID: "edit", Size: 3, Overwrite: true, PreserveLinks: true}
	serviceRequest(t, s, "POST", "/uploads", "owner", req, nil, 201, nil).Body.Close()
	serviceRequest(t, s, "PATCH", "/uploads/edit", "owner", []byte("new"), http.Header{"Upload-Offset": {"0"}}, 200, nil).Body.Close()
	serviceRequest(t, s, "POST", "/uploads/edit/commit", "owner", nil, nil, 200, nil).Body.Close()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "new" {
		t.Fatal(string(data), err)
	}
	got := make([]byte, len(acl))
	n, err := unix.Getxattr(path, attribute, got)
	if err != nil || n != len(acl) || !bytes.Equal(got, acl) {
		t.Fatalf("ACL changed: %x (%v)", got, err)
	}
}

func TestLinkedSaveRestoresOriginalAfterPartialWrite(t *testing.T) {
	dir := t.TempDir()
	target, alias, stage := filepath.Join(dir, "target"), filepath.Join(dir, "alias"), filepath.Join(dir, "stage")
	original := bytes.Repeat([]byte("a"), 64)
	if err := os.WriteFile(target, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(target, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stage, bytes.Repeat([]byte("b"), 8192), 0600); err != nil {
		t.Fatal(err)
	}
	var limit unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_FSIZE, &limit); err != nil {
		t.Fatal(err)
	}
	signal.Ignore(unix.SIGXFSZ)
	defer signal.Reset(unix.SIGXFSZ)
	if err := unix.Setrlimit(unix.RLIMIT_FSIZE, &unix.Rlimit{Cur: 4096, Max: limit.Max}); err != nil {
		t.Fatal(err)
	}
	err := commitLinkedFile(stage, target)
	if restore := unix.Setrlimit(unix.RLIMIT_FSIZE, &limit); restore != nil {
		t.Fatal(restore)
	}
	data, readErr := os.ReadFile(alias)
	if !errors.Is(err, unix.EFBIG) || readErr != nil || !bytes.Equal(original, data) {
		t.Fatalf("partial write: %v, alias has %d bytes (%v)", err, len(data), readErr)
	}
	a, aErr := os.Stat(alias)
	b, bErr := os.Stat(target)
	if aErr != nil || bErr != nil || !os.SameFile(a, b) {
		t.Fatal("hard link identity changed", aErr, bErr)
	}
}
