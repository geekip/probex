package files

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
)

func TestServiceWindowsShortcuts(t *testing.T) {
	s := newService(t)
	dir := t.TempDir()
	target, next := filepath.Join(dir, "真实 file.txt"), filepath.Join(dir, "next.txt")
	for _, path := range []string{target, next} {
		if err := os.WriteFile(path, []byte("shortcut target"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(dir, "入口.lnk")
	serviceRequest(t, s, "POST", "/links", "owner", map[string]any{"path": link, "target": target}, nil, 201, nil).Body.Close()
	var l Link
	serviceRequest(t, s, "GET", "/links?path="+url.QueryEscape(link), "owner", nil, nil, 200, &l).Body.Close()
	if l.Kind != "shortcut" || l.Target != target || l.Hard {
		t.Fatal(l)
	}
	_, err := withShortcut(link, func(shortcut *ole.IDispatch) (string, error) {
		v, err := oleutil.PutProperty(shortcut, "Arguments", "--keep-this")
		if err != nil {
			return "", err
		}
		v.Clear()
		v, err = oleutil.CallMethod(shortcut, "Save")
		if err == nil {
			v.Clear()
		}
		return "", err
	})
	if err != nil {
		t.Fatal(err)
	}
	var resolved Entry
	serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(link), "owner", nil, nil, 200, &resolved).Body.Close()
	if resolved.Path != target || resolved.Type != "file" {
		t.Fatal(resolved)
	}
	renamed := filepath.Join(dir, "renamed.lnk")
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": link, "destination": renamed, "target": next}, nil, 200, nil).Body.Close()
	args, err := withShortcut(renamed, func(shortcut *ole.IDispatch) (string, error) {
		v, err := oleutil.GetProperty(shortcut, "Arguments")
		if err != nil {
			return "", err
		}
		defer v.Clear()
		return v.ToString(), nil
	})
	if err != nil || args != "--keep-this" {
		t.Fatal(args, err)
	}
	serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(renamed), "owner", nil, nil, 200, &resolved).Body.Close()
	if resolved.Path != next {
		t.Fatal(resolved)
	}
	// A shortcut converted to a hard link retains its chosen name, including
	// .lnk; its regular-file content must not be parsed as a Shell link.
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": renamed, "target": next, "hard": true}, nil, 200, nil).Body.Close()
	serviceRequest(t, s, "GET", "/links?path="+url.QueryEscape(renamed), "owner", nil, nil, 200, &l).Body.Close()
	if !l.Hard || l.Kind != "hard" {
		t.Fatal(l)
	}
	serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(renamed), "owner", nil, nil, 200, &resolved).Body.Close()
	if resolved.Path != renamed || resolved.Type != "file" {
		t.Fatal(resolved)
	}
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": renamed, "target": dir}, nil, 200, nil).Body.Close()
	serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(renamed), "owner", nil, nil, 200, &resolved).Body.Close()
	if resolved.Type != "directory" {
		t.Fatal(resolved)
	}
	serviceRequest(t, s, "PUT", "/links", "owner", map[string]any{"path": renamed, "target": filepath.Join(dir, "absent")}, nil, 200, nil).Body.Close()
	serviceRequest(t, s, "GET", "/resolve?path="+url.QueryEscape(renamed), "owner", nil, nil, 404, nil).Body.Close()
}

func TestServiceWindowsLinkReplacementRestoresAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "alias")
	target := filepath.Join(dir, "target")
	if err := os.WriteFile(target, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "missing-temp")
	if err := replaceLink(missing, path, true); err == nil {
		t.Fatal("missing replacement accepted")
	}
	if got, err := os.Readlink(path); err != nil || got != target {
		t.Fatal("old alias not restored", got, err)
	}
	if _, err := os.Lstat(missing + ".previous"); !os.IsNotExist(err) {
		t.Fatal("backup retained", err)
	}
}
