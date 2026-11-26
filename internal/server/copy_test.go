package server

import (
	"probex/internal/protocol"
	"testing"
)

func TestCopyPathParsing(t *testing.T) {
	for _, test := range []struct {
		raw        string
		windows    bool
		node, path string
	}{
		{"node-a:/data/a:b.txt", false, "node-a", "/data/a:b.txt"},
		{"1:C:\\Users\\name\\file.txt", false, "1", "C:\\Users\\name\\file.txt"},
		{"node-a:C:/Users/name/file.txt", true, "node-a", "C:/Users/name/file.txt"},
		{"C:\\Users\\name\\file.txt", true, "", "C:\\Users\\name\\file.txt"},
		{"C:relative.txt", true, "", "C:relative.txt"},
		{"\\\\host\\share\\file.txt", true, "", "\\\\host\\share\\file.txt"},
		{"./name:with:colons", false, "", "./name:with:colons"},
		{"/data/name:with:colons", false, "", "/data/name:with:colons"},
		{"a:/data/file.txt", false, "a", "/data/file.txt"},
	} {
		got, err := parseCopyPath(test.raw, test.windows)
		if err != nil || got.node != test.node || got.path != test.path {
			t.Fatal(test, got, err)
		}
	}
	for _, args := range [][]string{nil, {"one"}, {"a", "b"}, {"node:a", "node:b"}, {"node:a", "local", "dest"}} {
		if _, err := parseCopyPaths(args, false); err == nil {
			t.Fatal("invalid endpoints accepted", args)
		}
	}
}

func TestCopyNodeResolutionUsesIDsAndRejectsAmbiguousNames(t *testing.T) {
	nodes := []protocol.Node{{ID: "1", Name: "same"}, {ID: "2", Name: "same"}, {ID: "3", Name: "1"}}
	got, err := resolveNode(nodes, "1")
	if err != nil || got.ID != "1" {
		t.Fatal(got, err)
	}
	if _, err := resolveNode(nodes, "same"); err == nil {
		t.Fatal("ambiguous name accepted")
	}
}

func TestCopyWindowsRemoteJoinPreservesUNCAndDriveRoots(t *testing.T) {
	c := copyClient{node: protocol.Node{Info: protocol.Info{OS: "windows"}}}
	for _, test := range []struct{ parent, want string }{{`C:\`, `C:\file.txt`}, {`\\server\share\`, `\\server\share\file.txt`}, {`//server/share`, `//server/share\file.txt`}} {
		if got := c.remoteJoin(test.parent, "file.txt"); got != test.want {
			t.Fatal(got, test.want)
		}
	}
}
