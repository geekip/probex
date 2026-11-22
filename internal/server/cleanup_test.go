package server

import (
	"database/sql"
	"errors"
	"path/filepath"
	"probex/internal/protocol"
	"testing"
	"time"
)

func TestHistoryClearAndNodeDeletionRemoveOwnedData(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "cleanup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	target := nodeRow{Node: protocol.Node{Name: "target", Info: protocol.Info{Hostname: "private-device"}}}
	if err := s.saveEnrollment(&target, hash("join"), time.Now().Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	other := nodeRow{Node: protocol.Node{Name: "other", Admission: "approved"}, KeyHash: hash("other-secret")}
	if err := s.insertNode(&other); err != nil {
		t.Fatal(err)
	}
	for _, command := range []protocol.Command{
		{ID: "target-output", NodeID: target.ID, Command: "printf secret", Status: "succeeded", Stdout: []byte("secret"), Stderr: []byte("private error")},
		{ID: "other-output", NodeID: other.ID, Command: "echo other", Status: "succeeded"},
	} {
		if err := s.insertCommand(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.clearHistory(target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.command("target-output"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("cleared output retained", err)
	}
	if _, err := s.command("other-output"); err != nil {
		t.Fatal("other node history cleared", err)
	}
	if valid, err := s.enrollmentValid(target.ID, hash("join")); err != nil || !valid {
		t.Fatal("clearing history changed credentials", valid, err)
	}
	if err := s.insertCommand(protocol.Command{ID: "target-new", NodeID: target.ID, Status: "running"}); err != nil {
		t.Fatal(err)
	}
	if err := s.deleteNode(target.ID); err != nil {
		t.Fatal(err)
	}
	for table, column := range map[string]string{"nodes": "id", "commands": "node_id", "enrollments": "node_id"} {
		var count int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM "+table+" WHERE "+column+"=?", target.ID).Scan(&count); err != nil || count != 0 {
			t.Fatal("deleted node data remains", table, count, err)
		}
	}
	if _, err := s.node(other.ID); err != nil {
		t.Fatal("other node removed", err)
	}
	if _, err := s.command("other-output"); err != nil {
		t.Fatal("other node output removed", err)
	}
	if err := s.deleteNode(target.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing node deletion", err)
	}
}

func TestStartupPurgesOldTerminalAndDeletedNodeData(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old-records.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	active := nodeRow{Node: protocol.Node{Name: "active", Admission: "approved"}, KeyHash: hash("active")}
	if err := s.insertNode(&active); err != nil {
		t.Fatal(err)
	}
	removed := nodeRow{Node: protocol.Node{Name: "removed"}}
	if err := s.saveEnrollment(&removed, hash("revoked"), time.Now().Add(time.Hour), true); err != nil {
		t.Fatal(err)
	}
	// Reproduce the previous version's tombstone and terminal transcript format.
	if err := s.updateNode(removed.ID, func(n *protocol.Node) { n.Admission = "deleted" }); err != nil {
		t.Fatal(err)
	}
	for _, command := range []protocol.Command{
		{ID: "terminal", NodeID: active.ID, Command: "[interactive shell]", Status: "running", Stdout: []byte("old transcript")},
		{ID: "literal-command", NodeID: active.ID, Command: "[interactive shell]", TimeoutSeconds: 60, Status: "succeeded"},
		{ID: "normal-command", NodeID: active.ID, Command: "uname -a", TimeoutSeconds: 60, Status: "running"},
		{ID: "removed-command", NodeID: removed.ID, Command: "id", TimeoutSeconds: 60, Status: "succeeded"},
	} {
		if err := s.insertCommand(command); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, id := range []string{"terminal", "removed-command"} {
		if _, err := s.command(id); !errors.Is(err, sql.ErrNoRows) {
			t.Fatal("old data remains", id, err)
		}
	}
	if _, err := s.command("literal-command"); err != nil {
		t.Fatal("real CLI command mistaken for terminal", err)
	}
	if command, err := s.command("normal-command"); err != nil || command.Status != "unknown" {
		t.Fatal("normal command recovery lost", command, err)
	}
	if _, err := s.node(removed.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("old deleted node remains", err)
	}
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM enrollments WHERE node_id=?", removed.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("old enrollment remains", count, err)
	}
	next := nodeRow{Node: protocol.Node{Name: "removed", Admission: "approved"}}
	if err := s.insertNode(&next); err != nil || next.ID == removed.ID {
		t.Fatal("deleted ID reused or name unavailable", next, err)
	}
}
