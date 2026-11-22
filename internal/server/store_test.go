package server

import (
	"path/filepath"
	"probex/internal/protocol"
	"testing"
	"time"
)

func TestRestartPreservesHistoryAndMarksUnfinishedUnknown(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	n := nodeRow{Node: protocol.Node{Name: "node", Admission: "approved", JoinMode: "key", CreatedAt: time.Now().UTC()}, KeyHash: hash("secret")}
	if err := s.insertNode(&n); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"queued", "running", "succeeded"} {
		c := protocol.Command{ID: status, NodeID: n.ID, Status: status, Command: "echo hello", CreatedAt: time.Now().UTC(), Stdout: []byte("retained")}
		if err := s.insertCommand(c); err != nil {
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
	for _, id := range []string{"queued", "running", "succeeded"} {
		c, err := s.command(id)
		if err != nil {
			t.Fatal(err)
		}
		want := "unknown"
		if id == "succeeded" {
			want = id
		}
		if c.Status != want || string(c.Stdout) != "retained" {
			t.Fatalf("bad recovered record: %+v", c)
		}
	}
	got, err := s.node(n.ID)
	if err != nil || got.Admission != "approved" {
		t.Fatal("node not retained", err)
	}
}
